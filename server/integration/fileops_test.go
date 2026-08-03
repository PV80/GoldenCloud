//go:build integration

package integration

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// 4. The ordinary operations a file manager performs.
func TestFileOperations(t *testing.T) {
	s := startServer(t, "alice")
	c := s.client("alice")

	t.Run("mkcol", func(t *testing.T) {
		if code, _ := c.mkcol("/Projects"); code != http.StatusCreated {
			t.Fatalf("MKCOL = %d, want 201", code)
		}
		if code, _ := c.mkcol("/Projects"); code != http.StatusMethodNotAllowed {
			t.Fatalf("MKCOL on an existing collection = %d, want 405", code)
		}
		if code, _ := c.mkcol("/Missing/Deep"); code != http.StatusConflict {
			t.Fatalf("MKCOL with a missing parent = %d, want 409", code)
		}
		st, err := os.Stat(filepath.Join(s.userDir("alice"), "Projects"))
		if err != nil || !st.IsDir() {
			t.Fatalf("the collection is not a directory on disk: %v", err)
		}
	})

	t.Run("put and get", func(t *testing.T) {
		if code, _ := c.put("/Projects/report.txt", "quarterly numbers"); code != http.StatusCreated {
			t.Fatalf("PUT = %d", code)
		}
		if code, body := c.get("/Projects/report.txt"); code != http.StatusOK || body != "quarterly numbers" {
			t.Fatalf("GET = %d %q", code, body)
		}
		// Overwriting truncates.
		if code, _ := c.put("/Projects/report.txt", "short"); code != http.StatusNoContent && code != http.StatusCreated {
			t.Fatalf("overwrite PUT = %d", code)
		}
		if _, body := c.get("/Projects/report.txt"); body != "short" {
			t.Fatalf("after overwrite the file reads %q", body)
		}
	})

	t.Run("propfind", func(t *testing.T) {
		code, body := c.propfind("/Projects", "1")
		if code != http.StatusMultiStatus {
			t.Fatalf("PROPFIND = %d, want 207", code)
		}
		if err := wellFormedXML(body); err != nil {
			t.Fatalf("PROPFIND body is not well-formed XML: %v\n%s", err, body)
		}
		if n := strings.Count(body, "<D:response>"); n != 2 {
			t.Fatalf("PROPFIND Depth 1 returned %d responses, want 2 (the collection and one file):\n%s", n, body)
		}
		if !strings.Contains(body, "report.txt") {
			t.Fatalf("PROPFIND does not list report.txt:\n%s", body)
		}
		if !strings.Contains(body, "<D:collection") {
			t.Fatalf("PROPFIND does not mark the collection as one:\n%s", body)
		}

		code, body = c.propfind("/Projects", "0")
		if code != http.StatusMultiStatus {
			t.Fatalf("PROPFIND Depth 0 = %d", code)
		}
		if n := strings.Count(body, "<D:response>"); n != 1 {
			t.Fatalf("PROPFIND Depth 0 returned %d responses, want 1", n)
		}

		if code, _ := c.propfind("/does-not-exist", "1"); code != http.StatusNotFound {
			t.Fatalf("PROPFIND on a missing path = %d, want 404", code)
		}
	})

	// D-019: Depth: infinity is refused rather than walked.
	t.Run("propfind depth infinity is refused", func(t *testing.T) {
		code, body := c.propfind("/", "infinity")
		if code != http.StatusForbidden {
			t.Fatalf("PROPFIND Depth: infinity = %d, want 403", code)
		}
		if !strings.Contains(body, "propfind-finite-depth") {
			t.Fatalf("the refusal does not carry DAV:propfind-finite-depth:\n%s", body)
		}
	})

	t.Run("rename", func(t *testing.T) {
		if code, _ := c.move("/Projects/report.txt", "/Projects/report-final.txt"); code != http.StatusCreated && code != http.StatusNoContent {
			t.Fatalf("MOVE = %d", code)
		}
		if code, _ := c.get("/Projects/report.txt"); code != http.StatusNotFound {
			t.Fatalf("the old name still resolves: %d", code)
		}
		if code, body := c.get("/Projects/report-final.txt"); code != http.StatusOK || body != "short" {
			t.Fatalf("the new name reads %d %q", code, body)
		}
		// Rename a collection.
		if code, _ := c.move("/Projects", "/Archive"); code != http.StatusCreated && code != http.StatusNoContent {
			t.Fatalf("MOVE of a collection = %d", code)
		}
		if code, body := c.get("/Archive/report-final.txt"); code != http.StatusOK || body != "short" {
			t.Fatalf("after renaming the collection: %d %q", code, body)
		}
	})

	t.Run("copy", func(t *testing.T) {
		if code, _ := c.copy("/Archive/report-final.txt", "/Archive/copy.txt"); code != http.StatusCreated {
			t.Fatalf("COPY = %d", code)
		}
		if _, body := c.get("/Archive/copy.txt"); body != "short" {
			t.Fatalf("the copy reads %q", body)
		}
		if _, body := c.get("/Archive/report-final.txt"); body != "short" {
			t.Fatal("COPY damaged the source")
		}
	})

	t.Run("delete", func(t *testing.T) {
		if code, _ := c.delete("/Archive/copy.txt"); code != http.StatusNoContent {
			t.Fatalf("DELETE = %d, want 204", code)
		}
		if code, _ := c.delete("/Archive/copy.txt"); code != http.StatusNotFound {
			t.Fatalf("DELETE of a missing file = %d, want 404", code)
		}
		// Deleting a collection removes it and its contents.
		if code, _ := c.delete("/Archive"); code != http.StatusNoContent {
			t.Fatalf("DELETE of a collection = %d", code)
		}
		if _, err := os.Stat(filepath.Join(s.userDir("alice"), "Archive")); !os.IsNotExist(err) {
			t.Fatalf("the collection survived DELETE: %v", err)
		}
	})

	t.Run("lock and unlock", func(t *testing.T) {
		if code, _ := c.put("/locked.txt", "content"); code != http.StatusCreated {
			t.Fatalf("PUT = %d", code)
		}
		code, token := c.lock("/locked.txt")
		if code != http.StatusOK && code != http.StatusCreated {
			t.Fatalf("LOCK = %d", code)
		}
		if !strings.HasPrefix(token, "<") || !strings.HasSuffix(token, ">") {
			t.Fatalf("Lock-Token %q is not a Coded-URL", token)
		}
		if !strings.Contains(token, ":") {
			t.Fatalf("Lock-Token %q is not a URI", token)
		}
		if code := c.unlock("/locked.txt", token); code != http.StatusNoContent {
			t.Fatalf("UNLOCK = %d, want 204", code)
		}
		if code, _ := c.put("/locked.txt", "after unlock"); code >= 300 {
			t.Fatalf("PUT after UNLOCK = %d", code)
		}
	})

	t.Run("unicode and spaces in names", func(t *testing.T) {
		for _, name := range []string{
			"/Sales%20Report.txt",
			"/caf%C3%A9.txt",
			"/%E6%97%A5%E6%9C%AC%E8%AA%9E.txt",
			"/file%20with%20%23hash.txt",
			"/file+plus.txt",
		} {
			if code, _ := c.put(name, "content of "+name); code != http.StatusCreated {
				t.Fatalf("PUT %s = %d", name, code)
			}
			if code, body := c.get(name); code != http.StatusOK || body != "content of "+name {
				t.Fatalf("GET %s = %d %q", name, code, body)
			}
		}
	})
}

