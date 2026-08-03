//go:build integration

package integration

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 6. A connection dropped mid-transfer — the office wi-fi dying, a laptop lid
// closing, a tunnel blipping — must leave the server healthy: no leaked file
// descriptors, no leaked locks, and the next request served normally.
func TestReconnectMidTransfer(t *testing.T) {
	s := startServer(t, "alice")
	c := s.client("alice")

	// Warm the server up so the fd baseline is not measured before the first
	// user jail has been opened.
	if code, _ := c.put("/warmup.txt", "warm"); code != http.StatusCreated {
		t.Fatalf("warmup PUT = %d", code)
	}
	if code, _ := c.put("/contended.txt", "original"); code != http.StatusCreated {
		t.Fatalf("setup PUT = %d", code)
	}
	// A file big enough that a download can be abandoned part way through.
	big := strings.Repeat("0123456789abcdef", 4<<20) // 64 MiB
	if code, _ := c.put("/download.bin", big); code != http.StatusCreated {
		t.Fatalf("setup PUT of the big file = %d", code)
	}

	baseline := settledFDs(t, s)
	t.Logf("baseline open file descriptors: %d", baseline)

	const rounds = 25

	t.Run("upload aborted mid-body", func(t *testing.T) {
		for i := 0; i < rounds; i++ {
			abortUpload(t, s, fmt.Sprintf("/aborted-%d.bin", i))
		}
		if !s.alive() {
			t.Fatalf("the server died on an aborted upload:\n%s", s.stderr.String())
		}
	})

	t.Run("upload aborted on a locked path", func(t *testing.T) {
		// The interesting case: the handler takes a temporary lock for the
		// duration of the PUT. If an aborted body skipped the release, the
		// path would stay locked forever.
		for i := 0; i < rounds; i++ {
			abortUpload(t, s, "/contended.txt")
		}
		// abortUpload sends a RST and returns without waiting, so the handler
		// for the final abort may still be running its deferred release of the
		// temporary per-request PUT lock when we get here. That release is
		// asynchronous to us, so a single immediate LOCK can race it and see a
		// transient 423. A genuine leak, by contrast, never clears — so poll for
		// a short window and only fail if the path stays locked, which is the
		// real property under test. (Same async-teardown tolerance settledFDs
		// already applies to the descriptor count.)
		code, token := lockUntilFree(t, c, "/contended.txt", 5*time.Second)
		if code != http.StatusOK && code != http.StatusCreated {
			t.Fatalf("LOCK still refused (%d) 5s after aborted uploads — a lock was genuinely leaked", code)
		}
		if code := c.unlock("/contended.txt", token); code != http.StatusNoContent {
			t.Fatalf("UNLOCK = %d", code)
		}
		if code, _ := c.put("/contended.txt", "after the aborts"); code >= 300 {
			t.Fatalf("PUT after aborted uploads = %d — the path is still locked", code)
		}
		if _, body := c.get("/contended.txt"); body != "after the aborts" {
			t.Fatalf("the file reads %q", body)
		}
	})

	t.Run("download abandoned part way", func(t *testing.T) {
		for i := 0; i < rounds; i++ {
			abortDownload(t, s, "/download.bin")
		}
		if !s.alive() {
			t.Fatalf("the server died on an abandoned download:\n%s", s.stderr.String())
		}
	})

	t.Run("server still works afterwards", func(t *testing.T) {
		if code, body := c.get("/warmup.txt"); code != http.StatusOK || body != "warm" {
			t.Fatalf("GET after the aborts = %d %q", code, body)
		}
		if code, _ := c.put("/after.txt", "still here"); code != http.StatusCreated {
			t.Fatalf("PUT after the aborts = %d", code)
		}
		if code, _ := c.propfind("/", "1"); code != http.StatusMultiStatus {
			t.Fatalf("PROPFIND after the aborts = %d", code)
		}
		if code, _ := c.mkcol("/after-dir"); code != http.StatusCreated {
			t.Fatalf("MKCOL after the aborts = %d", code)
		}
	})

	t.Run("no file descriptors leaked", func(t *testing.T) {
		after := settledFDs(t, s)
		t.Logf("open file descriptors: baseline %d, after %d aborted transfers %d",
			baseline, rounds*3, after)
		// Idle keep-alive connections and the odd runtime fd make an exact
		// match unrealistic; a leak of one descriptor per aborted transfer
		// would be 75 and is what this catches.
		if after > baseline+20 {
			t.Fatalf("open file descriptors grew from %d to %d over %d aborted transfers",
				baseline, after, rounds*3)
		}
	})

	t.Run("graceful shutdown still works", func(t *testing.T) {
		s.stop()
		if s.alive() {
			t.Fatal("the server did not exit on SIGINT after the aborted transfers")
		}
		log := s.stderr.String()
		if !strings.Contains(log, "stopped") {
			t.Fatalf("the server did not shut down gracefully:\n%s", log)
		}
	})
}

// lockUntilFree tries to LOCK a path until it succeeds or the window expires.
// It exists to tolerate the brief interval in which a just-aborted request's
// temporary lock is still being released; a permanent leak never clears and so
// still fails the caller. It returns the last status and, on success, the token.
func lockUntilFree(t *testing.T, c *davClient, path string, within time.Duration) (int, string) {
	t.Helper()
	deadline := time.Now().Add(within)
	var code int
	var token string
	for {
		code, token = c.lock(path)
		if code == http.StatusOK || code == http.StatusCreated {
			return code, token
		}
		if time.Now().After(deadline) {
			return code, token
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// abortUpload announces a large body, sends a fraction of it, and then resets
// the connection.
func abortUpload(t *testing.T, s *server, path string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", s.addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	const announced = 200 << 20
	fmt.Fprintf(conn, "PUT %s HTTP/1.1\r\nHost: %s\r\nAuthorization: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
		path, s.addr, basicHeader("alice", s.passwords["alice"]), announced)
	chunk := make([]byte, 128<<10)
	for i := range chunk {
		chunk[i] = byte(i)
	}
	if _, err := conn.Write(chunk); err != nil {
		t.Fatalf("writing the partial body: %v", err)
	}
	// SetLinger(0) makes Close send a RST rather than a FIN, which is what a
	// vanished network link looks like to the server.
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.Close()
}

// abortDownload starts a GET, reads a little, and drops the connection.
func abortDownload(t *testing.T, s *server, path string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", s.addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nAuthorization: %s\r\n\r\n",
		path, s.addr, basicHeader("alice", s.passwords["alice"]))
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("reading the response head: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", path, resp.StatusCode)
	}
	if _, err := io.CopyN(io.Discard, resp.Body, 64<<10); err != nil && err != io.EOF {
		t.Fatalf("reading part of the body: %v", err)
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.Close()
}

// settledFDs reads the server's descriptor count once it has stopped changing,
// so a connection the kernel has not finished tearing down is not mistaken for
// a leak.
func settledFDs(t *testing.T, s *server) int {
	t.Helper()
	last := -1
	stable := 0
	for i := 0; i < 100; i++ {
		n := s.openFDs()
		if n == last {
			stable++
			if stable >= 5 {
				return n
			}
		} else {
			stable = 0
			last = n
		}
		time.Sleep(100 * time.Millisecond)
	}
	return last
}
