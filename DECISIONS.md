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

---

## Phase 3 — Packaging + deploy kit

### D-009 — `--config` is a global flag, shared by `serve` and every `user` subcommand

**Context.** The brief names the CLI surface as `serve --config <path>`,
`user add|remove|passwd|list`, and `version`, without saying how the `user`
subcommands find `users.yaml`. The runbook has to write a literal command.

**Decision.** `--config` is a global flag. `goldencloud user add --config
/etc/goldencloud/config.yaml alice` reads `users_file` out of the same config
the server uses, rather than taking a separate `--users-file` path. The
alternative — teaching the operator two ways to name the same file — is how you
end up with an admin CLI editing one file while the server reads another.

Consequently the runbook and `install.sh` document `--config` on every `user`
invocation, and assume `user add`/`user passwd` prompt for the password twice
on the terminal and never accept it as an argument (a command line is
world-readable, cf. D-006).

**Consequence.** A contract on `server/cmd/goldencloud`. If it ships a different
flag, `deploy/RUNBOOK.md` steps 103–111 and 153–162, `deploy/install.sh`, and
`docs/SECURITY.md` all need the same one-word change.

### D-010 — `storage_root` is a dedicated sub-folder of the mount, not the mount itself

**Context.** The brief says the storage root is "typically `/mnt/wd`", which is
the whole WD Local Access share — the same share that already holds whatever
the office put on the NAS before GoldenCloud existed.

**Decision.** `/mnt/wd` is the mount; `/mnt/wd/goldencloud` is the storage root.
Per-user folders are created under it.

**Consequence.** A user called `Documents` or `Public` cannot collide with an
existing top-level folder on the NAS, and "everything GoldenCloud owns" is one
directory you can archive, move, or point at a different NAS. The systemd unit
still declares `ReadWritePaths=/mnt/wd` (the mount) so the unit survives moving
the storage root, with a comment saying it can be narrowed.

### D-011 — fstab automounts, but the service hard-requires the real mount unit

**Context.** Two requirements pull in opposite directions: boot must never hang
waiting for a NAS that is switched off (D-008's failure mode is bad, but an
unreachable headless Pi is worse), and the server must never start without its
storage.

**Decision.** The fstab entry carries `_netdev,nofail,x-systemd.automount`, so
boot never blocks. `goldencloud.service` then declares `Requires=mnt-wd.mount`
and `RequiresMountsFor=/mnt/wd` — the *real* mount, not the automount trigger.

The distinction matters for more than tidiness: `ProtectSystem=strict` plus
`ReadWritePaths=/mnt/wd` are evaluated when the service's private mount
namespace is built. If `/mnt/wd` were still an untriggered autofs placeholder at
that instant, the service would get a namespace pointing at nothing. Requiring
the real mount removes the race.

**Consequence.** A Pi with the NAS unplugged boots normally to a login prompt,
with `goldencloud.service` in a clean `failed` state naming the mount as the
reason. That is the diagnosable failure.

### D-012 — Default port 8080, loopback, duplicated in exactly two files

**Context.** The port appears in `/etc/goldencloud/config.yaml` (`listen`) and
in `/etc/cloudflared/config.yml` (the ingress `service:` URL). Nothing can
enforce agreement between two programs' config files.

**Decision.** Standardise on `127.0.0.1:8080` everywhere — example config,
runbook, `install.sh`, Dockerfile `EXPOSE`, cloudflared template — and make the
mismatch a first-class troubleshooting entry (`502 Bad Gateway`) rather than
pretending it will not happen.

**Consequence.** Anyone changing the port has exactly two files to edit, and the
symptom of forgetting the second one is documented at the point they will look.

### D-013 — The container image is a secondary path, explicitly not the supported deployment

**Context.** `deploy/Dockerfile` and `deploy/docker-compose.example.yml` are
deliverables, but the runbook, the troubleshooting section, and the support
burden all assume systemd on a Pi.

**Decision.** Ship both, and say so in the files themselves. The image is
multi-stage, `CGO_ENABLED=0`, distroless-static, non-root (UID 65532), with a
build-time assertion that the binary is not dynamically linked. The Compose file
carries the same hardening ideas (`read_only`, `cap_drop: ALL`,
`no-new-privileges`) and a loud comment that `listen` must become `0.0.0.0:8080`
inside a container — because loopback in a container is not loopback on the host
— and therefore must have TLS termination in front of it.

**Consequence.** Someone who already runs Docker is not blocked, and nobody
reaches for the container thinking it is the blessed path.

### D-014 — Client logic lives in a Windows-free library, the tray app is a shell

