package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PV80/GoldenCloud/server/internal/config"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadServerDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	users := write(t, dir, "users.yaml", "users: []\n")
	p := write(t, dir, "server.yaml", "storage_root: \""+dir+"\"\nusers_file: \""+users+"\"\n")

	c, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Listen != config.DefaultListen {
		t.Errorf("Listen = %q, want default %q", c.Listen, config.DefaultListen)
	}
	if c.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", c.LogLevel)
	}
	if c.RequireMountpoint {
		t.Errorf("RequireMountpoint = true, want false by default (D-022)")
	}
	if c.TLS.Enabled || c.TrustedProxy.Enabled {
		t.Errorf("TLS/TrustedProxy should default to false")
	}
	if c.TrustedProxy.Header != config.DefaultTrustedProxyHeader {
		t.Errorf("TrustedProxy.Header = %q, want %q", c.TrustedProxy.Header, config.DefaultTrustedProxyHeader)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	users := write(t, dir, "users.yaml", "users: []\n")
	p := write(t, dir, "server.yaml",
		"storage_root: \""+dir+"\"\nusers_file: \""+users+"\"\nlissten: \":80\"\n")
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("want error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "lissten") {
		t.Fatalf("error should name the offending field, got: %v", err)
	}
}

func TestLoadReportsLineNumbers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	users := write(t, dir, "users.yaml", "users: []\n")
	p := write(t, dir, "server.yaml",
		"storage_root: \""+dir+"\"\nusers_file: \""+users+"\"\nlog_level: shouty\n")
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("want error for bad log_level, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"log_level", "line 3", "shouty"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should contain %q", msg, want)
		}
	}
}

// D-003: plaintext HTTP on a non-loopback listener must refuse to start.
func TestD003NonLoopbackRequiresExplicitOptIn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		listen       string
		tls          bool
		trustedProxy bool
		wantErr      bool
	}{
		{"loopback ipv4 plaintext", "127.0.0.1:8080", false, false, false},
		{"loopback ipv4 other", "127.0.0.53:8080", false, false, false},
		{"loopback ipv6 plaintext", "[::1]:8080", false, false, false},
		{"localhost plaintext", "localhost:8080", false, false, false},
		{"wildcard plaintext", "0.0.0.0:8080", false, false, true},
		{"wildcard v6 plaintext", "[::]:8080", false, false, true},
		{"empty host plaintext", ":8080", false, false, true},
		{"lan address plaintext", "192.168.1.10:8080", false, false, true},
		{"lan address with tls", "192.168.1.10:8080", true, false, false},
		{"lan address with trusted proxy", "192.168.1.10:8080", false, true, false},
		{"wildcard with tls", "0.0.0.0:443", true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			users := write(t, dir, "users.yaml", "users: []\n")
			body := "listen: \"" + tc.listen + "\"\n" +
				"storage_root: \"" + dir + "\"\n" +
				"users_file: \"" + users + "\"\n"
			if tc.tls {
				cert := write(t, dir, "c.pem", "x")
				key := write(t, dir, "k.pem", "x")
				body += "tls:\n  enabled: true\n  cert_file: \"" + cert + "\"\n  key_file: \"" + key + "\"\n"
			}
			if tc.trustedProxy {
				body += "trusted_proxy:\n  enabled: true\n  header: \"X-Forwarded-For\"\n  allowed_cidrs: [\"127.0.0.1/32\"]\n"
			}
			p := write(t, dir, "server.yaml", body)
			_, err := config.Load(p)
			if tc.wantErr && err == nil {
				t.Fatalf("listen %q with tls=%v proxy=%v: want refusal, got nil",
					tc.listen, tc.tls, tc.trustedProxy)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("listen %q with tls=%v proxy=%v: unexpected error: %v",
					tc.listen, tc.tls, tc.trustedProxy, err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "listen") {
				t.Errorf("refusal should name the listen field, got: %v", err)
			}
		})
	}
}

func TestTrustedProxyRequiresAllowedCIDRs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	users := write(t, dir, "users.yaml", "users: []\n")
	p := write(t, dir, "server.yaml",
		"listen: \"0.0.0.0:8080\"\nstorage_root: \""+dir+"\"\nusers_file: \""+users+"\"\n"+
			"trusted_proxy:\n  enabled: true\n  header: \"X-Forwarded-For\"\n  allowed_cidrs: []\n")
	_, err := config.Load(p)
	if err == nil || !strings.Contains(err.Error(), "allowed_cidrs") {
		t.Fatalf("want allowed_cidrs error, got %v", err)
	}
}

func TestTrustedProxyRejectsBadCIDR(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	users := write(t, dir, "users.yaml", "users: []\n")
	p := write(t, dir, "server.yaml",
		"listen: \"127.0.0.1:8080\"\nstorage_root: \""+dir+"\"\nusers_file: \""+users+"\"\n"+
			"trusted_proxy:\n  enabled: true\n  allowed_cidrs: [\"not-a-cidr\"]\n")
	_, err := config.Load(p)
	if err == nil || !strings.Contains(err.Error(), "not-a-cidr") {
		t.Fatalf("want CIDR error, got %v", err)
	}
}

