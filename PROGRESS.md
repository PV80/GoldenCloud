# Progress

## ⚠️ Human action gates

Two things cannot be done without you. Everything else is built and green
before you touch either of these.

### Gate 1 — Cloudflare account + domain (blocks deployment, not the build)

You need to provide, or do yourself following `deploy/RUNBOOK.md` §6:

1. A Cloudflare account (free tier is enough).
2. A domain added to that Cloudflare account, with its nameservers pointed at
   Cloudflare.
3. The hostname you want staff to connect to — e.g. `cloud.yourcompany.com`.

The runbook walks through creating the tunnel and the DNS record. Nothing in
this repository stores a Cloudflare token; the token lives only on the Pi, in
`/etc/cloudflared/`, with mode `0600`.

### Gate 2 — Server hostname to bake into the Windows client

The client installer needs the hostname from Gate 1 compiled in, so staff only
ever type a username and password.

- Set repository variable `GOLDENCLOUD_SERVER_URL` to `https://cloud.yourcompany.com`
  (Settings → Secrets and variables → Actions → Variables → New repository variable).
- Re-run the release workflow, or push a new tag.

Until this is set, release builds still succeed but the app displays an
"unconfigured build" banner and refuses to save credentials — deliberately, so
an unconfigured installer can never be handed to staff by mistake.

**There is no third gate.** The rclone and WinFsp supply-chain hashes were
initially left blank for a human to fill in; they have since been pinned to real,
independently cross-checked values, so that is no longer your problem.

---

## Phase status

| Phase | Name                     | Status                                   |
| ----- | ------------------------ | ---------------------------------------- |
| 0     | Scaffold                 | ✅ complete                              |
| 1     | Server core              | ✅ complete, verified locally            |
| 2     | Client                   | ✅ builds and tests green on Windows CI  |
| 3     | Packaging + deploy kit   | ✅ complete, docs verified vs real binary |
| 4     | Handover                 | ⬜ blocked only on the two gates above   |

---

## Phase 0 — Scaffold

**Acceptance criteria.** Repo structure, `DECISIONS.md`, `PROGRESS.md`, CI
skeleton green.

**Passed.** Four-component layout; `DECISIONS.md` seeded D-001..D-008 and now
carries D-001..D-023; both human gates recorded at the top of this file;
`ROADMAP.md` records the v1 non-goals; `ci.yml` and `release.yml` landed.

**Deferred.** Nothing.

---

## Phase 1 — Server core

**Acceptance criteria.** All server unit + integration tests pass in CI,
including user-isolation and path-traversal suites. Admin CLI works end to end.

**Passed — verified by the orchestrator by running it, not by reading it:**

| Gate                          | Result                                                |
| ----------------------------- | ----------------------------------------------------- |
| `gofmt -l .`                  | clean                                                 |
| `go vet ./...`                | clean                                                 |
| `staticcheck ./...` (2025.1.1) | clean                                                 |
| `go mod tidy`                 | leaves `go.mod`/`go.sum` unchanged                    |
| `go test -race ./...`         | all packages pass, **77.2%** total statement coverage |
| Integration suite             | **all pass, 41.9s**, against the real compiled binary |
| `FuzzJailEscape`              | 57,891 executions, no escape                          |
| Cross-compile                 | static `linux/arm64` + `linux/amd64`, version ldflag wired |

Per-package coverage: `config` 85.3%, `auth` 85.2%, `fsjail` 81.0%,
`webdavx` 78.5%, `cmd/goldencloud` 66.7%.

**The mandatory isolation test passes.** `TestUserIsolation` denies Bob's data
to Alice across a long table of encodings — `../`, `%2e%2e`, double-encoded
`%252e%252e`, `....//`, `..;/`, NUL-byte splices, and absolute paths — for read,
write, delete and listing alike.

**Admin CLI verified end to end.** Created two users with `--password-stdin`,
confirmed folders are created `0700`, confirmed `user list` prints no bcrypt
hashes, confirmed quota conversion (50GB → 46.6GiB).

**Two real bugs the server agent found and fixed:**