**Context.** The brief requires the client to be testable against a mocked
server in CI. A WinForms tray app that also builds its own command lines,
computes its own retry schedule and validates its own URLs is testable only by
driving a UI on a real Windows box with a real drive letter.

**Decision.** Everything worth asserting on lives in `client/GoldenCloud.Core`
(`net8.0`, no Windows dependency): URL validation, mount-command generation,
backoff, the `ICredentialStore` abstraction and the WebDAV sign-in probe over an
injected `HttpClient`. `client/GoldenCloud.Tray` (`net8.0-windows`) holds only
the Windows implementations and the UI — P/Invoke, registry, process launching,
NotifyIcon.

**Consequence.** `dotnet test` proves the behaviour in milliseconds with no
sockets, no registry and no sleeping. The rule to apply when adding code: if you
want to write a test for it, it belongs in Core.

### D-015 — The password reaches the mount process by environment block or stdin, never argv

**Context.** D-006 forbids the password on a command line. The two strategies
need it in different forms: rclone wants its "obscured" encoding, `net use`
wants the plain value.

**Decision.** `MountCommand` is a value type with separate `Arguments`,
`Environment` and `StandardInput` members, so "no secret in argv" is a property
a unit test asserts rather than a convention. rclone receives the obscured
password in `RCLONE_WEBDAV_PASS`; `net use` is given the literal `*` placeholder
and the password is written to the child's standard input. The remote is spelled
`:webdav:`, rclone's connection-string form, so no `rclone.conf` is ever created.

Obscuring is done by running the bundled `rclone.exe obscure -` with the password
on stdin — correct by construction, because rclone does it — with a managed
AES-CTR re-implementation (`RcloneObscure`) as the fallback if that fails.

**Consequence.** A command line captured from WMI, a crash dump of the parent, or
a log line built from `RedactedCommandLine` cannot contain the password. The
managed obscurer's tests prove self-consistency only, not byte-compatibility with
rclone; that is why it is second choice and not first.

### D-016 — The baked-in server URL is generated by MSBuild, not edited into source

**Context.** D-007 requires the address to be fixed at build time from
`GOLDENCLOUD_SERVER_URL`. The obvious implementation — a script rewriting
`BuildConfig.cs` before publishing — dirties the working tree, is not idempotent,
and leaves a real customer hostname in `git status` waiting to be committed.

**Decision.** `GoldenCloud.Tray.csproj` carries a target that writes
`BuildConfig` into `obj/` from the `GoldenCloudServerUrl` MSBuild property, using
`WriteOnlyWhenDifferent` so repeat builds are no-ops. `build.ps1` passes
`-p:GoldenCloudServerUrl=...` and refuses outright to bake in a plain `http://`
address for a non-loopback host.

**Consequence.** The checkout is never modified by a build, the same inputs
always produce the same output, and an unconfigured build is a compile-time
constant (`BuildConfig.IsConfigured == false`) rather than a runtime string
comparison.

### D-017 — rclone and WinFsp are downloaded at build time against pinned hashes

**Context.** The installer must carry `rclone.exe` (~60 MB) and the WinFsp
redistributable. Neither can go in git.

**Decision.** `client/thirdparty.ps1` fetches both from their official release
URLs into the gitignored `client/thirdparty/`, verifying the SHA256 recorded in
`client/thirdparty.pins.json`. A mismatch aborts the build. An **empty** pin is
treated as "not yet recorded": the build warns loudly with the hash it actually
saw and continues. `GOLDENCLOUD_RCLONE_EXE` and `GOLDENCLOUD_WINFSP_MSI` override
the download for air-gapped builds.

**Assumption.** The pins ship empty. The client was authored without outbound
network access, so the true hashes could not be computed, and a fabricated hash
that looks verified is worse than an honest blank. Filling them in is a one-off
human step documented in `client/thirdparty/README.md`.

**Consequence.** CI is green from the first run, and the day someone pastes the
hashes in, supply-chain verification switches from advisory to enforced with no
other change.

### D-018 — Nullable reference types are on; nullable warnings are not errors

**Context.** There is no .NET SDK in the environment the client was written in,
so nothing in `client/` could be compiled or run locally; CI on `windows-latest`
is the first compiler to see it.

**Decision.** `Nullable=enable` across the client, but `TreatWarningsAsErrors`
off and nullable warnings left as warnings. Every Windows-only type also carries
an explicit `[SupportedOSPlatform("windows")]` even though the
`net8.0-windows` target framework already implies it.

**Consequence.** A single flow-analysis disagreement cannot turn CI red for a
non-defect, and the warnings are still visible in the build log. Promoting them
to errors is a good first change for anyone working with an SDK to hand; it is
recorded as such in `client/README.md`.
