# Decisions

Every non-obvious choice, and every assumption made in the absence of an
instruction. Newest section at the bottom. Format: **D-nnn — decision**, then
context, then consequence.

---

## Phase 0 — Scaffold

### D-001 — Go for the server, `golang.org/x/net/webdav` for the protocol

**Context.** The brief requires a single static binary that cross-compiles to
`linux/arm64` (Raspberry Pi 5) and `linux/amd64` (mini-PC), and says "Go
standard library plus at most a WebDAV and a YAML package".

**Decision.** Go 1.24, with exactly two non-stdlib direct dependencies:

- `golang.org/x/net/webdav` — the protocol handler. Semi-official (Go team owns
  `x/net`), implements PROPFIND/PROPPATCH/LOCK/UNLOCK correctly, and streams
  rather than buffering.
- `gopkg.in/yaml.v3` — `users.yaml` parsing.

`golang.org/x/crypto/bcrypt` is a third, unavoidable: password hashing is not in
the standard library and hand-rolling it would be strictly worse. Counted as
part of "the security floor", not as a framework dependency.

**Consequence.** `go.mod` has three `require` lines. Any PR adding a fourth
needs a decision entry here.

### D-002 — Jailing is enforced at the `webdav.FileSystem` layer, not by URL parsing

**Context.** Per-user isolation is the single most security-critical property in
the system. URL-prefix checks are the classic way to get this wrong: `..`
segments, percent-encoding, UTF-8 overlongs, Windows `\` separators, absolute
paths, NTFS alternate-data-stream syntax, and symlinks in the storage root all
defeat naive string comparison.

**Decision.** Each authenticated request is served by a `webdav.Handler` whose
`FileSystem` is a per-user `fsjail.Dir` rooted at that user's folder. Every path
that enters the jail is normalised and re-validated, and the final resolved path
is checked to be inside the root *after* symlink evaluation. The jail is the
only thing that opens files; no code path in the server accepts a caller-supplied
absolute path.

**Consequence.** A traversal bug requires a bug in one small, heavily-tested
file (`internal/fsjail`) rather than anywhere in the request pipeline. See the
`fsjail` fuzz and table tests.

### D-003 — HTTP Basic auth, but refused on non-loopback plaintext

**Context.** The brief mandates Basic-over-HTTPS and forbids credentials over
plain HTTP on a non-loopback interface. But the deployment terminates TLS at
Cloudflare, so the Go server itself legitimately speaks plain HTTP to
`cloudflared` over loopback.

**Decision.** The server refuses to start if the listen address is neither a
loopback address nor explicitly marked TLS-enabled/trusted-proxy in config. A
request arriving without TLS on a non-loopback listener is rejected before the
credential is read.

**Consequence.** The safe configuration is the default; the unsafe one requires
an explicit, named opt-in that the runbook never tells the reader to use.

### D-004 — `users.yaml` as the user store, edited only through the admin CLI

**Context.** The brief specifies a `users.yaml` file with username, bcrypt hash,
optional quota, and root folder. Three users at launch; tens, not thousands,
ever.

**Decision.** No database. `users.yaml` is the source of truth, rewritten
atomically (write to temp file in the same directory, `fsync`, `rename`) by the
admin CLI so a crash mid-edit cannot leave a truncated user list. The server
reloads it on SIGHUP.

**Consequence.** Backup is `cp users.yaml users.yaml.bak`. An operator *can*
hand-edit it; the CLI is the supported path and validates on write.

### D-005 — rclone + WinFsp as the primary client mount strategy

**Context.** Windows' built-in WebDAV redirector (`net use`) has a 50 MB default
file-size limit, aggressive caching, poor behaviour on flaky links, and a
long history of silently dropping mappings. The brief asks for both strategies.

**Decision.** Primary: bundle `rclone` and WinFsp, drive `rclone mount` as a
child process. Fallback: `net use` with the `FileSizeLimitInBytes` registry fix,
applied only after an explicit consent prompt (it is a machine-wide change under
`HKLM` and requires elevation).

**Consequence.** The installer carries the WinFsp redistributable. If WinFsp
installation fails or is declined, the app degrades to the fallback rather than
failing.

### D-006 — Credentials live in Windows Credential Manager, never on disk

**Context.** "Credentials stored in Windows Credential Manager, never in plain
text."

**Decision.** `CredWrite`/`CredRead` (`CRED_TYPE_GENERIC`,
`CRED_PERSIST_LOCAL_MACHINE` scoped to the user) via P/Invoke. The password is
handed to `rclone` through an obscured value passed on a private stdin-fed
config, never written to `rclone.conf` on disk and never placed in a command
line (command lines are world-readable via WMI on Windows).

**Consequence.** Signing out deletes the credential. There is no "remember me"
file to leak.

### D-007 — Server address baked in at build time

**Context.** "Server address is pre-baked at build time from a config constant,
so staff only enter username and password."

**Decision.** `client/GoldenCloud.Tray/BuildConfig.cs` holds the constant, and
CI overrides it from the `GOLDENCLOUD_SERVER_URL` build variable. A debug build
falls back to `https://cloud.example.com` and the app shows a visible banner
saying the build is unconfigured, so an unconfigured installer can never be
mistaken for a real one.

**Consequence.** Human gate 2 in `PROGRESS.md` is exactly this value.

### D-008 — Assumption: storage root is a plain filesystem path

**Context.** The WD My Cloud Home's Local Access share is mounted by the OS.

**Assumption.** The server never speaks SMB itself. It sees `/mnt/wd` (or any
path) as a normal directory. Mounting is the operating system's job and is
covered by `RUNBOOK.md` with `/etc/fstab` and a `systemd` mount dependency so
the server does not start before the share is present.

**Consequence.** If the share is absent at boot, the server fails its
preflight check loudly instead of silently creating user folders on the Pi's SD
card — a failure mode that would look like working software while storing data
in the wrong place.
