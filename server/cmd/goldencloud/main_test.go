package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/PV80/GoldenCloud/server/internal/config"
)

type result struct {
	code   int
	stdout string
	stderr string
}

// cli runs the command line in-process with the given stdin.
func cli(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, streams{in: strings.NewReader(stdin), out: &out, err: &errb})
	return result{code: code, stdout: out.String(), stderr: errb.String()}
}

// newTree creates a storage root and a config file pointing at it, and returns
// the config path and the storage root.
func newTree(t *testing.T) (cfgPath, storage string) {
	t.Helper()
	dir := t.TempDir()
	storage = filepath.Join(dir, "storage")
	if err := os.Mkdir(storage, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath = filepath.Join(dir, "goldencloud.yaml")
	body := "listen: \"127.0.0.1:0\"\n" +
		"storage_root: \"" + storage + "\"\n" +
		"users_file: \"users.yaml\"\n" +
		"require_mountpoint: false\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath, storage
}

func usersOf(t *testing.T, cfgPath string) []config.User {
	t.Helper()
	users, err := config.LoadUsers(filepath.Join(filepath.Dir(cfgPath), "users.yaml"))
	if err != nil {
		t.Fatalf("LoadUsers: %v", err)
	}
	return users
}

// --- top level -------------------------------------------------------------------

func TestVersion(t *testing.T) {
	t.Parallel()
	r := cli(t, "", "version")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, version) {
		t.Fatalf("stdout %q does not contain version %q", r.stdout, version)
	}
}

func TestUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{}, {"help"}, {"--help"}} {
		r := cli(t, "", args...)
		combined := r.stdout + r.stderr
		for _, want := range []string{"serve", "user", "version"} {
			if !strings.Contains(combined, want) {
				t.Errorf("usage for %v missing %q:\n%s", args, want, combined)
			}
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	t.Parallel()
	r := cli(t, "", "frobnicate")
	if r.code == 0 {
		t.Fatal("unknown command exited 0")
	}
	if !strings.Contains(r.stderr, "frobnicate") {
		t.Fatalf("stderr should name the unknown command: %q", r.stderr)
	}
}

// --- user add ---------------------------------------------------------------------

func TestUserAdd(t *testing.T) {
	t.Parallel()
	cfg, storage := newTree(t)

	r := cli(t, "hunter2hunter2\n", "user", "add", "alice", "--config", cfg, "--password-stdin")
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
	}
	users := usersOf(t, cfg)
	if len(users) != 1 || users[0].Username != "alice" {
		t.Fatalf("users = %+v", users)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(users[0].PasswordHash), []byte("hunter2hunter2")); err != nil {
		t.Fatalf("stored hash does not verify: %v", err)
	}
	st, err := os.Stat(filepath.Join(storage, "alice"))
	if err != nil {
		t.Fatalf("user folder not created: %v", err)
	}
	if !st.IsDir() {
		t.Fatal("user folder is not a directory")
	}
	if perm := st.Mode().Perm(); perm != 0o700 {
		t.Fatalf("user folder mode = %o, want 700", perm)
	}
	if strings.Contains(r.stdout, users[0].PasswordHash) {
		t.Fatal("user add printed the password hash")
	}
}

func TestUserAddWithQuota(t *testing.T) {
	t.Parallel()
	cfg, _ := newTree(t)
	r := cli(t, "hunter2hunter2\n", "user", "add", "alice", "--config", cfg, "--password-stdin", "--quota", "50GB")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if got := usersOf(t, cfg)[0].Quota; got != 50*1000*1000*1000 {
		t.Fatalf("quota = %d", got)
	}
}

