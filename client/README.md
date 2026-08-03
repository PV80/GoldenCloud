# GoldenCloud Windows client

A tray application that signs a member of staff in once and keeps a drive letter
(`G:` by default) connected to their own folder on the office server. No VPN, no
server address to type, no .NET runtime to install.

---

## Layout

| Path                     | What it is                                                                              |
| ------------------------ | --------------------------------------------------------------------------------------- |
| `GoldenCloud.sln`        | The solution CI restores, builds and tests.                                               |
| `GoldenCloud.Core/`      | `net8.0`, no Windows dependency. All the logic worth testing.                             |
| `GoldenCloud.Tray/`      | `net8.0-windows`, WinForms. Windows-specific implementations and the UI. Nothing else.    |
| `GoldenCloud.Core.Tests/`| xUnit tests for `GoldenCloud.Core`, against a stubbed server. No sockets are opened.      |
| `build.ps1`              | Publishes the self-contained single-file `win-x64` executable to `build/app/`.             |
| `package.ps1`            | Runs Inno Setup, producing `installer/Output/GoldenCloudSetup.exe`.                        |
| `thirdparty.ps1`         | Downloads and hash-verifies `rclone.exe` and the WinFsp MSI.                               |
| `thirdparty.pins.json`   | The pinned versions, URLs and SHA256s.                                                     |
| `installer/`             | The Inno Setup script and its output.                                                     |

### Why the split

`GoldenCloud.Core` exists so the interesting decisions — which command to run,
how long to wait before retrying, whether an address is safe to send a password
to — can be tested on any machine, in milliseconds, with no Windows, no server
and no drive letter. `GoldenCloud.Tray` is then deliberately thin: it starts
processes, touches the registry, and draws the menu.

```
GoldenCloud.Core                        GoldenCloud.Tray
─────────────────────────────────       ────────────────────────────────────
ServerUrlValidator                      Program (entry point, single instance)
AccountService                          TrayApplicationContext (icon + menu)
IWebDavProbe / WebDavProbe              SignInForm
ICredentialStore                  ◀──── WindowsCredentialStore (CredWriteW…)
InMemoryCredentialStore (tests)
IMountCommandBuilder              ◀──── MountSupervisor (runs the commands)
  RcloneMountCommandBuilder
  NetUseMountCommandBuilder
IPasswordObscurer                 ◀──── RcloneProcessObscurer
  RcloneObscure / Managed…               WinFspDetector, WebClientLimit, AutoStart
ReconnectBackoff
DriveLetters
```

---

## Building locally

You need the .NET 8 SDK and Windows.

```powershell
cd client

# Restore, build and test — this is exactly what CI runs.
dotnet restore GoldenCloud.sln
dotnet build   GoldenCloud.sln -c Release --no-restore
dotnet test    GoldenCloud.sln -c Release --no-build

# Publish a single-file executable with your server address baked in.
$env:GOLDENCLOUD_SERVER_URL = 'https://cloud.yourcompany.com'
./build.ps1 -Configuration Release

# Build the installer.
./package.ps1
# -> client/installer/Output/GoldenCloudSetup.exe
```

`dotnet restore` and `dotnet build` also work on Linux and macOS because
`EnableWindowsTargeting` is set; only publishing and packaging need Windows.

Omit `GOLDENCLOUD_SERVER_URL` and the build still succeeds, but it produces an
**unconfigured** client: a red `UNCONFIGURED BUILD` banner in the sign-in window,
sign-in disabled, and a refusal to store anything (D-007). That is on purpose —
an unconfigured installer must never be mistaken for a real one.

### Diagnostics

```
GoldenCloud.Tray.exe --diagnostics
```

shows the baked-in server address, whether WinFsp is present, which mount
strategy would be chosen, the current WebDAV size limit, and where the data
directory is. The Start-menu group has a shortcut for it.

---

## The two mount strategies

`MountSupervisor` picks one at mount time. The user can force either through
`strategy=` in `%LOCALAPPDATA%\GoldenCloud\settings.ini`.

