package auth_test

import (
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/PV80/GoldenCloud/server/internal/auth"
	"github.com/PV80/GoldenCloud/server/internal/config"
)

// Cost 4 keeps the suite fast; the dummy hash used to equalise timing for
// unknown usernames must have the same cost or the timing test is meaningless.
const testCost = bcrypt.MinCost

var testDummyHash = []byte("$2a$04$Tc2LCCzXpnX0HZ1uTfZquuvuQu5rySNxta3idAsPP73Drjrwwu76O")

func hash(t *testing.T, pw string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), testCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

type fixture struct {
	a     *auth.Authenticator
	store *auth.Store
	srv   *httptest.Server
	seen  chan config.User
}

func newFixture(t *testing.T, opts auth.Options) *fixture {
	t.Helper()
	store := auth.NewStore([]config.User{
		{Username: "alice", PasswordHash: hash(t, "alice-secret"), Root: "alice"},
		{Username: "bob", PasswordHash: hash(t, "bob-secret"), Root: "bob"},
	})
	if opts.DummyHash == nil {
		opts.DummyHash = testDummyHash
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	a := auth.New(store, opts)
	f := &fixture{a: a, store: store, seen: make(chan config.User, 16)}
	f.srv = httptest.NewServer(a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.UserFrom(r.Context())
		if !ok {
			t.Error("handler reached without a user in context")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		select {
		case f.seen <- u:
		default:
		}
		fmt.Fprintf(w, "hello %s", u.Username)
	})))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) do(t *testing.T, user, pass string, withHeader bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.srv.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if withHeader {
		req.SetBasicAuth(user, pass)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestSuccessAttachesUser(t *testing.T) {
	t.Parallel()
	f := newFixture(t, auth.Options{})
	resp := f.do(t, "alice", "alice-secret", true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	select {
	case u := <-f.seen:
		if u.Username != "alice" || u.Root != "alice" {
			t.Fatalf("context user = %+v", u)
		}
	default:
		t.Fatal("handler never ran")
	}
}

func TestFailuresAllLookIdentical(t *testing.T) {
	t.Parallel()
	f := newFixture(t, auth.Options{})

	cases := []struct {
		name string
		req  func() *http.Request
	}{
		{"wrong password", func() *http.Request {
			r, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
			r.SetBasicAuth("alice", "not-alices-secret")
			return r
		}},
		{"unknown user", func() *http.Request {
			r, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
			r.SetBasicAuth("mallory", "anything")
			return r
		}},
		{"empty password", func() *http.Request {
			r, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
			r.SetBasicAuth("alice", "")
			return r
		}},
		{"empty username", func() *http.Request {
			r, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
			r.SetBasicAuth("", "alice-secret")
			return r
		}},
		{"missing header", func() *http.Request {
			r, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
			return r
		}},
		{"not basic", func() *http.Request {
			r, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
			r.Header.Set("Authorization", "Bearer abcdef")
			return r
		}},
		{"basic without base64", func() *http.Request {
			r, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
			r.Header.Set("Authorization", "Basic not-base64!!")
			return r
		}},
		{"basic without colon", func() *http.Request {
			r, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
			r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("alicenopassword")))
			return r
		}},
		{"empty basic", func() *http.Request {
			r, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
			r.Header.Set("Authorization", "Basic ")
			return r
		}},
		{"lowercase scheme", func() *http.Request {
			r, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
			r.Header.Set("Authorization", "basic "+base64.StdEncoding.EncodeToString([]byte("alice:wrong")))
			return r
		}},
	}

	var bodies []string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.DefaultClient.Do(tc.req())
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
			got := resp.Header.Get("WWW-Authenticate")
			if got != `Basic realm="GoldenCloud", charset="UTF-8"` {
				t.Fatalf("WWW-Authenticate = %q", got)
			}
			buf := make([]byte, 512)
			n, _ := resp.Body.Read(buf)
			bodies = append(bodies, string(buf[:n]))
		})
	}
	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Fatalf("failure bodies differ: %q vs %q — this leaks which usernames exist", bodies[0], bodies[i])
		}
	}
}

func TestCaseSensitiveUsername(t *testing.T) {
	t.Parallel()
	f := newFixture(t, auth.Options{})
	resp := f.do(t, "Alice", "alice-secret", true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: usernames are lowercase and must not match case-insensitively", resp.StatusCode)
	}
}