1. `webdav.NewMemLS` issues colliding lock tokens — it counts from zero in every
   instance, so two users' first locks were both `1`. Wrapped in a per-user
   namespace that rejects foreign tokens (D-020).
2. Bcrypt-per-request made the drive unusable: WebDAV re-authenticates on every
   request and cost-12 bcrypt is ~250 ms on a Pi. A five-minute cache of
   *successful* verifications, keyed by an HMAC over username + stored hash +
   password, took the isolation suite from 104 s to 1.3 s. Failures are never
   cached and a password change invalidates instantly (D-021).

**Deferred.** Quota is parsed, stored and reported but **not enforced on write**
— first item in `ROADMAP.md`. No audit log — second item.

---

## Phase 2 — Client

**Acceptance criteria.** Client builds in CI; a mocked-server integration test
proves sign-in, mount command generation, credential storage, and reconnect
logic. `docs/CLIENT-TEST.md` written with literal numbered steps.

**Passed:**

- `GoldenCloud.Core` compiles clean — 0 warnings, 0 errors.
- **`GoldenCloud.Core.Tests`: 135 tests, 135 passed, 0 failed**, run for real on
  Linux after installing the .NET 8 SDK. They cover all four required areas:
  sign-in against a stubbed WebDAV endpoint (207 vs 401, and the request really
  is `PROPFIND` + `Depth: 0` + correct Basic credential), mount-command
  generation for both strategies, credential storage round-trip and deletion on
  sign-out, and the reconnect backoff schedule with its jitter band and cap.
- An explicit test asserts the password appears in **no** process argument, no
  file name, and no redacted command line — only in the environment or on stdin.
- All 9 `GoldenCloud.Tray` source files pass a Roslyn syntax parse.
- Supply-chain pins are real and enforced: rclone 1.68.2 cross-checked against
  the publisher's own `SHA256SUMS` *and* an independent download-and-hash;
  WinFsp 2.0.23075 hashed from the release asset.
- All four NuGet pins confirmed to exist on nuget.org.
- `docs/CLIENT-TEST.md` written: 58 steps, pass/fail box each.

**The tray app compiles.** It could not be built locally — the Ubuntu-packaged
.NET SDK omits the WindowsDesktop targets — but CI's `windows-latest` runner
built `GoldenCloud.Tray` and produced `GoldenCloudSetup.exe` through Inno Setup
on the first attempt, with no fix round needed. The supply-chain step fetched
rclone and WinFsp and both pinned hashes matched.

**Still not proven — the honest gap.** Compiling is not running. Nothing has
exercised the Windows-only *behaviour*: `CredWriteW` P/Invoke marshalling
against the real Credential Manager, WinFsp registry detection on a machine
where WinFsp is actually installed, WinForms tray interaction, whether `net use`
reliably reads a password from a redirected stdin pipe, and whether
`rclone obscure -` accepts stdin. `docs/CLIENT-TEST.md` on real hardware is the
only thing that can settle those. Nothing here should be read as a claim that
the tray app has been *used*.

---

## Phase 3 — Packaging + deploy kit

**Acceptance criteria.** Tagged release produces all three artefacts.
`RUNBOOK.md` complete and reviewed for a zero-Linux-knowledge reader.

**Passed:** `deploy/RUNBOOK.md` at 170 contiguous numbered steps; hardened
`goldencloud.service`; commented `goldencloud.example.yaml` — with a server test
that loads the shipped file verbatim so config and docs cannot drift; cloudflared
templates; `Dockerfile` + compose; shellcheck-clean `install.sh`;
`docs/{CLIENT-TEST,SECURITY,STAFF-GUIDE,OTHER-PLATFORMS}.md`;
`scripts/check_docs.py` passes.

**Documentation reconciled against the real binary.** The docs were written
before the server existed, so every command output in them was invented. A full
verification pass ran each documented command against the compiled binary and
diffed the result. **28 mismatches were found and fixed** — 4 by the orchestrator
and 24 by the sweep. The significant ones:

