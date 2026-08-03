//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
)

// 1. Authentication succeeds and fails in the ways it should, against the real
// binary reading a users.yaml the real admin CLI wrote.
func TestAuthenticationSuccessAndFailure(t *testing.T) {
	s := startServer(t, "alice", "bob")

	t.Run("success", func(t *testing.T) {
		c := s.client("alice")
		if code, _ := c.put("/hello.txt", "hello"); code != http.StatusCreated {
			t.Fatalf("PUT = %d, want 201", code)
		}
		code, body := c.get("/hello.txt")
		if code != http.StatusOK || body != "hello" {
			t.Fatalf("GET = %d %q", code, body)
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		c := s.clientWith("alice", "not-alices-password")
		code, _ := c.get("/hello.txt")
		if code != http.StatusUnauthorized {
			t.Fatalf("GET with a wrong password = %d, want 401", code)
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		c := s.clientWith("mallory", "anything-at-all")
		code, _ := c.get("/hello.txt")
		if code != http.StatusUnauthorized {
			t.Fatalf("GET as an unknown user = %d, want 401", code)
		}
	})

	t.Run("no credentials", func(t *testing.T) {
		c := s.clientWith("", "")
		req := c.request(http.MethodGet, "/hello.txt", nil)
		req.Header.Del("Authorization")
		resp := c.do(req)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET without credentials = %d, want 401", resp.StatusCode)
		}
		if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, `realm="GoldenCloud"`) {
			t.Fatalf("WWW-Authenticate = %q", got)
		}
	})

	t.Run("OPTIONS needs no credentials", func(t *testing.T) {
		c := s.clientWith("", "")
		req := c.request(http.MethodOptions, "/", nil)
		req.Header.Del("Authorization")
		resp := c.do(req)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("OPTIONS / = %d, want 200 (the Windows redirector probes before authenticating)", resp.StatusCode)
		}
		if got := resp.Header.Get("DAV"); got != "1, 2, 3" {
			t.Fatalf("DAV = %q", got)
		}
		if got := resp.Header.Get("MS-Author-Via"); got != "DAV" {
			t.Fatalf("MS-Author-Via = %q", got)
		}
	})

	t.Run("password change takes effect on reload", func(t *testing.T) {
		s2 := startServer(t, "carol")
		old := s2.client("carol")
		if code, _ := old.put("/x.txt", "x"); code != http.StatusCreated {
			t.Fatalf("setup PUT = %d", code)
		}
		s2.addUserPassword(t, "carol", "a-brand-new-password")
		s2.reload()
		if code, _ := old.get("/x.txt"); code != http.StatusUnauthorized {
			t.Fatalf("the old password still works after reload: %d", code)
		}
		fresh := s2.clientWith("carol", "a-brand-new-password")
		if code, body := fresh.get("/x.txt"); code != http.StatusOK || body != "x" {
			t.Fatalf("the new password does not work: %d %q", code, body)
		}
	})

	t.Run("removed user loses access on reload", func(t *testing.T) {
		s3 := startServer(t, "dave", "erin")
		dave := s3.client("dave")
		if code, _ := dave.put("/d.txt", "d"); code != http.StatusCreated {
			t.Fatalf("setup PUT = %d", code)
		}
		s3.removeUser("dave")
		s3.reload()
		if code, _ := dave.get("/d.txt"); code != http.StatusUnauthorized {
			t.Fatalf("a removed user still has access: %d", code)
		}
		erin := s3.client("erin")
		if code, _ := erin.put("/e.txt", "e"); code != http.StatusCreated {
			t.Fatalf("erin lost access when dave was removed: %d", code)
		}
	})
}
