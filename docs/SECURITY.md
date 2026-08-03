# Security model

Written for the person who has to decide whether to trust this with the
company's files, and for the person who has to answer "is it safe?" from
someone who will not read a threat model.

Everything here describes v1 as designed. Where something is deliberately *not*
protected, it says so plainly — a security document that only lists strengths is
not useful.

---

## The one-paragraph version

Each staff member has a username and password. Every request they make travels
over HTTPS, is authenticated, and is served from a folder that is theirs alone —
enforced by the server, not by the app on their PC. The server is not reachable
from the internet or even from your own office network; the only route in is an
outbound tunnel that Cloudflare terminates with HTTPS. Files live on the office
storage and are never copied onto staff laptops, so a stolen laptop leaks a
saved credential you can revoke, not a copy of the files. Passwords are stored
as bcrypt hashes on the server and in Windows Credential Manager on the PC —
never in a plain-text file, never in a command line.

---

## What is encrypted, where

| Leg of the journey | Protected by | Notes |
| --- | --- | --- |
| Staff PC → Cloudflare | **HTTPS (TLS 1.3)** | Certificate issued and renewed by Cloudflare. This is the leg that crosses coffee-shop Wi-Fi and hotel networks, and it is fully encrypted. |
| Cloudflare → your office | **Encrypted tunnel** (QUIC or TLS) | Established outbound by `cloudflared` from your office and cryptographically authenticated with the tunnel credentials. |
| `cloudflared` → GoldenCloud server | **Plain HTTP over `127.0.0.1`** | Deliberate. Both ends are the same physical machine and the traffic never touches a network interface. Adding TLS here would mean a certificate to renew forever in exchange for nothing. |
| GoldenCloud server → WD unit | **SMB 3.0 encryption** | The `vers=3.0` mount option. Traffic between the Pi and the NAS crosses your office LAN encrypted. |
| Files sitting on the WD unit | **Not encrypted by GoldenCloud** | See "What is not protected" below. |
| Password on the staff PC | **Windows Credential Manager (DPAPI)** | Encrypted with a key derived from the Windows user's login. |
| Password on the server | **bcrypt hash** | Not reversible. See below. |

### The one honest caveat about Cloudflare

Cloudflare terminates TLS at its edge. That means, in principle, Cloudflare can
see the traffic passing through the tunnel. This is true of every reverse-proxy
CDN and is the price of not opening a port on your router.

For most offices this is the right trade: Cloudflare's security posture is
considerably better than a hand-configured port forward, and the alternative
usually ends up being no remote access at all, which pushes people onto
consumer file-sharing services with much worse properties.

If your data cannot tolerate that, the options are to run the server on your own
public IP with `tls.enabled: true` and your own certificate, or to keep it
LAN-only. Neither is covered by
[`../deploy/RUNBOOK.md`](../deploy/RUNBOOK.md), and both need a real port
forward.

---

## Why per-user jailing is the thing that matters most

Every other control in this system fails gracefully. If TLS were somehow
downgraded, an attacker would need to be on the network path. If the tunnel
credentials leaked, you would revoke them. If a password leaked, you would reset
it and one person's files are exposed.

**If the jail leaks, one authenticated user reads everybody's files** — payroll,
contracts, HR, the lot — using nothing but their own valid password. No attack
is required. It is the failure with the largest blast radius and the lowest cost
to trigger, which is why it gets disproportionate attention.

### How it is enforced

Isolation is **not** done by checking the URL. URL-prefix checks are the classic
way to get this wrong, and every one of these defeats a naive string comparison:

