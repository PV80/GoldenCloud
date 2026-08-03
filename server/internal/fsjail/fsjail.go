// Package fsjail implements a webdav.FileSystem that is confined to a single
// directory tree.
//
// It is the security core of GoldenCloud (see DECISIONS.md, D-002): per-user
// isolation is enforced here, at the filesystem layer, and nowhere else. No
// other code in the server opens a caller-influenced path.
//
// Confinement is provided by os.Root (Go 1.24+), which performs every operation
// relative to an open directory file descriptor and refuses to traverse out of
// it — including through symlinks, which are resolved by the kernel against the
// root rather than by string manipulation. On top of that, incoming names are
// normalised and validated before they are handed to os.Root, so that inputs
// which cannot be part of a legitimate filename (NUL bytes, backslash
// separators, NTFS alternate-data-stream colons, over-long components) are
// rejected outright rather than silently reinterpreted (see DECISIONS.md,
// D-023).
package fsjail

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"golang.org/x/net/webdav"
)

// ErrInvalidPath is returned for names that cannot address anything inside the
// jail. It wraps fs.ErrInvalid so callers using errors.Is with the standard
// sentinel also match.
var ErrInvalidPath = errors.New("fsjail: invalid path")

const (
	// maxPathLen bounds the whole virtual path. Long paths are a denial of
	// service vector against path resolution and a common overflow probe.
	maxPathLen = 4096
	// maxNameLen bounds a single path component, matching NAME_MAX on Linux
	// and the Windows per-component limit.
	maxNameLen = 255
)

// Dir is a webdav.FileSystem rooted at a single directory. The zero value is
// not usable; construct one with Open.
type Dir struct {
	root *os.Root
	// name is the absolute path of the root, kept for diagnostics only. It is
	// never used to build a path that is subsequently opened.
	name string
}

var _ webdav.FileSystem = (*Dir)(nil)

// Open creates a jail rooted at dir, which must already exist and be a
// directory. The directory is held open for the lifetime of the Dir, so
// renaming or replacing it afterwards cannot redirect the jail.
func Open(dir string) (*Dir, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("fsjail: root %q: %w", dir, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("fsjail: root %q: %w", dir, errors.New("not a directory"))
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("fsjail: root %q: %w", dir, err)
	}
	return &Dir{root: r, name: dir}, nil
}

// Close releases the root directory handle. Subsequent operations fail.
func (d *Dir) Close() error {
	if d == nil || d.root == nil {
		return nil
	}
	return d.root.Close()
}

// RootPath reports the absolute path the jail is rooted at. For logging and
// preflight messages only.
func (d *Dir) RootPath() string { return d.name }

// invalid builds an *fs.PathError for a rejected name, quoting only the virtual
// name so the on-disk layout is never disclosed.
func invalid(op, name, why string) error {
	return &fs.PathError{
		Op:   op,
		Path: name,
		Err:  fmt.Errorf("%w: %s", ErrInvalidPath, why),
	}
}

// resolve normalises a virtual WebDAV path into a path relative to the jail
// root. It returns "." for the root itself. It never returns a path containing
// a ".." element, an absolute path, or an empty string.
func resolve(op, name string) (string, error) {
	if len(name) > maxPathLen {
		return "", invalid(op, "", fmt.Sprintf("path longer than %d bytes", maxPathLen))
	}
	if strings.IndexByte(name, 0) >= 0 {
		return "", invalid(op, name, "contains a NUL byte")
	}
	// A backslash is a path separator to every Windows client and a legal
	// filename byte on Linux. Accepting it means the client and the server
	// disagree about the shape of the tree, which is exactly the confusion a
	// traversal exploit needs. Reject it.
	if strings.IndexByte(name, '\\') >= 0 {
		return "", invalid(op, name, `contains a backslash`)
	}
	// A colon is the NTFS alternate-data-stream separator ("file.txt:hidden",
	// "file.txt::$DATA") and is illegal in Windows filenames anyway.
	if strings.IndexByte(name, ':') >= 0 {
		return "", invalid(op, name, "contains a colon")
	}
	for i := 0; i < len(name); i++ {
		if name[i] < 0x20 || name[i] == 0x7f {
			return "", invalid(op, name, "contains a control character")
		}
	}

	// path.Clean on an absolute path collapses ".", "..", and repeated
	// separators, and — crucially — discards any ".." that would climb above
	// the root, so "/../../etc/passwd" becomes "/etc/passwd" (a name *inside*
	// the jail) rather than an escape.
	cleaned := path.Clean("/" + name)
	rel := strings.TrimPrefix(cleaned, "/")
	if rel == "" {
		return ".", nil
	}
	for _, elem := range strings.Split(rel, "/") {
		switch {
		case elem == "" || elem == "." || elem == "..":
			// Unreachable after Clean; belt and braces.
			return "", invalid(op, name, "unresolvable path element")
		case len(elem) > maxNameLen:
			return "", invalid(op, name, fmt.Sprintf("path component longer than %d bytes", maxNameLen))
		}
	}
	return rel, nil
}

