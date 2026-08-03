package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"text/tabwriter"

	"golang.org/x/crypto/bcrypt"

	"github.com/PV80/GoldenCloud/server/internal/config"
)

// bcryptCost is the work factor for new passwords. 12 is roughly a quarter of a
// second on a Raspberry Pi 5, which is a tolerable login cost and an
// intolerable brute-force cost.
const bcryptCost = 12

const (
	minPasswordLen = 8
	// bcrypt silently ignores anything past 72 bytes, so refuse rather than
	// let someone believe a longer passphrase is being used in full.
	maxPasswordLen = 72
)

func cmdUser(args []string, s streams) int {
	if len(args) == 0 {
		fmt.Fprintln(s.err, "goldencloud user: expected one of add, remove, passwd, list")
		usage(s.err)
		return 2
	}
	switch args[0] {
	case "add":
		return userAdd(args[1:], s)
	case "remove", "rm", "del", "delete":
		return userRemove(args[1:], s)
	case "passwd", "password":
		return userPasswd(args[1:], s)
	case "list", "ls":
		return userList(args[1:], s)
	default:
		fmt.Fprintf(s.err, "goldencloud user: unknown subcommand %q\n", args[0])
		usage(s.err)
		return 2
	}
}

// loadForAdmin loads the config and the current user list. A users file that
// does not exist yet is treated as an empty list so that the very first
// "user add" works on a fresh install.
func loadForAdmin(configPath string) (*config.Config, []config.User, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, err
	}
	users, err := config.LoadUsers(cfg.UsersFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil, nil
		}
		return nil, nil, err
	}
	return cfg, users, nil
}

func userAdd(args []string, s streams) int {
	fset := newFlagSet("user add", s)
	configPath := fset.String("config", DefaultConfigPath, "configuration file")
	fromStdin := fset.Bool("password-stdin", false, "read the password from standard input")
	quota := fset.String("quota", "", "storage quota, e.g. 50GB")
	positional, err := parseWithPositionals(fset, args)
	if err != nil {
		return 2
	}
	if len(positional) == 0 {
		return fail(s, "user add: a username is required")
	}
	if len(positional) > 1 {
		return fail(s, "user add: expected one username, got %v", positional)
	}
	name := positional[0]
	if err := config.ValidateUsername(name); err != nil {
		return fail(s, "user add: %v", err)
	}

	cfg, users, err := loadForAdmin(*configPath)
	if err != nil {
		return fail(s, "%v", err)
	}
	for _, u := range users {
		if u.Username == name {
			return fail(s, "user add: user %q already exists; use \"goldencloud user passwd %s\" to change their password", name, name)
		}
	}

	var quotaBytes int64
	if *quota != "" {
		quotaBytes, err = config.ParseSize(*quota)
		if err != nil {
			return fail(s, "user add: --quota: %v", err)
		}
	}

	password, err := readPassword(s, *fromStdin, fmt.Sprintf("New password for %s: ", name))
	if err != nil {
		return fail(s, "user add: %v", err)
	}
	defer zero(password)
	hash, err := bcrypt.GenerateFromPassword(password, bcryptCost)
	if err != nil {
		return fail(s, "user add: hashing the password: %v", err)
	}

	u := config.User{Username: name, PasswordHash: string(hash), Root: name, Quota: quotaBytes}
	dir := u.Dir(cfg.StorageRoot)
	if err := ensureUserDir(dir); err != nil {
		return fail(s, "user add: %v", err)
	}
	if err := config.SaveUsers(cfg.UsersFile, append(users, u)); err != nil {
		return fail(s, "user add: %v", err)
	}

	fmt.Fprintf(s.out, "Added user %q.\n", name)
	fmt.Fprintf(s.out, "  folder: %s\n", dir)
	fmt.Fprintf(s.out, "  quota:  %s\n", config.FormatSize(quotaBytes))
	fmt.Fprintf(s.out, "Reload the running server with: systemctl reload goldencloud\n")
	return 0
}

func userPasswd(args []string, s streams) int {
	fset := newFlagSet("user passwd", s)
	configPath := fset.String("config", DefaultConfigPath, "configuration file")
	fromStdin := fset.Bool("password-stdin", false, "read the password from standard input")
	positional, err := parseWithPositionals(fset, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		return fail(s, "user passwd: expected exactly one username")
	}
	name := positional[0]

	cfg, users, err := loadForAdmin(*configPath)
	if err != nil {
		return fail(s, "%v", err)
	}
	idx := -1
	for i, u := range users {
		if u.Username == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fail(s, "user passwd: no such user %q", name)
	}

	password, err := readPassword(s, *fromStdin, fmt.Sprintf("New password for %s: ", name))
	if err != nil {
		return fail(s, "user passwd: %v", err)
	}
	defer zero(password)
	hash, err := bcrypt.GenerateFromPassword(password, bcryptCost)
	if err != nil {
		return fail(s, "user passwd: hashing the password: %v", err)
	}
	users[idx].PasswordHash = string(hash)
	if err := config.SaveUsers(cfg.UsersFile, users); err != nil {
		return fail(s, "user passwd: %v", err)
	}
	fmt.Fprintf(s.out, "Password for %q changed.\n", name)
	fmt.Fprintf(s.out, "Reload the running server with: systemctl reload goldencloud\n")
	return 0
}