- `..` path segments, in any quantity
- percent-encoded `%2e%2e`, and double-encoded `%252e%252e`
- UTF-8 overlong encodings of `.` and `/`
- Windows-style `\` separators
- absolute paths beginning with `/`
- NTFS alternate-data-stream syntax (`file.txt:hidden`)
- a symlink inside the storage root pointing outwards
- a race between checking a path and opening it

Instead, each authenticated request is served by a handler whose entire view of
the filesystem is a jail rooted at that user's folder. Every path entering the
jail is normalised and re-validated, and the final resolved path is checked to
be inside the root **after** symlinks are evaluated. No code path in the server
accepts a caller-supplied absolute path. See
[`../DECISIONS.md`](../DECISIONS.md), D-002.

The practical consequence: a traversal bug would require a bug in one small,
heavily fuzz-tested file, rather than being possible anywhere in the request
pipeline.

**Verify it yourself.** Steps 38–41 of [`CLIENT-TEST.md`](CLIENT-TEST.md) and
step 140 of [`../deploy/RUNBOOK.md`](../deploy/RUNBOOK.md) are exactly this
test. Run them. If they ever fail, stop the rollout.

---

## What an attacker who steals a laptop gets

Assume the worst realistic case: a staff laptop is stolen, powered on, and the
thief has time and skill.

### If the laptop is locked and BitLocker is on

**They get nothing.** The stored credential is encrypted with a key derived from
the Windows login. No Windows password, no credential, no drive. The files were
never on the laptop.

> This is the case where "your files live in the office, not on your laptop"
> stops being a slogan and becomes the whole defence. Make sure BitLocker is on.

### If the laptop is unlocked, or the Windows password is weak or known

They get **that one user's access, until you revoke it.** Specifically:

| They get | They do not get |
| --- | --- |
| The `G:` drive, already mounted, showing that user's files | Any other user's files — the jail applies to them exactly as it does to the legitimate user |
| The ability to read, change and delete those files | The plain-text password (Credential Manager does not display stored secrets) |
| The server's address, which is in the app | Administrative access to the server |
| | Anything on the WD unit outside that user's folder |
| | The ability to create or modify users |

**What to do, in order:**

1. Revoke or reset that user immediately — see below. Fifteen seconds.
2. Ask what was in that folder, so you know what may have been read.
3. Restore anything deleted from backup.
4. Issue a new password on a new machine.

### If a staff member's password itself leaks

Same blast radius as the unlocked laptop: one user's folder, and only until you
reset it. The password is not reusable anywhere else in the system — there is no
shared account and no administrative login exposed over the network at all.

---

## How credentials are stored

### On the staff PC

Windows Credential Manager (`CRED_TYPE_GENERIC`), encrypted by Windows with a
key tied to that user's login.

Three properties worth stating explicitly, because they are the ones that go
wrong in other products:

- **Never written to a config file.** In particular, never to an `rclone.conf`
  on disk — the password is passed to the mount process through a private
  channel that does not touch the filesystem.
- **Never placed on a command line.** Command lines on Windows are readable by
  any account on the machine via WMI. Steps 45 and 46 of
  [`CLIENT-TEST.md`](CLIENT-TEST.md) verify both of these directly.
- **Deleted on sign-out.** Signing out removes the credential; it does not merely
  unmount the drive. There is no leftover "remember me" file.

See [`../DECISIONS.md`](../DECISIONS.md), D-006.

### On the server

`users.yaml` holds a **bcrypt hash** of each password, never the password.
Bcrypt is deliberately slow, so guessing against a stolen hash file is expensive
rather than instant, and each hash carries its own random salt, so cracking one
tells an attacker nothing about the others.

A copy of `users.yaml` therefore does **not** let anyone sign in. It is still
the most sensitive file on the machine and is stored `root:goldencloud` mode
`0640` — readable by the server, and by nobody else.

### The two other secrets on the server

| File | What it protects | Permissions | If it leaks |
| --- | --- | --- | --- |
| `/etc/goldencloud/wd.credentials` | The WD unit's Local Access password, in plain text | `0600`, `root:root` | Anyone on your office LAN could mount the WD share directly. Change the password on the WD unit and update this file. |
| `/etc/cloudflared/<uuid>.json` | The tunnel's private key | `0600`, `root:root` | Someone could impersonate your tunnel. Delete and recreate the tunnel — see [`../deploy/cloudflared/README.md`](../deploy/cloudflared/README.md). |

The WD credentials file has to be plain text: the kernel needs it at boot,
before anyone is logged in to type a password. `0600` is what stands between it
and every other account on the machine. Note also that the config backup in
maintenance task M-5 of the runbook **contains this file** — treat that archive
as a secret.

---

## How to revoke a user immediately

### One person

On the server:

```bash
sudo goldencloud user remove --config /etc/goldencloud/config.yaml alice
sudo systemctl reload goldencloud
```

Their next request fails with 401. Any transfer already in flight ends with the
connection.

**Their files are not deleted.** That is deliberate: "cut off access now" and
"decide what happens to the leaver's files" are two different decisions, made at
two different times, usually by two different people. Runbook maintenance task
M-2 covers archiving or deleting the folder afterwards.

### Or, keep the account but change the password

```bash
sudo goldencloud user passwd --config /etc/goldencloud/config.yaml alice
sudo systemctl reload goldencloud
```

Right for a suspected password leak where the person still works there. Every
device using the old password stops working at once, including the stolen
laptop.

### Everybody, right now

```bash
sudo systemctl stop cloudflared
```

The tunnel closes and the service becomes unreachable from outside the office
within seconds. Nothing is deleted. Investigate, then
`sudo systemctl start cloudflared`.

**Verify a revocation actually took:**

```bash
curl -sS -u alice:whatever-it-was -o /dev/null \
  -w 'HTTP status: %{http_code}\n' https://cloud.yourcompany.com/
