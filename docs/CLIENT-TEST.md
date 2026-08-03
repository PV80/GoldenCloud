# Windows client — manual test checklist

**Target machine:** a *fresh* Windows 10 22H2 installation (Windows 11 works
too). "Fresh" matters: a machine you have already developed on has WinFsp,
.NET runtimes, firewall exceptions, and stale credentials that hide real
failures from real staff PCs.

**Time:** about 45 minutes, plus one reboot.

**What passing means:** at the end you have a working `G:` drive in File
Explorer, files move both ways, one user cannot see another user's files, and
the drive comes back by itself after a reboot.

Run this **before** handing `GoldenCloudSetup.exe` to anybody. Section 15 of
[`../deploy/RUNBOOK.md`](../deploy/RUNBOOK.md) sends you here.

---

## How to record results

Every step has a result line. Tick one, and write down anything odd — including
things that worked but looked wrong, which are the bugs that bite later.

```
Tester: ______________________   Date: ____________
Machine: _____________________   Windows build: ____________  (winver)
Installer version: ___________   Server version: ____________
```

**A single FAIL blocks the rollout.** Note it, stop, and fix it. Do not "carry
on and see if the rest works" — later steps assume the earlier ones passed and
you will waste an hour chasing a symptom of a cause you already found.

---

## Part 1 — Stand up a server to test against

You need a running GoldenCloud server with **two** users before you touch the
client. Do not test against the live office server: you will be creating and
deleting test files and restarting things.

Pick **one** of the three options.

### Option A — Server on the Windows test machine itself (recommended)

Simplest, because everything is on `127.0.0.1` and no TLS or network
configuration is involved.

**1.** Install Go 1.25 or later on the test machine from
<https://go.dev/dl/>, accepting the defaults. Open a **new** PowerShell window
afterwards so the `PATH` change takes effect.

**Result:** ☐ Pass ☐ Fail — `go version` prints a version. Notes: ______

**2.** Get the source and build a Windows server binary:

```powershell
git clone https://github.com/PV80/GoldenCloud.git
cd GoldenCloud\server
go build -o goldencloud.exe .\cmd\goldencloud
.\goldencloud.exe version
```

*Expected:* a version string.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**3.** Create a test storage folder and a config file:

```powershell
mkdir C:\goldencloud-test\storage
@"
listen: "127.0.0.1:8080"
storage_root: "C:/goldencloud-test/storage"
users_file: "C:/goldencloud-test/users.yaml"
log_level: "debug"
tls:
  enabled: false
  cert_file: ""
  key_file: ""
trusted_proxy:
  enabled: false
  header: "X-Forwarded-For"
  allowed_cidrs: []
"@ | Out-File -Encoding utf8 C:\goldencloud-test\config.yaml
```

Note the **forward slashes** in the paths — YAML treats a backslash as an
escape character.

**Result:** ☐ Pass ☐ Fail — `C:\goldencloud-test\config.yaml` exists. Notes: ______

**4.** Create two test users:

```powershell
.\goldencloud.exe user add --config C:\goldencloud-test\config.yaml testalice
.\goldencloud.exe user add --config C:\goldencloud-test\config.yaml testbob
.\goldencloud.exe user list --config C:\goldencloud-test\config.yaml
```

Use passwords you can retype quickly — `TestAlice123!` and `TestBob123!` are
fine for a throwaway server. **Write them down.**

*Expected:* `user list` shows both.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**5.** Start the server and **leave this window open** for the whole test:

```powershell
.\goldencloud.exe serve --config C:\goldencloud-test\config.yaml
```

*Expected:* log lines ending in `listening addr=127.0.0.1:8080`, then it sits
there. Watching this window during later steps is the fastest way to see what
the client is actually sending.

**Result:** ☐ Pass ☐ Fail — Notes: ______

Skip to step 9.

### Option B — Server on a Linux dev box or a spare Pi

Use this if you already have the server running somewhere on Linux.

