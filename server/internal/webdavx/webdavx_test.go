package webdavx_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/PV80/GoldenCloud/server/internal/auth"
	"github.com/PV80/GoldenCloud/server/internal/config"
	"github.com/PV80/GoldenCloud/server/internal/webdavx"
)

const testCost = bcrypt.MinCost

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type env struct {
	srv     *httptest.Server
	storage string
	dav     *webdavx.Server
}

func newEnv(t *testing.T, usernames ...string) *env {
	t.Helper()
	storage := t.TempDir()
	users := make([]config.User, 0, len(usernames))
	for _, name := range usernames {
		if err := os.Mkdir(filepath.Join(storage, name), 0o700); err != nil {
			t.Fatal(err)
		}
		h, err := bcrypt.GenerateFromPassword([]byte(name+"-pw"), testCost)
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, config.User{Username: name, PasswordHash: string(h), Root: name})
	}
	store := auth.NewStore(users)
	a := auth.New(store, auth.Options{Logger: quietLogger(), FailureThreshold: 1 << 30})
	dav := webdavx.New(webdavx.Options{StorageRoot: storage, Logger: quietLogger()})
	t.Cleanup(func() { _ = dav.Close() })
	e := &env{storage: storage, dav: dav}
	e.srv = httptest.NewServer(dav.Handler(a))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) req(t *testing.T, method, user, path string, body io.Reader) *http.Request {
	t.Helper()
	r, err := http.NewRequest(method, e.srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if user != "" {
		r.SetBasicAuth(user, user+"-pw")
	}
	return r
}

func (e *env) do(t *testing.T, method, user, path string, body io.Reader) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(e.req(t, method, user, path, body))
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func drain(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// --- protocol surface ------------------------------------------------------------

// The Windows WebDAV redirector sends OPTIONS / with no credentials before it
// will even prompt for a password. If that 401s, the drive never maps.
func TestOptionsRootWithoutCredentials(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	for _, path := range []string{"/", "/alice", "/anything/at/all"} {
		resp := e.do(t, http.MethodOptions, "", path, nil)
		body := drain(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("OPTIONS %s = %d, want 200 (the Windows redirector probes before authenticating)", path, resp.StatusCode)
		}
		if body != "" {
			t.Errorf("OPTIONS %s returned a body: %q", path, body)
		}
		if got := resp.Header.Get("DAV"); got != "1, 2, 3" {
			t.Errorf("OPTIONS %s DAV = %q, want %q", path, got, "1, 2, 3")
		}
		if got := resp.Header.Get("MS-Author-Via"); got != "DAV" {
			t.Errorf("OPTIONS %s MS-Author-Via = %q, want DAV", path, got)
		}
		for _, verb := range []string{"PROPFIND", "PROPPATCH", "MKCOL", "MOVE", "COPY", "LOCK", "UNLOCK", "PUT", "DELETE"} {
			if !strings.Contains(resp.Header.Get("Allow"), verb) {
				t.Errorf("OPTIONS %s Allow = %q, missing %s", path, resp.Header.Get("Allow"), verb)
			}
		}
		if resp.Header.Get("Public") == "" {
			t.Errorf("OPTIONS %s: no Public header (some Windows builds require it)", path)
		}
		if resp.Header.Get("WWW-Authenticate") != "" {
			t.Errorf("OPTIONS %s issued an auth challenge", path)
		}
	}
}

func TestOptionsAsterisk(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	req, err := http.NewRequest(http.MethodOptions, e.srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.URL.Opaque = "*"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OPTIONS * = %d, want 200", resp.StatusCode)
	}
}

func TestDAVHeadersOnEveryResponse(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	resp := e.do(t, http.MethodPut, "alice", "/f.txt", strings.NewReader("x"))
	drain(t, resp)
	if got := resp.Header.Get("DAV"); got != "1, 2, 3" {
		t.Errorf("PUT DAV = %q", got)
	}
	if got := resp.Header.Get("MS-Author-Via"); got != "DAV" {
		t.Errorf("PUT MS-Author-Via = %q", got)
	}
}

func TestUnauthenticatedNonOptionsIsChallenged(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	resp := e.do(t, "PROPFIND", "", "/", nil)
	drain(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("PROPFIND without credentials = %d, want 401", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), `realm="GoldenCloud"`) {
		t.Fatalf("WWW-Authenticate = %q", resp.Header.Get("WWW-Authenticate"))
	}
}

// --- Windows client quirks --------------------------------------------------------

func TestTranslateHeaderIsTolerated(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	drain(t, e.do(t, http.MethodPut, "alice", "/f.txt", strings.NewReader("payload")))

	req := e.req(t, http.MethodGet, "alice", "/f.txt", nil)
	req.Header.Set("Translate", "f")
	req.Header.Set("X-MSDAVEXT_Error", "917504; Access+denied.")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := drain(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET with Translate: f = %d, want 200", resp.StatusCode)
	}
	if body != "payload" {
		t.Fatalf("body = %q, want %q", body, "payload")
	}
}

// D-019: PROPFIND with Depth: infinity is refused, per RFC 4918 §9.1, rather
// than walking an arbitrarily large tree on a Raspberry Pi.
func TestPropfindDepthInfinityIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	drain(t, e.do(t, http.MethodPut, "alice", "/f.txt", strings.NewReader("x")))

	for _, depth := range []string{"infinity", "Infinity", "INFINITY"} {
		req := e.req(t, "PROPFIND", "alice", "/", nil)
		req.Header.Set("Depth", depth)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body := drain(t, resp)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("PROPFIND Depth: %s = %d, want 403", depth, resp.StatusCode)
		}
		if !strings.Contains(body, "propfind-finite-depth") {
			t.Fatalf("403 body should carry DAV:propfind-finite-depth, got %q", body)
		}
		if got := resp.Header.Get("DAV"); got != "1, 2, 3" {
			t.Fatalf("DAV header on the refusal = %q", got)
		}
	}
}

