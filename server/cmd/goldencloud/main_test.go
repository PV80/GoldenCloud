package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/PV80/GoldenCloud/server/internal/auth"
	"github.com/PV80/GoldenCloud/server/internal/config"
	"github.com/PV80/GoldenCloud/server/internal/webdavx"
)

// TestMain turns the bcrypt work factor down for the suite. Hashing a dozen
// passwords at the production cost of 12 costs half a minute of CI time and
// proves nothing that cost 4 does not.
func TestMain(m *testing.M) {
	bcryptCost = bcrypt.MinCost
	os.Exit(m.Run())
}

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

// D-008/D-022: when require_mountpoint is set and the storage lives on the root
// filesystem, the share is not mounted and the server must refuse to start
// rather than silently fill the local disk.
func TestPreflightRequiresMountpoint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mp, err := nearestMountpoint(dir)
	if err != nil {
		t.Fatalf("nearestMountpoint: %v", err)
	}
	if mp != "/" {
		t.Skipf("this machine's temp directory is already on a dedicated filesystem (%s)", mp)
	}
	cfg := &config.Config{StorageRoot: dir, RequireMountpoint: true}
	err = preflight(cfg, nil)
	if err == nil {
		t.Fatal("preflight passed on the root filesystem with require_mountpoint: true")
	}
	for _, want := range []string{dir, "mount"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}