**6.** On the Linux box, follow sections 8 to 12 of
[`../deploy/RUNBOOK.md`](../deploy/RUNBOOK.md), but set `storage_root` to a
local folder such as `/srv/goldencloud-test` rather than the WD share. Create
`testalice` and `testbob`.

**Result:** ☐ Pass ☐ Fail — `curl -u testalice ... http://127.0.0.1:8080/` on the Linux box does not return 401. Notes: ______

**7.** From the **Windows** test machine, forward the port over SSH so the
client still sees the server on loopback:

```powershell
ssh -N -L 8080:127.0.0.1:8080 youruser@your-linux-box
```

Leave that window open. It prints nothing — that is correct.

*Why an SSH tunnel rather than exposing the server on the LAN:* the server
deliberately refuses to serve plain HTTP on a non-loopback address
([`../DECISIONS.md`](../DECISIONS.md), D-003). Rather than switching that
safeguard off for a test — and risking someone leaving it off — the tunnel
makes the remote server appear on `127.0.0.1:8080` on the Windows machine,
which is exactly the shape of the real deployment.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**8.** Confirm from Windows:

```powershell
curl.exe -sS -o NUL -w "HTTP status: %{http_code}`n" http://127.0.0.1:8080/
```

*Expected:* `HTTP status: 401`. That is correct — the server is alive and asking
for credentials.

**Result:** ☐ Pass ☐ Fail — Notes: ______

### Option C — Docker

If you already run Docker Desktop, use
[`../deploy/docker-compose.example.yml`](../deploy/docker-compose.example.yml)
with the `ports: - "127.0.0.1:8080:8080"` line uncommented and the `cloudflared`
service removed. Create the two users with the `docker compose run` recipe at
the bottom of that file, then continue from step 9.

**Result:** ☐ Pass ☐ Fail — Notes: ______

---

## Part 2 — Build the installer for the test server

**9.** The server address is compiled into the installer, so staff never type it
([`../DECISIONS.md`](../DECISIONS.md), D-007). For this test it must point at
your test server. On the machine where you build the client:

```powershell
cd GoldenCloud\client
$env:GOLDENCLOUD_SERVER_URL = "http://127.0.0.1:8080"
.\build.ps1 -Configuration Release
.\package.ps1
```

*Expected:* `client\installer\Output\GoldenCloudSetup.exe` exists.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**10.** Confirm you did **not** accidentally build an unconfigured installer.
If `GOLDENCLOUD_SERVER_URL` was empty, the app shows a visible "unconfigured
build" banner and refuses to save credentials. You will see this in step 17 if
it happened.

**Result:** ☐ Pass ☐ Fail — Notes: ______

> If you are testing a release build from GitHub rather than building it
> yourself, its baked-in URL is your **live** server, not the test one. In that
> case skip Part 1 and test against the live server with two real accounts you
> created for the purpose — and remember to remove them afterwards.

---

## Part 3 — Baseline the fresh machine

Establishing what is *not* there is what makes the install steps meaningful.

**11.** Confirm the Windows version:

```powershell
winver
```

*Expected:* a dialog saying **Version 22H2** (or Windows 11). Write the build
number on your result sheet.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**12.** Confirm WinFsp is **not** already installed:

```powershell
Get-ChildItem "C:\Program Files (x86)\WinFsp" -ErrorAction SilentlyContinue
```

*Expected:* no output. If WinFsp is already there, this is not a fresh machine
and you are not testing what staff will experience.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**13.** Confirm `G:` is free:

```powershell
Get-PSDrive -PSProvider FileSystem | Select-Object Name, Used, Free
```

*Expected:* no drive named `G`. If `G:` is taken (a USB stick, a mapped share),
either free it or note which letter the app falls back to.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**14.** Confirm no GoldenCloud credential exists yet:

```powershell
cmdkey /list | Select-String -Pattern "GoldenCloud"
```

*Expected:* no output.

**Result:** ☐ Pass ☐ Fail — Notes: ______

---

## Part 4 — Install

**15.** Copy `GoldenCloudSetup.exe` to the test machine's Desktop and
double-click it.

*Expected:* Windows may show a **"Windows protected your PC"** SmartScreen
warning, because the installer is not code-signed. Click **More info** →
**Run anyway**.

**Note this on your result sheet.** Staff will hit it too, and they need to be
warned in advance or they will assume it is a virus and stop. This is expected
for an unsigned installer, not a defect.

**Result:** ☐ Pass ☐ Fail — SmartScreen seen? ☐ Yes ☐ No. Notes: ______

**16.** Work through the installer, accepting the defaults.

*Expected:* it installs the app, and it installs **WinFsp** as part of the
process — you may see a second, nested installer flash past, and a User Account
Control prompt asking for administrator permission. Click **Yes**.

*If WinFsp installation fails or you decline it:* the app is designed to fall
back to Windows' built-in WebDAV redirector
([`../DECISIONS.md`](../DECISIONS.md), D-005). Note it and carry on — but the
fallback has a 50 MB file size limit until a registry fix is applied, so
step 25 will fail. That is a known, documented degradation, not a surprise.

**Result:** ☐ Pass ☐ Fail — WinFsp installed? ☐ Yes ☐ No. Notes: ______

**17.** Confirm WinFsp is now present:

```powershell
Get-ChildItem "C:\Program Files (x86)\WinFsp\bin"
```

*Expected:* a listing containing `winfsp-x64.dll`.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**18.** Confirm the app launched. Look at the notification area at the bottom
right of the screen — click the small **^** arrow to see hidden icons.

*Expected:* a GoldenCloud tray icon, and no "unconfigured build" banner. If you
see that banner, step 9 did not set the URL; go back and rebuild.

**Result:** ☐ Pass ☐ Fail — Notes: ______

---

## Part 5 — First sign-in

**19.** Click the GoldenCloud tray icon.

*Expected:* a sign-in window asking for **username and password only**. There
must be **no server address field** — the address is baked in. If you are asked
for a server address, that is a failure.

**Result:** ☐ Pass ☐ Fail — Server address field present? ☐ Yes (FAIL) ☐ No (pass). Notes: ______

**20.** Enter a **deliberately wrong** password for `testalice` and sign in.

*Expected:* a clear, human error message — something like "Sorry, that username
or password was not recognised." **Not** a raw `401 Unauthorized`, a stack
trace, or a silent failure. No drive appears.

Check the server window from step 5: it should log a failed authentication.

**Result:** ☐ Pass ☐ Fail — Error message shown: ______________________

**21.** Now sign in correctly as `testalice`.

*Expected:* the window closes or shows a connected state, and within a few
seconds a notification says the drive is ready.

**Result:** ☐ Pass ☐ Fail — Time to connect: ______ seconds. Notes: ______

---

## Part 6 — The drive

**22.** Open **File Explorer** (Windows key + E) and click **This PC**.

*Expected:* a drive letter **`G:`** with a GoldenCloud-ish label, listed under
Network locations or Devices and drives.

**Result:** ☐ Pass ☐ Fail — Drive letter shown: ______. Notes: ______

**23.** Double-click `G:`.

*Expected:* it opens. It is empty — `testalice` has no files yet. It should open
in **under two seconds**; a long hang here means the mount is misbehaving.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**24.** Create a file directly in the drive: right-click in `G:` → **New** →
**Text Document**, name it `hello.txt`, open it, type some text, save, close.

*Expected:* it saves without error.

Then confirm it really landed on the server:

```powershell
Get-ChildItem C:\goldencloud-test\storage\testalice
```

(or `ls /srv/goldencloud-test/testalice` on the Linux box for Option B).

*Expected:* `hello.txt` is there, with a non-zero size.

**Result:** ☐ Pass ☐ Fail — Notes: ______

---

## Part 7 — Large file, both directions

This is the step that catches the classic Windows WebDAV 50 MB limit.

**25.** Make a 500 MB test file on the local disk:

```powershell
$f = [System.IO.File]::Create("C:\Users\Public\bigtest.bin")
$f.SetLength(500MB)
$f.Close()
Get-Item C:\Users\Public\bigtest.bin | Select-Object Name, Length
```

*Expected:* `Length` is `524288000`.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**26.** In File Explorer, copy `C:\Users\Public\bigtest.bin` **into** `G:`.

*Expected:* a progress dialog, then completion with no error. Time it.

*If it fails around 50 MB* with "The file size exceeds the limit allowed": the
app is using the `net use` fallback rather than WinFsp. Check step 16. The app
should offer to apply the registry fix — note whether it does, and whether it
asks for consent before making that machine-wide change (it must; the change is
under `HKLM` and needs elevation).

**Result:** ☐ Pass ☐ Fail — Time taken: ______. Notes: ______

**27.** Verify the size on the server:

```powershell
Get-Item C:\goldencloud-test\storage\testalice\bigtest.bin | Select-Object Length
```

*Expected:* exactly `524288000`. A truncated file is a hard failure.

**Result:** ☐ Pass ☐ Fail — Size on server: ______. Notes: ______

**28.** Copy it back **out**: from `G:\bigtest.bin` to `C:\Users\Public\roundtrip.bin`.

*Expected:* completes with no error.

**Result:** ☐ Pass ☐ Fail — Time taken: ______. Notes: ______

**29.** Confirm the round trip is byte-for-byte identical:

```powershell
$a = (Get-FileHash C:\Users\Public\bigtest.bin -Algorithm SHA256).Hash
$b = (Get-FileHash C:\Users\Public\roundtrip.bin -Algorithm SHA256).Hash
if ($a -eq $b) { "MATCH" } else { "MISMATCH - $a vs $b" }
```

*Expected:* `MATCH`.

**A mismatch is the most serious failure in this document.** It means data
corruption in transit. Stop, and do not ship.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**30.** Test a file with an awkward name — accented characters, spaces, and a
long name are all things that break naive path handling. In `G:`, create a
folder called `Tést Fôlder — 2026` and a text file inside it called
`résumé (final) v2.txt`.

*Expected:* both created, both visible after pressing F5 to refresh, and both
readable back.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**31.** Open a document from the drive and edit it in place — open
`G:\hello.txt` in Notepad, change it, save.

*Expected:* saves cleanly with no "file in use" or "cannot create backup file"
error. (Microsoft Office files are a harder case; if you have Word installed,
repeat with a `.docx`.)

**Result:** ☐ Pass ☐ Fail — Notes: ______

**32.** Delete something. Delete `G:\hello.txt`.

*Expected:* it disappears, and it is gone from the server folder too. It will
**not** go to the Windows Recycle Bin — network drives do not use it. Note this;
staff need to know deletes are immediate. (Server-side trash is on the roadmap,
see [`../ROADMAP.md`](../ROADMAP.md).)

**Result:** ☐ Pass ☐ Fail — Notes: ______

**33.** Clean up:

```powershell
Remove-Item C:\Users\Public\bigtest.bin, C:\Users\Public\roundtrip.bin
```

**Result:** ☐ Pass ☐ Fail — Notes: ______

---

## Part 8 — Isolation between users

**The most important test in this document.** If this fails, nothing else
matters.

**34.** Confirm what Alice currently has. Note the contents of `G:` — it should
contain `bigtest.bin` and `Tést Fôlder — 2026`.

**Result:** ☐ Pass ☐ Fail — Contents noted: ______

**35.** Sign out: click the tray icon → **Sign out**.

*Expected:* the `G:` drive disappears from File Explorer within a few seconds.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**36.** Confirm the stored credential was actually deleted:

```powershell
cmdkey /list | Select-String -Pattern "GoldenCloud"
```

*Expected:* **no output.** Signing out must remove the credential from Windows
Credential Manager, not merely unmount the drive
([`../DECISIONS.md`](../DECISIONS.md), D-006).

**Result:** ☐ Pass ☐ Fail — Notes: ______

**37.** Sign in as `testbob`.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**38.** Open `G:`.

*Expected:* **empty.** Bob must not see `bigtest.bin`, must not see
`Tést Fôlder — 2026`, and must not see any folder named `testalice`.

**Result:** ☐ Pass ☐ Fail — What Bob can see: ______________________

**39.** Try to escape the jail. In the File Explorer address bar, type each of
these and press Enter:

```
G:\..
G:\..\testalice
G:\..\..\
G:\%2e%2e\testalice
```

*Expected:* every one fails, or lands back at the root of `G:`. None of them
shows Alice's files or a directory listing above Bob's folder.

**Result:** ☐ Pass ☐ Fail — Any that escaped: ______________________

**40.** Try the same from the command line, which bypasses Explorer's own path
tidying:

```powershell
cmd /c "dir G:\..\testalice"
cmd /c "dir G:\..\.."
```

*Expected:* errors — "The system cannot find the path specified" or similar.
Never a listing of Alice's files.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**41.** Have Bob create a file: `G:\bob-only.txt`. Then check the server:

```powershell
Get-ChildItem C:\goldencloud-test\storage -Recurse -Name
```

*Expected:*

```
testalice
testalice\bigtest.bin
testalice\Tést Fôlder — 2026
testbob
testbob\bob-only.txt
```

Each user's files under their own folder, and nowhere else.

**Result:** ☐ Pass ☐ Fail — Notes: ______

> **If any of steps 38 to 41 failed, stop the entire rollout.** Per-user
> isolation is the single most security-critical property in the system
> ([`../DECISIONS.md`](../DECISIONS.md), D-002). Raise it as a bug immediately.

---

## Part 9 — Sign out, sign back in

**42.** Sign out of Bob's session (tray icon → Sign out).

*Expected:* `G:` disappears.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**43.** Sign back in as `testalice`.

*Expected:* `G:` reappears within a few seconds, and it contains **Alice's**
files — `bigtest.bin` is back, `bob-only.txt` is not there.

This proves the app is not caching one user's view and showing it to another.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**44.** Confirm the credential is stored for Alice now:

```powershell
cmdkey /list | Select-String -Pattern "GoldenCloud"
```

*Expected:* one entry. Confirm the password itself is **not** shown — Credential
Manager never displays stored secrets, and neither should anything else.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**45.** Confirm the password is not sitting in a file anywhere obvious:

```powershell
Get-ChildItem "$env:APPDATA\GoldenCloud", "$env:LOCALAPPDATA\GoldenCloud" -Recurse -ErrorAction SilentlyContinue |
  Select-String -Pattern "TestAlice123" -ErrorAction SilentlyContinue
