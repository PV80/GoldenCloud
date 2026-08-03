// Command goldencloud is the GoldenCloud WebDAV server and its admin CLI.
//
// Usage:
//
//	goldencloud serve  --config /etc/goldencloud/goldencloud.yaml
//	goldencloud user   add|remove|passwd|list [options]
//	goldencloud version
//
// See server/README.md for the configuration format and the security model.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// DefaultConfigPath is where the systemd unit in deploy/ puts the config.
const DefaultConfigPath = "/etc/goldencloud/goldencloud.yaml"

// streams lets every command be driven from a test without touching the real
// process streams.
type streams struct {
	in  io.Reader
	out io.Writer
	err io.Writer
}

func main() {
	os.Exit(run(os.Args[1:], streams{in: os.Stdin, out: os.Stdout, err: os.Stderr}))
}

func run(args []string, s streams) int {
	if len(args) == 0 {
		usage(s.err)
		return 2
	}
	switch args[0] {
	case "serve":
		return cmdServe(args[1:], s)
	case "user":
		return cmdUser(args[1:], s)
	case "version", "--version", "-version":
		fmt.Fprintf(s.out, "goldencloud %s\n", version)
		return 0
	case "help", "--help", "-h", "-help":
		usage(s.out)
		return 0
	default:
		fmt.Fprintf(s.err, "goldencloud: unknown command %q\n\n", args[0])
		usage(s.err)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `goldencloud — a private WebDAV drive, one folder per user.

Usage:
  goldencloud serve  --config <path>          run the server
  goldencloud user   add    <name> [options]  create a user and their folder
  goldencloud user   passwd <name> [options]  set a user's password
  goldencloud user   remove <name> [options]  remove a user
  goldencloud user   list          [options]  list users (never their hashes)
  goldencloud version                         print the version and exit

Common options:
  --config <path>     configuration file (default `+DefaultConfigPath+`)

user add / user passwd options:
  --password-stdin    read the password from standard input instead of prompting
  --quota <size>      storage quota, e.g. 50GB or 500MiB (user add only)

user remove options:
  --delete-data       also delete the user's folder and everything in it

The server refuses to start if it would accept passwords in plaintext on a
non-loopback address. See server/README.md.
`)
}

// fail prints an error the way every command should and returns the exit code.
func fail(s streams, format string, args ...any) int {
	fmt.Fprintf(s.err, "goldencloud: "+format+"\n", args...)
	return 1
}

// newFlagSet returns a FlagSet that reports errors to s.err and does not
// call os.Exit.
func newFlagSet(name string, s streams) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(s.err)
	return fs
}

// parseWithPositionals parses flags that may appear before, between or after
// positional arguments, which is how people actually type command lines.
func parseWithPositionals(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}
