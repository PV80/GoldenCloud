// Package config loads and validates the GoldenCloud server configuration and
// the user store (users.yaml).
//
// Validation is deliberately strict and noisy: an operator following
// deploy/RUNBOOK.md should get an error naming the offending field and the line
// it is on, not a server that starts and misbehaves. In particular it
// implements D-003 — the server refuses to start when it would accept HTTP
// Basic credentials in plaintext on a non-loopback interface.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// DefaultListen is the address used when the config file does not set one. It
// is loopback-only on purpose: the supported deployment terminates TLS at
// cloudflared, which connects over loopback.
const DefaultListen = "127.0.0.1:8080"

// DefaultLogLevel is used when log_level is unset.
const DefaultLogLevel = "info"

// DefaultTrustedProxyHeader is the header consulted for the client address when
// trusted_proxy is enabled and no header is named.
const DefaultTrustedProxyHeader = "X-Forwarded-For"

// TrustedProxy tells the server it sits behind a reverse proxy that has already
// terminated TLS. It is the explicit opt-in required by D-003.
type TrustedProxy struct {
	// Enabled allows plaintext HTTP on a non-loopback listen address and makes
	// the server believe Header for the client's address.
	Enabled bool `yaml:"enabled"`
	// Header carries the client address. Usually X-Forwarded-For; Cloudflare
	// also sends CF-Connecting-IP.
	Header string `yaml:"header"`
	// AllowedCIDRs are the networks the proxy itself connects from. Header is
	// only believed when the peer is inside one of them, because otherwise any
	// client could choose its own apparent address.
	AllowedCIDRs []string `yaml:"allowed_cidrs"`
}

// TLS holds the direct-TLS settings. In the supported deployment TLS is
// terminated by Cloudflare and this stays disabled.
type TLS struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// Config is the validated server configuration.
type Config struct {
	// Listen is the TCP address the HTTP server binds.
	Listen string `yaml:"listen"`
	// StorageRoot is the absolute directory that holds every user folder.
	StorageRoot string `yaml:"storage_root"`
	// UsersFile is the path to users.yaml.
	UsersFile string `yaml:"users_file"`
	// LogLevel is one of debug, info, warn, error.
	LogLevel string `yaml:"log_level"`
	// RequireMountpoint makes the server refuse to start unless StorageRoot
	// lives on a filesystem of its own rather than on the root filesystem.
	// See D-008 and D-022.
	RequireMountpoint bool `yaml:"require_mountpoint"`
	// TrustedProxy configures a reverse proxy in front of the server.
	TrustedProxy TrustedProxy `yaml:"trusted_proxy"`
	// TLS configures direct TLS termination by the server itself.
	TLS TLS `yaml:"tls"`

	// Path is the file this config was loaded from. Not settable in YAML.
	Path string `yaml:"-"`
}

// fileConfig mirrors Config but distinguishes "absent" from "false"/"" so that
// defaults can be applied only to fields the operator did not set.
type fileConfig struct {
	Listen            *string       `yaml:"listen"`
	StorageRoot       *string       `yaml:"storage_root"`
	UsersFile         *string       `yaml:"users_file"`
	LogLevel          *string       `yaml:"log_level"`
	RequireMountpoint *bool         `yaml:"require_mountpoint"`
	TrustedProxy      *TrustedProxy `yaml:"trusted_proxy"`
	TLS               *TLS          `yaml:"tls"`
}