```

*Expected:* **no output.** The password must never be written to disk in plain
text, and in particular must never appear in an `rclone.conf`
([`../DECISIONS.md`](../DECISIONS.md), D-006).

**Result:** ☐ Pass ☐ Fail — Notes: ______

**46.** Confirm the password is not on a command line, where any account on the
machine could read it:

```powershell
Get-CimInstance Win32_Process |
  Where-Object { $_.CommandLine -like "*TestAlice123*" } |
  Select-Object Name, CommandLine
```

*Expected:* **no output.**

**Result:** ☐ Pass ☐ Fail — Notes: ______

---

## Part 10 — Reboot and autostart

**47.** With Alice signed in and `G:` working, restart Windows:

```powershell
Restart-Computer
```

**Result:** ☐ Pass ☐ Fail — Notes: ______

**48.** Log back into Windows as the same Windows user.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**49.** *(Option A only.)* Restart the test server — it stopped when the machine
rebooted:

```powershell
cd GoldenCloud\server
.\goldencloud.exe serve --config C:\goldencloud-test\config.yaml
```

*(Option B only: re-establish the SSH tunnel from step 7.)*

On the real deployment the server runs as a systemd service and is already up;
this is an artefact of the test rig, not something staff will ever do.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**50.** Wait 60 seconds after logging in, then look at the tray.

*Expected:* the GoldenCloud icon is there without you launching it, and it has
signed in by itself using the stored credential.

**Result:** ☐ Pass ☐ Fail — Time until icon appeared: ______. Notes: ______

**51.** Open File Explorer.

*Expected:* `G:` is present and contains Alice's files. **The staff member did
not type a password.** This is the behaviour that decides whether people
actually use the thing.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**52.** Confirm autostart is registered rather than being a coincidence:

```powershell
Get-CimInstance Win32_StartupCommand | Where-Object { $_.Name -like "*GoldenCloud*" }
```

*Expected:* one entry pointing at the installed executable. (If the app uses a
Scheduled Task instead, check with
`Get-ScheduledTask | Where-Object TaskName -like "*GoldenCloud*"`.)

**Result:** ☐ Pass ☐ Fail — Mechanism found: ______

---

## Part 11 — Failure behaviour

Staff will hit these. Find out now what they see.

**53.** Break the connection: stop the test server (Ctrl+C in its window, or
close the SSH tunnel).

*Expected within a minute:* the app notices and says something human — a tray
notification or a changed icon. `G:` may go read-only or disappear. What must
**not** happen is a crash, a stack trace, or File Explorer hanging so badly the
machine needs a reboot.

**Result:** ☐ Pass ☐ Fail — What the user sees: ______________________

**54.** Restart the server.

*Expected:* the app reconnects by itself within a minute or two, or offers a
clear "reconnect" action. `G:` comes back.

**Result:** ☐ Pass ☐ Fail — Time to recover: ______. Notes: ______

**55.** Simulate a revoked user. On the server:

```powershell
.\goldencloud.exe user passwd --config C:\goldencloud-test\config.yaml testalice
```

Set a different password, then restart the server. On the client, try to browse
`G:`.

*Expected:* the app detects the rejection and prompts to sign in again. It must
**not** silently retry forever, and it must **not** show stale cached files as
though nothing has changed.

**Result:** ☐ Pass ☐ Fail — Notes: ______

---

## Part 12 — Uninstall

**56.** Sign out, then uninstall: **Settings → Apps → Installed apps →
GoldenCloud → Uninstall**.

*Expected:* it uninstalls cleanly. It may reasonably leave WinFsp behind, since
other software might use it — note which it does.

**Result:** ☐ Pass ☐ Fail — WinFsp left behind? ☐ Yes ☐ No. Notes: ______

**57.** Confirm nothing is left over:

```powershell
Get-PSDrive -PSProvider FileSystem | Select-Object Name
cmdkey /list | Select-String -Pattern "GoldenCloud"
Get-CimInstance Win32_StartupCommand | Where-Object { $_.Name -like "*GoldenCloud*" }
```

*Expected:* no `G:` drive, no stored credential, no startup entry.

**Result:** ☐ Pass ☐ Fail — Notes: ______

**58.** Confirm the files survived on the server:

```powershell
Get-ChildItem C:\goldencloud-test\storage -Recurse -Name
```

*Expected:* everything still there. Uninstalling the client must never delete
anybody's files.

**Result:** ☐ Pass ☐ Fail — Notes: ______

---

## Sign-off

```
Total steps: 58
Passed: ______   Failed: ______

Blocking failures (must be fixed before rollout):
  _______________________________________________
  _______________________________________________

Non-blocking observations (worth telling staff about):
  _______________________________________________
  _______________________________________________

Approved for rollout:  ☐ Yes   ☐ No

Signed: __________________________  Date: ____________
```

**Clean up the test rig:** delete `C:\goldencloud-test`, remove `testalice` and
`testbob` from any server that is not throwaway, and delete the test-configured
`GoldenCloudSetup.exe` so it cannot be handed to staff by mistake — it points at
`127.0.0.1`, and on a staff PC that means nothing at all.

---

## See also

- [`../deploy/RUNBOOK.md`](../deploy/RUNBOOK.md) — full server setup.
- [`STAFF-GUIDE.md`](STAFF-GUIDE.md) — the page you hand to staff.
- [`OTHER-PLATFORMS.md`](OTHER-PLATFORMS.md) — Mac and iPhone.
- [`SECURITY.md`](SECURITY.md) — what is protected and how.