// The example config shipped in deploy/ must load. It is what every operator
// copies, and a server that cannot read its own documented configuration is
// worse than no example at all.
func TestShippedExampleConfigLoads(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../../deploy/goldencloud.example.yaml")
	if err != nil {
		t.Skipf("deploy/goldencloud.example.yaml not present: %v", err)
	}
	dir := t.TempDir()
	storage := filepath.Join(dir, "storage")
	if err := os.Mkdir(storage, 0o755); err != nil {
		t.Fatal(err)
	}
	users := write(t, dir, "users.yaml", "users: []\n")
	body := strings.ReplaceAll(string(raw), "/mnt/wd/goldencloud", storage)
	body = strings.ReplaceAll(body, "/etc/goldencloud/users.yaml", users)
	p := write(t, dir, "config.yaml", body)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("the shipped example config does not load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:8080" {
		t.Errorf("example listen = %q", cfg.Listen)
	}
	if cfg.TrustedProxy.Enabled || cfg.TLS.Enabled {
		t.Errorf("the example config should ship with tls and trusted_proxy disabled")
	}
}

func TestTLSRequiresCertAndKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	users := write(t, dir, "users.yaml", "users: []\n")
	p := write(t, dir, "server.yaml",
		"listen: \"0.0.0.0:443\"\nstorage_root: \""+dir+"\"\nusers_file: \""+users+"\"\n"+
			"tls:\n  enabled: true\n")
	_, err := config.Load(p)
	if err == nil || !strings.Contains(err.Error(), "cert_file") {
		t.Fatalf("want cert_file error, got %v", err)
	}
}

func TestStorageRootMustBeAbsolute(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	users := write(t, dir, "users.yaml", "users: []\n")
	p := write(t, dir, "server.yaml", "storage_root: \"relative/path\"\nusers_file: \""+users+"\"\n")
	_, err := config.Load(p)
	if err == nil || !strings.Contains(err.Error(), "storage_root") {
		t.Fatalf("want storage_root error, got %v", err)
	}
}

func TestMissingRequiredFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := write(t, dir, "server.yaml", "listen: \"127.0.0.1:1\"\n")
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("want error for missing storage_root/users_file")
	}
	if !strings.Contains(err.Error(), "storage_root") {
		t.Errorf("error should name storage_root, got %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()
	_, err := config.Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("want error for missing config file")
	}
}

// --- users.yaml ---------------------------------------------------------------

const aliceHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

func TestLoadUsers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := write(t, dir, "users.yaml", `users:
  - username: alice
    password_hash: "`+aliceHash+`"
    quota: 50GB
    root: alice
  - username: bob
    password_hash: "`+aliceHash+`"
    root: bob-folder
`)
	us, err := config.LoadUsers(p)
	if err != nil {
		t.Fatalf("LoadUsers: %v", err)
	}
	if len(us) != 2 {
		t.Fatalf("got %d users, want 2", len(us))
	}
	if us[0].Username != "alice" || us[0].Root != "alice" {
		t.Errorf("alice = %+v", us[0])
	}
	if us[0].Quota != 50*1000*1000*1000 {
		t.Errorf("alice quota = %d, want 50e9", us[0].Quota)
	}
	if us[1].Quota != 0 {
		t.Errorf("bob quota = %d, want 0 (unlimited)", us[1].Quota)
	}
}

func TestLoadUsersDefaultsRootToUsername(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := write(t, dir, "users.yaml", "users:\n  - username: carol\n    password_hash: \""+aliceHash+"\"\n")
	us, err := config.LoadUsers(p)
	if err != nil {
		t.Fatal(err)
	}
	if us[0].Root != "carol" {
		t.Fatalf("Root = %q, want %q", us[0].Root, "carol")
	}
}

func TestLoadUsersErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			"duplicate username",
			"users:\n  - username: a\n    password_hash: \"" + aliceHash + "\"\n" +
				"  - username: a\n    password_hash: \"" + aliceHash + "\"\n",
			[]string{"username", "duplicate", "line 4"},
		},
		{
			"empty username",
			"users:\n  - username: \"\"\n    password_hash: \"" + aliceHash + "\"\n",
			[]string{"username", "line 2"},
		},
		{
			"illegal username",
			"users:\n  - username: \"../root\"\n    password_hash: \"" + aliceHash + "\"\n",
			[]string{"username", "line 2"},
		},
		{
			"uppercase username",
			"users:\n  - username: Alice\n    password_hash: \"" + aliceHash + "\"\n",
			[]string{"username", "lowercase"},
		},
		{
			"missing hash",
			"users:\n  - username: a\n",
			[]string{"password_hash", "line 2"},
		},
		{
			"plaintext password in hash field",
			"users:\n  - username: a\n    password_hash: \"hunter2\"\n",
			[]string{"password_hash", "bcrypt", "line 3"},
		},
		{
			"bad quota",
			"users:\n  - username: a\n    password_hash: \"" + aliceHash + "\"\n    quota: \"fifty gigs\"\n",
			[]string{"quota", "line 4"},
		},
		{
			"traversal in root",
			"users:\n  - username: a\n    password_hash: \"" + aliceHash + "\"\n    root: \"../../etc\"\n",
			[]string{"root", "line 4"},
		},
		{
			"absolute root",
			"users:\n  - username: a\n    password_hash: \"" + aliceHash + "\"\n    root: \"/etc\"\n",
			[]string{"root", "line 4"},
		},
		{
			"duplicate root",
			"users:\n  - username: a\n    password_hash: \"" + aliceHash + "\"\n    root: shared\n" +
				"  - username: b\n    password_hash: \"" + aliceHash + "\"\n    root: shared\n",
			[]string{"root", "line 7"},
		},
		{
			"unknown field",
			"users:\n  - username: a\n    password_hash: \"" + aliceHash + "\"\n    quotaa: 1GB\n",
			[]string{"quotaa"},
		},
		{
			"not a mapping",
			"users:\n  - alice\n",
			[]string{"line 2"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := write(t, dir, "users.yaml", tc.body)
			_, err := config.LoadUsers(p)
			if err == nil {
				t.Fatalf("want error, got nil")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q should contain %q", err.Error(), w)
				}
			}
		})
	}
}