func TestPropfindDepthZeroAndOne(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	drain(t, e.do(t, "MKCOL", "alice", "/dir", nil))
	drain(t, e.do(t, http.MethodPut, "alice", "/dir/f.txt", strings.NewReader("x")))

	for _, tc := range []struct {
		depth string
		want  int
	}{{"0", 1}, {"1", 2}, {"", 2}} {
		req := e.req(t, "PROPFIND", "alice", "/dir", nil)
		if tc.depth != "" {
			req.Header.Set("Depth", tc.depth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body := drain(t, resp)
		if resp.StatusCode != http.StatusMultiStatus {
			t.Fatalf("PROPFIND Depth %q = %d, want 207", tc.depth, resp.StatusCode)
		}
		if n := strings.Count(body, "<D:response>"); n != tc.want {
			t.Fatalf("PROPFIND Depth %q returned %d responses, want %d:\n%s", tc.depth, n, tc.want, body)
		}
	}
}

// --- the property that matters: isolation -----------------------------------------

func TestUserIsolation(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice", "bob")
	if err := os.WriteFile(filepath.Join(e.storage, "bob", "bobs-payslip.txt"), []byte("BOB SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.storage, "root-level.txt"), []byte("STORAGE ROOT"), 0o600); err != nil {
		t.Fatal(err)
	}

	paths := []string{
		"/bobs-payslip.txt",
		"/bob/bobs-payslip.txt",
		"/../bob/bobs-payslip.txt",
		"/..%2fbob%2fbobs-payslip.txt",
		"/%2e%2e/bob/bobs-payslip.txt",
		"/....//bob/bobs-payslip.txt",
		"/root-level.txt",
		"/../root-level.txt",
		"/../../../../etc/passwd",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			resp := e.do(t, http.MethodGet, "alice", p, nil)
			body := drain(t, resp)
			if strings.Contains(body, "BOB SECRET") || strings.Contains(body, "STORAGE ROOT") ||
				strings.Contains(body, "root:x:") {
				t.Fatalf("GET %s leaked another user's data (status %d)", p, resp.StatusCode)
			}
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("GET %s = 200, want a rejection", p)
			}
		})
	}

	// Nor may alice write into bob's tree.
	resp := e.do(t, http.MethodPut, "alice", "/../bob/planted.txt", strings.NewReader("planted"))
	drain(t, resp)
	if _, err := os.Stat(filepath.Join(e.storage, "bob", "planted.txt")); err == nil {
		t.Fatal("alice created a file in bob's folder")
	}
	if _, err := os.Stat(filepath.Join(e.storage, "planted.txt")); err == nil {
		t.Fatal("alice created a file in the storage root")
	}
}