func TestNearestMountpointFindsAnAncestor(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat("/proc/self"); err != nil {
		t.Skip("no /proc on this machine")
	}
	got, err := nearestMountpoint("/proc/self/fd")
	if err != nil {
		t.Fatalf("nearestMountpoint: %v", err)
	}
	if got != "/proc" {
		t.Fatalf("nearestMountpoint(/proc/self/fd) = %q, want /proc", got)
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

func TestNearestMountpointOfRoot(t *testing.T) {
	t.Parallel()
	got, err := nearestMountpoint("/")
	if err != nil {
		t.Fatalf("nearestMountpoint(/): %v", err)
	}
	if got != "/" {
		t.Fatalf("nearestMountpoint(/) = %q", got)
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
	dir := t.TempDir()
	ok, err := isMountpoint(dir)
	if err != nil {
		t.Fatalf("isMountpoint: %v", err)
	}
	if ok {
		t.Fatalf("%s was reported as a mountpoint", dir)
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

// --- serve internals ------------------------------------------------------------

func TestReloadPicksUpNewUsers(t *testing.T) {
	t.Parallel()
	cfgPath, storage := newTree(t)
	cli(t, "hunter2hunter2\n", "user", "add", "alice", "--config", cfgPath, "--password-stdin")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	users, err := config.LoadUsers(cfg.UsersFile)
	if err != nil {
		t.Fatal(err)
	}
	store := auth.NewStore(users)
	dav := webdavx.New(webdavx.Options{StorageRoot: storage, Logger: quietLogger()})
	defer dav.Close()

	cli(t, "hunter2hunter2\n", "user", "add", "bob", "--config", cfgPath, "--password-stdin")
	if err := reload(cfg, store, dav, quietLogger()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if n := len(store.Users()); n != 2 {
		t.Fatalf("store has %d users after reload, want 2", n)
	}
	if _, ok := store.Lookup("bob"); !ok {
		t.Fatal("bob is missing after reload")
	}

	cli(t, "", "user", "remove", "alice", "--config", cfgPath)
	if err := reload(cfg, store, dav, quietLogger()); err != nil {
		t.Fatalf("reload after remove: %v", err)
	}
	if _, ok := store.Lookup("alice"); ok {
		t.Fatal("alice survived a reload after being removed")
	}
}

func TestReloadRejectsABrokenUsersFile(t *testing.T) {
	t.Parallel()
	cfgPath, storage := newTree(t)
	cli(t, "hunter2hunter2\n", "user", "add", "alice", "--config", cfgPath, "--password-stdin")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	users, err := config.LoadUsers(cfg.UsersFile)
	if err != nil {
		t.Fatal(err)
	}
	store := auth.NewStore(users)
	dav := webdavx.New(webdavx.Options{StorageRoot: storage, Logger: quietLogger()})
	defer dav.Close()

	if err := os.WriteFile(cfg.UsersFile, []byte("users:\n  - username: alice\n    password_hash: hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reload(cfg, store, dav, quietLogger()); err == nil {
		t.Fatal("reload accepted a users.yaml with a plaintext password")
	}
	// The previous list must still be serving.
	if _, ok := store.Lookup("alice"); !ok {
		t.Fatal("a failed reload dropped the working user list")
	}
}

func TestParsePrefixes(t *testing.T) {
	t.Parallel()
	got, err := parsePrefixes([]string{"127.0.0.1/32", "10.0.0.0/8", "::1/128"})
	if err != nil {
		t.Fatalf("parsePrefixes: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d prefixes", len(got))
	}
	if _, err := parsePrefixes([]string{"not-a-cidr"}); err == nil {
		t.Fatal("parsePrefixes accepted a non-CIDR")
	}
}

func TestNewLoggerLevels(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		level string
		debug bool
	}{
		{"debug", true}, {"info", false}, {"warn", false}, {"error", false}, {"nonsense", false},
	} {
		var buf bytes.Buffer
		log := newLogger(streams{err: &buf}, tc.level)
		log.Debug("a debug line")
		if got := strings.Contains(buf.String(), "a debug line"); got != tc.debug {
			t.Errorf("level %q: debug logged = %v, want %v", tc.level, got, tc.debug)
		}
	}
}

func TestCheckPassword(t *testing.T) {
	t.Parallel()
	if err := checkPassword([]byte("shorty")); err == nil {
		t.Error("a 6-character password was accepted")
	}
	if err := checkPassword(bytes.Repeat([]byte("x"), 73)); err == nil {
		t.Error("a 73-byte password was accepted; bcrypt would silently truncate it")
	}
	if err := checkPassword([]byte("long-enough-password")); err != nil {
		t.Errorf("a good password was rejected: %v", err)
	}
}

func TestUnescapeMountinfo(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"/mnt/wd":            "/mnt/wd",
		`/mnt/my\040share`:   "/mnt/my share",
		`/mnt/a\011b`:        "/mnt/a\tb",
		`/mnt/back\134slash`: `/mnt/back\slash`,
		`/mnt/trailing\`:     `/mnt/trailing\`,
		`/mnt/bad\99x`:       `/mnt/bad\99x`,
		`/a\040b\040c`:       "/a b c",
	} {
		if got := unescapeMountinfo(in); got != want {
			t.Errorf("unescapeMountinfo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDifferentDeviceFromParent(t *testing.T) {
	t.Parallel()
	diff, err := differentDeviceFromParent(t.TempDir())
	if err != nil {
		t.Fatalf("differentDeviceFromParent: %v", err)
	}
	if diff {
		t.Skip("this machine's temp directory is its own filesystem")
	}
	if _, err := differentDeviceFromParent(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("differentDeviceFromParent succeeded on a missing path")
	}
}

func TestEnsureUserDirRejectsAFile(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureUserDir(p); err == nil {
		t.Fatal("ensureUserDir accepted a regular file")
	}
}

func TestUserSubcommandErrors(t *testing.T) {
	t.Parallel()
	cfg, _ := newTree(t)
	for _, args := range [][]string{
		{"user"},
		{"user", "frobnicate"},
		{"user", "passwd", "--config", cfg},
		{"user", "remove", "--config", cfg},
		{"user", "add", "a", "b", "--config", cfg},
	} {
		r := cli(t, "", args...)
		if r.code == 0 {
			t.Errorf("%v exited 0", args)
		}
		if r.stderr == "" {
			t.Errorf("%v said nothing on stderr", args)
		}
	}
}

func TestUserCommandsWithAnUnreadableConfig(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	for _, args := range [][]string{
		{"user", "list", "--config", missing},
		{"user", "add", "alice", "--password-stdin", "--config", missing},
		{"user", "remove", "alice", "--config", missing},
		{"user", "passwd", "alice", "--password-stdin", "--config", missing},
	} {
		r := cli(t, "hunter2hunter2\n", args...)
		if r.code == 0 {
			t.Errorf("%v exited 0 with a missing config", args)
		}
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
