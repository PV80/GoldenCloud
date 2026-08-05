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

**Decision.** The constant is baked in at compile time from the
`GOLDENCLOUD_SERVER_URL` build variable. A build with no value set falls back to
`https://cloud.example.com` and the app shows a visible banner saying the build
is unconfigured, so an unconfigured installer can never be mistaken for a real
one.

**As implemented.** There is no checked-in `BuildConfig.cs` — an earlier draft of
this entry named one. `build.ps1` passes `-p:GoldenCloudServerUrl=<url>` and
`GoldenCloud.Tray.csproj` generates `GoldenCloudBuildConfig.g.cs` under `obj/`
before compilation. Generating rather than rewriting a tracked file keeps the
working tree clean and the build idempotent, and `build.ps1` refuses a
malformed URL or a plain `http://` address for any non-loopback host rather than
baking in something that would send credentials in clear.

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

**Resolution (orchestrator).** The pins no longer ship empty. The client agent
worked behind a proxy that returned 403 for `downloads.rclone.org` and correctly
refused to fabricate hashes; the orchestrator reached the artefacts by a
different route and pinned the real values:

- rclone 1.68.2 zip — `812bf76c…d993`, cross-checked against rclone's own
  published `SHA256SUMS` **and** an independent download-and-hash. Both agreed.
- WinFsp 2.0.23075 MSI — `6324dc81…c101`, hashed from the GitHub release asset.

The rclone URL was moved from `downloads.rclone.org` to the GitHub release,
which serves a byte-identical archive (same published sum) and is reachable from
restricted build networks.

**Consequence.** Supply-chain verification is **enforced, not advisory**, from
the first CI run, and human gate count stays at two — this did not become a
third. The empty-pin warning path remains as the version-bump workflow.

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

---

## Phase 1 — Server core

### D-019 — `PROPFIND` with `Depth: infinity` is refused; an absent `Depth` means 1

**Context.** RFC 4918 §9.1 says a server SHOULD support `Depth: infinity` on
`PROPFIND`, but explicitly permits refusing it with `403` and a
`DAV:propfind-finite-depth` error body. The server runs on a Raspberry Pi in
front of a NAS share that may hold hundreds of thousands of files. One
`PROPFIND` with infinite depth walks all of them, in one request, holding the
result in memory before it can be written.

**Decision.** Refuse it. `PROPFIND` with `Depth: infinity` returns `403` with

```xml
<D:error xmlns:D="DAV:"><D:propfind-finite-depth/></D:error>
```

and the response still carries `DAV: 1, 2, 3` so the client knows the server is
otherwise compliant. Depth `0` and `1` are fully supported.

Secondly, RFC 4918 defaults an *absent* `Depth` header to infinity. Since we
refuse infinity, honouring that default would turn a header omission into an
error the client cannot act on. An absent `Depth` is therefore treated as `1`,
which is what every real client that omits it actually wants.

**Consequence.** The Windows redirector, rclone, macOS Finder and `cadaver` all
send an explicit `Depth` of `0` or `1` and are unaffected. A tool that insists
on infinite depth gets a specific, documented, RFC-sanctioned error rather than
a server that appears to hang. Tested in `internal/webdavx` and in the
integration suite.

### D-020 — Lock tokens are namespaced per user

**Context.** Each user gets their own `webdav.NewMemLS()` (D-002's isolation
extended to locking). But `memLS` issues tokens from a counter that starts at
zero in every instance and emits them as bare decimal strings — so Alice's first
lock and Bob's first lock are both the token `1`. A token minted by one user's
lock system therefore validates against another's.

No cross-user compromise follows from that on its own, because reaching another
user's lock system requires that user's password. It is still a token collision
in a security-relevant namespace, and the sort of thing that becomes a real bug
the moment anything else starts keying off a token.

**Decision.** Wrap each user's lock system in `scopedLS`, which prefixes every
token with 12 random bytes generated when the lock system is created, and
refuses any token that does not carry that prefix. Tokens become
`opaquelocktoken:<random>-<n>`, which is also a valid Coded-URL — `memLS`'s bare
`1` is not.