func TestLoadUsersEmptyIsValid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, body := range []string{"users: []\n", "", "users:\n"} {
		p := write(t, dir, "users.yaml", body)
		us, err := config.LoadUsers(p)
		if err != nil {
			t.Fatalf("body %q: %v", body, err)
		}
		if len(us) != 0 {
			t.Fatalf("body %q: got %d users", body, len(us))
		}
	}
}

// --- size parsing --------------------------------------------------------------

func TestParseSize(t *testing.T) {
	t.Parallel()
	ok := []struct {
		in   string
		want int64
	}{
		{"0", 0},
		{"1024", 1024},
		{"50GB", 50 * 1000 * 1000 * 1000},
		{"50 GB", 50 * 1000 * 1000 * 1000},
		{"50gb", 50 * 1000 * 1000 * 1000},
		{"1KB", 1000},
		{"1KiB", 1024},
		{"1MiB", 1024 * 1024},
		{"2GiB", 2 * 1024 * 1024 * 1024},
		{"1TB", 1000 * 1000 * 1000 * 1000},
		{"1T", 1024 * 1024 * 1024 * 1024},
		{"3M", 3 * 1024 * 1024},
		{"1.5GiB", 1610612736},
		{"  8G  ", 8 * 1024 * 1024 * 1024},
	}
	for _, tc := range ok {
		got, err := config.ParseSize(tc.in)
		if err != nil {
			t.Errorf("ParseSize(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseSize(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
	bad := []string{"", "GB", "-5GB", "fifty", "5XB", "5 5GB", "1e9GB", "٥GB", "9999999999999999999999GB"}
	for _, in := range bad {
		if got, err := config.ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) = %d, want error", in, got)
		}
	}
}

func TestFormatSize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int64
		want string
	}{
		{0, "unlimited"},
		{512, "512B"},
		{1024, "1KiB"},
		{50 * 1000 * 1000 * 1000, "46.6GiB"},
		{2 * 1024 * 1024 * 1024, "2GiB"},
	}
	for _, tc := range cases {
		if got := config.FormatSize(tc.in); got != tc.want {
			t.Errorf("FormatSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- user helpers ---------------------------------------------------------------

func TestUserDirIsUnderStorageRoot(t *testing.T) {
	t.Parallel()
	u := config.User{Username: "alice", Root: "alice"}
	got := u.Dir("/mnt/wd")
	if got != filepath.Join("/mnt/wd", "alice") {
		t.Fatalf("Dir = %q", got)
	}
}

func TestSaveUsersAtomicRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "users.yaml")
	in := []config.User{
		{Username: "alice", PasswordHash: aliceHash, Root: "alice", Quota: 50 * 1000 * 1000 * 1000},
		{Username: "bob", PasswordHash: aliceHash, Root: "bob"},
	}
	if err := config.SaveUsers(p, in); err != nil {
		t.Fatalf("SaveUsers: %v", err)
	}
	out, err := config.LoadUsers(p)
	if err != nil {
		t.Fatalf("LoadUsers after save: %v", err)
	}
	if len(out) != 2 || out[0].Username != "alice" || out[1].Username != "bob" {
		t.Fatalf("round trip mismatch: %+v", out)
	}
	if out[0].Quota != in[0].Quota {
		t.Fatalf("quota lost: %d", out[0].Quota)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("users.yaml mode = %o, want 600 (it holds password hashes)", perm)
	}
	// No temp files left behind.
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("stray files after atomic save: %d entries", len(ents))
	}
}

func TestSaveUsersRejectsInvalid(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "users.yaml")
	err := config.SaveUsers(p, []config.User{{Username: "../evil", PasswordHash: aliceHash}})
	if err == nil {
		t.Fatal("SaveUsers should validate before writing")
	}
	if _, statErr := os.Stat(p); statErr == nil {
		t.Fatal("SaveUsers wrote a file despite failing validation")
	}
}