### Primary — rclone + WinFsp, chunker-wrapped (D-005, D-028)

```
rclone.exe mount gcdrive: G: --vfs-cache-mode=writes --dir-cache-time=10s
           --volname=GoldenCloud --network-mode --no-console --log-level=NOTICE
           --cache-dir=… --log-file=…
```

with the remotes defined entirely in the child process's environment
(`RCLONE_CONFIG_<NAME>_<KEY>`), so **no `rclone.conf` is ever created, read or
written**:

| Variable | Value |
| --- | --- |
| `RCLONE_CONFIG_GCWEBDAV_TYPE` | `webdav` |
| `RCLONE_CONFIG_GCWEBDAV_URL` | the baked-in server URL |
| `RCLONE_CONFIG_GCWEBDAV_VENDOR` | `other` |
| `RCLONE_CONFIG_GCWEBDAV_USER` | the signed-in username |
| `RCLONE_CONFIG_GCWEBDAV_PASS` | the obscured password — **never an argument** |
| `RCLONE_CONFIG_GCDRIVE_TYPE` | `chunker` |
| `RCLONE_CONFIG_GCDRIVE_REMOTE` | `gcwebdav:` |
| `RCLONE_CONFIG_GCDRIVE_CHUNK_SIZE` | `95Mi` |
| `RCLONE_CONFIG_GCDRIVE_FAIL_HARD` | `true` |

- Chosen when WinFsp is installed **and** `rclone.exe` sits next to the tray app.
- The drive mounts `gcdrive:`, a **chunker overlay**: files over 95 MiB are
  stored as `name.rclone_chunk.001…` parts, each safely below Cloudflare's
  100 MB proxied-upload cap (D-024), and reassembled transparently on read.
  Files at or under 95 MiB are stored as ordinary single files, and files
  uploaded by other WebDAV clients read back unchanged. Verified end-to-end
  against the real server: a 300 MB upload stores as four sub-cap chunks and
  round-trips with an identical SHA-256.
- Chunks are plain byte-splits — with nothing but a shell,
  `cat name.rclone_chunk.* > name` reconstructs the file, so the data does not
  depend on rclone to be readable.
- Proper streaming, survives flaky links, and the mount ends cleanly when the
  process is killed.

### Fallback — the Windows WebDAV redirector

```
net.exe use G: https://…/ * /user:alice /persistent:no
```

- Chosen when WinFsp is missing, which happens if its install was declined.
- The literal `*` makes `net.exe` prompt for the password, which the supervisor
  writes to the child's **standard input**. It is never an argument.
- Windows refuses to download any file over **50 MB** through this path until
  `HKLM\SYSTEM\CurrentControlSet\Services\WebClient\Parameters\FileSizeLimitInBytes`
  is raised. The tray app detects that and offers to fix it, behind a dialog that
  spells out that it is machine-wide, needs administrator rights, and restarts
  the WebClient service. Declining is a supported answer; the app keeps working
  for files under the limit.
- Unmounting needs an explicit `net use G: /delete /y`.

### Reconnecting

A health-check loop reads the drive root every 15 seconds — existence alone is
not enough, because a dropped mapping can leave the letter present but
unresponsive. The probe runs on a pool thread with its own 10-second timeout so a
wedged redirector cannot stall the supervisor.

When the drive is not healthy the supervisor re-mounts, backing off
2s → 4s → 8s → … capped at 5 minutes, each delay jittered by ±20% so a room full
of PCs that lost the link together do not retry in lockstep. A success resets the
schedule. `ReconnectBackoff` is pure and unit-tested; no test sleeps.

---

## Where credentials live (D-006)

**Windows Credential Manager**, as a generic credential under the target name
`GoldenCloud`, written through `CredWriteW` and read through `CredReadW`
(`CRED_TYPE_GENERIC`, `CRED_PERSIST_LOCAL_MACHINE`). Windows encrypts the blob
at rest with the signed-in user's key.

You can see it at Control Panel → Credential Manager → Windows Credentials, or:

