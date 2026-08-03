//go:build integration

package integration

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// binaryPath is the goldencloud binary under test.
var binaryPath string

func TestMain(m *testing.M) {
	binaryPath = os.Getenv("GOLDENCLOUD_BINARY")
	tmp := ""
	if binaryPath == "" {
		var err error
		tmp, err = os.MkdirTemp("", "goldencloud-bin")
		if err != nil {
			fmt.Fprintln(os.Stderr, "integration: cannot create a temp dir:", err)
			os.Exit(1)
		}
		binaryPath = filepath.Join(tmp, "goldencloud")
		cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/goldencloud")
		cmd.Dir = ".."
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "integration: cannot build the binary:", err)
			os.Exit(1)
		}
	}
	if _, err := os.Stat(binaryPath); err != nil {
		fmt.Fprintln(os.Stderr, "integration: GOLDENCLOUD_BINARY is not usable:", err)
		os.Exit(1)
	}
	code := m.Run()
	if tmp != "" {
		_ = os.RemoveAll(tmp)
	}
	os.Exit(code)
}

// lockedBuffer collects a subprocess's stderr without racing the test goroutine.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// server is a running goldencloud process with a temporary storage root.
type server struct {
	t         *testing.T
	dir       string
	storage   string
	cfgPath   string
	usersPath string
	addr      string
	cmd       *exec.Cmd
	stderr    *lockedBuffer
	passwords map[string]string
	stopped   bool
}

// freePort asks the kernel for an unused port and gives it straight back. The
// window between the two is small enough in practice, and the alternative —
// parsing the port out of the server's log — couples the tests to a log line.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// startServer creates a storage root, adds the named users with the real admin
// CLI, and starts the real server against them.
func startServer(t *testing.T, usernames ...string) *server {
	t.Helper()
	dir := t.TempDir()
	storage := filepath.Join(dir, "storage")
	if err := os.Mkdir(storage, 0o755); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	s := &server{
		t:         t,
		dir:       dir,
		storage:   storage,
		cfgPath:   filepath.Join(dir, "config.yaml"),
		usersPath: filepath.Join(dir, "users.yaml"),
		addr:      fmt.Sprintf("127.0.0.1:%d", port),
		passwords: map[string]string{},
	}
	cfg := fmt.Sprintf(`listen: "127.0.0.1:%d"
storage_root: "%s"
users_file: "%s"
log_level: "debug"
require_mountpoint: false
tls:
  enabled: false
trusted_proxy:
  enabled: false
`, port, storage, s.usersPath)
	if err := os.WriteFile(s.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range usernames {
		s.addUser(name, name+"-correct-horse")
	}
	s.start()
	return s
}

// addUser runs the real "goldencloud user add" against the same config.
func (s *server) addUser(name, password string) {
	s.t.Helper()
	cmd := exec.Command(binaryPath, "user", "add", "--config", s.cfgPath, "--password-stdin", name)
	cmd.Stdin = strings.NewReader(password)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("user add %s: %v\n%s", name, err, out)
	}
	s.passwords[name] = password
}

func (s *server) removeUser(name string) {
	s.t.Helper()
	cmd := exec.Command(binaryPath, "user", "remove", "--config", s.cfgPath, name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("user remove %s: %v\n%s", name, err, out)
	}
	delete(s.passwords, name)
}

func (s *server) start() {
	s.t.Helper()
	s.stderr = &lockedBuffer{}
	s.cmd = exec.Command(binaryPath, "serve", "--config", s.cfgPath)
	s.cmd.Stdout = s.stderr
	s.cmd.Stderr = s.stderr
	if err := s.cmd.Start(); err != nil {
		s.t.Fatalf("starting the server: %v", err)
	}
	s.t.Cleanup(s.stop)
	s.waitReady()
}