func TestUserAddRejections(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		stdin string
		args  []string
		want  string
	}{
		{"no username", "pw\n", []string{"user", "add", "--password-stdin"}, "username"},
		{"illegal username", "hunter2hunter2\n", []string{"user", "add", "../evil", "--password-stdin"}, "username"},
		{"uppercase username", "hunter2hunter2\n", []string{"user", "add", "Alice", "--password-stdin"}, "lowercase"},
		{"short password", "abc\n", []string{"user", "add", "alice", "--password-stdin"}, "characters"},
		{"empty password", "\n", []string{"user", "add", "alice", "--password-stdin"}, "password"},
		{"bad quota", "hunter2hunter2\n", []string{"user", "add", "alice", "--password-stdin", "--quota", "fifty"}, "quota"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := newTree(t)
			args := append(append([]string{}, tc.args...), "--config", cfg)
			r := cli(t, tc.stdin, args...)
			if r.code == 0 {
				t.Fatalf("expected failure, got 0\nstdout: %s", r.stdout)
			}
			if !strings.Contains(strings.ToLower(r.stderr), tc.want) {
				t.Fatalf("stderr %q should mention %q", r.stderr, tc.want)
			}
		})
	}
}

func TestUserAddDuplicate(t *testing.T) {
	t.Parallel()
	cfg, _ := newTree(t)
	if r := cli(t, "hunter2hunter2\n", "user", "add", "alice", "--config", cfg, "--password-stdin"); r.code != 0 {
		t.Fatalf("first add failed: %s", r.stderr)
	}
	r := cli(t, "hunter2hunter2\n", "user", "add", "alice", "--config", cfg, "--password-stdin")
	if r.code == 0 {
		t.Fatal("duplicate add succeeded")
	}
	if !strings.Contains(r.stderr, "alice") {
		t.Fatalf("stderr = %q", r.stderr)
	}
	if n := len(usersOf(t, cfg)); n != 1 {
		t.Fatalf("users after duplicate add = %d", n)
	}
}

func TestUserAddReusesExistingFolder(t *testing.T) {
	t.Parallel()
	cfg, storage := newTree(t)
	existing := filepath.Join(storage, "alice")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := cli(t, "hunter2hunter2\n", "user", "add", "alice", "--config", cfg, "--password-stdin"); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(existing, "keep.txt")); err != nil {
		t.Fatalf("existing data was destroyed: %v", err)
	}
}

// --- user list ---------------------------------------------------------------------

