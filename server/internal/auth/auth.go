// Package auth implements HTTP Basic authentication against the GoldenCloud
// user store.
//
// Three properties matter here and are tested:
//
//   - A failure never reveals whether the username exists. Every rejection
//     produces the same status, headers and body, and an unknown username still
//     costs a full bcrypt comparison against a dummy hash so the response time
//     does not give the answer away either.
//   - Credentials are compared in constant time with respect to the stored
//     username set.
//   - Repeated failures from the same client for the same username are slowed
//     down with an exponential backoff, so a tunnel exposed to the internet is
//     not a free password-guessing oracle.
package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/PV80/GoldenCloud/server/internal/config"
)

// DefaultRealm is the HTTP Basic realm presented to clients. The Windows
// redirector shows it in its credential prompt.
const DefaultRealm = "GoldenCloud"

// defaultDummyHash is a bcrypt hash of a random 32-byte string at the same cost
// the admin CLI uses for real passwords. It exists so that authenticating an
// unknown username costs the same as authenticating a known one.
var defaultDummyHash = []byte("$2a$12$rVFds0RZ5bTs2hvVg9istO3Q3fGm9HTTTeVzbqD0IQpES7e81z5VO")

type contextKey struct{}

var userKey contextKey

// UserFrom returns the authenticated user attached to ctx by the middleware.
func UserFrom(ctx context.Context) (config.User, bool) {
	u, ok := ctx.Value(userKey).(config.User)
	return u, ok
}

// WithUser attaches a user to a context. Exported for tests and for wiring code
// that authenticates by other means.
func WithUser(ctx context.Context, u config.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// Store is a live, concurrency-safe set of users. The server swaps its contents
// wholesale when users.yaml is reloaded on SIGHUP.
type Store struct {
	mu   sync.RWMutex
	list []config.User
}

// NewStore returns a store holding a copy of users.
func NewStore(users []config.User) *Store {
	s := &Store{}
	s.Replace(users)
	return s
}

// Replace atomically swaps the whole user set.
func (s *Store) Replace(users []config.User) {
	cp := make([]config.User, len(users))
	copy(cp, users)
	s.mu.Lock()
	s.list = cp
	s.mu.Unlock()
}

// Users returns a copy of the current user set.
func (s *Store) Users() []config.User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]config.User, len(s.list))
	copy(cp, s.list)
	return cp
}

// Lookup finds a user by exact username. It compares against every entry with
// a constant-time comparison and does not stop early, so the time taken does
// not depend on where in the list a username sits or how many leading
// characters a guess got right.
func (s *Store) Lookup(username string) (config.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	want := []byte(username)
	var found config.User
	match := 0
	for _, u := range s.list {
		if subtle.ConstantTimeCompare([]byte(u.Username), want) == 1 {
			found = u
			match = 1
		}
	}
	return found, match == 1
}

// Options configures an Authenticator. The zero value is usable.
type Options struct {
	// Realm is the HTTP Basic realm. Defaults to DefaultRealm.
	Realm string
	// FailureThreshold is how many consecutive failures for one
	// client-IP-and-username pair are allowed before backoff starts.
	FailureThreshold int
	// BaseDelay is the lockout after the first failure past the threshold.
	BaseDelay time.Duration
	// MaxDelay caps the exponential backoff.
	MaxDelay time.Duration
	// TrustedProxyHeader names the header carrying the real client address
	// (e.g. X-Forwarded-For). Empty disables forwarded-address handling.
	TrustedProxyHeader string
	// TrustedProxyCIDRs are the networks a trusted proxy connects from. The
	// header is believed only when the peer is inside one of them; otherwise
	// any client could pick its own rate-limit bucket.
	TrustedProxyCIDRs []netip.Prefix
	// DummyHash is compared against when the username is unknown, to equalise
	// response time. It must have the same bcrypt cost as real passwords.
	DummyHash []byte
	// Now is the clock, for tests.
	Now func() time.Time
	// Logger receives authentication failures. Defaults to slog.Default().
	Logger *slog.Logger
}

const (
	defaultFailureThreshold = 5
	defaultBaseDelay        = time.Second
	defaultMaxDelay         = 5 * time.Minute
	// idleReset forgets a client's failure history once it has behaved (or
	// gone away) for this long.
	idleReset = 15 * time.Minute
	// maxTrackedClients bounds the backoff table so a flood of distinct
	// usernames cannot exhaust memory.
	maxTrackedClients = 8192
)

// Authenticator is the HTTP Basic middleware.
type Authenticator struct {
	store *Store
	opt   Options

	mu       sync.Mutex
	failures map[string]*failureState
}

type failureState struct {
	count       int
	lockedUntil time.Time
	last        time.Time
}

// New returns an Authenticator over store.
func New(store *Store, opt Options) *Authenticator {
	if opt.Realm == "" {
		opt.Realm = DefaultRealm
	}
	if opt.FailureThreshold <= 0 {
		opt.FailureThreshold = defaultFailureThreshold
	}
	if opt.BaseDelay <= 0 {
		opt.BaseDelay = defaultBaseDelay
	}
	if opt.MaxDelay <= 0 {
		opt.MaxDelay = defaultMaxDelay
	}
	if opt.MaxDelay < opt.BaseDelay {
		opt.MaxDelay = opt.BaseDelay
	}
	if len(opt.DummyHash) == 0 {
		opt.DummyHash = defaultDummyHash
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	return &Authenticator{store: store, opt: opt, failures: map[string]*failureState{}}
}

// Wrap returns a handler that authenticates before delegating to next.
func (a *Authenticator) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := a.Authenticate(w, r)
		if !ok {
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
	})
}