func TestRateLimitLocksOutAfterThreshold(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := &fakeClock{t: now}
	f := newFixture(t, auth.Options{
		FailureThreshold: 3,
		BaseDelay:        time.Second,
		MaxDelay:         time.Minute,
		Now:              clock.Now,
	})

	for i := 0; i < 3; i++ {
		resp := f.do(t, "alice", "wrong", true)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, resp.StatusCode)
		}
	}
	resp := f.do(t, "alice", "wrong", true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status after threshold = %d, want 429", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" {
		t.Fatal("429 without Retry-After")
	}

	// Even the correct password is refused while locked out.
	resp2 := f.do(t, "alice", "alice-secret", true)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("correct password during lockout = %d, want 429", resp2.StatusCode)
	}

	// After the backoff expires, the correct password works again.
	clock.advance(2 * time.Second)
	resp3 := f.do(t, "alice", "alice-secret", true)
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("after backoff = %d, want 200", resp3.StatusCode)
	}
}

func TestRateLimitBacksOffExponentially(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{t: time.Now()}
	f := newFixture(t, auth.Options{
		FailureThreshold: 1,
		BaseDelay:        time.Second,
		MaxDelay:         8 * time.Second,
		Now:              clock.Now,
	})
	var delays []time.Duration
	for i := 0; i < 6; i++ {
		resp := f.do(t, "alice", "wrong", true)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("attempt %d unexpectedly rate limited", i)
		}
		resp = f.do(t, "alice", "wrong", true)
		ra := resp.Header.Get("Retry-After")
		resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: want 429, got %d", i, resp.StatusCode)
		}
		var secs int
		if _, err := fmt.Sscanf(ra, "%d", &secs); err != nil {
			t.Fatalf("bad Retry-After %q", ra)
		}
		d := time.Duration(secs) * time.Second
		delays = append(delays, d)
		clock.advance(d + time.Second)
	}
	if !sort.SliceIsSorted(delays, func(i, j int) bool { return delays[i] < delays[j] }) {
		t.Fatalf("delays not monotonically non-decreasing: %v", delays)
	}
	if delays[len(delays)-1] > 8*time.Second {
		t.Fatalf("delay %v exceeded MaxDelay", delays[len(delays)-1])
	}
	if delays[len(delays)-1] <= delays[0] {
		t.Fatalf("delays did not grow: %v", delays)
	}
}

func TestRateLimitIsPerIPAndUsername(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{t: time.Now()}
	f := newFixture(t, auth.Options{
		FailureThreshold: 2,
		BaseDelay:        time.Minute,
		MaxDelay:         time.Hour,
		Now:              clock.Now,
	})
	for i := 0; i < 2; i++ {
		f.do(t, "alice", "wrong", true).Body.Close()
	}
	if r := f.do(t, "alice", "wrong", true); r.StatusCode != http.StatusTooManyRequests {
		r.Body.Close()
		t.Fatalf("alice not locked out: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
	// A different username from the same IP is unaffected.
	r := f.do(t, "bob", "bob-secret", true)
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("bob = %d, want 200: lockout must not be shared across usernames", r.StatusCode)
	}
}

func TestSuccessResetsFailureCount(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{t: time.Now()}
	f := newFixture(t, auth.Options{
		FailureThreshold: 3, BaseDelay: time.Minute, MaxDelay: time.Hour, Now: clock.Now,
	})
	for i := 0; i < 2; i++ {
		f.do(t, "alice", "wrong", true).Body.Close()
	}
	f.do(t, "alice", "alice-secret", true).Body.Close()
	for i := 0; i < 2; i++ {
		r := f.do(t, "alice", "wrong", true)
		code := r.StatusCode
		r.Body.Close()
		if code != http.StatusUnauthorized {
			t.Fatalf("attempt %d after reset = %d, want 401", i, code)
		}
	}
}

func TestStoreReplaceIsLive(t *testing.T) {
	t.Parallel()
	f := newFixture(t, auth.Options{})
	f.store.Replace([]config.User{
		{Username: "carol", PasswordHash: hash(t, "carol-secret"), Root: "carol"},
	})
	r := f.do(t, "alice", "alice-secret", true)
	code := r.StatusCode
	r.Body.Close()
	if code != http.StatusUnauthorized {
		t.Fatalf("removed user still authenticates: %d", code)
	}
	r = f.do(t, "carol", "carol-secret", true)
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("new user = %d, want 200", r.StatusCode)
	}
}