// Load reads, defaults and validates the server configuration at path.
func Load(configPath string) (*Config, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", configPath, err)
	}

	var fc fileConfig
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&fc); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", configPath, err)
	}

	c := &Config{
		Listen:   DefaultListen,
		LogLevel: DefaultLogLevel,
		Path:     configPath,
		// D-022: default off. The systemd unit hard-requires the mount unit
		// (D-011), and with storage_root a sub-folder of the mount (D-010) an
		// unmounted share already fails the "storage_root does not exist"
		// check. Requiring a dedicated filesystem is an extra belt for
		// operators who want it, not the default.
		TrustedProxy: TrustedProxy{Header: DefaultTrustedProxyHeader},
	}
	if fc.Listen != nil {
		c.Listen = *fc.Listen
	}
	if fc.StorageRoot != nil {
		c.StorageRoot = *fc.StorageRoot
	}
	if fc.UsersFile != nil {
		c.UsersFile = *fc.UsersFile
	}
	if fc.LogLevel != nil {
		c.LogLevel = *fc.LogLevel
	}
	if fc.RequireMountpoint != nil {
		c.RequireMountpoint = *fc.RequireMountpoint
	}
	if fc.TrustedProxy != nil {
		c.TrustedProxy = *fc.TrustedProxy
		if c.TrustedProxy.Header == "" {
			c.TrustedProxy.Header = DefaultTrustedProxyHeader
		}
	}
	if fc.TLS != nil {
		c.TLS = *fc.TLS
	}

	// users_file may be given relative to the config file, which is how the
	// example config ships.
	if c.UsersFile != "" && !filepath.IsAbs(c.UsersFile) {
		c.UsersFile = filepath.Join(filepath.Dir(configPath), c.UsersFile)
	}

	if err := c.validate(&doc); err != nil {
		return nil, fmt.Errorf("%s: %w", configPath, err)
	}
	return c, nil
}

func (c *Config) validate(doc *yaml.Node) error {
	fail := func(field, format string, args ...any) error {
		msg := fmt.Sprintf(format, args...)
		if line := keyLine(doc, strings.Split(field, ".")...); line > 0 {
			return fmt.Errorf("line %d: %s: %s", line, field, msg)
		}
		return fmt.Errorf("%s: %s", field, msg)
	}

	if c.StorageRoot == "" {
		return fail("storage_root", "is required (the directory holding every user folder)")
	}
	if !filepath.IsAbs(c.StorageRoot) {
		return fail("storage_root", "must be an absolute path, got %q", c.StorageRoot)
	}
	if c.UsersFile == "" {
		return fail("users_file", "is required (the path to users.yaml)")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fail("log_level", "must be one of debug, info, warn, error; got %q", c.LogLevel)
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return fail("listen", "is not a host:port address: %v", err)
	}
	if c.TLS.Enabled {
		if c.TLS.CertFile == "" {
			return fail("tls.cert_file", "is required when tls.enabled is true")
		}
		if c.TLS.KeyFile == "" {
			return fail("tls.key_file", "is required when tls.enabled is true")
		}
	}

	if c.TrustedProxy.Enabled {
		if len(c.TrustedProxy.AllowedCIDRs) == 0 {
			return fail("trusted_proxy.allowed_cidrs",
				"must not be empty when trusted_proxy.enabled is true: without it "+
					"any client could forge its own apparent address in the %q header "+
					"and defeat rate limiting", c.TrustedProxy.Header)
		}
		for _, cidr := range c.TrustedProxy.AllowedCIDRs {
			if _, err := netip.ParsePrefix(cidr); err != nil {
				return fail("trusted_proxy.allowed_cidrs", "%q is not a CIDR range (want something like 127.0.0.1/32): %v", cidr, err)
			}
		}
	}

	// D-003. Basic credentials must never cross a network in the clear.
	if !c.TLS.Enabled && !c.TrustedProxy.Enabled && !isLoopbackAddr(c.Listen) {
		return fail("listen",
			"refusing to start: %q is not a loopback address and neither tls.enabled "+
				"nor trusted_proxy is set, so HTTP Basic passwords would cross the "+
				"network in plaintext. Either listen on 127.0.0.1 and put "+
				"cloudflared in front of it (the supported deployment), or set "+
				"tls.enabled, or set trusted_proxy: true if TLS really is "+
				"terminated by a proxy you control",
			c.Listen)
	}
	return nil
}

// isLoopbackAddr reports whether a host:port listen address binds only the
// loopback interface. Hostnames other than "localhost" are treated as
// non-loopback: resolving them at config time would make the safety of the
// configuration depend on DNS.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		return false // ":8080" binds every interface
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// keyLine walks a YAML document looking for a dotted key path and returns the
// 1-based line of its key token, or 0 if not present.
func keyLine(n *yaml.Node, keys ...string) int {
	if n == nil || len(keys) == 0 {
		return 0
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return 0
		}
		return keyLine(n.Content[0], keys...)
	}
	if n.Kind != yaml.MappingNode {
		return 0
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Value != keys[0] {
			continue
		}
		if len(keys) == 1 {
			return k.Line
		}
		return keyLine(v, keys[1:]...)
	}
	return 0
}