func TestSameRelativePathIsDifferentFilePerUser(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice", "bob")
	drain(t, e.do(t, http.MethodPut, "alice", "/shared-name.txt", strings.NewReader("alice data")))
	drain(t, e.do(t, http.MethodPut, "bob", "/shared-name.txt", strings.NewReader("bob data")))

	if got := drain(t, e.do(t, http.MethodGet, "alice", "/shared-name.txt", nil)); got != "alice data" {
		t.Fatalf("alice sees %q", got)
	}
	if got := drain(t, e.do(t, http.MethodGet, "bob", "/shared-name.txt", nil)); got != "bob data" {
		t.Fatalf("bob sees %q", got)
	}
}

func TestPropfindNeverListsAnotherUser(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice", "bob")
	drain(t, e.do(t, http.MethodPut, "bob", "/bob-only.txt", strings.NewReader("x")))
	drain(t, e.do(t, http.MethodPut, "alice", "/alice-only.txt", strings.NewReader("x")))

	req := e.req(t, "PROPFIND", "alice", "/", nil)
	req.Header.Set("Depth", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := drain(t, resp)
	if strings.Contains(body, "bob-only.txt") || strings.Contains(body, "bob") {
		t.Fatalf("alice's PROPFIND mentions bob:\n%s", body)
	}
	if !strings.Contains(body, "alice-only.txt") {
		t.Fatalf("alice's PROPFIND is missing her own file:\n%s", body)
	}
}

// --- locking -----------------------------------------------------------------------

const lockBody = `<?xml version="1.0" encoding="utf-8"?>
<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope>
<D:locktype><D:write/></D:locktype><D:owner><D:href>test</D:href></D:owner></D:lockinfo>`

func lockToken(t *testing.T, e *env, user, path string) string {
	t.Helper()
	req := e.req(t, "LOCK", user, path, strings.NewReader(lockBody))
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Timeout", "Second-3600")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := drain(t, resp)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("LOCK %s as %s = %d: %s", path, user, resp.StatusCode, body)
	}
	tok := resp.Header.Get("Lock-Token")
	if tok == "" {
		t.Fatalf("LOCK %s as %s returned no Lock-Token: %s", path, user, body)
	}
	return tok
}

func TestLockSystemsArePerUser(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice", "bob")
	drain(t, e.do(t, http.MethodPut, "alice", "/doc.txt", strings.NewReader("a")))
	drain(t, e.do(t, http.MethodPut, "bob", "/doc.txt", strings.NewReader("b")))

	aliceTok := lockToken(t, e, "alice", "/doc.txt")
	// Bob must be able to lock the same *path* — it is a different file.
	bobTok := lockToken(t, e, "bob", "/doc.txt")
	if aliceTok == bobTok {
		t.Fatal("two users were issued the same lock token")
	}

	// Alice's token must be worthless against bob's resource.
	req := e.req(t, http.MethodPut, "bob", "/doc.txt", strings.NewReader("stolen"))
	req.Header.Set("If", "("+strings.Trim(aliceTok, "<>")+")")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, resp)
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		t.Fatalf("bob's locked resource was written with alice's token (status %d)", resp.StatusCode)
	}
	if got := drain(t, e.do(t, http.MethodGet, "bob", "/doc.txt", nil)); got == "stolen" {
		t.Fatal("alice's lock token let a write through on bob's file")
	}

	// Bob's own token works.
	req = e.req(t, http.MethodPut, "bob", "/doc.txt", strings.NewReader("bobs write"))
	req.Header.Set("If", "("+strings.Trim(bobTok, "<>")+")")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, resp)
	if resp.StatusCode >= 300 {
		t.Fatalf("bob could not write with his own token: %d", resp.StatusCode)
	}
}