**Consequence.** Presenting another user's `If:` token yields `412 Precondition
Failed` rather than being silently accepted. Asserted directly in the
integration suite's isolation test.

### D-021 — A successful credential verification is cached for five minutes

**Context.** WebDAV is stateless: the client sends `Authorization` on every
request, and a file manager makes dozens of requests for a single user action.
Bcrypt at cost 12 — the cost the admin CLI uses, chosen so a stolen
`users.yaml` is expensive to attack — takes roughly a quarter of a second on a
Raspberry Pi 5. Paying that per request makes the drive feel broken: in this
repository's own integration suite it turned a 1.3-second test into 104 seconds.

The two requirements are in direct conflict. Lowering the bcrypt cost would
weaken the thing bcrypt is there for.

**Decision.** Keep cost 12 and cache *successful* verifications for five
minutes. The cache is indexed by an HMAC-SHA256, keyed by a random per-process
value, over the username, the stored bcrypt hash and the offered password.

Three properties fall out of that construction:

- Failures are never cached, so the brute-force cost is unchanged. An attacker
  guessing passwords pays full bcrypt for every guess, plus the backoff.
- Changing a password changes the stored hash, which changes the index, so every
  cached entry for that user is invalidated the instant `users.yaml` is
  reloaded. There is no revocation delay.
- The cache holds no password material usable outside this process: the key is
  random and dies with the process.

**Consequence.** Repeat requests cost microseconds. A password change takes
effect on `systemctl reload goldencloud`, not five minutes later.
`auth.Options.CredentialCacheTTL` set to a negative value disables the cache
entirely for anyone who wants the slower, purer behaviour.

### D-022 — `require_mountpoint` asks which filesystem the storage is on, and defaults to off

**Context.** D-008 says the server must fail loudly rather than write to the SD
card when the share is not mounted. D-010 then put `storage_root` *inside* the
mount, at `/mnt/wd/goldencloud` rather than at `/mnt/wd`. A literal "is
`storage_root` a mountpoint" check would be false in the supported deployment
and would refuse to start every time.

**Decision.** `require_mountpoint: true` means "`storage_root` must live on a
filesystem other than the root filesystem". The check walks up from
`storage_root` to the nearest entry in `/proc/self/mountinfo` (falling back to
comparing device numbers with the parent) and fails if that turns out to be `/`.

It defaults to **false**, because D-011 already has `goldencloud.service`
hard-requiring `mnt-wd.mount`, and because with `storage_root` a sub-folder of
the share an unmounted NAS already fails the "`storage_root` does not exist"
preflight check. The flag is a second belt for operators who want one, not the
primary guard.

**Consequence.** The example config in `deploy/` does not mention the key and
still behaves correctly. `nearestMountpoint` is Linux-only; setting
`require_mountpoint: true` on another platform is refused with an explicit
message rather than silently passing.

### D-023 — The jail rejects backslash, colon and control characters outright

**Context.** `fsjail` normalises paths before handing them to `os.Root`. Three
byte classes are legal in a Linux filename but cannot appear in a legitimate
filename from this system's clients, and each is a documented traversal or
confusion vector:

- **Backslash** is a path separator to every Windows client and an ordinary byte
  to Linux. Accepting it means client and server disagree about the shape of the
  tree — precisely the disagreement a traversal exploit needs.
- **Colon** is the NTFS alternate-data-stream separator (`file.txt:hidden`,
  `file.txt::$DATA`) and is illegal in Windows filenames anyway.
- **NUL and other C0 control characters** truncate paths in C string APIs and
  have no business in a filename.

**Decision.** Reject all three with `fsjail.ErrInvalidPath` rather than trying to
interpret them, along with paths over 4096 bytes and components over 255 bytes.
Rejection happens before any filesystem call.

**Consequence.** A Linux-created file whose name genuinely contains a backslash
or colon is invisible over WebDAV. That is a deliberate trade: those names cannot
be created or opened by a Windows client in any case, and the alternative is
carrying an ambiguity through the one piece of code the whole security model
rests on. Covered by `TestRejectedNames` and by `FuzzJailEscape`.

---

## Post-release audit findings

### D-024 — KNOWN CONSTRAINT: Cloudflare caps proxied uploads at 100 MB; multi-GB uploads do not survive the tunnel yet

**Context.** An external review (correctly) pointed out what neither the build
nor the audit caught: Cloudflare enforces a maximum HTTP request body size on
all proxied traffic — **100 MB on Free and Pro plans**, 200 MB on Business —
answering `413 Request Entity Too Large` above it, and this applies to
Cloudflare Tunnel hostnames. rclone's generic WebDAV backend uploads each file
as a single `PUT`, so through the tunnel any file over the plan limit fails.
The 1 GB integration test is real but connects directly to the Go server; no
automated test crosses Cloudflare, so nothing in CI could have caught it.

**Status.** Downloads of any size are unaffected (responses are not capped).
Uploads over 100 MB work on the office LAN and fail over the tunnel. This
breaks the "multi-GB streaming" requirement for remote staff and is recorded in
`PROGRESS.md` as a blocking item for real-world deployment.

**The options** (an architectural choice the project owner has to make):

1. **Client-side chunking via rclone's `chunker` overlay** — wrap the WebDAV
   remote in rclone's chunker backend so every file is stored as ≤ 95 MB
   chunks. Self-contained, free, no server change; but files appear as chunk
   parts to any *other* WebDAV client (macOS Finder, the `net use` fallback),
   and a chunked store is only reassemblable by rclone.
2. **Carry the drive over a raw TCP tunnel** — `cloudflared` can proxy
   arbitrary TCP; the tray app would run a bundled `cloudflared access tcp`
   forwarder and mount against `127.0.0.1`. No body-size limit applies to a
   TCP stream. Cleanest data path; adds a second bundled binary, a second
   process to supervise, and Cloudflare Access configuration to the runbook.
3. **Pay Cloudflare** — Business raises the cap to 200 MB (still not
   multi-GB); Enterprise is negotiable. Money for a limit that chunking
   removes for free.

No option is implemented yet; the decision gates it.

### D-025 — A cached user handler rebuilds when the user's folder assignment changes

**Context.** The external review found that `webdavx` cached per-user handlers
by username alone. A SIGHUP reload after editing a user's `root` in
`users.yaml` swapped the auth store but left the cached handler — which holds
the old directory open — serving the old folder until restart. Worst case: the
old folder is later assigned to a new user, and two users quietly share it.

**Decision.** The cache entry records the absolute folder it was built for,
and `handlerFor` compares it against the folder the *currently authenticated*
user record resolves to, rebuilding on mismatch. Self-healing on every request
rather than dependent on the reload path remembering to invalidate. Covered by
`TestReloadedRootChangeTakesEffect`.

### D-026 — Release artefacts are pinned to the commit the run was dispatched from

**Context.** The external review caught that a dispatched release run builds
the commit the run started on, while the release action minted the tag at the
branch tip at *publish* time — so `v0.1.0`'s binaries embed a different
revision than the tag names. Provenance, not correctness: the delta was
documentation-only, this time.

**Decision.** Every job in the release workflow checks out `github.sha`
explicitly, and the release action receives `target_commitish: github.sha`, so
a dispatched release mints its tag at exactly the commit the artefacts were
built from. Tag-push releases are unaffected (the tag exists; the field is
ignored). The client build now also receives the tag as its assembly version,
so future installers stop claiming 0.1.0 forever.

### D-027 — Cached third-party binaries are re-verified on every build

**Context.** The external review noted `thirdparty.ps1` verified hashes only
on fresh download; an already-present `rclone.exe` or `winfsp.msi` was trusted
as-is. The rclone pin also covers the *zip*, so the extracted exe could never
be re-checked against it directly.

**Decision.** The WinFsp MSI is re-hashed against its pin on every build. For
rclone, extraction writes a sidecar recording the exe's own hash and the zip
pin it came from; later builds verify both and re-download on any mismatch.
Behaviour verified functionally (fresh, cached, tampered-exe, tampered-msi
scenarios) under PowerShell 7 before commit.

### D-028 — The 100 MB cap is defeated with rclone's chunker overlay, client-side

**Context.** D-024 laid out three ways past Cloudflare's 100 MB proxied-upload
cap. The project owner's directive was two words: **"No payments."** That
eliminates the paid plans, and between the two free options the chunker beats
the raw-TCP tunnel on every axis that matters here: no second bundled binary,
no extra supervised process on every staff PC, no Cloudflare Zero Trust
configuration in a runbook aimed at a first-time Linux user, and no change to
the server at all.

**Decision.** The tray app mounts `gcdrive:` — an rclone `chunker` remote
wrapping the WebDAV remote — with `chunk_size=95Mi` (99,614,720 bytes, safely
under the 100,000,000-byte cap) and `fail_hard=true` so a missing chunk is a
loud error rather than a silently truncated file. Both remotes are defined
purely through `RCLONE_CONFIG_*` environment variables: still no rclone.conf,
and the password still never touches an argument (D-006).

**Verified end-to-end** with the real rclone 1.68.2 and the real server binary,
using exactly the environment the builder generates: a 300 MB upload stored as
four chunks of ≤ 99,614,720 bytes plus a 79-byte metadata object; the download
round-tripped with an identical SHA-256; the mounted listing showed one 300 MB
file, not parts; a ≤ 95 MiB file was stored as a plain single file; a file
uploaded by plain WebDAV read back unchanged through the chunker; and a delete
removed every chunk.

**Consequence.** Staff on the Windows app get genuinely unlimited file sizes
through the tunnel. The accepted trade-offs: non-rclone clients (macOS Finder,
iOS Files, the no-WinFsp fallback) see chunk parts for large files and remain
subject to the 100 MB cap for their own uploads — documented in
`OTHER-PLATFORMS.md` and the runbook — and large files on the WD share itself
are stored as parts. The parts are plain byte-splits (`cat parts > file`
reconstructs exactly), so no data is ever hostage to rclone or GoldenCloud.

### D-029 — The server builds on Windows for local testing, though it only ships for Linux

**Context.** A layman trying GoldenCloud on a single Windows laptop needs to run
the server there. The release only ships `linux/amd64` and `linux/arm64`
binaries, and the server did not even cross-compile to Windows: the atomic
`users.yaml` writer read `syscall.Stat_t` inline to preserve `root:goldencloud`
ownership across a rewrite, which does not exist off Unix.

**Decision.** The ownership-preservation is now a build-tagged helper —
`preserveOwner` in `owner_unix.go` (the real Unix behaviour) and
`owner_other.go` (a no-op). The server therefore cross-compiles to
`windows/amd64` unchanged in behaviour on Linux, purely so it can be run on a
Windows laptop for a local end-to-end try (`GoldenCloud-LocalTest` kit). The
supported production target is still Linux only; `require_mountpoint` remains
Linux-only and refuses loudly elsewhere (D-022).

**Consequence.** No release artefact changes — Windows is a convenience build,
not a shipped one. The kit's server binary is stamped `v0.1.0-localtest` so it
can never be mistaken for a release download.

### D-030 — Owner directions from the first hands-on test, and what they change

**Context.** During the owner's first local test (2026-08-05) three directions
arrived: (1) `G:` is already taken on their machine — use `Z:`; (2) the system
"should connect us to https://home.mycloud.com/"; (3) worldwide File Explorer
access, each person with their own login.

**Recorded as follows.**

1. **Drive letter.** The local-test kit now mounts `Z:`. The real tray app
   already lets the letter be chosen (settings.ini / sign-in), but it does
   **not** yet detect that its default letter is taken and pick a free one —
   on a machine like the owner's, the default `G:` would fail exactly as the
   kit did. Promoted to the roadmap as a pre-rollout client item.
2. **home.mycloud.com cannot be the target, and that is by design.** That URL
   is Western Digital's own cloud portal — the discontinued service this whole
   project exists to replace. The brief itself mandates "no dependency on
   Western Digital's cloud services anywhere in the system". GoldenCloud uses
   the WD box only as local disks (its Local Access SMB share, mounted by the
   Pi); worldwide access happens under a domain the owner controls via
   Cloudflare Tunnel. The public hostname will be something like
   `cloud.<owner-domain>`, never `home.mycloud.com`.
3. **WD account logins cannot carry over.** Nobody but WD holds those
   passwords, and the WD account system lives in the cloud being retired.
   Per-user access is delivered by GoldenCloud's own accounts
   (`goldencloud user add`, one per person, each jailed to their own folder) —
   which satisfies "every user with their own login", but they are new logins
   the owner issues, not the pre-existing WD ones.

**Consequence.** Expectation-setting for handover: gates 1 and 2 stand
unchanged, and the staff-facing hostname and logins are GoldenCloud's own.

### D-031 — Considered and declined: rebuilding the drive on WD's still-live web service

**Context.** The owner correctly pointed out that WD discontinued only the
Discovery desktop app (the Explorer drive-letter experience); the
home.mycloud.com web portal still works. The natural question follows: why not
build the drive letter on top of WD's live web service, OneDrive-style, and
need no office hardware at all?

**Assessment.** Technically semi-possible — the web portal rides a proprietary
WD cloud API — but rejected as a foundation:

1. WD closed its My Cloud Home developer programme; there is no sanctioned
   third-party access. Anything built would be reverse-engineered, unsupported,
   and breakable by any WD-side change, silently and permanently.
2. The desktop app's shutdown was a business decision about the same service.
   The web portal is the last surviving piece of a product line being wound
   down, not a stable platform; building on it re-creates the exact
   single-vendor dependency whose failure started this project.
3. All traffic would relay through WD's cloud again: their performance, their
   terms, their kill switch.
4. The brief is explicit: "No dependency on Western Digital's cloud services
   anywhere in the system" and "permanently, on infrastructure I own".

**Consequence.** The architecture stands: WD box as LAN storage, GoldenCloud
server + tunnel on an always-on machine in the office (existing Windows PC
preferred, Pi as fallback). The gap the owner feels — "the website still
works, why add hardware?" — is real but temporary by WD's own trajectory; the
decision trades a second, later funeral for one small always-on box now.

### D-032 — The real deployment's facts, confirmed from the owner's hardware

**Context.** The owner shared photos of the actual WD unit and their domain, so
the deployment stops being hypothetical.

**Recorded.**

- **Device:** WD My Cloud Home, single-bay, 3 TB (P/N family WDBVXC0030). Rear
  connectors: one Gigabit Ethernet (to the router) and one USB-A port. That USB
  port is a *host* port — the box uses it to ingest from USB sticks; it cannot
  make the box appear as a USB disk to a computer. Nothing in this design ever
  plugs into it.
- **Topology, as designed and now confirmed feasible:** the WD unit keeps its
  one ethernet cable to the router, untouched. The Pi plugs into the router
  too. They meet over the office network via the WD Local Access (SMB) share —
  no direct cable between Pi and WD exists or is possible, and none is needed.
  If router ports run short, any small gigabit switch (or Wi-Fi for the Pi,
  though wired is preferred) solves it.
- **Domain (gate 1, part-answered):** the owner already owns
  `goldenivyinvestments.com`. Remaining for gate 1: add the domain to a free
  Cloudflare account and point its nameservers there. Note for care: if the
  domain already carries a website or email, Cloudflare's import copies the
  existing DNS records — hosting and email stay where they are; only the
  address book moves.
- **Gate 2, now decidable:** the staff hostname will be
  `cloud.goldenivyinvestments.com` pending the owner's nod, and
  `GOLDENCLOUD_SERVER_URL=https://cloud.goldenivyinvestments.com` is the value
  to set before cutting the configured release.

Device serial, MAC and the printed claim code are deliberately **not** recorded
here — this repository is public.