// --- users.yaml ----------------------------------------------------------------

// User is one entry in users.yaml.
type User struct {
	// Username is the HTTP Basic username. Lowercase, filesystem-safe.
	Username string `yaml:"username"`
	// PasswordHash is a bcrypt hash. Never a plaintext password.
	PasswordHash string `yaml:"password_hash"`
	// Quota is the storage limit in bytes, 0 meaning unlimited. Reported but
	// not enforced in v1 (see ROADMAP.md).
	Quota int64 `yaml:"-"`
	// Root is the user's folder, relative to Config.StorageRoot.
	Root string `yaml:"root"`
}

// Dir returns the absolute directory this user's jail is rooted at.
func (u User) Dir(storageRoot string) string {
	return filepath.Join(storageRoot, filepath.FromSlash(u.Root))
}

// userYAML is the on-disk shape; Quota is a human-readable string there.
type userYAML struct {
	Username     string `yaml:"username"`
	PasswordHash string `yaml:"password_hash"`
	Quota        string `yaml:"quota,omitempty"`
	Root         string `yaml:"root"`
}

type usersFile struct {
	Users []userYAML `yaml:"users"`
}

var userFields = map[string]bool{
	"username": true, "password_hash": true, "quota": true, "root": true,
}

var usernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// ValidateUsername checks a username. Usernames are lowercase and
// filesystem-safe because they are also, by default, folder names: allowing
// "Alice" and "alice" to differ would create two accounts that collide on a
// case-insensitive filesystem.
func ValidateUsername(name string) error {
	switch {
	case name == "":
		return errors.New("a username is required")
	case strings.ToLower(name) != name:
		return fmt.Errorf("username %q must be lowercase", name)
	case !usernameRE.MatchString(name):
		return fmt.Errorf("username %q is not allowed; use 1-32 characters from a-z, 0-9, dot, dash and underscore, starting with a letter or digit", name)
	}
	return nil
}

// ValidateRoot checks a user folder name: it must be a clean relative path
// that stays inside the storage root.
func ValidateRoot(root string) error { return validateRoot(root) }

// LoadUsers reads and validates users.yaml.
func LoadUsers(path string) ([]User, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("users file: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	users, err := parseUsers(&doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return users, nil
}

func parseUsers(doc *yaml.Node) ([]User, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil // empty file
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: top level must be a mapping with a \"users\" key", root.Line)
	}
	var seq *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if k.Value == "users" {
			seq = v
			continue
		}
		return nil, fmt.Errorf("line %d: unknown field %q (only \"users\" is allowed)", k.Line, k.Value)
	}
	if seq == nil || seq.Tag == "!!null" {
		return nil, nil
	}
	if seq.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("line %d: users: must be a list", seq.Line)
	}

	out := make([]User, 0, len(seq.Content))
	seenName := map[string]int{}
	seenRoot := map[string]int{}

	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("line %d: each users entry must be a mapping with username and password_hash", item.Line)
		}
		lineOf := func(field string) int {
			if l := keyLine(item, field); l > 0 {
				return l
			}
			return item.Line
		}
		fail := func(field, format string, args ...any) error {
			return fmt.Errorf("line %d: users: %s: %s", lineOf(field), field, fmt.Sprintf(format, args...))
		}
		for i := 0; i+1 < len(item.Content); i += 2 {
			k := item.Content[i]
			if !userFields[k.Value] {
				return nil, fmt.Errorf("line %d: users: unknown field %q", k.Line, k.Value)
			}
		}
		var uy userYAML
		if err := item.Decode(&uy); err != nil {
			return nil, fmt.Errorf("line %d: users: %w", item.Line, err)
		}

		if err := ValidateUsername(uy.Username); err != nil {
			return nil, fail("username", "%v", err)
		}
		if prev, dup := seenName[uy.Username]; dup {
			return nil, fmt.Errorf("line %d: users: username: duplicate username %q (first defined on line %d)",
				lineOf("username"), uy.Username, prev)
		}
		seenName[uy.Username] = lineOf("username")

		if uy.PasswordHash == "" {
			return nil, fail("password_hash", "is required; create the user with \"goldencloud user add %s\" rather than editing this file", uy.Username)
		}
		if _, err := bcrypt.Cost([]byte(uy.PasswordHash)); err != nil {
			return nil, fail("password_hash", "is not a bcrypt hash (%v); it must never contain a plaintext password", err)
		}

		u := User{Username: uy.Username, PasswordHash: uy.PasswordHash, Root: uy.Root}
		if u.Root == "" {
			u.Root = uy.Username
		}
		if err := validateRoot(u.Root); err != nil {
			return nil, fail("root", "%v", err)
		}
		if prev, dup := seenRoot[u.Root]; dup {
			return nil, fmt.Errorf("line %d: users: root: %q is already used by another user (line %d); folders must not be shared",
				lineOf("root"), u.Root, prev)
		}
		seenRoot[u.Root] = lineOf("root")

		if uy.Quota != "" {
			n, err := ParseSize(uy.Quota)
			if err != nil {
				return nil, fail("quota", "%v", err)
			}
			u.Quota = n
		}
		out = append(out, u)
	}
	return out, nil
}