func wellFormedXML(body string) error {
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// 5. Several users hammering the server at once stay isolated and correct.
func TestConcurrentUsers(t *testing.T) {
	users := []string{"alice", "bob", "carol", "dave"}
	s := startServer(t, users...)

	const perUser = 12
	var wg sync.WaitGroup
	var failures atomic.Int64

	for _, user := range users {
		for i := 0; i < perUser; i++ {
			wg.Add(1)
			go func(user string, i int) {
				defer wg.Done()
				c := s.client(user)
				path := fmt.Sprintf("/concurrent-%d.txt", i)
				want := fmt.Sprintf("%s wrote %d", user, i)

				if code, _ := c.put(path, want); code != http.StatusCreated {
					t.Errorf("%s PUT %s = %d", user, path, code)
					failures.Add(1)
					return
				}
				if code, body := c.get(path); code != http.StatusOK || body != want {
					t.Errorf("%s GET %s = %d %q, want %q", user, path, code, body, want)
					failures.Add(1)
					return
				}
				if code, _ := c.mkcol(fmt.Sprintf("/dir-%d", i)); code != http.StatusCreated {
					t.Errorf("%s MKCOL = %d", user, code)
					failures.Add(1)
					return
				}
				if code, _ := c.move(path, fmt.Sprintf("/dir-%d/moved.txt", i)); code >= 300 {
					t.Errorf("%s MOVE = %d", user, code)
					failures.Add(1)
					return
				}
				if code, body := c.get(fmt.Sprintf("/dir-%d/moved.txt", i)); code != http.StatusOK || body != want {
					t.Errorf("%s GET after MOVE = %d %q", user, code, body)
					failures.Add(1)
				}
			}(user, i)
		}
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d concurrent operations failed", failures.Load())
	}

	// Every user must see exactly their own writes, and nobody else's.
	for _, user := range users {
		c := s.client(user)
		code, body := c.propfind("/", "1")
		if code != http.StatusMultiStatus {
			t.Fatalf("%s PROPFIND = %d", user, code)
		}
		for _, other := range users {
			if other == user {
				continue
			}
			if strings.Contains(body, other+" wrote") {
				t.Fatalf("%s can see %s's data", user, other)
			}
		}
		for i := 0; i < perUser; i++ {
			want := fmt.Sprintf("%s wrote %d", user, i)
			if _, got := c.get(fmt.Sprintf("/dir-%d/moved.txt", i)); got != want {
				t.Fatalf("%s: file %d reads %q, want %q", user, i, got, want)
			}
		}
	}
}
