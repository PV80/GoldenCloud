//go:build integration

package integration

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bobsSecret is planted in bob's folder and must never appear in a response to
// alice, whatever she asks for.
const bobsSecret = "BOB-PAYSLIP-CONFIDENTIAL"

// storageSecret is planted in the storage root itself — above every user's
// jail — and is likewise never visible to anyone.
const storageSecret = "STORAGE-ROOT-ONLY"

// aliceOwn is the one file alice is entitled to.
const aliceOwn = "alice's own data"

// escapeTargets are the paths alice will try in order to reach bob's file or
// anything else outside her own folder. They are sent on the wire byte for
// byte: the client does not clean them, and neither may the server.
//
// Some of these normalise to a harmless name *inside* alice's own folder — that
// is a correct outcome, and the tests below distinguish "resolved to something
// of alice's" from "reached outside", which is the only thing that matters.
var escapeTargets = []string{
	"/bobs-payslip.txt",
	"/bob/bobs-payslip.txt",
	"/../bob/bobs-payslip.txt",
	"/../../bob/bobs-payslip.txt",
	"/./../bob/bobs-payslip.txt",
	"/alice/../bob/bobs-payslip.txt",
	"/%2e%2e/bob/bobs-payslip.txt",
	"/%2E%2E/bob/bobs-payslip.txt",
	"/..%2fbob%2fbobs-payslip.txt",
	"/..%252fbob%252fbobs-payslip.txt",
	"/%252e%252e%252fbob%252fbobs-payslip.txt",
	"/....//bob/bobs-payslip.txt",
	"/..;/bob/bobs-payslip.txt",
	"/..%00/bob/bobs-payslip.txt",
	"/%00/../bob/bobs-payslip.txt",
	"/bobs-payslip.txt%00.png",
	"/%5c..%5cbob%5cbobs-payslip.txt",
	"/..%5c..%5cbob%5cbobs-payslip.txt",
	"//bob/bobs-payslip.txt",
	"///bob/bobs-payslip.txt",
	"/./././../bob/bobs-payslip.txt",
	"/a/b/c/../../../../bob/bobs-payslip.txt",
	"/root-level.txt",
	"/../root-level.txt",
	"/../../root-level.txt",
	"/../../../../../../../../etc/passwd",
	"/etc/passwd",
	"/%2e%2e%2f%2e%2e%2f%2e%2e%2fetc%2fpasswd",
	"/bobs-payslip.txt::$DATA",
	"/bobs-payslip.txt:stream",
	"/../users.yaml",
	"/../../config.yaml",
	"/\u002e\u002e/bob/bobs-payslip.txt",
}