func userRemove(args []string, s streams) int {
	fset := newFlagSet("user remove", s)
	configPath := fset.String("config", DefaultConfigPath, "configuration file")
	deleteData := fset.Bool("delete-data", false, "also delete the user's folder and its contents")
	positional, err := parseWithPositionals(fset, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		return fail(s, "user remove: expected exactly one username")
	}
	name := positional[0]

	cfg, users, err := loadForAdmin(*configPath)
	if err != nil {
		return fail(s, "%v", err)
	}
	kept := make([]config.User, 0, len(users))
	var removed *config.User
	for i, u := range users {
		if u.Username == name {
			removed = &users[i]
			continue
		}
		kept = append(kept, u)
	}
	if removed == nil {
		return fail(s, "user remove: no such user %q", name)
	}
	dir := removed.Dir(cfg.StorageRoot)

	if err := config.SaveUsers(cfg.UsersFile, kept); err != nil {
		return fail(s, "user remove: %v", err)
	}
	fmt.Fprintf(s.out, "Removed user %q.\n", name)

	if *deleteData {
		if err := os.RemoveAll(dir); err != nil {
			return fail(s, "user remove: the user is gone but their folder could not be deleted: %v", err)
		}
		fmt.Fprintf(s.out, "Deleted %s and everything in it.\n", dir)
	} else {
		fmt.Fprintf(s.out, "Their files were left in place at %s.\n", dir)
		fmt.Fprintf(s.out, "Delete them yourself, or re-run with --delete-data.\n")
	}
	fmt.Fprintf(s.out, "Reload the running server with: systemctl reload goldencloud\n")
	return 0
}

func userList(args []string, s streams) int {
	fset := newFlagSet("user list", s)
	configPath := fset.String("config", DefaultConfigPath, "configuration file")
	if _, err := parseWithPositionals(fset, args); err != nil {
		return 2
	}
	cfg, users, err := loadForAdmin(*configPath)
	if err != nil {
		return fail(s, "%v", err)
	}
	if len(users) == 0 {
		fmt.Fprintf(s.out, "No users yet. Create one with: goldencloud user add <name>\n")
		return 0
	}
	// Password hashes are deliberately not a column and never will be.
	tw := tabwriter.NewWriter(s.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "USERNAME\tQUOTA\tFOLDER")
	for _, u := range users {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", u.Username, config.FormatSize(u.Quota), u.Dir(cfg.StorageRoot))
	}
	if err := tw.Flush(); err != nil {
		return fail(s, "user list: %v", err)
	}
	return 0
}

// ensureUserDir creates a user's folder if it is missing, and never touches its
// contents if it already exists — re-adding a user must not destroy their data.
func ensureUserDir(dir string) error {
	st, err := os.Stat(dir)
	switch {
	case err == nil && st.IsDir():
		return nil
	case err == nil:
		return fmt.Errorf("%s exists but is not a directory", dir)
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	return nil
}

// readPassword obtains a new password, either from standard input (for
// scripting and for the installer) or by prompting twice with the terminal
// echo turned off.
func readPassword(s streams, fromStdin bool, prompt string) ([]byte, error) {
	var pw []byte
	if fromStdin {
		raw, err := io.ReadAll(io.LimitReader(s.in, 4096))
		if err != nil {
			return nil, fmt.Errorf("reading the password from standard input: %w", err)
		}
		pw = bytes.TrimRight(raw, "\r\n")
	} else {
		var err error
		pw, err = promptPasswordTwice(s, prompt)
		if err != nil {
			return nil, err
		}
	}
	if err := checkPassword(pw); err != nil {
		zero(pw)
		return nil, err
	}
	return pw, nil
}

func checkPassword(pw []byte) error {
	if len(pw) < minPasswordLen {
		return fmt.Errorf("the password must be at least %d characters", minPasswordLen)
	}
	if len(pw) > maxPasswordLen {
		return fmt.Errorf("the password must be at most %d characters (bcrypt ignores anything longer)", maxPasswordLen)
	}
	return nil
}

// promptPasswordTwice reads a password from the terminal without echoing it,
// then asks for it again and checks the two match.
func promptPasswordTwice(s streams, prompt string) ([]byte, error) {
	first, err := promptOnce(s, prompt)
	if err != nil {
		return nil, err
	}
	second, err := promptOnce(s, "Repeat password: ")
	if err != nil {
		zero(first)
		return nil, err
	}
	defer zero(second)
	if !bytes.Equal(first, second) {
		zero(first)
		return nil, errors.New("the two passwords do not match")
	}
	return first, nil
}

func promptOnce(s streams, prompt string) ([]byte, error) {
	fmt.Fprint(s.err, prompt)
	pw, err := readPasswordNoEcho(int(os.Stdin.Fd()))
	fmt.Fprintln(s.err)
	if err == nil {
		return pw, nil
	}
	if !errors.Is(err, errNotATerminal) {
		return nil, err
	}
	// Not a terminal: fall back to a plain read so a pipe still works, but say
	// so, because the password will be visible to anything reading the stream.
	fmt.Fprintln(s.err, "warning: standard input is not a terminal; the password will not be hidden")
	line, err := bufio.NewReader(s.in).ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, err
	}
	return bytes.TrimRight([]byte(line), "\r\n"), nil
}

// zero overwrites a password buffer once it is no longer needed.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