func (s *server) waitReady() {
	s.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", s.addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
			s.t.Fatalf("the server exited before it listened:\n%s", s.stderr.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	s.t.Fatalf("the server never listened on %s:\n%s", s.addr, s.stderr.String())
}

func (s *server) stop() {
	if s.stopped || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	s.stopped = true
	_ = s.cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
}

// reload sends SIGHUP, which makes the server re-read users.yaml.
func (s *server) reload() {
	s.t.Helper()
	if err := s.cmd.Process.Signal(syscallSIGHUP); err != nil {
		s.t.Fatalf("SIGHUP: %v", err)
	}
	// Give the signal handler a moment; there is no synchronous handshake.
	time.Sleep(300 * time.Millisecond)
}

func (s *server) alive() bool {
	if s.cmd == nil || s.cmd.Process == nil {
		return false
	}
	return s.cmd.Process.Signal(syscallSignal0) == nil
}

// openFDs counts the server process's open file descriptors, which is how the
// reconnect test detects a leak.
func (s *server) openFDs() int {
	ents, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", s.cmd.Process.Pid))
	if err != nil {
		s.t.Skipf("cannot read the server's fd table: %v", err)
	}
	return len(ents)
}

func (s *server) userDir(name string) string { return filepath.Join(s.storage, name) }

// --- a small WebDAV client ---------------------------------------------------

type davClient struct {
	t    *testing.T
	base string
	user string
	pass string
	http *http.Client
}

func (s *server) client(user string) *davClient {
	s.t.Helper()
	pass, ok := s.passwords[user]
	if !ok {
		s.t.Fatalf("no password recorded for %q", user)
	}
	return s.clientWith(user, pass)
}

func (s *server) clientWith(user, pass string) *davClient {
	return &davClient{
		t:    s.t,
		base: "http://" + s.addr,
		user: user,
		pass: pass,
		http: &http.Client{
			Timeout: 10 * time.Minute,
			Transport: &http.Transport{
				Proxy:               nil,
				MaxIdleConnsPerHost: 16,
				DisableCompression:  true,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// request builds a request whose path goes on the wire exactly as given, with
// no client-side cleaning — which matters when the path is a traversal attempt.
func (c *davClient) request(method, rawPath string, body io.Reader) *http.Request {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base, body)
	if err != nil {
		c.t.Fatal(err)
	}
	req.URL.Opaque = rawPath
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	return req
}

func (c *davClient) do(req *http.Request) *http.Response {
	c.t.Helper()
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", req.Method, req.URL.Opaque, err)
	}
	return resp
}

// call performs a request and returns status and body, closing the body.
func (c *davClient) call(method, rawPath string, body io.Reader, headers ...string) (int, string) {
	c.t.Helper()
	req := c.request(method, rawPath, body)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp := c.do(req)
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		c.t.Fatalf("%s %s: reading the body: %v", method, rawPath, err)
	}
	return resp.StatusCode, string(b)
}

func (c *davClient) put(path, content string) (int, string) {
	return c.call(http.MethodPut, path, strings.NewReader(content))
}

func (c *davClient) get(path string) (int, string) {
	return c.call(http.MethodGet, path, nil)
}

func (c *davClient) mkcol(path string) (int, string) {
	return c.call("MKCOL", path, nil)
}

func (c *davClient) delete(path string) (int, string) {
	return c.call(http.MethodDelete, path, nil)
}

func (c *davClient) propfind(path, depth string) (int, string) {
	return c.call("PROPFIND", path, nil, "Depth", depth)
}

func (c *davClient) move(from, to string) (int, string) {
	return c.call("MOVE", from, nil, "Destination", c.base+to, "Overwrite", "T")
}

func (c *davClient) copy(from, to string) (int, string) {
	return c.call("COPY", from, nil, "Destination", c.base+to, "Overwrite", "T")
}

const lockRequest = `<?xml version="1.0" encoding="utf-8"?>
<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope>
<D:locktype><D:write/></D:locktype><D:owner><D:href>integration</D:href></D:owner></D:lockinfo>`

// lock returns the status and the Lock-Token header.
func (c *davClient) lock(path string) (int, string) {
	c.t.Helper()
	req := c.request("LOCK", path, strings.NewReader(lockRequest))
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Timeout", "Second-60")
	resp := c.do(req)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, resp.Header.Get("Lock-Token")
}

func (c *davClient) unlock(path, token string) int {
	c.t.Helper()
	req := c.request("UNLOCK", path, nil)
	req.Header.Set("Lock-Token", token)
	resp := c.do(req)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// basicHeader is the Authorization header value for raw-socket requests.
func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// addUserPassword changes an existing user's password with the real CLI.
func (s *server) addUserPassword(t *testing.T, name, password string) {
	t.Helper()
	cmd := exec.Command(binaryPath, "user", "passwd", "--config", s.cfgPath, "--password-stdin", name)
	cmd.Stdin = strings.NewReader(password)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("user passwd %s: %v\n%s", name, err, out)
	}
	s.passwords[name] = password
}
