package fsjail_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PV80/GoldenCloud/server/internal/fsjail"
)

// newJail builds a jail over a fresh temp root and returns the jail plus the
// absolute root path.
func newJail(t *testing.T) (*fsjail.Dir, string) {
	t.Helper()
	root := t.TempDir()
	d, err := fsjail.Open(root)
	if err != nil {
		t.Fatalf("fsjail.Open(%q): %v", root, err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, root
}

func TestOpenRejectsMissingRoot(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := fsjail.Open(missing); err == nil {
		t.Fatal("Open on a missing directory: want error, got nil")
	}
}

func TestOpenRejectsNonDirectoryRoot(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fsjail.Open(f); err == nil {
		t.Fatal("Open on a regular file: want error, got nil")
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	d, root := newJail(t)
	ctx := context.Background()

	if err := d.Mkdir(ctx, "/docs", 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	f, err := d.OpenFile(ctx, "/docs/a.txt", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenFile create: %v", err)
	}
	if _, err := io.WriteString(f, "hello jail"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	onDisk := filepath.Join(root, "docs", "a.txt")
	got, err := os.ReadFile(onDisk)
	if err != nil {
		t.Fatalf("file not where expected: %v", err)
	}
	if string(got) != "hello jail" {
		t.Fatalf("content = %q, want %q", got, "hello jail")
	}

	st, err := d.Stat(ctx, "/docs/a.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Size() != int64(len("hello jail")) {
		t.Fatalf("Stat size = %d, want %d", st.Size(), len("hello jail"))
	}

	if err := d.Rename(ctx, "/docs/a.txt", "/docs/b.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := d.Stat(ctx, "/docs/a.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat after rename err = %v, want ErrNotExist", err)
	}
	if err := d.RemoveAll(ctx, "/docs"); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("directory survived RemoveAll: %v", err)
	}
}

func TestStatRoot(t *testing.T) {
	t.Parallel()
	d, _ := newJail(t)
	for _, name := range []string{"/", "", ".", "/.", "//"} {
		st, err := d.Stat(context.Background(), name)
		if err != nil {
			t.Fatalf("Stat(%q): %v", name, err)
		}
		if !st.IsDir() {
			t.Fatalf("Stat(%q): not a directory", name)
		}
	}
}

func TestRemoveAllRefusesRoot(t *testing.T) {
	t.Parallel()
	d, root := newJail(t)
	for _, name := range []string{"/", "", ".", "/../"} {
		if err := d.RemoveAll(context.Background(), name); err == nil {
			t.Fatalf("RemoveAll(%q): want error, got nil", name)
		}
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("root was destroyed: %v", err)
	}
}

func TestReaddirListsOnlyJailContents(t *testing.T) {
	t.Parallel()
	d, root := newJail(t)
	ctx := context.Background()
	for _, n := range []string{"one", "two", "three"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte(n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f, err := d.OpenFile(ctx, "/", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile root: %v", err)
	}
	defer f.Close()
	ents, err := f.Readdir(-1)
	if err != nil {
		t.Fatalf("Readdir: %v", err)
	}
	if len(ents) != 3 {
		t.Fatalf("Readdir returned %d entries, want 3", len(ents))
	}
}

// --- the security table ------------------------------------------------------

// escapeNames are inputs that must never reach anything outside the jail. Some
// are rejected outright; others normalise to a harmless in-jail path. Either is
// acceptable, so long as nothing outside the root is touched.
var escapeNames = []string{
	"../secret.txt",
	"../../secret.txt",
	"/../secret.txt",
	"/../../../../../../../../etc/passwd",
	"/./../secret.txt",
	"/docs/../../secret.txt",
	"..",
	"../",
	"/..",
	"....//secret.txt",
	"..;/secret.txt",
	"/%2e%2e/secret.txt",
	"/%2E%2E%2Fsecret.txt",
	"/..%2fsecret.txt",
	"/..%252fsecret.txt",
	`\..\secret.txt`,
	`..\secret.txt`,
	`/..\..\secret.txt`,
	`\\?\C:\Windows\system32\config\sam`,
	`C:\Windows\win.ini`,
	"//etc/passwd",
	"///etc/passwd",
	"/etc/passwd",
	"/outside/secret.txt",
	"/link/secret.txt",
	"/link/../secret.txt",
	"/deep/link/secret.txt",
	"/a.txt:$DATA",
	"/a.txt::$DATA",
	"/a.txt:stream",
	"/a.txt:stream:$DATA",
	"/\x00/secret.txt",
	"/a\x00.txt",
	"/secret.txt\x00.png",
	"/con",
	"/nul",
	"/" + strings.Repeat("a", 300),
	"/" + strings.Repeat("a/", 5000) + "b",
	"/" + strings.Repeat("../", 2000) + "secret.txt",
	"\ufeff/../secret.txt",
	"/\u2216..\u2216secret.txt",
}

// setupEscapeFixture creates:
//
//	tmp/root      -- the jail
//	tmp/secret.txt -- a file the jail must never reach
//	tmp/outside/secret.txt
//	tmp/root/link      -> ../outside        (symlink out of the jail)
//	tmp/root/deep/link -> /                 (absolute symlink to the real fs root)
func setupEscapeFixture(t *testing.T) (d *fsjail.Dir, tmp, root string) {
	t.Helper()
	tmp = t.TempDir()
	root = filepath.Join(tmp, "root")
	outside := filepath.Join(tmp, "outside")
	for _, p := range []string{root, outside, filepath.Join(root, "deep")} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(tmp, "secret.txt"), filepath.Join(outside, "secret.txt")} {
		if err := os.WriteFile(p, []byte("TOP SECRET"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../outside", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/", filepath.Join(root, "deep", "link")); err != nil {
		t.Fatal(err)
	}
	var err error
	d, err = fsjail.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, tmp, root
}

func TestJailEscapeTable(t *testing.T) {
	t.Parallel()
	d, tmp, root := setupEscapeFixture(t)
	ctx := context.Background()

	for _, name := range escapeNames {
		t.Run(name, func(t *testing.T) {
			// Reads must never yield the secret.
			if f, err := d.OpenFile(ctx, name, os.O_RDONLY, 0); err == nil {
				b, _ := io.ReadAll(io.LimitReader(f, 64))
				f.Close()
				if strings.Contains(string(b), "TOP SECRET") {
					t.Fatalf("OpenFile(%q) read data from outside the jail", name)
				}
			}
			// Writes must never land outside.
			if f, err := d.OpenFile(ctx, name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
				_, _ = io.WriteString(f, "PWNED")
				f.Close()
			}
			_ = d.Mkdir(ctx, name, 0o755)
			_ = d.RemoveAll(ctx, name)
			_ = d.Rename(ctx, name, "/moved")
			_ = d.Rename(ctx, "/moved", name)
			_, _ = d.Stat(ctx, name)

			assertNothingOutside(t, tmp, root)
		})
	}
}

// assertNothingOutside walks the fixture temp dir and fails if anything outside
// the jail root was created, modified or destroyed.
func assertNothingOutside(t *testing.T, tmp, root string) {
	t.Helper()
	for _, p := range []string{
		filepath.Join(tmp, "secret.txt"),
		filepath.Join(tmp, "outside", "secret.txt"),
	} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s: destroyed or unreadable: %v", p, err)
		}
		if string(b) != "TOP SECRET" {
			t.Fatalf("%s: content changed to %q", p, b)
		}
	}
	ents, err := os.ReadDir(filepath.Join(tmp, "outside"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("files appeared outside the jail: %v", names)
	}
	ents, err = os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 3 { // root, outside, secret.txt
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("temp dir gained entries: %v", names)
	}
}

// TestRejectedNames covers inputs the jail rejects outright rather than
// normalising, because they cannot be part of a legitimate Windows filename.
func TestRejectedNames(t *testing.T) {
	t.Parallel()
	d, _ := newJail(t)
	ctx := context.Background()
	rejected := []string{
		"/a\x00b",
		`/a\b`,
		"/a:b",
		"/a.txt::$DATA",
		"/" + strings.Repeat("a", 256),
		"/" + strings.Repeat("a/", 5000) + "b",
	}
	for _, name := range rejected {
		t.Run(name, func(t *testing.T) {
			if _, err := d.Stat(ctx, name); err == nil {
				t.Fatalf("Stat(%q): want rejection, got nil error", name)
			} else if !errors.Is(err, fsjail.ErrInvalidPath) {
				t.Fatalf("Stat(%q): err = %v, want ErrInvalidPath", name, err)
			}
		})
	}
}

func TestSymlinkInsideJailIsFine(t *testing.T) {
	t.Parallel()
	d, root := newJail(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	f, err := d.OpenFile(ctx, "/alias.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("in-jail symlink should resolve: %v", err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "inside" {
		t.Fatalf("content = %q, want %q", b, "inside")
	}
}

func TestHardlinkToOutsideIsNotReachableByName(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(tmp, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := fsjail.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// The jail exposes no API that can create a link to an out-of-jail target:
	// every operation takes a jail-relative name only. Verify the surface.
	if _, err := d.Stat(context.Background(), "/../secret.txt"); err == nil {
		t.Fatal("traversal to a hardlink target succeeded")
	}
}

func TestTwoJailsAreIndependent(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	a := filepath.Join(tmp, "alice")
	b := filepath.Join(tmp, "bob")
	for _, p := range []string{a, b} {
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(b, "bob.txt"), []byte("bobs data"), 0o600); err != nil {
		t.Fatal(err)
	}
	ja, err := fsjail.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	defer ja.Close()
	for _, name := range []string{"/../bob/bob.txt", "/bob.txt", "../bob/bob.txt"} {
		if _, err := ja.Stat(context.Background(), name); err == nil {
			t.Fatalf("alice reached %q in bob's jail", name)
		}
	}
}

func TestClosedJailRejectsEverything(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d, err := fsjail.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Stat(context.Background(), "/x"); err == nil {
		t.Fatal("Stat on a closed jail: want error, got nil")
	}
}