// validateRoot checks a user folder name. It must be a relative path inside the
// storage root; anything that could climb out is refused here as well as by the
// jail, because a bad users.yaml should fail loudly at load rather than at the
// first request.
func validateRoot(root string) error {
	if root == "" {
		return errors.New("must not be empty")
	}
	if strings.ContainsAny(root, "\x00\\:") {
		return fmt.Errorf("%q contains a NUL byte, backslash or colon", root)
	}
	if filepath.IsAbs(root) || strings.HasPrefix(root, "/") {
		return fmt.Errorf("%q must be relative to storage_root, not absolute", root)
	}
	clean := path.Clean(root)
	if clean != root {
		return fmt.Errorf("%q is not a clean path (did you mean %q?)", root, clean)
	}
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("%q escapes storage_root", root)
	}
	return nil
}

// SaveUsers validates users and writes them to path atomically: a temporary
// file in the same directory, fsynced, then renamed over the target, with the
// directory fsynced afterwards. A crash mid-write leaves the previous file
// intact rather than a truncated user list. See D-004.
func SaveUsers(path string, users []User) error {
	uf := usersFile{Users: make([]userYAML, 0, len(users))}
	for _, u := range users {
		root := u.Root
		if root == "" {
			root = u.Username
		}
		uy := userYAML{
			Username:     u.Username,
			PasswordHash: u.PasswordHash,
			Root:         root,
		}
		if u.Quota > 0 {
			uy.Quota = formatSizeExact(u.Quota)
		}
		uf.Users = append(uf.Users, uy)
	}

	var buf bytes.Buffer
	buf.WriteString("# GoldenCloud user store. Managed by \"goldencloud user\" — see server/README.md.\n")
	buf.WriteString("# Passwords are bcrypt hashes; there is no way to recover a lost password,\n")
	buf.WriteString("# only to set a new one with \"goldencloud user passwd <name>\".\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(uf); err != nil {
		return fmt.Errorf("users file: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("users file: %w", err)
	}

	// Validate what we are about to write, so a bad in-memory list can never
	// be persisted.
	var doc yaml.Node
	if err := yaml.Unmarshal(buf.Bytes(), &doc); err != nil {
		return fmt.Errorf("users file: %w", err)
	}
	if _, err := parseUsers(&doc); err != nil {
		return fmt.Errorf("refusing to write an invalid users file: %w", err)
	}

	// A fresh file is private to its owner. An existing one keeps the mode and
	// ownership the operator gave it — deploy/RUNBOOK.md sets 0640
	// root:goldencloud so the server, which runs as goldencloud, can read a
	// file only root can write. Forcing 0600 here would lock the server out of
	// its own user list the first time an admin ran "user add".
	return writeFileAtomic(path, buf.Bytes(), 0o600)
}

func writeFileAtomic(dst string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".tmp*")
	if err != nil {
		return fmt.Errorf("atomic write: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op once the rename has succeeded
	}()

	if st, err := os.Stat(dst); err == nil {
		perm = st.Mode().Perm()
	}
	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("atomic write: %w", err)
	}
	// On Unix, hand the temp file the existing file's owner so a root-written
	// users.yaml keeps its root:goldencloud ownership across a rewrite. A no-op
	// on platforms without Unix ownership (e.g. a Windows box used for local
	// testing), which is why it lives in a build-tagged helper.
	preserveOwner(tmp, dst)
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("atomic write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("atomic write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("atomic write: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("atomic write: %w", err)
	}
	// Fsync the directory so the rename itself survives a power cut.
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("atomic write: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return fmt.Errorf("atomic write: fsync %s: %w", dir, err)
	}
	return nil
}

// --- human-readable sizes --------------------------------------------------------

var sizeUnits = map[string]int64{
	"":    1,
	"b":   1,
	"k":   1 << 10,
	"kib": 1 << 10,
	"kb":  1000,
	"m":   1 << 20,
	"mib": 1 << 20,
	"mb":  1000 * 1000,
	"g":   1 << 30,
	"gib": 1 << 30,
	"gb":  1000 * 1000 * 1000,
	"t":   1 << 40,
	"tib": 1 << 40,
	"tb":  1000 * 1000 * 1000 * 1000,
	"p":   1 << 50,
	"pib": 1 << 50,
	"pb":  1000 * 1000 * 1000 * 1000 * 1000,
}

// ParseSize converts a human-readable size such as "50GB", "500 MiB" or "1024"
// into bytes. A bare letter suffix is binary (1G = 1GiB = 2^30); a letter
// followed by "B" is decimal (1GB = 10^9), matching how disks are sold.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty size")
	}
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	numPart, unitPart := s[:i], strings.TrimSpace(s[i:])
	if numPart == "" {
		return 0, fmt.Errorf("%q does not start with a number", s)
	}
	mult, ok := sizeUnits[strings.ToLower(unitPart)]
	if !ok {
		return 0, fmt.Errorf("%q has an unknown unit %q; use B, KB, MB, GB, TB or KiB, MiB, GiB, TiB", s, unitPart)
	}
	n, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number: %w", numPart, err)
	}
	if n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("%q is not a usable size", s)
	}
	total := n * float64(mult)
	if total > float64(math.MaxInt64) {
		return 0, fmt.Errorf("%q is too large", s)
	}
	return int64(total), nil
}