// rewrite replaces the on-disk path in a *fs.PathError with the virtual name,
// so error strings that reach a log or a client never disclose the storage
// layout.
func rewrite(name string, err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return &fs.PathError{Op: pe.Op, Path: name, Err: pe.Err}
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return &os.LinkError{Op: le.Op, Old: name, New: le.New, Err: le.Err}
	}
	return err
}

// Mkdir implements webdav.FileSystem.
func (d *Dir) Mkdir(_ context.Context, name string, perm os.FileMode) error {
	rel, err := resolve("mkdir", name)
	if err != nil {
		return err
	}
	if rel == "." {
		return invalid("mkdir", name, "the jail root already exists")
	}
	return rewrite(name, d.root.Mkdir(rel, perm))
}

// OpenFile implements webdav.FileSystem.
func (d *Dir) OpenFile(_ context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	rel, err := resolve("open", name)
	if err != nil {
		return nil, err
	}
	f, err := d.root.OpenFile(rel, flag, perm)
	if err != nil {
		return nil, rewrite(name, err)
	}
	return &file{File: f, name: name}, nil
}

// RemoveAll implements webdav.FileSystem.
func (d *Dir) RemoveAll(_ context.Context, name string) error {
	rel, err := resolve("removeall", name)
	if err != nil {
		return err
	}
	if rel == "." {
		return invalid("removeall", name, "refusing to remove the jail root")
	}
	err = d.root.RemoveAll(rel)
	if errors.Is(err, fs.ErrNotExist) {
		// RFC 4918: DELETE of a missing resource is 404, which the webdav
		// handler derives from the error.
		return rewrite(name, err)
	}
	return rewrite(name, err)
}

// Rename implements webdav.FileSystem.
func (d *Dir) Rename(_ context.Context, oldName, newName string) error {
	oldRel, err := resolve("rename", oldName)
	if err != nil {
		return err
	}
	newRel, err := resolve("rename", newName)
	if err != nil {
		return err
	}
	if oldRel == "." || newRel == "." {
		return invalid("rename", oldName, "refusing to rename the jail root")
	}
	return rewrite(oldName, d.root.Rename(oldRel, newRel))
}

// Stat implements webdav.FileSystem.
func (d *Dir) Stat(_ context.Context, name string) (os.FileInfo, error) {
	rel, err := resolve("stat", name)
	if err != nil {
		return nil, err
	}
	fi, err := d.root.Stat(rel)
	if err != nil {
		return nil, rewrite(name, err)
	}
	return fi, nil
}

// file wraps *os.File so that errors surfaced during a transfer carry the
// virtual name rather than the on-disk path.
type file struct {
	*os.File
	name string
}

func (f *file) Read(p []byte) (int, error) {
	n, err := f.File.Read(p)
	if err != nil {
		return n, rewrite(f.name, err)
	}
	return n, nil
}

func (f *file) Write(p []byte) (int, error) {
	n, err := f.File.Write(p)
	if err != nil {
		return n, rewrite(f.name, err)
	}
	return n, nil
}

func (f *file) Seek(offset int64, whence int) (int64, error) {
	n, err := f.File.Seek(offset, whence)
	if err != nil {
		return n, rewrite(f.name, err)
	}
	return n, nil
}

func (f *file) Readdir(count int) ([]fs.FileInfo, error) {
	fis, err := f.File.Readdir(count)
	if err != nil {
		return fis, rewrite(f.name, err)
	}
	return fis, nil
}

func (f *file) Stat() (fs.FileInfo, error) {
	fi, err := f.File.Stat()
	if err != nil {
		return nil, rewrite(f.name, err)
	}
	return fi, nil
}

func (f *file) Close() error { return rewrite(f.name, f.File.Close()) }
