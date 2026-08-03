# GoldenCloud server

A WebDAV server that gives each staff member one folder and no way to reach
anyone else's. Single static binary, no database, no runtime dependencies.

This document covers the configuration format, the CLI, and the security model.
For deploying it on a Raspberry Pi behind a Cloudflare Tunnel, read
[`../deploy/RUNBOOK.md`](../deploy/RUNBOOK.md). For the threat model as an
operator sees it, read [`../docs/SECURITY.md`](../docs/SECURITY.md).

---

## Contents

- [Build](#build)
- [Configuration](#configuration)
- [The user store](#the-user-store)
- [Command line](#command-line)
- [Security model](#security-model)
- [Protocol notes](#protocol-notes)
- [Operating notes](#operating-notes)
- [Development](#development)

---

## Build

Go 1.25 or newer. Three non-stdlib dependencies, all pinned in `go.mod`
(see [D-001](../DECISIONS.md)).

```bash
go build -o goldencloud ./cmd/goldencloud
```

Release builds are static and cross-compiled:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -ldflags "-s -w -X main.version=v1.0.0" \
  -o goldencloud-linux-arm64 ./cmd/goldencloud
```

`main.version` is the only build-time variable. `goldencloud version` prints it.

---

## Configuration

One YAML file, by default `/etc/goldencloud/config.yaml`. Every key is validated
on load and an error names the field and, where the parser can tell, the line.
Unknown keys are an error rather than a silent no-op — a typo in a security
setting must not look like it worked.

A fully annotated example ships as
[`../deploy/goldencloud.example.yaml`](../deploy/goldencloud.example.yaml); a
test in this repository loads that file verbatim so it cannot drift.

```yaml
# The TCP address to bind. See "Security model" before changing it off loopback.
listen: "127.0.0.1:8080"          # default

# Absolute path to the directory holding every user's folder. Required.
storage_root: "/mnt/wd/goldencloud"

# Path to the user store. Relative paths resolve against this file's directory.
users_file: "/etc/goldencloud/users.yaml"

# debug | info | warn | error
log_level: "info"                 # default

# Refuse to start unless storage_root lives on a filesystem of its own,
# rather than on the root filesystem. See D-022.
require_mountpoint: false         # default

# Terminate TLS in the server itself. Not used in the supported deployment,
# where Cloudflare terminates TLS at its edge.
tls:
  enabled: false
  cert_file: ""
  key_file: ""

# Assert that a reverse proxy in front of the server has already done the TLS.
trusted_proxy:
  enabled: false
  header: "X-Forwarded-For"
  allowed_cidrs: []
```

### Field reference

| Field | Type | Default | Notes |
| ----- | ---- | ------- | ----- |
| `listen` | `host:port` | `127.0.0.1:8080` | Refused if non-loopback without `tls.enabled` or `trusted_proxy.enabled`. |
| `storage_root` | absolute path | *(required)* | Must exist and be a directory at startup. |
| `users_file` | path | *(required)* | Relative paths resolve against the config file's directory. |
| `log_level` | enum | `info` | `debug` is very noisy; it logs one line per request plus internals. |
| `require_mountpoint` | bool | `false` | See [D-022](../DECISIONS.md). Linux only. |
| `tls.enabled` | bool | `false` | Requires `cert_file` and `key_file`. |
| `trusted_proxy.enabled` | bool | `false` | Requires a non-empty `allowed_cidrs`. |
| `trusted_proxy.header` | string | `X-Forwarded-For` | Only the **rightmost** value is used. |
| `trusted_proxy.allowed_cidrs` | list of CIDRs | `[]` | The header is believed only when the peer is inside one of these. |

### Why the server may refuse to start

These are deliberate refusals, not bugs. Each one prevents a configuration that
looks like it works while being unsafe.

| Refusal | Cause |
| ------- | ----- |
| `listen: ... refusing to start` | A non-loopback address with neither TLS nor a trusted proxy: HTTP Basic passwords would cross the network in the clear ([D-003](../DECISIONS.md)). |
| `trusted_proxy.allowed_cidrs must not be empty` | With `enabled: true` and no CIDR list, any client could forge its own apparent address and defeat rate limiting. |
| `storage_root ... does not exist` | The share is probably not mounted. Writing anyway would put everyone's files on the local disk ([D-008](../DECISIONS.md)). |
| `storage_root ... is on the root filesystem` | `require_mountpoint: true` and nothing is mounted there. |
| `password_hash ... is not a bcrypt hash` | Someone hand-edited `users.yaml` and put a plaintext password in it. |

---

## The user store

`users.yaml` is the source of truth. There is no database
([D-004](../DECISIONS.md)).

```yaml
users:
  - username: alice
    password_hash: "$2a$12$..."   # bcrypt, cost 12
    quota: 50GB                   # optional; reported, not enforced in v1
    root: alice                   # optional; defaults to the username
```

- **`username`** — 1 to 32 characters from `a-z 0-9 . - _`, starting with a
  letter or digit. Lowercase only, because it is also a folder name and two
  accounts differing only in case would collide on a case-insensitive
  filesystem.
- **`password_hash`** — a bcrypt hash. Anything else is rejected at load, so a
  plaintext password left in this field fails loudly instead of silently never
  matching.
- **`quota`** — human-readable. A bare letter is binary (`1G` = 1 GiB = 2³⁰); a
  letter followed by `B` is decimal (`1GB` = 10⁹), matching how disks are sold.
  `50GB`, `500 MiB`, `1024` and `1.5GiB` all parse. Reported but not enforced in
  v1; see [`../ROADMAP.md`](../ROADMAP.md).
- **`root`** — the folder under `storage_root`. Must be a clean relative path
  that stays inside it. Two users may not share one.

Edit it only through the CLI. Every write is atomic: a temporary file in the
same directory, `fsync`, `rename`, then `fsync` of the directory, so a power cut
mid-write leaves the previous file intact rather than a truncated user list. An
existing file keeps the mode and ownership you gave it (the runbook ships it as
`0640 root:goldencloud`); a newly created one is `0600`.

---

## Command line

```
goldencloud serve  --config <path>          run the server
goldencloud user   add    <name> [options]  create a user and their folder
goldencloud user   passwd <name> [options]  set a user's password
goldencloud user   remove <name> [options]  remove a user
goldencloud user   list          [options]  list users
goldencloud version                         print the version and exit
```

`--config` defaults to `/etc/goldencloud/config.yaml` and may appear anywhere on
the command line ([D-009](../DECISIONS.md)).

### `serve`

```bash
goldencloud serve --config /etc/goldencloud/config.yaml
```

Before binding a port it runs a preflight check:

1. `storage_root` exists and is a directory.
2. If `require_mountpoint` is set, `storage_root` is on a filesystem of its own.
3. Every user's folder exists, creating any that do not (mode `0700`).

If step 1 or 2 fails the server exits non-zero with an explanation. It does not
start degraded.

Signals:

| Signal | Effect |
| ------ | ------ |
| `SIGHUP` | Re-read `users.yaml`. A file that does not parse is logged and **ignored** — the previous user list keeps serving, because a typo must not take the office offline. |
| `SIGTERM`, `SIGINT` | Graceful shutdown: stop accepting, let in-flight transfers finish, 15 second grace, then close. |

`systemctl reload goldencloud` sends `SIGHUP`. A password change or a new user
takes effect on reload with no dropped connections.

### `user add`

```bash
goldencloud user add --config /etc/goldencloud/config.yaml alice
```

Prompts for the password twice with terminal echo disabled. It is never accepted
as a command-line argument, because command lines are readable by every account
on the machine ([D-006](../DECISIONS.md), [D-015](../DECISIONS.md)).

| Option | Effect |
| ------ | ------ |
| `--password-stdin` | Read the password from standard input instead of prompting. For scripts and installers. |
| `--quota <size>` | Set the quota, e.g. `--quota 50GB`. |

Passwords must be 8 to 72 characters. The upper bound is bcrypt's: it silently
ignores anything past 72 bytes, and a passphrase that is quietly truncated is
worse than one that is refused.

Creating a user whose folder already exists reuses it and leaves its contents
alone, so re-adding a user does not destroy their files.

### `user passwd`

```bash
goldencloud user passwd --config /etc/goldencloud/config.yaml alice
```

Same prompting rules. Takes effect on the next `SIGHUP`; the credential cache is
keyed by the stored hash, so there is no revocation delay
([D-021](../DECISIONS.md)).

### `user remove`

```bash
goldencloud user remove --config /etc/goldencloud/config.yaml alice
```

Removes the account and prints where the files were left. It does **not** delete
data unless you pass `--delete-data`: "cut off access now" and "destroy their
work" are different decisions and should not share a command.

### `user list`

```
USERNAME  QUOTA      FOLDER
alice     46.6GiB    /mnt/wd/goldencloud/alice
bob       unlimited  /mnt/wd/goldencloud/bob
```

There is no column for the password hash and there never will be.

---

## Security model

### Isolation is enforced by the filesystem, not by URL parsing

Each authenticated request is served by a `webdav.Handler` whose `FileSystem` is
an `fsjail.Dir` rooted at that user's folder. `fsjail` is built on `os.Root`,
which performs every operation relative to an open directory file descriptor and
refuses to traverse out of it — including through symlinks, which the kernel
resolves against the root rather than a string comparison
([D-002](../DECISIONS.md)).

No other code in the server opens a caller-influenced path. A traversal bug
would have to be a bug in one small file.

Before a name reaches `os.Root` it is normalised and validated. These are
rejected outright ([D-023](../DECISIONS.md)):

| Rejected | Why |
| -------- | --- |
| NUL and C0 control bytes | Truncate paths in C string APIs. |
| Backslash | A separator to Windows, a byte to Linux — the disagreement a traversal needs. |
| Colon | NTFS alternate-data-stream syntax (`file.txt::$DATA`). |
| Components over 255 bytes, paths over 4096 | Resource exhaustion and overflow probes. |

`..` segments, percent-encoded traversal, repeated slashes and absolute paths are
normalised away by `path.Clean`, which discards any `..` that would climb above
the root — so `/../../etc/passwd` addresses a file *inside* the jail called
`etc/passwd`, not `/etc/passwd`.

This is covered by a 33-entry escape table, a two-jail isolation test, a fuzz
target with a checked-in seed corpus, and the integration suite's
`TestUserIsolation`, which drives the real binary and asserts that nothing ever
appears outside a user's folder.

### Authentication

HTTP Basic, verified against bcrypt hashes ([D-003](../DECISIONS.md)).

- **Failures are indistinguishable.** A wrong password, an unknown username, a
  missing header and a malformed header all produce the same status, the same
  headers and the same body. An unknown username still costs a full bcrypt
  comparison against a dummy hash of the same cost, so the response time does
  not answer the question either.
- **Usernames are compared in constant time** against the whole store, without
  stopping early.
- **Repeated failures back off.** Five consecutive failures for one
  client-address-and-username pair start an exponential lockout, capped at five
  minutes, returning `429` with `Retry-After`. Requests carrying no credential
  at all — the normal first probe from every WebDAV client — never count.
- **Successful verifications are cached for five minutes**, indexed by an
  HMAC keyed with a random per-process value over the username, the stored hash
  and the offered password. Failures are never cached, and a password change
  invalidates every entry for that user immediately
  ([D-021](../DECISIONS.md)).

### Plaintext refusal

The server refuses to start if `listen` is not a loopback address and neither
`tls.enabled` nor `trusted_proxy.enabled` is set. The safe configuration is the
default; the unsafe one requires a named opt-in that the runbook never tells you
to use.

`trusted_proxy` additionally requires `allowed_cidrs`. The forwarded header is
believed only when the peer address is inside one of those ranges, and only its
**rightmost** entry is used — the one appended by the nearest proxy, which is
the only one a client cannot forge.

### Locking

Each user gets their own in-memory lock system in its own random token
namespace. Presenting another user's lock token yields `412 Precondition Failed`
rather than being silently accepted ([D-020](../DECISIONS.md)).

### What the server does not do

- **No quota enforcement.** The field is read and reported; writes are not
  refused. See [`../ROADMAP.md`](../ROADMAP.md).
- **No audit log** beyond ordinary request logging.
- **No file versioning or trash.** A delete is a delete.

---

## Protocol notes

The server advertises `DAV: 1, 2, 3` and `MS-Author-Via: DAV`, plus `Allow` and
`Public`, on every response.

**`OPTIONS` is answered without credentials, on every path.** The Windows WebDAV
redirector probes `OPTIONS /` before it will prompt for a password; if that
returns `401` the drive never maps. The response is identical for every path and
discloses nothing.

**`PROPFIND` with `Depth: infinity` is refused** with `403` and a
`DAV:propfind-finite-depth` body, per RFC 4918 §9.1. An *absent* `Depth` is
treated as `1` rather than the RFC's default of infinity, so a client that omits
the header gets a usable listing instead of an error
([D-019](../DECISIONS.md)). Depth `0` and `1` are fully supported.

**Microsoft headers are tolerated.** `Translate: f` and `X-MSDAVEXT_Error` are
accepted and ignored.

**Transfers stream.** Neither uploads nor downloads are buffered in memory: a
320 MiB round trip grows the server's heap by under 1 MiB, asserted with
`runtime.ReadMemStats`. `Range` requests are supported.

**Errors do not leak the storage layout.** Filesystem errors are rewritten to
carry the virtual path the client asked for, never the on-disk path.

---

## Operating notes

### Adding a user

```bash
sudo goldencloud user add --config /etc/goldencloud/config.yaml alice
sudo systemctl reload goldencloud
```

### Backing up the user store

```bash
sudo cp /etc/goldencloud/users.yaml "/etc/goldencloud/users.yaml.$(date +%F)"
```

It holds bcrypt hashes, not passwords, but it is still the crown jewels.

### Log levels

`info` gives lifecycle messages and warnings, including every failed sign-in
with the username and client address. `debug` adds one line per request. Nothing
logs a password at any level; if you ever see one, that is a bug worth
reporting.

### Performance

Roughly a quarter of a second is spent on bcrypt the first time a client
authenticates, and microseconds thereafter for five minutes. Transfers are
limited by the network and the NAS, not by the server.

---

## Development

```bash
go test ./...                                        # unit tests
go test -race ./...                                  # with the race detector
go test -run=Fuzz -fuzz=FuzzJailEscape ./internal/fsjail
go test -tags=integration ./integration/...          # against the real binary
```

The integration suite compiles the binary itself, or uses `GOLDENCLOUD_BINARY`
if set. `GOLDENCLOUD_BIG_FILE_BYTES` overrides the 1 GiB round-trip size on a
constrained machine.

Lint gates, matching CI:

```bash
gofmt -l .
go vet ./...
go install honnef.co/go/tools/cmd/staticcheck@2025.1.1 && staticcheck ./...
go mod tidy && git diff --exit-code -- go.mod go.sum
```

### Layout

| Package | Responsibility |
| ------- | -------------- |
| `internal/fsjail` | The security core: a `webdav.FileSystem` confined to one directory. |
| `internal/config` | Config and `users.yaml` loading, validation and atomic writes. |
| `internal/auth` | HTTP Basic middleware, backoff, credential cache. |
| `internal/webdavx` | Per-user WebDAV handlers, lock scoping, Windows quirks. |
| `cmd/goldencloud` | The binary: `serve`, `user`, `version`. |
| `integration` | End-to-end tests driving the real compiled binary. |

Every unit of behaviour here was written test-first. If you change `fsjail`,
add the escape attempt to the table in `fsjail_test.go` and to the fuzz seed
corpus before you change the implementation.