func TestUnlock(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	drain(t, e.do(t, http.MethodPut, "alice", "/doc.txt", strings.NewReader("a")))
	tok := lockToken(t, e, "alice", "/doc.txt")

	req := e.req(t, "UNLOCK", "alice", "/doc.txt", nil)
	req.Header.Set("Lock-Token", tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, resp)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("UNLOCK = %d, want 204", resp.StatusCode)
	}
	// Now an unconditional write succeeds again.
	resp = e.do(t, http.MethodPut, "alice", "/doc.txt", strings.NewReader("free"))
	drain(t, resp)
	if resp.StatusCode >= 300 {
		t.Fatalf("PUT after UNLOCK = %d", resp.StatusCode)
	}
}

// --- ordinary file operations ---------------------------------------------------

func TestFileOperations(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")

	if resp := e.do(t, "MKCOL", "alice", "/folder", nil); resp.StatusCode != http.StatusCreated {
		drain(t, resp)
		t.Fatalf("MKCOL = %d, want 201", resp.StatusCode)
	} else {
		drain(t, resp)
	}
	resp := e.do(t, http.MethodPut, "alice", "/folder/a.txt", strings.NewReader("content"))
	drain(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT = %d, want 201", resp.StatusCode)
	}
	if got := drain(t, e.do(t, http.MethodGet, "alice", "/folder/a.txt", nil)); got != "content" {
		t.Fatalf("GET = %q", got)
	}

	// HEAD
	resp = e.do(t, http.MethodHead, "alice", "/folder/a.txt", nil)
	drain(t, resp)
	if resp.StatusCode != http.StatusOK || resp.ContentLength != 7 {
		t.Fatalf("HEAD = %d len %d", resp.StatusCode, resp.ContentLength)
	}

	// MOVE
	req := e.req(t, "MOVE", "alice", "/folder/a.txt", nil)
	req.Header.Set("Destination", e.srv.URL+"/folder/b.txt")
	req.Header.Set("Overwrite", "T")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, resp)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("MOVE = %d", resp.StatusCode)
	}
	if got := drain(t, e.do(t, http.MethodGet, "alice", "/folder/b.txt", nil)); got != "content" {
		t.Fatalf("GET after MOVE = %q", got)
	}

	// COPY
	req = e.req(t, "COPY", "alice", "/folder/b.txt", nil)
	req.Header.Set("Destination", e.srv.URL+"/folder/c.txt")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("COPY = %d", resp.StatusCode)
	}

	// DELETE
	resp = e.do(t, http.MethodDelete, "alice", "/folder/c.txt", nil)
	drain(t, resp)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204", resp.StatusCode)
	}
	resp = e.do(t, http.MethodGet, "alice", "/folder/c.txt", nil)
	drain(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET after DELETE = %d, want 404", resp.StatusCode)
	}
}

func TestMoveAcrossJailBoundaryIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice", "bob")
	drain(t, e.do(t, http.MethodPut, "alice", "/mine.txt", strings.NewReader("mine")))

	req := e.req(t, "MOVE", "alice", "/mine.txt", nil)
	req.Header.Set("Destination", e.srv.URL+"/../bob/stolen.txt")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, resp)
	if _, err := os.Stat(filepath.Join(e.storage, "bob", "stolen.txt")); err == nil {
		t.Fatal("MOVE escaped the jail")
	}
}

func TestRangeRequest(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	drain(t, e.do(t, http.MethodPut, "alice", "/f.bin", strings.NewReader("0123456789")))
	req := e.req(t, http.MethodGet, "alice", "/f.bin", nil)
	req.Header.Set("Range", "bytes=2-5")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := drain(t, resp)
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("Range GET = %d, want 206", resp.StatusCode)
	}
	if body != "2345" {
		t.Fatalf("Range body = %q, want %q", body, "2345")
	}
}

func TestMissingUserFolderIsUnavailableNotACrash(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	if err := os.RemoveAll(filepath.Join(e.storage, "alice")); err != nil {
		t.Fatal(err)
	}
	resp := e.do(t, http.MethodGet, "alice", "/f.txt", nil)
	drain(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET with a missing user folder = %d, want 503", resp.StatusCode)
	}
}

func TestPropfindXMLIsWellFormed(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	drain(t, e.do(t, http.MethodPut, "alice", "/f.txt", strings.NewReader("hello")))
	req := e.req(t, "PROPFIND", "alice", "/", nil)
	req.Header.Set("Depth", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := drain(t, resp)
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("PROPFIND response is not well-formed XML: %v\n%s", err, body)
		}
	}
}

// --- concurrency ----------------------------------------------------------------

