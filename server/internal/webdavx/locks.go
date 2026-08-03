package webdavx

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"golang.org/x/net/webdav"
)

// tokenScheme is the URI scheme RFC 4918 §6.5 suggests for lock tokens, and the
// one webdav.NewMemLS uses.
const tokenScheme = "opaquelocktoken:"

// scopedLS wraps a per-user lock system so that its tokens are unguessable and
// unique across users.
//
// webdav.NewMemLS issues tokens from a counter that restarts at zero for every
// instance, so two users' first locks are handed the identical token string. A
// token from one user's lock system would then validate against another's.
// Giving every user's lock system a random prefix removes the collision, and
// rejecting a token that does not carry this instance's prefix means a token
// belonging to another user is not merely useless but explicitly refused.
// See DECISIONS.md, D-020.
type scopedLS struct {
	inner  webdav.LockSystem
	prefix string
}

var _ webdav.LockSystem = (*scopedLS)(nil)

// newScopedLS returns an in-memory lock system with a random token namespace.
func newScopedLS() (*scopedLS, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	return &scopedLS{inner: webdav.NewMemLS(), prefix: hex.EncodeToString(b[:])}, nil
}

// wrap turns an inner token into the externally visible Coded-URL. webdav's
// memLS hands out bare decimal counters ("1", "2", ...), which are neither
// unique across users nor valid URIs, so this also fixes the token syntax.
func (s *scopedLS) wrap(token string) string {
	return tokenScheme + s.prefix + "-" + token
}

func (s *scopedLS) unwrap(token string) (string, bool) {
	want := tokenScheme + s.prefix + "-"
	if !strings.HasPrefix(token, want) {
		return "", false
	}
	return token[len(want):], true
}

func (s *scopedLS) Create(now time.Time, details webdav.LockDetails) (string, error) {
	tok, err := s.inner.Create(now, details)
	if err != nil {
		return "", err
	}
	return s.wrap(tok), nil
}

func (s *scopedLS) Refresh(now time.Time, token string, duration time.Duration) (webdav.LockDetails, error) {
	inner, ok := s.unwrap(token)
	if !ok {
		return webdav.LockDetails{}, webdav.ErrNoSuchLock
	}
	return s.inner.Refresh(now, inner, duration)
}

func (s *scopedLS) Unlock(now time.Time, token string) error {
	inner, ok := s.unwrap(token)
	if !ok {
		return webdav.ErrNoSuchLock
	}
	return s.inner.Unlock(now, inner)
}

func (s *scopedLS) Confirm(now time.Time, name0, name1 string, conditions ...webdav.Condition) (func(), error) {
	inner := make([]webdav.Condition, 0, len(conditions))
	for _, c := range conditions {
		if c.Token == "" {
			// An ETag-only condition carries no lock token to translate.
			inner = append(inner, c)
			continue
		}
		tok, ok := s.unwrap(c.Token)
		if !ok {
			// The token was minted by a different user's lock system (or is
			// fabricated). Refuse rather than ignore it.
			return nil, webdav.ErrConfirmationFailed
		}
		c.Token = tok
		inner = append(inner, c)
	}
	return s.inner.Confirm(now, name0, name1, inner...)
}