func TestUserListNeverPrintsHashes(t *testing.T) {
	t.Parallel()
	cfg, _ := newTree(t)
	cli(t, "hunter2hunter2\n", "user", "add", "alice", "--config", cfg, "--password-stdin", "--quota", "50GB")
	cli(t, "hunter2hunter2\n", "user", "add", "bob", "--config", cfg, "--password-stdin")

	r := cli(t, "", "user", "list", "--config", cfg)
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "alice") || !strings.Contains(r.stdout, "bob") {
		t.Fatalf("listing missing users:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "$2a$") || strings.Contains(r.stdout, "$2b$") || strings.Contains(r.stdout, "$2y$") {
		t.Fatalf("user list printed a password hash:\n%s", r.stdout)
	}
	for _, u := range usersOf(t, cfg) {
		if strings.Contains(r.stdout, u.PasswordHash) {
			t.Fatalf("user list printed %s's hash", u.Username)
		}
	}
	if !strings.Contains(r.stdout, "50GB") && !strings.Contains(r.stdout, "46.6GiB") {
		t.Fatalf("listing does not show the quota:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "unlimited") {
		t.Fatalf("listing does not mark bob as unlimited:\n%s", r.stdout)
	}
}

func TestUserListEmpty(t *testing.T) {
	t.Parallel()
	cfg, _ := newTree(t)
	r := cli(t, "", "user", "list", "--config", cfg)
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if !strings.Contains(strings.ToLower(r.stdout+r.stderr), "no users") {
		t.Fatalf("empty listing should say so: %q", r.stdout+r.stderr)
	}
}

// --- user passwd -------------------------------------------------------------------

func TestUserPasswd(t *testing.T) {
	t.Parallel()
	cfg, _ := newTree(t)
	cli(t, "hunter2hunter2\n", "user", "add", "alice", "--config", cfg, "--password-stdin")
	old := usersOf(t, cfg)[0].PasswordHash

	r := cli(t, "correct-horse-battery\n", "user", "passwd", "alice", "--config", cfg, "--password-stdin")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	got := usersOf(t, cfg)[0]
	if got.PasswordHash == old {
		t.Fatal("hash unchanged")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(got.PasswordHash), []byte("correct-horse-battery")); err != nil {
		t.Fatalf("new password does not verify: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(got.PasswordHash), []byte("hunter2hunter2")); err == nil {
		t.Fatal("old password still verifies")
	}
}

func TestUserPasswdUnknown(t *testing.T) {
	t.Parallel()
	cfg, _ := newTree(t)
	r := cli(t, "correct-horse-battery\n", "user", "passwd", "nobody", "--config", cfg, "--password-stdin")
	if r.code == 0 {
		t.Fatal("passwd for an unknown user succeeded")
	}
	if !strings.Contains(r.stderr, "nobody") {
		t.Fatalf("stderr = %q", r.stderr)
	}
}

// --- user remove -------------------------------------------------------------------

func TestUserRemoveKeepsDataByDefault(t *testing.T) {
	t.Parallel()
	cfg, storage := newTree(t)
	cli(t, "hunter2hunter2\n", "user", "add", "alice", "--config", cfg, "--password-stdin")
	cli(t, "hunter2hunter2\n", "user", "add", "bob", "--config", cfg, "--password-stdin")
	if err := os.WriteFile(filepath.Join(storage, "alice", "work.txt"), []byte("work"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := cli(t, "", "user", "remove", "alice", "--config", cfg)
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	users := usersOf(t, cfg)
	if len(users) != 1 || users[0].Username != "bob" {
		t.Fatalf("users after remove = %+v", users)
	}
	if _, err := os.Stat(filepath.Join(storage, "alice", "work.txt")); err != nil {
		t.Fatalf("remove deleted the user's data without being asked: %v", err)
	}
	if !strings.Contains(r.stdout, filepath.Join(storage, "alice")) {
		t.Fatalf("remove should say where the data was left:\n%s", r.stdout)
	}
}

func TestUserRemoveDeleteData(t *testing.T) {
	t.Parallel()
	cfg, storage := newTree(t)
	cli(t, "hunter2hunter2\n", "user", "add", "alice", "--config", cfg, "--password-stdin")
	if err := os.WriteFile(filepath.Join(storage, "alice", "work.txt"), []byte("work"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := cli(t, "", "user", "remove", "alice", "--config", cfg, "--delete-data")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(storage, "alice")); !os.IsNotExist(err) {
		t.Fatalf("--delete-data left the folder behind: %v", err)
	}
}

func TestUserRemoveUnknown(t *testing.T) {
	t.Parallel()
	cfg, _ := newTree(t)
	r := cli(t, "", "user", "remove", "nobody", "--config", cfg)
	if r.code == 0 {
		t.Fatal("removing an unknown user succeeded")
	}
}

// --- atomic rewrite ------------------------------------------------------------------

func TestUsersFileIsRewrittenAtomically(t *testing.T) {
	t.Parallel()
	cfg, _ := newTree(t)
	dir := filepath.Dir(cfg)
	cli(t, "hunter2hunter2\n", "user", "add", "alice", "--config", cfg, "--password-stdin")
	cli(t, "hunter2hunter2\n", "user", "add", "bob", "--config", cfg, "--password-stdin")
	cli(t, "", "user", "remove", "alice", "--config", cfg)

	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
		if strings.HasPrefix(e.Name(), ".") || strings.Contains(e.Name(), ".tmp") {
			t.Errorf("atomic-write temp file left behind: %q", e.Name())
		}
	}
	want := map[string]bool{"goldencloud.yaml": true, "storage": true, "users.yaml": true}
	if len(names) != len(want) {
		t.Fatalf("unexpected files in the config directory: %v", names)
	}
	for _, n := range names {
		if !want[n] {
			t.Fatalf("unexpected file %q in the config directory: %v", n, names)
		}
	}
	st, err := os.Stat(filepath.Join(dir, "users.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("users.yaml mode = %o, want 600", perm)
	}
}

// --- serve preflight ------------------------------------------------------------------

func TestPreflightMissingStorageRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := &config.Config{
		StorageRoot: filepath.Join(dir, "not-there"),
		UsersFile:   filepath.Join(dir, "users.yaml"),
	}
	err := preflight(cfg, nil)
	if err == nil {
		t.Fatal("preflight passed with a missing storage root")
	}
	if !strings.Contains(err.Error(), cfg.StorageRoot) {
		t.Fatalf("error should name the path: %v", err)
	}
}

func TestPreflightStorageRootIsAFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "file")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := preflight(&config.Config{StorageRoot: f}, nil); err == nil {
		t.Fatal("preflight passed with a file as the storage root")
	}
}

// D-008: a temp directory is never a mountpoint, so requiring one must fail
// loudly rather than silently writing to the wrong disk.
func TestPreflightRequiresMountpoint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := &config.Config{StorageRoot: dir, RequireMountpoint: true}
	err := preflight(cfg, nil)
	if err == nil {
		t.Fatal("preflight passed on a non-mountpoint with require_mountpoint: true")
	}
	for _, want := range []string{dir, "mount"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}

func TestPreflightCreatesMissingUserFolders(t *testing.T) {
	t.Parallel()
	storage := t.TempDir()
	users := []config.User{{Username: "alice", Root: "alice"}, {Username: "bob", Root: "bob"}}
	if err := preflight(&config.Config{StorageRoot: storage}, users); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	for _, u := range users {
		st, err := os.Stat(filepath.Join(storage, u.Root))
		if err != nil {
			t.Fatalf("%s: %v", u.Username, err)
		}
		if perm := st.Mode().Perm(); perm != 0o700 {
			t.Errorf("%s folder mode = %o, want 700", u.Username, perm)
		}
	}
}

func TestPreflightRejectsUserFolderThatIsAFile(t *testing.T) {
	t.Parallel()
	storage := t.TempDir()
	if err := os.WriteFile(filepath.Join(storage, "alice"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := preflight(&config.Config{StorageRoot: storage}, []config.User{{Username: "alice", Root: "alice"}})
	if err == nil {
		t.Fatal("preflight passed with a file where a user folder should be")
	}
}

func TestIsMountpointOnRoot(t *testing.T) {
	t.Parallel()
	ok, err := isMountpoint("/")
	if err != nil {
		t.Fatalf("isMountpoint(/): %v", err)
	}
	if !ok {
		t.Fatal("/ is not reported as a mountpoint")
	}
}

func TestIsMountpointOnTempDir(t *testing.T) {
	t.Parallel()
	ok, err := isMountpoint(t.TempDir())
	if err != nil {
		t.Fatalf("isMountpoint: %v", err)
	}
	if ok {
		t.Fatal("a temp directory was reported as a mountpoint")
	}
}

// --- serve argument handling ----------------------------------------------------------

func TestServeRejectsBadConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	// Non-loopback with no TLS and no trusted proxy: D-003 must refuse.
	body := "listen: \"0.0.0.0:8080\"\nstorage_root: \"" + dir + "\"\nusers_file: \"users.yaml\"\n"
	if err := os.WriteFile(bad, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r := cli(t, "", "serve", "--config", bad)
	if r.code == 0 {
		t.Fatal("serve started with a config D-003 forbids")
	}
	if !strings.Contains(r.stderr, "listen") {
		t.Fatalf("stderr = %q", r.stderr)
	}
}

func TestServeRejectsMissingConfig(t *testing.T) {
	t.Parallel()
	r := cli(t, "", "serve", "--config", filepath.Join(t.TempDir(), "nope.yaml"))
	if r.code == 0 {
		t.Fatal("serve started without a config file")
	}
}