func TestConcurrentUsers(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice", "bob", "carol")
	var wg sync.WaitGroup
	var failures atomic.Int64
	for _, user := range []string{"alice", "bob", "carol"} {
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(user string, i int) {
				defer wg.Done()
				path := fmt.Sprintf("/f%d.txt", i)
				want := fmt.Sprintf("%s-%d", user, i)
				resp := e.do(t, http.MethodPut, user, path, strings.NewReader(want))
				drain(t, resp)
				if resp.StatusCode >= 300 {
					failures.Add(1)
					return
				}
				if got := drain(t, e.do(t, http.MethodGet, user, path, nil)); got != want {
					t.Errorf("%s %s = %q, want %q", user, path, got, want)
					failures.Add(1)
				}
			}(user, i)
		}
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d concurrent operations failed", failures.Load())
	}
}

// --- streaming ------------------------------------------------------------------

// patternReader produces a deterministic, incompressible-ish stream without
// holding it in memory, so the test can push hundreds of megabytes through the
// server while watching the server's heap.
type patternReader struct {
	remaining int64
	block     []byte
	off       int
}

func newPatternReader(n int64) *patternReader {
	block := make([]byte, 1<<16)
	rnd := rand.New(rand.NewSource(42))
	rnd.Read(block)
	return &patternReader{remaining: n, block: block}
}

func (p *patternReader) Read(b []byte) (int, error) {
	if p.remaining <= 0 {
		return 0, io.EOF
	}
	n := copy(b, p.block[p.off:])
	if int64(n) > p.remaining {
		n = int(p.remaining)
	}
	p.off = (p.off + n) % len(p.block)
	p.remaining -= int64(n)
	return n, nil
}

func patternSum(n int64) string {
	h := sha256.New()
	if _, err := io.Copy(h, newPatternReader(n)); err != nil {
		panic(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestLargeTransferDoesNotBufferInMemory pushes a multi-hundred-megabyte body
// through PUT and pulls it back with GET, sampling the heap throughout. A
// handler that buffered the body would show a heap proportional to the payload.
func TestLargeTransferDoesNotBufferInMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("large transfer test")
	}
	t.Parallel()
	const size = 320 << 20  // 320 MiB
	const budget = 96 << 20 // peak heap growth allowed
	e := newEnv(t, "alice")

	stop := make(chan struct{})
	var peak atomic.Uint64
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	done := make(chan struct{})
	go func() {
		defer close(done)
		var ms runtime.MemStats
		tick := time.NewTicker(15 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				runtime.ReadMemStats(&ms)
				for {
					old := peak.Load()
					if ms.HeapAlloc <= old || peak.CompareAndSwap(old, ms.HeapAlloc) {
						break
					}
				}
			}
		}
	}()

	req := e.req(t, http.MethodPut, "alice", "/big.bin", newPatternReader(size))
	req.ContentLength = size
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT big = %d", resp.StatusCode)
	}

	getResp := e.do(t, http.MethodGet, "alice", "/big.bin", nil)
	h := sha256.New()
	n, err := io.Copy(h, getResp.Body)
	getResp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	close(stop)
	<-done

	if n != size {
		t.Fatalf("GET returned %d bytes, want %d", n, size)
	}
	if got, want := hex.EncodeToString(h.Sum(nil)), patternSum(size); got != want {
		t.Fatalf("round-trip hash mismatch:\n got %s\nwant %s", got, want)
	}

	growth := int64(peak.Load()) - int64(base.HeapAlloc)
	t.Logf("payload %d MiB, peak heap %d MiB, growth %d MiB",
		size>>20, peak.Load()>>20, growth>>20)
	if growth > budget {
		t.Fatalf("heap grew by %d MiB transferring %d MiB — the body is being buffered",
			growth>>20, size>>20)
	}

	st, err := os.Stat(filepath.Join(e.storage, "alice", "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != size {
		t.Fatalf("stored size = %d, want %d", st.Size(), size)
	}
}