| Was documented | Reality |
| --- | --- |
| `GET /` returns `200` | returns **405** — `x/net/webdav` refuses a collection GET |
| `level=info msg="listening" addr=…` | `time=… level=INFO msg=listening addr=… tls=false trusted_proxy=false` |
| `log_level: info` logs one line per request | per-request logging is **debug**; `info` is lifecycle only |
| a missing `users.yaml` means "no users yet" | **`serve` refuses to start**; only the admin CLI treats it as empty |
| no sign-in rate limiting | 5 failures → 429 + `Retry-After`, and a *correct* password is refused mid-lockout |
| `Retype new password:` | `Repeat password:` |
| `user list` shows `-` for no quota | shows `unlimited` |
| `install.sh` covers steps 61–91 | it covers 75–102 |

Five preflight/config error strings and the troubleshooting `grep` patterns were
also wrong, and `require_mountpoint` was missing from a config file that claimed
to document every option.

The orchestrator independently re-verified the sharpest claims by running them:
the 405, the startup log line, `serve` refusing a missing users file, the 401→429
transition on the 6th attempt, and `Retry-After` being served to a *correct*
password during lockout. All held.

**No server bugs were found** — every divergence was the documentation being
wrong, which is the right direction given the server is the tested component.

**Deferred.** The installer does not ship rclone's MIT licence text: the rclone
Windows zip contains no `COPYING` entry, so the copy step silently skips. A
compliance loose end, recorded in `ROADMAP.md`.

---

## Phase 4 — Handover

Blocked only on the two gates at the top of this file. No engineering work
remains before them.

---

## Definition of done — honest status

| Criterion                                                              | Status |
| ---------------------------------------------------------------------- | ------ |
| CI fully green                                                          | ✅ **8/8 jobs green** on GitHub Actions, two consecutive runs |
| Release produces all three artefacts                                    | ✅ **v0.1.0 published**, all six assets, verified by download |
| Fresh Windows 10 machine gets a working `G:` following `CLIENT-TEST.md` | ⬜ needs real hardware — the tray app compiles but has never been run |
| `RUNBOOK.md` takes a fresh Pi from blank SD card to reachable server    | 🟡 every `goldencloud` command verified against the real binary; the Pi/WD/cloudflared steps need real hardware |
| Server correctness                                                      | ✅ verified locally and in CI, including the mandatory isolation suite |

### CI evidence

All eight jobs green on `6e4b5d1`: server lint, server unit tests, server
integration tests, cross-compile ×2 (arm64, amd64), client build + tests,
client installer, docs check. The two earlier red runs were mid-development
states — the integration package was still being written — and both Windows jobs
have passed on every run in which they were reached.

### Release evidence — v0.1.0

<https://github.com/PV80/GoldenCloud/releases/tag/v0.1.0>

| Asset | Size |
| --- | --- |
| `goldencloud-server-linux-arm64` (+ `.sha256`) | 7.0 MB |
| `goldencloud-server-linux-amd64` (+ `.sha256`) | 7.5 MB |
| `GoldenCloudSetup.exe` (+ `.sha256`) | 62.9 MB |

Not merely "the workflow went green" — the amd64 artefact was **downloaded from
the release and exercised**:

- published `.sha256` matches the downloaded bytes
- `version` prints `goldencloud v0.1.0` — the tag, not the branch name, which is
  what the `inputs.tag` fix was for
- created two users, served them, and confirmed on the released binary:
  `PUT` own file `201`, `GET` own file `200`, wrong password `401`, and Alice
  reading Bob's payslip via both `..` and `%2e%2e` → `404`

**`GoldenCloudSetup.exe` is an UNCONFIGURED build** — human gate 2 is not set, so
it shows the unconfigured banner and refuses to save credentials by design. It
is fine for inspecting the installer; it is **not** the build to hand to staff.
Set `GOLDENCLOUD_SERVER_URL` and re-run the release for that.

### A note on the release tag

This environment's git proxy returns **HTTP 403 on tag ref pushes** while
allowing branch pushes, so `v0.1.0` could not be pushed from here. The release
was produced by dispatching the release workflow with the tag as an input
instead. Working around this surfaced a real bug: all three version expressions
put `github.ref_name` ahead of `inputs.tag`, and `ref_name` is *always* set — on
a dispatched run it is the branch name. A manually triggered release would have
been tagged with the branch name and the binaries stamped with it. Fixed.
