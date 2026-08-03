// Package webdavx wires golang.org/x/net/webdav to one fsjail per user and
// smooths over the Windows WebDAV redirector's expectations.
//
// Each authenticated user gets their own webdav.Handler, their own
// fsjail.Dir and their own in-memory lock system. Nothing is shared between
// users, so one user can neither see nor steal another's locks, and a
// mis-scoped path cannot address another user's file because the filesystem
// object itself cannot reach outside their folder.
package webdavx

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"github.com/PV80/GoldenCloud/server/internal/auth"
	"github.com/PV80/GoldenCloud/server/internal/config"
	"github.com/PV80/GoldenCloud/server/internal/fsjail"
)

// davCompliance is the compliance class advertised in the DAV header. Class 2
// (locking) is what makes Windows treat the share as writable; class 3 is
// RFC 4918 conformance.
const davCompliance = "1, 2, 3"

// allowedMethods is advertised in Allow and Public. The Windows redirector
// reads Public on some builds and Allow on others.
const allowedMethods = "OPTIONS, GET, HEAD, POST, PUT, DELETE, TRACE, COPY, MOVE, MKCOL, PROPFIND, PROPPATCH, LOCK, UNLOCK"

// propfindFiniteDepth is the RFC 4918 §9.1 error body returned when a client
// asks for Depth: infinity. See DECISIONS.md, D-019.
const propfindFiniteDepth = xml.Header + `<D:error xmlns:D="DAV:"><D:propfind-finite-depth/></D:error>` + "\n"

// Options configures a Server.
type Options struct {
	// StorageRoot is the directory containing every user folder.
	StorageRoot string
	// Logger receives request and error logs. Defaults to slog.Default().
	Logger *slog.Logger
}

// Server routes authenticated requests to per-user WebDAV handlers.
type Server struct {
	storageRoot string
	log         *slog.Logger

	mu     sync.Mutex
	users  map[string]*userDAV
	closed bool
}

type userDAV struct {
	// dir is the absolute folder this handler was built for. A reload can
	// reassign a user's root; handlerFor compares dir against the folder the
	// *current* user record resolves to and rebuilds on mismatch, so a stale
	// handler can never keep serving a folder the user no longer owns.
	dir     string
	jail    *fsjail.Dir
	handler *webdav.Handler
}

// New returns a Server serving user folders beneath opt.StorageRoot.
func New(opt Options) *Server {
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	return &Server{
		storageRoot: opt.StorageRoot,
		log:         opt.Logger,
		users:       map[string]*userDAV{},
	}
}

// Handler returns an http.Handler that answers OPTIONS without credentials —
// the Windows redirector probes it before it will prompt for a password — and
// authenticates everything else before dispatching to the user's WebDAV
// handler.
func (s *Server) Handler(a *auth.Authenticator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setDAVHeaders(w.Header())
		if r.Method == http.MethodOptions {
			// The response is identical for every path and reveals nothing, so
			// it is safe — and necessary — to answer it unauthenticated.
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusOK)
			return
		}
		user, ok := a.Authenticate(w, r)
		if !ok {
			return
		}
		s.serve(w, r, user)
	})
}

// ServeHTTP serves a request that has already been authenticated by
// auth.Authenticator's middleware.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setDAVHeaders(w.Header())
	if r.Method == http.MethodOptions {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
		return
	}
	user, ok := auth.UserFrom(r.Context())
	if !ok {
		http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
		return
	}
	s.serve(w, r, user)
}