func TestPutOverwritesAtomicallyEnough(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	drain(t, e.do(t, http.MethodPut, "alice", "/f.txt", strings.NewReader("first version, long")))
	resp := e.do(t, http.MethodPut, "alice", "/f.txt", strings.NewReader("second"))
	drain(t, resp)
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusCreated {
		t.Fatalf("overwrite PUT = %d", resp.StatusCode)
	}
	if got := drain(t, e.do(t, http.MethodGet, "alice", "/f.txt", nil)); got != "second" {
		t.Fatalf("after overwrite = %q (truncation missing?)", got)
	}
}

func TestForgetDropsUserHandler(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	drain(t, e.do(t, http.MethodPut, "alice", "/f.txt", strings.NewReader("x")))
	e.dav.Forget("alice")
	// Still works: a new handler is built on demand.
	if got := drain(t, e.do(t, http.MethodGet, "alice", "/f.txt", nil)); got != "x" {
		t.Fatalf("after Forget, GET = %q", got)
	}
}

func TestRetainOnlyClosesRemovedUsers(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice", "bob")
	drain(t, e.do(t, http.MethodPut, "alice", "/f.txt", strings.NewReader("a")))
	drain(t, e.do(t, http.MethodPut, "bob", "/f.txt", strings.NewReader("b")))
	e.dav.Retain(map[string]bool{"alice": true})
	if got := drain(t, e.do(t, http.MethodGet, "alice", "/f.txt", nil)); got != "a" {
		t.Fatalf("alice broken after Retain: %q", got)
	}
	if got := drain(t, e.do(t, http.MethodGet, "bob", "/f.txt", nil)); got != "b" {
		t.Fatalf("bob broken after Retain: %q", got)
	}
}

// A user's root can be reassigned in users.yaml and reloaded over SIGHUP. The
// cached handler was built against the old folder — and holds it open — so the
// cache must notice the root changed and rebuild, or the user silently keeps
// their old folder until a restart. Worse, if the old folder is later assigned
// to a somebody else, two users would be serving the same directory.
func TestReloadedRootChangeTakesEffect(t *testing.T) {
	t.Parallel()
	storage := t.TempDir()
	for _, dir := range []string{"alice-old", "alice-new"} {
		if err := os.Mkdir(filepath.Join(storage, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	h, err := bcrypt.GenerateFromPassword([]byte("alice-pw"), testCost)
	if err != nil {
		t.Fatal(err)
	}
	store := auth.NewStore([]config.User{{Username: "alice", PasswordHash: string(h), Root: "alice-old"}})
	a := auth.New(store, auth.Options{Logger: quietLogger(), FailureThreshold: 1 << 30})
	dav := webdavx.New(webdavx.Options{StorageRoot: storage, Logger: quietLogger()})
	t.Cleanup(func() { _ = dav.Close() })
	srv := httptest.NewServer(dav.Handler(a))
	t.Cleanup(srv.Close)
	e := &env{srv: srv, storage: storage, dav: dav}

	drain(t, e.do(t, http.MethodPut, "alice", "/f.txt", strings.NewReader("old data")))
	if _, err := os.Stat(filepath.Join(storage, "alice-old", "f.txt")); err != nil {
		t.Fatalf("setup write did not land in the old folder: %v", err)
	}

	// The admin reassigns alice's folder and the server reloads users.yaml.
	store.Replace([]config.User{{Username: "alice", PasswordHash: string(h), Root: "alice-new"}})

	// Her old file must no longer be visible...
	resp := e.do(t, http.MethodGet, "alice", "/f.txt", nil)
	drain(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET after the root moved = %d, want 404 — the cached handler is still serving the old folder", resp.StatusCode)
	}
	// ...and new writes must land in the new folder, not the old one.
	drain(t, e.do(t, http.MethodPut, "alice", "/g.txt", strings.NewReader("new data")))
	if _, err := os.Stat(filepath.Join(storage, "alice-new", "g.txt")); err != nil {
		t.Fatalf("write after the root moved did not land in the new folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storage, "alice-old", "g.txt")); err == nil {
		t.Fatal("write after the root moved landed in the OLD folder")
	}
}

func TestPutEmptyBody(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "alice")
	resp := e.do(t, http.MethodPut, "alice", "/empty.txt", bytes.NewReader(nil))
	drain(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT empty = %d", resp.StatusCode)
	}
	st, err := os.Stat(filepath.Join(e.storage, "alice", "empty.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 0 {
		t.Fatalf("size = %d", st.Size())
	}
}