// FormatSize renders a byte count the way ParseSize accepts it, using binary
// units. Zero means unlimited.
func FormatSize(n int64) string {
	if n <= 0 {
		return "unlimited"
	}
	units := []struct {
		suffix string
		scale  int64
	}{
		{"PiB", 1 << 50}, {"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
	}
	for _, u := range units {
		if n >= u.scale {
			v := float64(n) / float64(u.scale)
			if v == math.Trunc(v) {
				return strconv.FormatInt(int64(v), 10) + u.suffix
			}
			return strconv.FormatFloat(v, 'f', 1, 64) + u.suffix
		}
	}
	return strconv.FormatInt(n, 10) + "B"
}

// formatSizeExact renders n so that ParseSize returns exactly n again. It is
// used when writing users.yaml, where a rounded quota would drift a little
// every time the file is rewritten.
func formatSizeExact(n int64) string {
	if n <= 0 {
		return ""
	}
	units := []struct {
		suffix string
		scale  int64
	}{
		{"PB", 1000 * 1000 * 1000 * 1000 * 1000},
		{"TB", 1000 * 1000 * 1000 * 1000},
		{"GB", 1000 * 1000 * 1000},
		{"MB", 1000 * 1000},
		{"KB", 1000},
		{"PiB", 1 << 50}, {"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
	}
	for _, u := range units {
		if n >= u.scale && n%u.scale == 0 {
			return strconv.FormatInt(n/u.scale, 10) + u.suffix
		}
	}
	return strconv.FormatInt(n, 10) + "B"
}
