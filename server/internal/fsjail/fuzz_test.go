package fsjail_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PV80/GoldenCloud/server/internal/fsjail"
)

// FuzzJailEscape asserts the single invariant the whole product rests on: no
// input, however malformed, lets a jail read, write, create or destroy anything
// outside its root.
//
//	go test -run=Fuzz -fuzz=FuzzJailEscape ./internal/fsjail
func FuzzJailEscape(f *testing.F) {
	for _, seed := range escapeNames {
		f.Add(seed)
	}
	for _, seed := range []string{
		"", "/", ".", "..", "...", "/a", "a/b/c",
		"/‮/secret.txt", "/．．/secret.txt",
		"/%00", "/a%2fb", "/.git/config", "/~/.ssh/id_rsa",
		"/a/./././../../../secret.txt", strings.Repeat("/..", 64),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, name string) {
		tmp := t.TempDir()
		root := filepath.Join(tmp, "root")
		outside := filepath.Join(tmp, "outside")
		for _, p := range []string{root, outside} {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		secrets := []string{
			filepath.Join(tmp, "secret.txt"),
			filepath.Join(outside, "secret.txt"),
		}
		for _, p := range secrets {
			if err := os.WriteFile(p, []byte("TOP SECRET"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink("../outside", filepath.Join(root, "link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/", filepath.Join(root, "abs")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../../..", filepath.Join(root, "up")); err != nil {
			t.Fatal(err)
		}

		d, err := fsjail.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		ctx := context.Background()

		if rf, err := d.OpenFile(ctx, name, os.O_RDONLY, 0); err == nil {
			b, _ := io.ReadAll(io.LimitReader(rf, 64))
			rf.Close()
			if strings.Contains(string(b), "TOP SECRET") {
				t.Fatalf("read out-of-jail data via %q", name)
			}
		}
		if wf, err := d.OpenFile(ctx, name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
			_, _ = io.WriteString(wf, "PWNED")
			wf.Close()
		}
		_ = d.Mkdir(ctx, name, 0o755)
		_ = d.Rename(ctx, name, "/moved")
		_ = d.Rename(ctx, "/moved", name)
		_ = d.RemoveAll(ctx, name)
		_, _ = d.Stat(ctx, name)

		for _, p := range secrets {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("%q destroyed %s: %v", name, p, err)
			}
			if string(b) != "TOP SECRET" {
				t.Fatalf("%q overwrote %s", name, p)
			}
		}
		ents, err := os.ReadDir(outside)
		if err != nil {
			t.Fatalf("%q damaged the outside directory: %v", name, err)
		}
		if len(ents) != 1 {
			t.Fatalf("%q created entries outside the jail: %d present", name, len(ents))
		}
		ents, err = os.ReadDir(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if len(ents) != 3 {
			var names []string
			for _, e := range ents {
				names = append(names, e.Name())
			}
			t.Fatalf("%q created entries beside the jail: %v", name, names)
		}
	})
}