func setDAVHeaders(h http.Header) {
	h.Set("DAV", davCompliance)
	h.Set("MS-Author-Via", "DAV")
	h.Set("Allow", allowedMethods)
	h.Set("Public", allowedMethods)
	h.Set("Accept-Ranges", "bytes")
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request, user config.User) {
	// D-019: bound PROPFIND. An unbounded Depth: infinity walk over a large
	// share is a denial of service against a Raspberry Pi, and RFC 4918 §9.1
	// explicitly allows refusing it.
	if r.Method == "PROPFIND" {
		switch depth := r.Header.Get("Depth"); {
		case depth == "":
			// RFC 4918 defaults an absent Depth to infinity. Since we refuse
			// infinity, defaulting to 1 gives such a client a useful listing
			// instead of an error it cannot act on.
			r.Header.Set("Depth", "1")
		case strings.EqualFold(depth, "infinity"):
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, propfindFiniteDepth)
			s.log.Debug("refused PROPFIND Depth: infinity",
				slog.String("user", user.Username), slog.String("path", r.URL.Path))
			return
		}
	}

	ud, err := s.handlerFor(user)
	if err != nil {
		s.log.Error("user folder unavailable",
			slog.String("user", user.Username), slog.Any("err", err))
		http.Error(w, "503 Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	rec := &recorder{ResponseWriter: w, status: http.StatusOK}
	start := time.Now()
	ud.handler.ServeHTTP(rec, r)
	s.log.Debug("webdav request",
		slog.String("user", user.Username),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", rec.status),
		slog.Int64("bytes", rec.written),
		slog.Duration("took", time.Since(start)))
}

// handlerFor returns the user's WebDAV handler, creating it on first use. A
// cached handler is only reused while it still points at the folder the user's
// current record resolves to; if users.yaml reassigned the root and was
// reloaded, the old handler (and the open directory handle inside its jail) is
// dropped and a fresh one is built against the new folder.
func (s *Server) handlerFor(user config.User) (*userDAV, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("webdavx: server is closed")
	}
	dir := user.Dir(s.storageRoot)
	if ud, ok := s.users[user.Username]; ok {
		if ud.dir == dir {
			return ud, nil
		}
		s.log.Info("user folder changed, rebuilding handler",
			slog.String("user", user.Username))
		delete(s.users, user.Username)
		_ = ud.jail.Close()
	}
	jail, err := fsjail.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("user %q: %w", user.Username, err)
	}
	ls, err := newScopedLS()
	if err != nil {
		jail.Close()
		return nil, fmt.Errorf("user %q: lock system: %w", user.Username, err)
	}
	ud := &userDAV{dir: dir, jail: jail}
	ud.handler = &webdav.Handler{
		FileSystem: jail,
		// A fresh in-memory lock system per user, in its own random token
		// namespace, so a user can neither enumerate nor present another
		// user's lock tokens.
		LockSystem: ls,
		Logger: func(r *http.Request, err error) {
			if err == nil {
				return
			}
			s.log.Debug("webdav handler error",
				slog.String("user", user.Username),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Any("err", err))
		},
	}
	s.users[user.Username] = ud
	return ud, nil
}

// Forget drops a user's cached handler and lock system. The next request from
// that user rebuilds both.
func (s *Server) Forget(username string) {
	s.mu.Lock()
	ud := s.users[username]
	delete(s.users, username)
	s.mu.Unlock()
	if ud != nil {
		_ = ud.jail.Close()
	}
}

// Retain drops every cached handler whose username is not in keep. It is called
// after users.yaml is reloaded so that a deleted user's jail is released.
func (s *Server) Retain(keep map[string]bool) {
	s.mu.Lock()
	var dropped []*userDAV
	for name, ud := range s.users {
		if !keep[name] {
			dropped = append(dropped, ud)
			delete(s.users, name)
		}
	}
	s.mu.Unlock()
	for _, ud := range dropped {
		_ = ud.jail.Close()
	}
}

// Close releases every open user folder.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	all := s.users
	s.users = map[string]*userDAV{}
	s.mu.Unlock()
	var firstErr error
	for _, ud := range all {
		if err := ud.jail.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// recorder captures the status and byte count for logging while preserving the
// io.ReaderFrom and http.Flusher optimisations that keep large transfers
// streaming rather than buffering.
type recorder struct {
	http.ResponseWriter
	status      int
	written     int64
	wroteHeader bool
}

func (rec *recorder) WriteHeader(code int) {
	if rec.wroteHeader {
		return
	}
	rec.wroteHeader = true
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *recorder) Write(b []byte) (int, error) {
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.written += int64(n)
	return n, err
}

func (rec *recorder) ReadFrom(src io.Reader) (int64, error) {
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}
	if rf, ok := rec.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(src)
		rec.written += n
		return n, err
	}
	n, err := io.Copy(rec.ResponseWriter, src)
	rec.written += n
	return n, err
}

func (rec *recorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