```powershell
cmdkey /list:GoldenCloud
```

What the client never does:

- writes the password to a file — no `rclone.conf`, no settings file, no log;
- puts the password on a command line — on Windows any user can read another
  process's command line through WMI, but not its environment block;
- stores anything before the server has accepted the credentials;
- stores anything at all in an unconfigured build.

`settings.ini` next to it holds only the drive letter, the strategy preference
and the last username, so the sign-in box can pre-fill.

**Sign out** deletes the credential (`CredDeleteW`) and unmounts. There is no
"remember me" file left behind.

### The rclone password encoding

rclone refuses a WebDAV password that has not been through its `obscure`
encoding, so the value handed to `RCLONE_CONFIG_GCWEBDAV_PASS` has to be in that form.
The tray app runs the bundled `rclone.exe obscure -` and feeds the password on
standard input, which is guaranteed correct because rclone does the work itself.
If that fails for any reason — rclone missing, blocked, or a future release that
changes the sub-command — it falls back to `RcloneObscure`, a managed
re-implementation in `GoldenCloud.Core`.

`RcloneObscure` is not a security boundary and rclone's own documentation says as
much; the real protection is that the value never touches disk and never enters
an argument vector. Its tests prove the encoding is self-consistent,
non-deterministic and URL-safe. They cannot prove byte-compatibility with rclone,
which is exactly why it is the fallback and not the primary.

---

## Tests

```powershell
dotnet test GoldenCloud.sln -c Release
```

`GoldenCloud.Core.Tests` runs against a `StubHttpMessageHandler` that plays the
part of a WebDAV server. Nothing opens a socket, nothing touches the registry,
nothing sleeps.

They assert:

1. **Sign-in.** 207 Multi-Status from a stub server signs in; 401 does not. The
   request is a `PROPFIND` with `Depth: 0` and the right Basic credential. A
   plain-`http://` or malformed address is refused with **zero** requests sent.
2. **Mount commands.** Exact argument vectors for both strategies, and — asserted
   explicitly for each — the password (plain *and* obscured) appears in **no**
   argument, in the file name, or in the loggable command line. It is only in the
   environment block (rclone) or on stdin (`net use`).
3. **Credentials.** Round-trip through `ICredentialStore`; a failed sign-in stores
   nothing; sign-out deletes; an unconfigured build refuses both.
4. **Backoff.** The exact 2/4/8/…/300 second schedule, the cap, the ±20% jitter
   band, that jitter never breaches the cap, and that a success resets it.
5. **URL validation.** `http://` refused unless the host is loopback; malformed
   hosts, ports, schemes, embedded credentials and query strings refused; trailing
   slashes and casing normalised idempotently.

---

## Installer

`installer/GoldenCloud.iss` produces one `GoldenCloudSetup.exe` that:

- installs the self-contained tray app into `Program Files\GoldenCloud`;
- puts `rclone.exe` beside it;
- installs the bundled WinFsp MSI **only when WinFsp is not already present**;
- creates the `HKCU\…\Run` autostart entry, which the tray menu also toggles;
- adds Start-menu shortcuts, including one for `--diagnostics`.

`rclone.exe` and `winfsp.msi` are not in git. `thirdparty.ps1` downloads them
from their official release URLs at build time and verifies a pinned SHA256 —
see [`thirdparty/README.md`](thirdparty/README.md), including the one-off step
needed to record the hashes.

---

## Notes for whoever edits this next

- Nullable reference types are on, but nullable warnings are **not** errors. The
  code was written without a .NET SDK to hand, and a single flow-analysis
  disagreement turning CI red for a non-defect is a worse outcome than a warning.
  If you have an SDK, promoting them is a good change to make.
- Anything you would want to write a test for belongs in `GoldenCloud.Core`.
  If you find yourself wanting to test something in `GoldenCloud.Tray`, that is
  a sign the logic is in the wrong project.
- Windows-only APIs carry `[SupportedOSPlatform("windows")]` even though the
  `net8.0-windows` target framework implies it, so a reader can see the boundary
  without checking the project file.