```

`401` means it worked.

---

## What the server itself is protected by

Beyond authentication and the jail, the server process runs under real
constraints. Every line is commented in
[`../deploy/goldencloud.service`](../deploy/goldencloud.service); the ones that
matter most:

| Control | What it stops |
| --- | --- |
| Runs as a dedicated `goldencloud` account with **no login shell and a locked password** | Nobody can log in as it. A compromise lands as an unprivileged account, not as root. |
| `listen: 127.0.0.1` | The operating system refuses connections from anywhere but the machine itself. Not the internet, not a laptop on the same office switch, not a compromised printer. |
| Refuses to start on a non-loopback address without TLS | The safe configuration is the default; the unsafe one needs an explicit, named opt-in the runbook never tells you to use ([D-003](../DECISIONS.md)). |
| `ProtectSystem=strict` + `ReadWritePaths=/mnt/wd` | The only place on the whole machine the process can write is the storage. Not `/usr`, not `/etc`, not its own config file, not `users.yaml`. |
| `NoNewPrivileges=yes` | The process can never gain more privilege than it starts with. Closes the usual route from "bug in a web server" to "root on the NAS". |
| `CapabilityBoundingSet=` (empty) | No Linux capabilities at all — not even the ability to bind a low port. |
| `SystemCallFilter=@system-service` | The kernel refuses syscalls a file server has no business making. |
| `Requires=mnt-wd.mount` | The server cannot start without its storage, so it can never silently write staff files onto the SD card ([D-008](../DECISIONS.md)). |
| Restart rate limiting | A broken configuration fails visibly instead of looping forever and burying the original error. |

---

## What is **not** protected

Stated plainly, because the gaps matter more than the strengths when you are
deciding what to put in here.

**Files are not encrypted at rest on the WD unit.** Anyone with physical access
to the drive, or with an existing Local Access account on it, can read them.
If the office is broken into and the NAS is carried out, the data goes with it.
Mitigate with physical security, and with the WD unit's own encryption if your
model offers it.

**There is no audit log.** You can see one line per request in `journalctl` —
but only at `log_level: debug`, which is not the runbook's default and is too
noisy to leave on. At `info` you get failed sign-ins and lifecycle messages and
nothing else. Either way there is no tamper-evident, append-only record of who
read or wrote which file. If you need to answer "did Alice open the payroll
folder on 14 March", v1 cannot tell you. It is the second item on
[`../ROADMAP.md`](../ROADMAP.md).

**Sign-in attempts are rate limited.** The 5th consecutive failure for one
client-IP-and-username pair starts an exponential backoff: 1 second, then 2, 4,
8 and so on with each further failure, capped at 5 minutes. While the delay is
running every request for that pair — *including one with the correct
password* — is answered `429 Too Many Requests` with a `Retry-After` header
giving the seconds remaining. Once the delay expires the correct password gets
in again and clears the counter. A pair that goes quiet for 15 minutes is
forgotten. Requests that carry no credentials at all are never counted, so an
unauthenticated `OPTIONS` probe from the Windows redirector cannot lock anybody
out.

Failures are logged at `warn` as `msg="authentication failed"`, and refusals
during a backoff as `msg="authentication rate limited"` with the remaining
`retry_after`.

Two limits worth knowing. The bucket is per IP *and* username, so an attacker
spraying one password across many usernames from one address is slowed per
account rather than globally. And behind the tunnel every request arrives from
`cloudflared` on loopback, so the real client address is only distinguished when
`trusted_proxy` is configured with the header and the CIDRs it may be believed
from — otherwise all remote users share one bucket. The runbook's default
configuration is the loopback case.

**Password quality still matters most** — step 104 of the runbook generates a
20-character random password, and that remains the primary defence.

**There is no multi-factor authentication.** A username and password is the
whole of authentication. If you need MFA, Cloudflare Access can be layered in
front of the tunnel without changing anything in GoldenCloud — but that is not
set up by the runbook.

**There is no quota enforcement.** One user can fill the shared storage and
affect everybody. The field exists in `users.yaml` and the server reports it,
but nothing stops a write. First item on [`../ROADMAP.md`](../ROADMAP.md).

**There is no versioning or server-side trash.** A deleted file is deleted. A
file overwritten with a worse version is overwritten. Backups are your only
recovery, which makes maintenance task M-5 in the runbook load-bearing rather
than optional.

**The staff PC is not hardened by this project.** If a laptop has malware, that
malware has whatever access the signed-in user has. Endpoint security, Windows
updates, and BitLocker are still your responsibility.

**Nothing protects against a malicious administrator.** Whoever has `sudo` on
the Pi can read every file. That is inherent to running your own server.

---

## A short checklist for the person responsible

- [ ] BitLocker is on for every laptop that has the client installed.
- [ ] Every staff password is long and randomly generated, not chosen.
- [ ] Passwords were delivered by a different route from the installer.
- [ ] `sudo ls -l /etc/goldencloud/` shows `wd.credentials` as `-rw-------`.
- [ ] `sudo ls -l /etc/cloudflared/` shows the `.json` as `-rw-------`.
- [ ] `curl` to the Pi's own LAN address on port 8080 says **connection refused**
      (runbook step 102).
- [ ] The isolation test passes (runbook step 140, or client test steps 38–41).
- [ ] A config backup exists, off the WD unit, and you have opened it once.
- [ ] Staff know to report a lost laptop immediately, and know it is not a
      telling-off.
- [ ] You have run the revoke command once, in anger-free conditions, so you are
      not learning it during an incident.

---

## Reporting a security problem

Do not open a public GitHub issue for a suspected vulnerability. Contact the
repository owner directly and give them a reasonable window to fix it.

If you believe per-user isolation has failed, take the tunnel down first
(`sudo systemctl stop cloudflared`) and report it second.

---

## See also

- [`../DECISIONS.md`](../DECISIONS.md) — D-002 (jailing), D-003 (auth and TLS),
  D-004 (user store), D-006 (credential storage).
- [`../deploy/goldencloud.service`](../deploy/goldencloud.service) — every
  hardening line, commented.
- [`../deploy/cloudflared/README.md`](../deploy/cloudflared/README.md) — tunnel
  credentials and health.
- [`../deploy/RUNBOOK.md`](../deploy/RUNBOOK.md) — sections 16 and 17 for
  incident response and maintenance.
- [`CLIENT-TEST.md`](CLIENT-TEST.md) — the tests that verify these claims.