// Authenticate verifies the request's credentials. When it returns false it has
// already written the response.
func (a *Authenticator) Authenticate(w http.ResponseWriter, r *http.Request) (config.User, bool) {
	username, password, wellFormed := basicCredentials(r.Header.Get("Authorization"))
	if !wellFormed {
		// No credential was offered, or the header was garbage. This is the
		// normal first request from every WebDAV client, so it must not count
		// against the backoff.
		a.challenge(w)
		return config.User{}, false
	}

	key := a.clientIP(r) + "\x00" + username
	if retry, locked := a.lockedFor(key); locked {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
		a.challenge429(w)
		a.opt.Logger.Warn("authentication rate limited",
			slog.String("username", username),
			slog.String("client", a.clientIP(r)),
			slog.Duration("retry_after", retry))
		return config.User{}, false
	}

	u, known := a.store.Lookup(username)
	hash := []byte(u.PasswordHash)
	if !known {
		// Spend the same time as a real comparison so the response time does
		// not disclose whether the username exists.
		hash = a.opt.DummyHash
	}
	err := bcrypt.CompareHashAndPassword(hash, []byte(password))
	if !known || err != nil {
		a.recordFailure(key)
		a.opt.Logger.Warn("authentication failed",
			slog.String("username", username),
			slog.String("client", a.clientIP(r)))
		a.challenge(w)
		return config.User{}, false
	}

	a.recordSuccess(key)
	return u, true
}

// challenge writes the single, uniform rejection. Every failure path uses it,
// so the response cannot be used to distinguish a wrong password from an
// unknown username.
func (a *Authenticator) challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="`+a.opt.Realm+`", charset="UTF-8"`)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte("401 Unauthorized\n"))
}

func (a *Authenticator) challenge429(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="`+a.opt.Realm+`", charset="UTF-8"`)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte("429 Too Many Requests\n"))
}

// basicCredentials parses an Authorization header. Unlike http.Request.BasicAuth
// it reports whether the header was a well-formed Basic credential at all,
// which decides whether a failure counts against the backoff.
func basicCredentials(header string) (username, password string, wellFormed bool) {
	const prefix = "basic "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header[len(prefix):]))
	if err != nil {
		return "", "", false
	}
	user, pass, found := strings.Cut(string(raw), ":")
	if !found {
		return "", "", false
	}
	return user, pass, true
}

// clientIP returns the address used to bucket rate limiting.
func (a *Authenticator) clientIP(r *http.Request) string {
	peer := peerHost(r.RemoteAddr)
	if a.opt.TrustedProxyHeader == "" || len(a.opt.TrustedProxyCIDRs) == 0 {
		return peer
	}
	addr, err := netip.ParseAddr(peer)
	if err != nil || !a.peerIsTrusted(addr) {
		return peer
	}
	fwd := r.Header.Get(a.opt.TrustedProxyHeader)
	if fwd == "" {
		return peer
	}
	// The rightmost entry is the one appended by the nearest proxy, which is
	// the only one a client cannot forge by sending the header itself.
	parts := strings.Split(fwd, ",")
	if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
		return ip
	}
	return peer
}

func (a *Authenticator) peerIsTrusted(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range a.opt.TrustedProxyCIDRs {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func peerHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// lockedFor reports the remaining lockout for key, if any.
func (a *Authenticator) lockedFor(key string) (time.Duration, bool) {
	now := a.opt.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.failures[key]
	if !ok {
		return 0, false
	}
	if now.Sub(st.last) > idleReset {
		delete(a.failures, key)
		return 0, false
	}
	if now.Before(st.lockedUntil) {
		return st.lockedUntil.Sub(now), true
	}
	return 0, false
}

func (a *Authenticator) recordFailure(key string) {
	now := a.opt.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked(now)
	st := a.failures[key]
	if st == nil {
		st = &failureState{}
		a.failures[key] = st
	}
	st.count++
	st.last = now
	if over := st.count - a.opt.FailureThreshold; over >= 0 {
		delay := a.opt.BaseDelay
		for i := 0; i < over && delay < a.opt.MaxDelay; i++ {
			delay *= 2
		}
		if delay > a.opt.MaxDelay {
			delay = a.opt.MaxDelay
		}
		st.lockedUntil = now.Add(delay)
	}
}

func (a *Authenticator) recordSuccess(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, key)
}

// pruneLocked drops entries that have gone idle, and — if the table is still
// oversized — the oldest ones, so a flood of distinct usernames cannot grow it
// without bound.
func (a *Authenticator) pruneLocked(now time.Time) {
	if len(a.failures) < maxTrackedClients {
		if len(a.failures) < 64 {
			return
		}
		for k, st := range a.failures {
			if now.Sub(st.last) > idleReset {
				delete(a.failures, k)
			}
		}
		return
	}
	for k, st := range a.failures {
		if now.Sub(st.last) > idleReset {
			delete(a.failures, k)
		}
	}
	for k := range a.failures {
		if len(a.failures) < maxTrackedClients {
			break
		}
		delete(a.failures, k)
	}
}