// TestUserIsolation is the single most important test in the repository.
//
// Alice and Bob are two staff members with two folders on the same server.
// Alice must not be able to read, write, list, move, copy, delete or lock
// anything of Bob's, nor anything in the storage root above them both, by any
// path she can put on the wire.
func TestUserIsolation(t *testing.T) {
	s := startServer(t, "alice", "bob")
	alice := s.client("alice")
	bob := s.client("bob")

	// Plant the data alice must never reach.
	if code, _ := bob.put("/bobs-payslip.txt", bobsSecret); code != http.StatusCreated {
		t.Fatalf("planting bob's file = %d", code)
	}
	if err := os.WriteFile(filepath.Join(s.storage, "root-level.txt"), []byte(storageSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	resetAlice(t, s, alice)

	// noSecrets fails if a response body carries anything alice is not entitled
	// to see. This is the check that actually matters.
	noSecrets := func(t *testing.T, method, path string, code int, body string) {
		t.Helper()
		switch {
		case strings.Contains(body, bobsSecret):
			t.Fatalf("%s %s leaked bob's data (status %d)", method, path, code)
		case strings.Contains(body, storageSecret):
			t.Fatalf("%s %s leaked data from the storage root (status %d)", method, path, code)
		case strings.Contains(body, "root:x:"):
			t.Fatalf("%s %s leaked /etc/passwd (status %d)", method, path, code)
		case strings.Contains(body, "password_hash"), strings.Contains(body, "$2a$"):
			t.Fatalf("%s %s leaked the user store (status %d)", method, path, code)
		case strings.Contains(body, "storage_root:"):
			t.Fatalf("%s %s leaked the server configuration (status %d)", method, path, code)
		}
	}

	// --- reads, against a folder holding only alice's own file ----------------

	t.Run("read", func(t *testing.T) {
		for _, path := range escapeTargets {
			t.Run(path, func(t *testing.T) {
				code, body := alice.get(path)
				noSecrets(t, "GET", path, code, body)
				// Alice's folder currently contains exactly one file, and it is
				// not any of these names, so nothing here may succeed.
				if code >= 200 && code < 300 {
					t.Fatalf("GET %s = %d — it should have been refused or 404", path, code)
				}
			})
		}
	})

	t.Run("propfind", func(t *testing.T) {
		for _, path := range escapeTargets {
			t.Run(path, func(t *testing.T) {
				code, body := alice.propfind(path, "1")
				noSecrets(t, "PROPFIND", path, code, body)
				if code == http.StatusMultiStatus {
					t.Fatalf("PROPFIND %s = 207 on a path alice owns nothing at:\n%s", path, body)
				}
			})
		}
	})

	// --- writes ---------------------------------------------------------------

	t.Run("write", func(t *testing.T) {
		for _, path := range escapeTargets {
			t.Run(path, func(t *testing.T) {
				alice.put(path, "ALICE-WAS-HERE")
				alice.mkcol(path + "-dir")
			})
		}
		assertNothingEscaped(t, s)
	})
	resetAlice(t, s, alice)

	t.Run("delete", func(t *testing.T) {
		for _, path := range escapeTargets {
			t.Run(path, func(t *testing.T) {
				alice.delete(path)
			})
		}
		assertNothingEscaped(t, s)
		if code, body := bob.get("/bobs-payslip.txt"); code != http.StatusOK || body != bobsSecret {
			t.Fatalf("bob's file was damaged by alice's DELETEs: %d %q", code, body)
		}
	})
	resetAlice(t, s, alice)

	t.Run("move and copy out", func(t *testing.T) {
		for _, dest := range escapeTargets {
			t.Run(dest, func(t *testing.T) {
				alice.copy("/alice-own.txt", dest)
				alice.move("/alice-own.txt", dest)
				// Put it back so the next destination starts from the same
				// state; a MOVE that lands inside her own folder is correct.
				alice.put("/alice-own.txt", aliceOwn)
			})
		}
		assertNothingEscaped(t, s)
	})
	resetAlice(t, s, alice)

	t.Run("move and copy in", func(t *testing.T) {
		for _, src := range escapeTargets {
			t.Run(src, func(t *testing.T) {
				alice.copy(src, "/stolen.txt")
				code, body := alice.get("/stolen.txt")
				noSecrets(t, "COPY-in then GET", src, code, body)
				alice.move(src, "/stolen.txt")
				code, body = alice.get("/stolen.txt")
				noSecrets(t, "MOVE-in then GET", src, code, body)
				alice.delete("/stolen.txt")
			})
		}
		if code, body := bob.get("/bobs-payslip.txt"); code != http.StatusOK || body != bobsSecret {
			t.Fatalf("bob's file was moved away: %d %q", code, body)
		}
		assertNothingEscaped(t, s)
	})
	resetAlice(t, s, alice)

	t.Run("lock", func(t *testing.T) {
		for _, path := range escapeTargets {
			t.Run(path, func(t *testing.T) {
				alice.lock(path)
			})
		}
		assertNothingEscaped(t, s)
		// Bob is unaffected by whatever alice locked.
		code, token := bob.lock("/bobs-payslip.txt")
		if code != http.StatusOK && code != http.StatusCreated {
			t.Fatalf("bob can no longer lock his own file: %d", code)
		}
		bob.unlock("/bobs-payslip.txt", token)
	})
	resetAlice(t, s, alice)

	// --- listings -------------------------------------------------------------

	t.Run("alice cannot see bob in her listing", func(t *testing.T) {
		code, body := alice.propfind("/", "1")
		if code != http.StatusMultiStatus {
			t.Fatalf("PROPFIND / = %d", code)
		}
		for _, forbidden := range []string{"bobs-payslip", "root-level", "users.yaml", "config.yaml"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("alice's listing mentions %q:\n%s", forbidden, body)
			}
		}
		if !strings.Contains(body, "alice-own.txt") {
			t.Fatalf("alice's listing is missing her own file:\n%s", body)
		}
		if n := strings.Count(body, "<D:response>"); n != 2 {
			t.Fatalf("alice's listing has %d entries, want 2 (her folder and her one file):\n%s", n, body)
		}
	})

	t.Run("the same path is a different file per user", func(t *testing.T) {
		if code, _ := alice.put("/shared-name.txt", "from alice"); code != http.StatusCreated {
			t.Fatalf("alice PUT = %d", code)
		}
		if code, _ := bob.put("/shared-name.txt", "from bob"); code != http.StatusCreated {
			t.Fatalf("bob PUT = %d", code)
		}
		if _, body := alice.get("/shared-name.txt"); body != "from alice" {
			t.Fatalf("alice sees %q", body)
		}
		if _, body := bob.get("/shared-name.txt"); body != "from bob" {
			t.Fatalf("bob sees %q", body)
		}
	})

	t.Run("bob's lock token is worthless to alice", func(t *testing.T) {
		if code, _ := alice.put("/contended.txt", "alice"); code != http.StatusCreated {
			t.Fatalf("setup PUT = %d", code)
		}
		if code, _ := bob.put("/contended.txt", "bob"); code != http.StatusCreated {
			t.Fatalf("setup PUT = %d", code)
		}
		code, bobToken := bob.lock("/contended.txt")
		if code != http.StatusOK && code != http.StatusCreated {
			t.Fatalf("bob LOCK = %d", code)
		}
		if bobToken == "" {
			t.Fatal("bob's LOCK returned no token")
		}
		aliceCode, aliceToken := alice.lock("/contended.txt")
		if aliceCode != http.StatusOK && aliceCode != http.StatusCreated {
			t.Fatalf("alice LOCK = %d", aliceCode)
		}
		if aliceToken == bobToken {
			t.Fatalf("alice and bob were issued the same lock token %q", aliceToken)
		}
		// Alice presents bob's token against her own locked resource. The two
		// lock systems share no token namespace, so it must be refused.
		status, _ := alice.call(http.MethodPut, "/contended.txt", strings.NewReader("with bob's token"),
			"If", "("+strings.Trim(bobToken, "<>")+")")
		if status >= 200 && status < 300 {
			t.Fatalf("alice wrote her locked file using bob's lock token (status %d)", status)
		}
		if _, body := alice.get("/contended.txt"); body == "with bob's token" {
			t.Fatal("bob's lock token let a write through on alice's file")
		}
		alice.unlock("/contended.txt", aliceToken)
		bob.unlock("/contended.txt", bobToken)
	})

	// Final sweep.
	assertNothingEscaped(t, s)
	if code, body := bob.get("/bobs-payslip.txt"); code != http.StatusOK || body != bobsSecret {
		t.Fatalf("bob's file did not survive the whole suite: %d %q", code, body)
	}
}

// resetAlice empties alice's folder and leaves exactly one file in it, so each
// phase starts from a state where anything that appears is attributable.
func resetAlice(t *testing.T, s *server, alice *davClient) {
	t.Helper()
	dir := s.userDir("alice")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("alice's folder: %v", err)
	}
	for _, e := range ents {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			t.Fatalf("clearing alice's folder: %v", err)
		}
	}
	if code, _ := alice.put("/alice-own.txt", aliceOwn); code != http.StatusCreated {
		t.Fatalf("re-creating alice's file = %d", code)
	}
}