func TestStoreConcurrentReplaceAndLookup(t *testing.T) {
	t.Parallel()
	store := auth.NewStore([]config.User{{Username: "alice", PasswordHash: hash(t, "x"), Root: "alice"}})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if i%2 == 0 {
					store.Replace([]config.User{{Username: "alice", PasswordHash: "h", Root: "alice"}})
				} else {
					store.Lookup("alice")
					store.Users()
				}
			}
		}(i)
	}
	wg.Wait()
}

// TestTimingDoesNotObviouslyLeak is a smoke test: an unknown username must not
// return dramatically faster than a known username with a wrong password, which
// is what happens when the bcrypt comparison is skipped for unknown users.
func TestTimingDoesNotObviouslyLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("timing smoke test")
	}
	t.Parallel()
	f := newFixture(t, auth.Options{FailureThreshold: 1 << 30})

	measure := func(user, pass string) time.Duration {
		const n = 12
		samples := make([]time.Duration, 0, n)
		for i := 0; i < n; i++ {
			start := time.Now()
			resp := f.do(t, user, pass, true)
			resp.Body.Close()
			samples = append(samples, time.Since(start))
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		return samples[len(samples)/2]
	}

	known := measure("alice", "definitely-wrong")
	unknown := measure("nosuchuser", "definitely-wrong")

	ratio := float64(unknown) / float64(known)
	t.Logf("known-user median %v, unknown-user median %v, ratio %.2f", known, unknown, ratio)
	if ratio < 0.25 || ratio > 4 {
		t.Fatalf("unknown-user path takes %.2fx the time of the known-user path; "+
			"the bcrypt comparison is probably being skipped, which leaks which usernames exist", ratio)
	}
}

func TestUserFromEmptyContext(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("GET", "/", nil)
	if _, ok := auth.UserFrom(req.Context()); ok {
		t.Fatal("UserFrom on a bare context returned a user")
	}
}

func TestRealmIsConfigurable(t *testing.T) {
	t.Parallel()
	f := newFixture(t, auth.Options{Realm: "Other"})
	r := f.do(t, "", "", false)
	defer r.Body.Close()
	if got := r.Header.Get("WWW-Authenticate"); !strings.Contains(got, `realm="Other"`) {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestTrustedProxyHeaderIsOnlyBelievedFromAllowedPeers(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{t: time.Now()}
	// The httptest server is reached over loopback, so 127.0.0.1/32 makes the
	// test client "the trusted proxy".
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	f := newFixture(t, auth.Options{
		FailureThreshold:   1,
		BaseDelay:          time.Minute,
		MaxDelay:           time.Hour,
		Now:                clock.Now,
		TrustedProxyHeader: "X-Forwarded-For",
		TrustedProxyCIDRs:  trusted,
	})

	fail := func(xff string) int {
		r, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
		r.SetBasicAuth("alice", "wrong")
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// One failure attributed to 203.0.113.7 locks only that bucket.
	if got := fail("203.0.113.7"); got != http.StatusUnauthorized {
		t.Fatalf("first failure = %d", got)
	}
	if got := fail("203.0.113.7"); got != http.StatusTooManyRequests {
		t.Fatalf("second failure from the same forwarded IP = %d, want 429", got)
	}
	if got := fail("198.51.100.9"); got != http.StatusUnauthorized {
		t.Fatalf("a different forwarded IP = %d, want 401: buckets are per client address", got)
	}
	// The rightmost entry wins, so a client prepending its own value cannot
	// pick someone else's bucket.
	if got := fail("1.2.3.4, 203.0.113.7"); got != http.StatusTooManyRequests {
		t.Fatalf("forged leading entry = %d, want 429 (rightmost entry must win)", got)
	}
}

func TestForwardedHeaderIgnoredWithoutTrustedCIDRs(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{t: time.Now()}
	f := newFixture(t, auth.Options{
		FailureThreshold: 1, BaseDelay: time.Minute, MaxDelay: time.Hour, Now: clock.Now,
	})
	do := func(xff string) int {
		r, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
		r.SetBasicAuth("alice", "wrong")
		r.Header.Set("X-Forwarded-For", xff)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := do("10.0.0.1"); got != http.StatusUnauthorized {
		t.Fatalf("first = %d", got)
	}
	// Changing the header must not buy a fresh bucket when no proxy is trusted.
	if got := do("10.0.0.2"); got != http.StatusTooManyRequests {
		t.Fatalf("second with a different forged header = %d, want 429", got)
	}
}