// assertNothingEscaped checks that nothing alice could have created has
// appeared in bob's folder or in the storage root, and that bob's file and the
// server's own files are untouched.
func assertNothingEscaped(t *testing.T, s *server) {
	t.Helper()

	bobDir := s.userDir("bob")
	ents, err := os.ReadDir(bobDir)
	if err != nil {
		t.Fatalf("bob's folder is unreadable: %v", err)
	}
	allowedInBob := map[string]bool{
		"bobs-payslip.txt": true,
		"shared-name.txt":  true,
		"contended.txt":    true,
	}
	for _, e := range ents {
		if !allowedInBob[e.Name()] {
			t.Fatalf("something appeared in bob's folder: %q", e.Name())
		}
	}
	b, err := os.ReadFile(filepath.Join(bobDir, "bobs-payslip.txt"))
	if err != nil {
		t.Fatalf("bob's file is gone: %v", err)
	}
	if string(b) != bobsSecret {
		t.Fatalf("bob's file was overwritten with %q", b)
	}

	rootEnts, err := os.ReadDir(s.storage)
	if err != nil {
		t.Fatal(err)
	}
	allowedInRoot := map[string]bool{"alice": true, "bob": true, "root-level.txt": true}
	for _, e := range rootEnts {
		if !allowedInRoot[e.Name()] {
			t.Fatalf("something appeared in the storage root, above every jail: %q", e.Name())
		}
	}
	rl, err := os.ReadFile(filepath.Join(s.storage, "root-level.txt"))
	if err != nil {
		t.Fatalf("the storage-root file is gone: %v", err)
	}
	if string(rl) != storageSecret {
		t.Fatalf("the storage-root file was overwritten with %q", rl)
	}

	// The config and user store live beside the storage root and must be
	// unchanged.
	users, err := os.ReadFile(s.usersPath)
	if err != nil {
		t.Fatalf("%s was destroyed: %v", s.usersPath, err)
	}
	if !strings.Contains(string(users), "username: alice") {
		t.Fatalf("the user store was rewritten:\n%s", users)
	}
	if _, err := os.Stat(s.cfgPath); err != nil {
		t.Fatalf("%s was destroyed: %v", s.cfgPath, err)
	}
}
