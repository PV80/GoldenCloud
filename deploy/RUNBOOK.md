# GoldenCloud operator runbook

**From a blank SD card to a private cloud drive your staff can reach from
anywhere, with the first three users created.**

This document assumes you have **never used Linux**. Every command is given in
full, with what it does and what you should see when it works. Where something
can go wrong, the wrong output is shown too, along with what to do about it.

Work through it in order. Do not skip ahead. Each section ends with a **✅ How
to know it worked** box — if that check does not pass, fix it before moving on,
because everything after it is built on top.

Expect **90 minutes to two hours** the first time, most of it waiting for
downloads.

---

## Contents

| Section | What you do | Steps |
| --- | --- | --- |
| [0](#0-how-to-read-this-runbook) | How to read this runbook | — |
| [1](#1-what-you-need-before-you-start) | Gather the hardware and the facts | 1–7 |
| [2](#2-flash-the-sd-card) | Put Raspberry Pi OS on the card | 8–21 |
| [3](#3-first-boot) | Turn the Pi on for the first time | 22–26 |
| [4](#4-connect-to-the-pi-from-windows) | Reach the Pi from your desk | 27–36 |
| [5](#5-update-the-operating-system) | Bring the OS up to date | 37–43 |
| [6](#6-turn-on-local-access-on-the-wd-my-cloud-home) | Wake the WD unit up | 44–52 |
| [7](#7-mount-the-wd-share-at-mntwd) | Attach the storage to the Pi | 53–74 |
| [8](#8-install-the-goldencloud-binary) | Put the server program on the Pi | 75–82 |
| [9](#9-create-the-configuration) | Tell the server what to do | 83–88 |
| [10](#10-ownership-and-the-storage-root) | Lock down who owns what | 89–93 |
| [11](#11-install-and-start-the-service) | Make it run at boot | 94–102 |
| [12](#12-create-the-first-three-users) | Create Alice, Bob and Carol | 103–111 |
| [13](#13-publish-it-to-the-internet-with-cloudflare-tunnel) | Make it reachable | 112–133 |
| [14](#14-prove-it-works-from-outside-the-office) | Test from a phone | 134–140 |
| [15](#15-hand-it-to-staff) | Roll it out | 141–144 |
| [16](#16-troubleshooting-by-symptom) | When something is wrong | — |
| [17](#17-maintenance) | Living with it | 145–170 |

Related documents:

- [`goldencloud.example.yaml`](goldencloud.example.yaml) — every configuration
  option, explained.
- [`goldencloud.service`](goldencloud.service) — the systemd unit, with every
  hardening line commented.
- [`cloudflared/README.md`](cloudflared/README.md) — what the tunnel is and how
  to check its health.
- [`install.sh`](install.sh) — the shortcut for sections 8 to 11.
- [`../docs/SECURITY.md`](../docs/SECURITY.md) — the security model in plain terms.
- [`../docs/STAFF-GUIDE.md`](../docs/STAFF-GUIDE.md) — the one page you hand to staff.
- [`../docs/CLIENT-TEST.md`](../docs/CLIENT-TEST.md) — the Windows client test checklist.
- [`../docs/OTHER-PLATFORMS.md`](../docs/OTHER-PLATFORMS.md) — Mac and iPhone.

---

## 0. How to read this runbook

### The two questions everybody asks first

**"Do I have to change anything on my router?"**

No. Nothing. No port forwarding, no firewall rule, no static IP, no dynamic
DNS, no "DMZ host", no UPnP. You will not log into your router at all.

Here is why, in plain terms. Normally, to let people on the internet reach a
computer in your office, you have to open a door in your router and tell the
internet where it is. That is what port forwarding means, and it is the part
that is fiddly, risky, and often impossible.

GoldenCloud does the opposite. A small program called `cloudflared` runs on
your Pi and **dials out** to Cloudflare — the same way your web browser dials
out to a website, and just as allowed by every router in existence. It keeps
that phone call open. When a staff member's laptop asks Cloudflare for
`https://cloud.yourcompany.com`, Cloudflare passes the request **down the call
your office already made**, and the answer comes back the same way.

Nothing ever connects *into* your office. Your office connects *out*. There is
no door to open because nobody is knocking.

This also means it works when port forwarding **cannot** work:

- **Behind carrier-grade NAT (CGNAT).** Many ISPs — most 4G, 5G, Starlink and
  a growing number of fibre plans — do not give you your own public IP address.
  You share one with hundreds of other customers. Port forwarding is flatly
  impossible on such a connection. GoldenCloud does not care, because it never
  needed an inbound address.
- **On a changing IP address.** The tunnel re-announces itself. You never need
  dynamic DNS.
- **On a network you do not control** — a serviced office, a shared building,
  a landlord's connection.

**"Is it safe?"**

The server only listens on `127.0.0.1`, which means the operating system
refuses connections to it from anywhere except the Pi itself. Not from the
internet, and not even from a laptop plugged into the same office switch. The
tunnel is the only route in, it is encrypted end to end by HTTPS, and it
demands a username and password on every single request. See
[`../docs/SECURITY.md`](../docs/SECURITY.md) for the full picture.

### Conventions used below

- A grey box is a command. Type it, or copy and paste it, then press **Enter**.
  Copy the whole line.
- `sudo` at the front of a command means "do this as the administrator". The
  first time you use it in a session it asks for your password. **Nothing
  appears on screen as you type the password** — no dots, no stars. That is
  normal. Type it and press Enter.
- Text like `<your-domain>` is a placeholder. Replace it, angle brackets and
  all, with your own value from the worksheet in section 1.
- **`nano`** is the text editor used throughout. When you are in it:
  - Move with the arrow keys. There is no mouse.
  - **Ctrl+O** then **Enter** saves. (That is the letter O, for "Out".)
  - **Ctrl+X** exits.
  - **Ctrl+K** deletes the whole current line.
  - If you make a mess, press **Ctrl+X** then **N** to leave without saving,
    and start the step again.
- If a command produces no output at all, that is usually success. Linux is
  silent when it is happy.

### The shortcut

Sections 8 to 11 can be done in one command with
[`install.sh`](install.sh). Section 8 tells you how. The manual steps are
still written out in full, because when something breaks at 6pm on a Friday you
will need to know what the script did.

---

## 1. What you need before you start

**1.** Gather the hardware. This is the whole shopping list.

| Item | Notes |
| --- | --- |
| Raspberry Pi 5, 4 GB or 8 GB | 4 GB is plenty. An 8 GB is not wasted. A mini-PC works too — see the note below. |
| Official Raspberry Pi 5 power supply (27 W USB-C) | Do not use a phone charger. An underpowered Pi corrupts files in ways that look like software bugs for weeks. |
| microSD card, 32 GB, A2-rated | SanDisk Extreme or Samsung PRO Endurance. Cheap cards fail. This one holds the operating system, not staff files. |
| microSD card reader | Many laptops have a slot. If not, a USB adapter costs very little. |
| Ethernet cable | Optional but strongly recommended. Wired is faster and one fewer thing to go wrong. |
| Raspberry Pi 5 case with a fan | The Pi 5 gets hot and throttles without one. |
| The WD My Cloud Home already on your network | Plugged in, powered on, and set up. |
| A Windows PC on the same network | This is where you do the work. |

> **Using a mini-PC instead of a Pi?** Skip sections 2 and 3 entirely. Install
> Ubuntu Server 24.04 LTS or Debian 12, make sure OpenSSH is enabled, then
> start at section 4. Everything from there is identical except that you use
> the `amd64` binary instead of `arm64`, which step 76 detects for you
> automatically.

**2.** Gather the accounts. You need three things that are not hardware:

- The **My Cloud Home account** that owns the WD unit — the email address and
  password used to set it up.
- A **Cloudflare account**. The free tier is enough. Sign up at
  <https://dash.cloudflare.com/sign-up> if you do not have one.
- A **domain name added to that Cloudflare account**, with its nameservers
  pointed at Cloudflare. If you already have a company domain, add it to
  Cloudflare and follow their nameserver instructions; the change takes minutes
  to a few hours. If you do not have one, buy one — Cloudflare sells them at
  cost.

> These are the two "human gates" recorded in [`../PROGRESS.md`](../PROGRESS.md).
> Nothing in this repository holds a Cloudflare token; it lives only on the Pi,
> in `/etc/cloudflared/`, readable by root only.

**3.** Open a text file, or take a sheet of paper, and write out this worksheet.
You will refer to it constantly. Fill in the blanks you already know now, and
the rest as you go.

```
  A. Pi hostname                 goldencloud            (suggested — keep it)
  B. Pi admin username           gcadmin                (suggested — keep it)
  C. Pi admin password           ______________________ (invent one now, 16+ chars)
  D. Wi-Fi network name          ______________________ (skip if using ethernet)
  E. Wi-Fi password              ______________________ (skip if using ethernet)
  F. WD My Cloud Home IP         ______________________ (section 6 finds this)
  G. WD share name               ______________________ (section 6 finds this)
  H. WD Local Access username    ______________________ (section 6 sets this)
  I. WD Local Access password    ______________________ (section 6 sets this)
  J. Your domain                 ______________________ (e.g. yourcompany.com)
  K. Public hostname             cloud.________________ (e.g. cloud.yourcompany.com)
  L. Tunnel UUID                 ______________________ (section 13 gives you this)
```

**4.** Invent the Pi admin password (row C) now, and make it a good one. Write
it down somewhere you will not lose it. There is no password reset on a
Raspberry Pi — if you forget it, you start again from section 2.

**5.** Decide row K, the hostname your staff will type. `cloud.yourcompany.com`
is conventional and it is what the rest of this document assumes. It must be a
name on the domain from row J.

**6.** On your Windows PC, download and install **Raspberry Pi Imager** from
<https://www.raspberrypi.com/software/>. Click the Windows download button, run
the installer, accept the defaults.

**7.** Put the microSD card into your card reader, and the card reader into your
Windows PC. If Windows offers to format the card, **click Cancel** — the Imager
will handle it.

> ### ✅ How to know it worked
>
> You have a Pi, a power supply, a card in the reader, Raspberry Pi Imager
> installed, and a worksheet with rows A, B, C, J and K filled in. Nothing has
> been switched on yet.

---

## 2. Flash the SD card

This writes the operating system onto the card. Everything already on the card
will be destroyed — check it is the right card.

**8.** Open **Raspberry Pi Imager** from the Start menu. You get a window with
three buttons: Raspberry Pi Device, Operating System, Storage.

**9.** Click **CHOOSE DEVICE** and select **Raspberry Pi 5**. This filters the
OS list to images that will actually boot on your hardware.

**10.** Click **CHOOSE OS**. In the list that appears, click **Raspberry Pi OS
(other)**, then click **Raspberry Pi OS Lite (64-bit)**.

> Read that twice. It must be **Lite** — no desktop, because the Pi will run
> headless with no monitor and a desktop is just extra software to keep patched.
> And it must be **64-bit**, because the GoldenCloud binary is 64-bit only. The
> description under the correct entry reads "A port of Debian Bookworm with no
> desktop environment (Compatible with Raspberry Pi 3/4/400/5)".

**11.** Click **CHOOSE STORAGE** and select your microSD card. Check the size
shown matches your card. **If you see your computer's own hard disk in this
list, do not click it.** If only one device is listed and it is the right size,
that is your card.

**12.** Click **NEXT**. A dialog asks "Would you like to apply OS
customisation settings?". Click **EDIT SETTINGS**.

> This screen is the single most useful thing in the Imager. It pre-configures
> the Pi so it joins your network and accepts remote logins the moment it boots
> — which is what lets you finish this entire runbook without ever plugging a
> monitor or keyboard into the Pi.

**13.** On the **GENERAL** tab, fill in:

- **Set hostname:** tick it, and enter `goldencloud` (worksheet row A). This is
  the name you will use to find the Pi on your network.
- **Set username and password:** tick it. Username `gcadmin` (row B), password
  from row C. **Do not use the username `pi`** — it is the first thing every
  automated attack tries.
- **Configure wireless LAN:** tick it only if you are using Wi-Fi. Enter your
  network name (row D) and password (row E). Set **Wireless LAN country** to
  your country — `GB`, `US`, `KE`, and so on. Getting the country wrong can
  stop Wi-Fi working entirely, because it controls which radio channels are
  legal.
  *If you are using an ethernet cable, leave this unticked.*
- **Set locale settings:** tick it. Choose your time zone and keyboard layout.
  The time zone matters — log timestamps you cannot map to real time are much
  harder to reason about.

**14.** Click the **SERVICES** tab. Tick **Enable SSH**, and select **Use
password authentication**.

> SSH is how you will type commands on the Pi from your Windows PC. Without
> this tick you would need a monitor and keyboard plugged into the Pi.

**15.** Click the **OPTIONS** tab. Tick **Eject media when finished**. Leave
the rest as they are.

**16.** Click **SAVE**.

**17.** Back at the "apply OS customisation settings?" dialog, click **YES**.

**18.** A warning appears: "All existing data on [your card] will be erased."
Check the drive letter and size one last time, then click **YES**.

**19.** Windows may pop up a User Account Control prompt asking for permission.
Click **Yes**.

**20.** Wait. The Imager writes, then verifies. Expect five to fifteen minutes
depending on your card and reader. You will see a progress bar labelled
"Writing..." then "Verifying...".

- **Expected end state:** a dialog saying **"Write Successful"** and telling you
  the card can be removed.
- **If you see "Verifying failed" or "Write failed":** the card or the reader is
  faulty, or the card was removed mid-write. Try a different USB port first,
  then a different card. Do not proceed with a card that failed verification —
  it will produce inexplicable errors later.

**21.** Remove the microSD card from your PC.

> ### ✅ How to know it worked
>
> Raspberry Pi Imager said **Write Successful**, and the card is out of your PC.
> If you put the card back in, Windows will show a small `bootfs` drive and
> offer to format a second partition it cannot read — **say no**. That second
> unreadable partition is Linux, and Windows not understanding it is correct.

---

## 3. First boot

**22.** With the Pi **unplugged from power**, push the microSD card into the
slot on the underside of the board until it clicks.

**23.** If you are using ethernet, plug a cable from the Pi's network port to
your router or switch now.

**24.** Plug the USB-C power supply into the Pi, and into the wall.

**25.** Watch the Pi. A red light comes on immediately (power). A green light
next to it should start flickering within a few seconds — that is the SD card
being read, and it is the sign the card is good.

- **Red light on, green light never flickers at all:** the card was not written
  correctly, is not seated properly, or is faulty. Unplug, reseat the card, try
  again. If still nothing, go back to section 2 with a different card.
- **No lights at all:** power supply or cable. Try a different USB-C cable and
  confirm the supply is a genuine 27 W Pi 5 unit.

**26.** Wait **two minutes** without touching anything. The Pi expands its
filesystem to fill the card and reboots itself once during this. Interrupting
it here is the most common way to end up with a broken installation.

> ### ✅ How to know it worked
>
> The green light has settled into an occasional flicker rather than constant
> activity, and two minutes have passed. If you are on ethernet, the lights on
> the network port are lit. You still have no way to talk to it — that is the
> next section.

---

## 4. Connect to the Pi from Windows

You do not need PuTTY or any other download. Windows 10 and 11 have `ssh`
built in.

**27.** On your Windows PC, press the **Windows key**, type `terminal`, and
press **Enter**. (On older Windows 10 builds, type `powershell` instead — it
works identically for everything here.)

A window opens with a prompt like `PS C:\Users\You>`.

**28.** Check the Pi is on the network and answering to its name:

```
ping goldencloud.local
```

*What it does:* asks the network "is there a machine called goldencloud here?"
and sends it four small packets.

*Expected output:*

```
Pinging goldencloud.local [192.168.1.42] with 32 bytes of data:
Reply from 192.168.1.42: bytes=32 time=1ms TTL=64
Reply from 192.168.1.42: bytes=32 time=1ms TTL=64
Reply from 192.168.1.42: bytes=32 time=1ms TTL=64
Reply from 192.168.1.42: bytes=32 time=1ms TTL=64
```

**Write down the IP address in square brackets** — you may need it.

*If you see* `Ping request could not find host goldencloud.local`: the name
lookup failed. This is common and not fatal. Go to step 29.

*If you see* `Request timed out` *four times*: the name resolved but the Pi is
not answering. Give it another minute and try again; if it still fails, the Pi
is not on the network — check the ethernet cable, or your Wi-Fi details from
step 13.

**29.** *(Only if step 28 could not find the host.)* Find the Pi's IP address a
different way. Open your router's admin page in a browser — usually
<http://192.168.1.1> or <http://192.168.0.1>, and the password is often printed
on a sticker on the router itself. Look for a list called "Connected Devices",
"DHCP Clients", or "Attached Devices". Find the entry named `goldencloud` and
write down its IP address.

Wherever this runbook says `goldencloud.local`, use that IP address instead.

**30.** Connect:

```
ssh gcadmin@goldencloud.local
```

*What it does:* opens a secure remote command line on the Pi, as the user you
created in step 13.

**31.** The very first time, you get this:

```
The authenticity of host 'goldencloud.local (192.168.1.42)' can't be established.
ED25519 key fingerprint is SHA256:kL9x2vQ8mN4pR7sT1uW3yZ6aB0cD5eF8gH2iJ4kL6mN.
Are you sure you want to continue connecting (yes/no/[fingerprint])?
```

Type `yes` and press Enter. (The whole word — `y` is not accepted.)

*What this is:* your PC is remembering the Pi's identity so that next time it
can tell you if something has been swapped out underneath you. You will not be
asked again.

**32.** Enter the password from worksheet row C.

**Nothing appears as you type.** No dots, no stars, no cursor movement. This is
deliberate. Type it carefully and press Enter.

*If you see* `Permission denied, please try again.`: the password is wrong, or
the username is. Check row B and row C. After three failures the connection
closes; just run step 30 again.

**33.** You should now see something like:

```
Linux goldencloud 6.6.51+rpt-rpi-2712 #1 SMP PREEMPT Debian 1:6.6.51-1+rpt3 aarch64

The programs included with the Debian GNU/Linux system are free software;
the exact distribution terms for each program are described in the individual
files in /usr/share/doc/*/copyright.

gcadmin@goldencloud:~ $
```

That last line is the Pi's prompt. **You are now typing on the Pi, not on your
Windows PC.** Everything from here until the end of the runbook is typed at this
prompt.

**34.** Confirm you are who you think you are, on the machine you think you are
on:

```
whoami && hostname
```

*Expected output:*

```
gcadmin
goldencloud
```

**35.** Confirm this is a 64-bit system:

```
uname -m
```

*Expected output:*

```
aarch64
```

`aarch64` is what 64-bit ARM calls itself. **If you see `armv7l`, you flashed
the 32-bit image.** Stop here, go back to step 10, and choose the 64-bit one.
Nothing later in this runbook will work on a 32-bit system.

**36.** Note how to get back. If you close the window, or the connection drops,
you reconnect with exactly step 30 — `ssh gcadmin@goldencloud.local` — and your
password. To leave deliberately, type `exit`.

> ### ✅ How to know it worked
>
> Your Terminal prompt reads `gcadmin@goldencloud:~ $`, `whoami` says `gcadmin`,
> and `uname -m` says `aarch64`.

---

## 5. Update the operating system

The image you flashed was built weeks or months ago. This brings it current.

**37.** Refresh the list of available software:

```
sudo apt update
```

*What it does:* downloads the current catalogue of packages. It does not install
anything.

*Expected output* (the last line matters):

```
Get:1 http://deb.debian.org/debian bookworm InRelease [151 kB]
Get:2 http://archive.raspberrypi.com/debian bookworm InRelease [39.0 kB]
...
Fetched 23.4 MB in 6s (3,901 kB/s)
Reading package lists... Done
Building dependency tree... Done
Reading state information... Done
42 packages can be upgraded. Run 'apt list --upgradable' to see them.
```

*If you see* `Temporary failure resolving 'deb.debian.org'`: the Pi has no
internet access. Check the cable, or re-check the Wi-Fi details from step 13.
Test with `ping -c 3 1.1.1.1` — if that works but names do not, it is a DNS
problem; try `sudo reboot` and start this section again.

**38.** Install the updates:

```
sudo apt full-upgrade -y
```

*What it does:* downloads and installs every available update. The `-y` answers
"yes" to the confirmation prompt in advance.

*Expected output:* many lines of `Get:`, `Unpacking`, `Setting up`, ending back
at your prompt. **This can take ten minutes or more** on a fresh image. Let it
finish. If it appears to hang for a minute or two on `Setting up`, that is
normal — do not interrupt it.

*If a purple full-screen dialog appears* asking about a configuration file or a
service restart: press **Tab** to highlight `<Ok>`, then **Enter**. If it asks
whether to keep your current version of a config file, choose to **keep the
local version** (usually the default).

**39.** Remove software that is no longer needed:

```
sudo apt autoremove --purge -y
```

*Expected output:* either a list of removed packages, or
`0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.` Both are fine.

**40.** Restart, so any new kernel takes effect:

```
sudo reboot
```

*Expected output:* your connection dies immediately with
`client_loop: send disconnect: Connection reset` or just
`Connection to goldencloud.local closed by remote host.` **That is what
success looks like here.**

**41.** Wait 60 seconds, then reconnect:

```
ssh gcadmin@goldencloud.local
```

You will not be asked about the fingerprint this time — only for your password.

**42.** Confirm what you are running:

```
cat /etc/os-release | head -2
```

*Expected output:*

```
PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
NAME="Debian GNU/Linux"
```

**43.** Install the two small tools the next sections need:

```
sudo apt install -y cifs-utils smbclient curl
```

*What it does:* `cifs-utils` lets Linux mount Windows-style network shares —
which is what the WD unit serves. `smbclient` lets you look at those shares
before mounting them, which makes diagnosing problems far easier. `curl`
downloads files.

*Expected output:* ends with lines like
`Setting up cifs-utils (2:7.0-2) ...` and returns to the prompt. If they are
already installed you get
`cifs-utils is already the newest version` — equally fine.

> ### ✅ How to know it worked
>
> Run `sudo apt update` once more. The last line should now read
> `All packages are up to date.` and `which mount.cifs` should print
> `/usr/sbin/mount.cifs`.

---

## 6. Turn on Local Access on the WD My Cloud Home

By default a My Cloud Home only talks to Western Digital's cloud service. Local
Access makes it also behave like an ordinary network drive on your own
network — which is what lets the Pi mount it. This is a supported, built-in
feature; you are not modifying the device.

> **Why this step exists at all.** GoldenCloud never speaks to Western Digital.
> It sees a folder on the Pi. The operating system is what turns the WD unit
> into that folder. This decoupling is deliberate — see
> [`../DECISIONS.md`](../DECISIONS.md), D-008 — and it means that if you ever
> replace the WD unit with a USB disk or a different NAS, nothing about
> GoldenCloud changes except one line of `/etc/fstab`.

**44.** On your Windows PC (not the Pi), open a browser and sign in to
<https://home.mycloud.com> with the My Cloud Home account that owns the device.
The mobile app works too.

**45.** Find the settings for your device and turn on **Local Access** (in some
firmware versions this appears under a "Local Access" or "Network Access"
heading in device or user settings). Turning it on will ask you to set or
confirm a password for local access.

**46.** Write the local access **username** into worksheet row H and the
**password** into row I. The username is usually the email address on the
account or the short name shown next to your profile — note exactly what the
device shows you.

> WD's web interface changes between firmware releases, so the exact wording
> above may differ on your unit. The steps that follow verify the result
> directly, so you do not have to trust the menu names — if steps 49 to 52
> succeed, Local Access is on, whatever the button was called.

**47.** Back on the Pi, find the WD unit's IP address. The most reliable way is
your router's connected-devices list (same page as step 29) — look for a device
named something like `MyCloud-XXXXXX` or `WDMyCloud`.

Write it into worksheet row F. This runbook uses `192.168.1.50` as the example.

**48.** *(Optional but worth it.)* Give the WD unit a fixed address. In your
router's DHCP settings there is usually an option called "DHCP Reservation",
"Static Lease", or "Address Reservation". Reserve the current address for the
WD unit's MAC address.

*Why:* if the WD unit's IP changes after a power cut, the mount in the next
section breaks and staff lose their drive until you notice. Five minutes now
saves a confusing afternoon later.

**49.** Check the Pi can reach it (replace the address with row F):

```
ping -c 4 192.168.1.50
```

*Expected output:*

```
PING 192.168.1.50 (192.168.1.50) 56(84) bytes of data.
64 bytes from 192.168.1.50: icmp_seq=1 ttl=64 time=0.652 ms
64 bytes from 192.168.1.50: icmp_seq=2 ttl=64 time=0.601 ms
64 bytes from 192.168.1.50: icmp_seq=3 ttl=64 time=0.588 ms
64 bytes from 192.168.1.50: icmp_seq=4 ttl=64 time=0.594 ms

--- 192.168.1.50 ping statistics ---
4 packets transmitted, 4 received, 0% packet loss, time 3050ms
```

*If you see* `100% packet loss` *or* `Destination Host Unreachable`: wrong IP
address, or the Pi and the WD unit are on different networks. Recheck row F.

**50.** Ask the WD unit what shares it is offering (replace the address with
row F and `YOURUSER` with row H):

```
smbclient -L //192.168.1.50 -U 'YOURUSER'
```

*What it does:* lists the network folders the device is publishing. Nothing is
mounted or changed.

You will be prompted for a password — use row I. Again, nothing appears as you
type.

*Expected output:*

```
Password for [WORKGROUP\YOURUSER]:

        Sharename       Type      Comment
        ---------       ----      -------
        Public          Disk
        TimeMachineBackup Disk
        SmartWare       Disk
        IPC$            IPC       IPC Service
```

**Write the share name you intend to use into worksheet row G.** `Public` is
present on every My Cloud Home and is the safe default. If Local Access created
a private share named after your user, that is a better choice — use it.

*If you see* `NT_STATUS_LOGON_FAILURE`: wrong username or password. Recheck
rows H and I. Some firmware wants the short username rather than the full email
address; try both.

*If you see* `NT_STATUS_CONNECTION_REFUSED` *or* `Connection to 192.168.1.50
failed`: Local Access is not actually on. Go back to step 45.

*If you see* `NT_STATUS_ACCESS_DENIED` *but the share list still prints*: that
is fine — the `IPC$` probe was denied but the listing worked.

**51.** Confirm you can actually read the share you chose (replace values from
rows F, G and H):

```
smbclient //192.168.1.50/Public -U 'YOURUSER' -c 'ls'
```

*Expected output:* a directory listing, ending with a line about blocks
available:

```
Password for [WORKGROUP\YOURUSER]:
  .                                   D        0  Mon Aug  3 09:14:22 2026
  ..                                  D        0  Mon Aug  3 09:14:22 2026
  Documents                           D        0  Mon Aug  3 09:14:22 2026

                3813376 blocks of size 1048576. 3211264 blocks available
```

An empty share is fine — you only need the "blocks available" line.

**52.** Note the free space from that last line. `3211264 blocks of size
1048576` means roughly 3.2 TB free. Sanity-check it against the size of your WD
unit; a wildly wrong number means you are looking at the wrong share.

> ### ✅ How to know it worked
>
> Step 51 printed a directory listing and a "blocks available" line, and
> worksheet rows F, G, H and I are all filled in.

---

## 7. Mount the WD share at `/mnt/wd`

"Mounting" means making the network drive appear as an ordinary folder on the
Pi. After this section, `/mnt/wd` **is** the WD unit.

We create the `goldencloud` service account first, because the mount needs to
know which account will own the files it shows.

**53.** Create the group and the account:

```
sudo groupadd --system goldencloud
```

```
sudo useradd --system --gid goldencloud --home-dir /nonexistent \
     --no-create-home --shell /usr/sbin/nologin \
     --comment "GoldenCloud WebDAV server" goldencloud
```

*What it does:* creates an account that the server will run as. It has **no
password, no home directory, and no login shell** — nobody can log in as it,
ever. If the server is ever compromised, the attacker lands here rather than as
an administrator.

*Expected output:* nothing at all from either command. Silence is success.

*If you see* `groupadd: group 'goldencloud' already exists`: you already ran
this. Harmless — carry on.

**54.** Confirm it exists:

```
id goldencloud
```

*Expected output:*

```
uid=999(goldencloud) gid=999(goldencloud) groups=999(goldencloud)
```

The numbers will differ on your machine. That is fine.

**55.** Create the folder the share will appear at:

```
sudo mkdir -p /mnt/wd
```

*Expected output:* nothing.

**56.** Create the folder that will hold GoldenCloud's own files:

```
sudo mkdir -p /etc/goldencloud
sudo chmod 0750 /etc/goldencloud
sudo chgrp goldencloud /etc/goldencloud
```

*Expected output:* nothing from any of the three.

**57.** Create the credentials file that holds the WD username and password:

```
sudo nano /etc/goldencloud/wd.credentials
```

An empty editor opens. Type exactly these three lines, substituting worksheet
rows H and I:

```
username=YOURUSER
password=YOURPASSWORD
domain=WORKGROUP
```

No spaces around the `=`. No quotation marks. If your password contains a `#`,
that is fine here — this file is not a shell script.

Save with **Ctrl+O**, **Enter**, then exit with **Ctrl+X**.

**58.** Lock the credentials file down. **This step is not optional.**

```
sudo chown root:root /etc/goldencloud/wd.credentials
sudo chmod 0600 /etc/goldencloud/wd.credentials
```

*What it does:* makes the file readable and writable by `root` only. Mode `0600`
means "owner read+write, group nothing, everyone else nothing".

*Why it matters:* this file contains a password in plain text. It has to, because
the kernel needs it at boot before anyone is logged in to type it. `0600` is
what stops every other account on the machine from reading it.

**59.** Verify the permissions took:

```
ls -l /etc/goldencloud/wd.credentials
```

*Expected output:*

```
-rw------- 1 root root 58 Aug  3 10:22 /etc/goldencloud/wd.credentials
```

The `-rw-------` at the start is the important part. **If you see anything else
— particularly `-rw-r--r--` — repeat step 58.**

**60.** Test the mount by hand before making it permanent. Replace the address
and share name with worksheet rows F and G, and keep the rest exactly as
written:

```
sudo mount -t cifs //192.168.1.50/Public /mnt/wd \
  -o credentials=/etc/goldencloud/wd.credentials,uid=goldencloud,gid=goldencloud,file_mode=0660,dir_mode=0770,iocharset=utf8,vers=3.0,noserverino,nobrl
```

*What the options mean:*

| Option | Why |
| --- | --- |
| `credentials=` | Read the username and password from the locked file, rather than putting them on the command line where every account on the machine could see them in the process list. |
| `uid=` / `gid=` | Show every file as owned by the `goldencloud` account, so the server can read and write them. SMB has no concept of Linux ownership, so we impose one. |
| `file_mode=0660` | Files are readable and writable by the owner and group, and invisible to everyone else. |
| `dir_mode=0770` | Same for folders, plus the ability to list them. |
| `iocharset=utf8` | Handle accented characters, emoji, and non-Latin filenames correctly instead of mangling them. |
| `vers=3.0` | Use SMB 3.0, which is encrypted and modern. SMB 1 is obsolete and insecure. |
| `noserverino` | Do not trust the NAS's file ID numbers. Some consumer NAS firmware reuses them, which confuses Linux into thinking two different files are the same file. |
| `nobrl` | Do not send byte-range lock requests to the NAS. Several consumer NAS units handle them badly, which shows up as SQLite and Office files refusing to open. |

*Expected output:* nothing. Silence means it mounted.

*If you see* `mount error(13): Permission denied`: the username or password in
the credentials file is wrong. Recheck step 57. Also try `domain=` with the
device name instead of `WORKGROUP`.

*If you see* `mount error(2): No such file or directory`: the share name is
wrong. Recheck row G against the list from step 50 — it is case-sensitive.

*If you see* `mount error(112): Host is down` *or* `Unable to find suitable
address`: the IP address is wrong or the WD unit is off. Recheck with step 49.

*If you see* `mount error(95): Operation not supported`: the NAS wants a
different SMB version. Try `vers=2.1` instead of `vers=3.0`, then `vers=1.0` as
a last resort — and if only `vers=1.0` works, check for a firmware update,
because SMB1 is genuinely insecure.

**61.** Confirm the mount is live:

```
findmnt /mnt/wd
```

*Expected output:*

```
TARGET  SOURCE               FSTYPE OPTIONS
/mnt/wd //192.168.1.50/Public cifs  rw,relatime,vers=3.0,cache=strict,username=...
```

*If it prints nothing*, the mount is not there — go back to step 60.

**62.** Confirm the `goldencloud` account can actually write to it:

```
sudo -u goldencloud touch /mnt/wd/.goldencloud-write-test
```

*What it does:* creates an empty file **as the goldencloud account**, which is
the account the server will use. Testing as yourself would prove nothing.

*Expected output:* nothing.

*If you see* `touch: cannot touch ... Permission denied`: the `uid=`/`gid=`
options did not take, or the share is read-only on the WD side. Unmount with
`sudo umount /mnt/wd` and repeat step 60, checking the options carefully.

**63.** Clean up the test file:

```
sudo rm /mnt/wd/.goldencloud-write-test
```

**64.** Unmount, so you can prove the permanent configuration works from
scratch:

```
sudo umount /mnt/wd
```

*Expected output:* nothing.

*If you see* `target is busy`: something is using the folder. Make sure your
own shell is not sitting inside it — run `cd ~` and try again.

**65.** Take a backup of the file you are about to edit. `/etc/fstab` controls
what gets mounted at boot, and a mistake in it can stop the machine booting:

```
sudo cp /etc/fstab /etc/fstab.backup
```

*Expected output:* nothing.

**66.** Open it:

```
sudo nano /etc/fstab
```

You will see a few existing lines. **Do not change them.** Use the **down
arrow** to get to the very bottom of the file, past the last line.

**67.** Add these two lines at the bottom, substituting worksheet rows F and G
into the first field. **The whole mount entry must be on one line** — if nano
wraps it visually on screen that is fine, as long as you do not press Enter in
the middle of it.

```
# GoldenCloud: WD My Cloud Home Local Access share
//192.168.1.50/Public  /mnt/wd  cifs  credentials=/etc/goldencloud/wd.credentials,uid=goldencloud,gid=goldencloud,file_mode=0660,dir_mode=0770,iocharset=utf8,vers=3.0,noserverino,nobrl,_netdev,nofail,x-systemd.automount,x-systemd.mount-timeout=30,x-systemd.idle-timeout=600  0  0
```

Three options in there are new, and they are the difference between a machine
that always comes back after a power cut and one that does not:

| Option | Why it is there |
| --- | --- |
| `_netdev` | "This is a network device." Tells the system not to even attempt the mount until networking is up. Without it, boot tries to mount before there is a network, fails, and gives up. |
| `nofail` | "If this cannot be mounted, carry on booting anyway." Without it, a WD unit that is switched off leaves the Pi sitting at an emergency prompt that you can only reach with a monitor and keyboard — the single most common way a headless Linux box becomes unreachable. |
| `x-systemd.automount` | "Do not mount it at boot; mount it the first time something touches `/mnt/wd`." This is what guarantees boot never waits on the NAS. The GoldenCloud service explicitly asks for the real mount before it starts, so the server still never runs without storage. |
| `x-systemd.mount-timeout=30` | Give up after 30 seconds rather than hanging indefinitely on an unresponsive NAS. |
| `x-systemd.idle-timeout=600` | Unmount after ten minutes of no activity, so a NAS reboot does not leave a permanently stale mount behind. It remounts automatically on next use. |

Save with **Ctrl+O**, **Enter**, exit with **Ctrl+X**.

**68.** Ask systemd to re-read `/etc/fstab` and build the mount units from it:

```
sudo systemctl daemon-reload
```

*Expected output:* nothing.

**69.** Check that systemd understood the line you wrote:

```
systemctl status mnt-wd.automount
```

*Expected output:*

```
● mnt-wd.automount - Automount /mnt/wd
     Loaded: loaded (/etc/fstab; generated)
     Active: active (waiting) since Mon 2026-08-03 10:31:02 UTC; 4s ago
      Where: /mnt/wd
```

`active (waiting)` is exactly right — it means "armed, waiting for someone to
touch the folder".

*If you see* `Unit mnt-wd.automount could not be found`: systemd could not parse
your fstab line. The usual causes are a line break in the middle of the options,
or a space inside the options list. Run `sudo nano /etc/fstab` and compare
character by character against step 67. If you need to start over:
`sudo cp /etc/fstab.backup /etc/fstab`.

**70.** Trigger the mount by looking inside the folder:

```
ls -la /mnt/wd
```

*Expected output:* the contents of your WD share, with everything owned by
`goldencloud`:

```
total 4
drwxrwx--- 2 goldencloud goldencloud    0 Aug  3 09:14 .
drwxr-xr-x 3 root        root        4096 Aug  3 10:28 ..
drwxrwx--- 2 goldencloud goldencloud    0 Aug  3 09:14 Documents
```

The first `ls` may pause for a second while the automount fires. That is normal.

**71.** Confirm it is genuinely mounted and not just an empty folder:

```
findmnt /mnt/wd && df -h /mnt/wd
```

*Expected output:*

```
TARGET  SOURCE               FSTYPE OPTIONS
/mnt/wd //192.168.1.50/Public cifs  rw,relatime,vers=3.0,...
Filesystem            Size  Used Avail Use% Mounted on
//192.168.1.50/Public 3.7T  574G  3.1T  16% /mnt/wd
```

**If `df` shows the size of the SD card (around 30G) instead of the size of your
WD unit, it is NOT mounted** — you are looking at an empty folder on the SD
card. Go back to step 69.

**72.** Now the real test: reboot and check it comes back by itself.

```
sudo reboot
```

*Expected output:* the connection drops.

**73.** Wait 60 seconds, reconnect, and check:

```
ssh gcadmin@goldencloud.local
```

**74.** Verify the storage returned without you doing anything:

```
ls /mnt/wd > /dev/null && findmnt /mnt/wd && df -h /mnt/wd | tail -1
```

*Expected output:* the same `findmnt` line and the same large filesystem size as
step 71.

> ### ✅ How to know it worked
>
> The Pi rebooted on its own, came back, and `/mnt/wd` shows your WD unit's
> real capacity — without you typing a mount command. The credentials file is
> `-rw-------`, and `sudo -u goldencloud touch /mnt/wd/.test` succeeds. (Delete
> that test file afterwards.)

---

## 8. Install the GoldenCloud binary

> ### The shortcut
>
> Sections 8, 9, 10 and 11 can be done in one command. If you would rather not
> type them out:
>
> ```
> cd ~
> curl -fLO https://raw.githubusercontent.com/PV80/GoldenCloud/main/deploy/install.sh
> chmod +x install.sh
> sudo ./install.sh --dry-run
> ```
>
> Read what the dry run says it will do, then run it for real without
> `--dry-run`. When it finishes, skip to section 12.
>
> Note that `install.sh` also needs `goldencloud.service` and
> `goldencloud.example.yaml` in the same folder, so the tidiest way is to clone
> the whole repository: `git clone https://github.com/PV80/GoldenCloud.git &&
> cd GoldenCloud/deploy && sudo ./install.sh`.
>
> **The manual steps below are the real explanation.** Read them even if you use
> the script, because they are what you will need when something breaks.

**75.** Go to a scratch directory:

```
cd /tmp
```

*Expected output:* nothing.

**76.** Work out which build you need:

```
uname -m
```

*Expected output:* `aarch64` on a Raspberry Pi 5, `x86_64` on a mini-PC.

- `aarch64` → you want **`goldencloud-server-linux-arm64`**
- `x86_64` → you want **`goldencloud-server-linux-amd64`**

The commands below use `arm64`. If you are on a mini-PC, change every `arm64`
to `amd64`.

**77.** Download the server binary:

```
curl -fLO https://github.com/PV80/GoldenCloud/releases/latest/download/goldencloud-server-linux-arm64
```

*What it does:* downloads the latest released server program. `-f` makes it fail
loudly on an error instead of silently saving an error page as if it were a
program; `-L` follows redirects; `-O` keeps the original filename.

*Expected output:* a progress bar, then back to the prompt:

```
  % Total    % Received % Xferd  Average Speed   Time    Time     Time  Current
                                 Dload  Upload   Total   Spent    Left  Speed
100 8452k  100 8452k    0     0  4231k      0  0:00:02  0:00:02 --:--:-- 6102k
```

*If you see* `curl: (22) The requested URL returned error: 404`: there is no
published release yet, or the architecture name is wrong. Check
<https://github.com/PV80/GoldenCloud/releases> in a browser.

**78.** Download its checksum:

```
curl -fLO https://github.com/PV80/GoldenCloud/releases/latest/download/goldencloud-server-linux-arm64.sha256
```

*Expected output:* another, much faster progress bar.

**79.** Verify that what you downloaded is exactly what was published:

```
sha256sum -c goldencloud-server-linux-arm64.sha256
```

*What it does:* recomputes a fingerprint of the downloaded file and compares it
to the one published alongside it. This catches a corrupted download, and it
catches a tampered one.

*Expected output:*

```
goldencloud-server-linux-arm64: OK
```

**If you see `FAILED` — stop.** Do not install it. Delete both files
(`rm goldencloud-server-linux-arm64*`) and download again. If it fails a second
time, something is wrong that you should not work around: raise an issue rather
than running the binary.

**80.** Install it into the system:

```
sudo install -o root -g root -m 0755 goldencloud-server-linux-arm64 /usr/local/bin/goldencloud
```

*What it does:* copies the file to `/usr/local/bin/goldencloud`, sets its owner
to `root`, and makes it executable (`0755` = owner can read/write/execute,
everyone else can read and execute). `/usr/local/bin` is on the system's search
path, so from now on you can just type `goldencloud` from anywhere.

Using `install` rather than `cp` matters on upgrades: it writes to a temporary
name and renames, so a running server never reads a half-written file.

*Expected output:* nothing.

**81.** Check it runs:

```
goldencloud version
```

*Expected output* (the exact version string depends on the release):

```
goldencloud v0.1.0
```

*If you see* `bash: /usr/local/bin/goldencloud: cannot execute binary file: Exec
format error`: you downloaded the wrong architecture. Go back to step 76.

*If you see* `command not found`: the copy in step 80 did not happen. Re-run it
and check for an error.

**82.** Tidy up:

```
rm -f /tmp/goldencloud-server-linux-arm64 /tmp/goldencloud-server-linux-arm64.sha256
cd ~
```

*Expected output:* nothing.

> ### ✅ How to know it worked
>
> `goldencloud version` prints a version number from any directory, and
> `ls -l /usr/local/bin/goldencloud` shows `-rwxr-xr-x 1 root root`.

---

## 9. Create the configuration

**83.** Create the configuration file:

```
sudo nano /etc/goldencloud/config.yaml
```

An empty editor opens. Type (or paste) this in:

```yaml
# GoldenCloud server configuration.
# Full documentation of every option: deploy/goldencloud.example.yaml

listen: "127.0.0.1:8080"

storage_root: "/mnt/wd/goldencloud"

users_file: "/etc/goldencloud/users.yaml"

log_level: "info"

tls:
  enabled: false
  cert_file: ""
  key_file: ""

trusted_proxy:
  enabled: false
  header: "X-Forwarded-For"
  allowed_cidrs: []
```

> **Pasting into nano over SSH:** right-click in the Terminal window pastes.
> Check afterwards that the indentation survived — YAML cares. Every indented
> line must start with **spaces, never a tab character.** If in doubt, type it
> by hand; it is only twenty lines.

The four lines that matter:

- **`listen: "127.0.0.1:8080"`** — the server accepts connections only from the
  Pi itself. Not from the internet, not from your office network. `cloudflared`
  will be the only thing that ever connects to it. Leave this exactly as it is.
- **`storage_root: "/mnt/wd/goldencloud"`** — where staff folders live. A
  sub-folder of the share rather than the share root, so GoldenCloud's data
  cannot collide with anything already on the WD unit.
- **`users_file`** — the user database. Section 12 fills it in.
- **`log_level: "info"`** — startup, shutdown and reload messages, plus
  warnings such as failed sign-ins. It does **not** write a line per request;
  that is `debug`, which is far noisier and wears the SD card out faster. Turn
  `debug` on only while chasing a specific problem.

Save with **Ctrl+O**, **Enter**, exit with **Ctrl+X**.

**84.** Set ownership and permissions:

```
sudo chown root:goldencloud /etc/goldencloud/config.yaml
sudo chmod 0640 /etc/goldencloud/config.yaml
```

*What it does:* `root` owns it, the `goldencloud` group can read it, nobody else
can see it at all. The server reads it; only you can change it.

*Expected output:* nothing.

**85.** Create an empty user database:

```
echo 'users: []' | sudo tee /etc/goldencloud/users.yaml > /dev/null
```

*What it does:* writes a valid but empty user list, so the server has something
to read on first start.

*Expected output:* nothing (the `> /dev/null` suppresses the echo).

**86.** Lock it down too:

```
sudo chown root:goldencloud /etc/goldencloud/users.yaml
sudo chmod 0640 /etc/goldencloud/users.yaml
```

*Expected output:* nothing.

**87.** Check your work:

```
sudo ls -l /etc/goldencloud/
```

*Expected output:*

```
total 12
-rw-r----- 1 root goldencloud  312 Aug  3 10:52 config.yaml
-rw-r----- 1 root goldencloud   11 Aug  3 10:53 users.yaml
-rw------- 1 root root          58 Aug  3 10:22 wd.credentials
```

Three files, with exactly those permission strings. In particular
`wd.credentials` must be `-rw-------`, tighter than the other two, because it
holds a real password.

**88.** Check the YAML parses. The quickest test is to ask the server to start
in the foreground and watch what it says:

```
sudo -u goldencloud /usr/local/bin/goldencloud serve --config /etc/goldencloud/config.yaml
```

*Expected output:* something like

```
time=2026-08-03T10:57:03.114Z level=INFO msg="goldencloud starting" version=v0.1.0
time=2026-08-03T10:57:03.114Z level=INFO msg="storage root ok" path=/mnt/wd/goldencloud
time=2026-08-03T10:57:03.114Z level=INFO msg="loaded users" count=0
time=2026-08-03T10:57:03.115Z level=INFO msg=listening addr=127.0.0.1:8080 tls=false trusted_proxy=false
```

Four lines, each starting with a timestamp and an upper-case level. The last
one repeats back the two safety settings — `tls=false trusted_proxy=false` is
what you want here, because Cloudflare does the HTTPS.

Then it sits there. That is correct — it is running. Press **Ctrl+C** to stop
it and get your prompt back.

*If you see a YAML error* naming a line number: go back to step 83 and check
that line, usually for a tab character or a missing quote.

*If you see* `goldencloud: preflight check failed: storage_root
/mnt/wd/goldencloud does not exist; create it, or check that the share is
mounted`: expected at this point. The next section creates it. Press Ctrl+C and
carry on.

> ### ✅ How to know it worked
>
> `sudo ls -l /etc/goldencloud/` shows the three files with the permissions
> above, and step 88 either started cleanly or complained specifically about
> the storage root and nothing else.

---

## 10. Ownership and the storage root

**89.** Create the folder that will hold every staff member's folder:

```
sudo mkdir -p /mnt/wd/goldencloud
```

*Expected output:* nothing.

*If you see* `No such file or directory`: `/mnt/wd` is not mounted. Go back to
step 70.

**90.** Give it to the service account:

```
sudo chown goldencloud:goldencloud /mnt/wd/goldencloud
sudo chmod 0750 /mnt/wd/goldencloud
```

*What it does:* the `goldencloud` account owns this folder and can create staff
folders inside it. `0750` means nobody outside that account and group can even
list it.

*Expected output:* nothing.

> **A note on SMB and ownership.** On a CIFS mount, `chown` and `chmod` often
> appear to do nothing, because the NAS does not store Linux ownership. That is
> fine — the `uid=goldencloud,gid=goldencloud,dir_mode=0770` options in your
> fstab line already force everything on the share to *appear* owned by the
> right account, which is what actually matters. If these two commands print
> `Operation not supported`, ignore it and move on; step 91 is the real test.

**91.** Prove the service account can write there:

```
sudo -u goldencloud touch /mnt/wd/goldencloud/.write-test && \
sudo -u goldencloud rm /mnt/wd/goldencloud/.write-test && \
echo "WRITE TEST PASSED"
```

*Expected output:*

```
WRITE TEST PASSED
```

*If you see* `Permission denied` *instead*: the mount options are wrong. Check
that `uid=goldencloud,gid=goldencloud` is in your fstab line (step 67), then
`sudo umount /mnt/wd && sudo systemctl daemon-reload && ls /mnt/wd` to remount
with the corrected options.

**92.** Confirm the service account genuinely cannot log in:

```
sudo -u goldencloud whoami
```

*Expected output:*

```
goldencloud
```

And confirm there is no way in from outside:

```
sudo passwd -S goldencloud
```

*Expected output:*

```
goldencloud L 08/03/2026 0 99999 7 -1
```

The **`L`** is the important character. It means "locked" — the account has no
usable password and nobody can log in as it.

**93.** Check the whole picture:

```
ls -ld /mnt/wd /mnt/wd/goldencloud
```

*Expected output:*

```
drwxrwx--- 2 goldencloud goldencloud    0 Aug  3 09:14 /mnt/wd
drwxr-x--- 2 goldencloud goldencloud    0 Aug  3 11:04 /mnt/wd/goldencloud
```

> ### ✅ How to know it worked
>
> Step 91 printed `WRITE TEST PASSED` and step 92 showed `L` for locked.

---

## 11. Install and start the service

Right now the server only runs while you are watching it. This section makes it
start automatically at boot and restart itself if it crashes.

**94.** Fetch the systemd unit file:

```
sudo curl -fL -o /etc/systemd/system/goldencloud.service \
  https://raw.githubusercontent.com/PV80/GoldenCloud/main/deploy/goldencloud.service
```

*What it does:* downloads the service definition — which account to run as, what
command to run, what to depend on, and a long list of restrictions on what the
process is allowed to do.

*Expected output:* a progress bar.

*If you have the repository cloned locally instead*, use:
`sudo install -o root -g root -m 0644 ~/GoldenCloud/deploy/goldencloud.service /etc/systemd/system/goldencloud.service`

**95.** Set its permissions:

```
sudo chown root:root /etc/systemd/system/goldencloud.service
sudo chmod 0644 /etc/systemd/system/goldencloud.service
```

*Expected output:* nothing.

**96.** Read it, so you know what you just installed:

```
less /etc/systemd/system/goldencloud.service
```

Scroll with the arrow keys or **Space**, quit with **q**. Every hardening line
is commented. The three worth understanding:

- `Requires=mnt-wd.mount` — **the server cannot start if the WD share is not
  mounted.** Without this, a NAS that was switched off at boot would mean the
  server quietly creating empty folders on the SD card, staff seeing an empty
  drive, saving work into it, and losing that work when the share came back.
  Failing loudly is much better than that.
- `ProtectSystem=strict` and `ReadWritePaths=/mnt/wd` — the only place on the
  entire machine the server can write is the storage. Not `/usr`, not `/etc`,
  not even its own configuration file.
- `NoNewPrivileges=yes` — the process can never gain more power than it starts
  with, which closes the usual route from "bug in a web server" to "root on your
  NAS".

**97.** Tell systemd about the new unit:

```
sudo systemctl daemon-reload
```

*Expected output:* nothing.

**98.** Start it, and set it to start at every boot:

```
sudo systemctl enable --now goldencloud
```

*What it does:* `enable` means "start at boot"; `--now` means "and also start it
right this second".

*Expected output:*

```
Created symlink /etc/systemd/system/multi-user.target.wants/goldencloud.service → /etc/systemd/system/goldencloud.service.
```

**99.** Check it is running:

```
systemctl status goldencloud
```

*Expected output:*

```
● goldencloud.service - GoldenCloud private WebDAV drive
     Loaded: loaded (/etc/systemd/system/goldencloud.service; enabled; preset: enabled)
     Active: active (running) since Mon 2026-08-03 11:12:44 UTC; 6s ago
   Main PID: 1442 (goldencloud)
      Tasks: 7 (limit: 4915)
        CPU: 41ms
     CGroup: /system.slice/goldencloud.service
             └─1442 /usr/local/bin/goldencloud serve --config /etc/goldencloud/config.yaml

Aug 03 11:12:44 goldencloud goldencloud[1442]: time=2026-08-03T11:12:44.187Z level=INFO msg=listening addr=127.0.0.1:8080 tls=false trusted_proxy=false
```

(That line carries two timestamps: `journalctl` adds the one on the left, and
the server writes its own `time=` field. They agree.)

The two words you are looking for are **`enabled`** and **`active (running)`**.

Press **q** to get your prompt back if it does not return by itself.

*If you see* `Active: failed`: read the log with step 100 — it will say exactly
what is wrong. The most likely causes are a YAML mistake in `config.yaml` or
`/mnt/wd` not being mounted.

*If you see* `Active: activating (auto-restart)` *and it keeps cycling*: the
server is crashing on start and being restarted. Again, step 100.

**100.** Read the log:

```
sudo journalctl -u goldencloud -n 30 --no-pager
```

*What it does:* shows the last 30 lines the service has written. `journalctl` is
where all Linux service logs live; you will use this command more than any
other.

To watch it live as things happen, use `sudo journalctl -u goldencloud -f` and
press **Ctrl+C** to stop watching.

**101.** Prove the server is actually answering:

```
curl -sS -o /dev/null -w 'HTTP status: %{http_code}\n' http://127.0.0.1:8080/
```

*Expected output:*

```
HTTP status: 401
```

**401 is the correct, healthy answer.** It means "who are you?" — the server is
alive and demanding credentials, and there are no users yet. A `200` here would
be alarming.

*If you see* `HTTP status: 000` *or* `Connection refused`: the server is not
running. Go back to step 99.

**102.** Prove it is **not** reachable from anywhere else. Find the Pi's own IP
address and try to connect to it on that address instead of loopback:

```
PI_IP=$(hostname -I | awk '{print $1}'); echo "Pi IP is $PI_IP"; \
curl -sS --max-time 5 -o /dev/null -w 'HTTP status: %{http_code}\n' "http://$PI_IP:8080/"
```

*Expected output:*

```
Pi IP is 192.168.1.42
curl: (7) Failed to connect to 192.168.1.42 port 8080 after 0 ms: Connection refused
HTTP status: 000
```

**"Connection refused" is the success condition here.** It proves the server is
bound to loopback only and that nothing on your office network — not a laptop,
not a guest device, not a compromised printer — can reach it. The only door is
the tunnel you are about to build.

*If you get* `HTTP status: 401` *instead*: your `listen` address is not
loopback. Go back to step 83 and fix it before continuing.

> ### ✅ How to know it worked
>
> `systemctl status goldencloud` says `enabled` and `active (running)`,
> `curl` to `127.0.0.1:8080` returns **401**, and `curl` to the Pi's LAN address
> returns **connection refused**.

---

## 12. Create the first three users

**103.** Create your first user. Replace `alice` with a real username — lower
case, no spaces, no accents. First names or `firstname.lastname` both work well.

```
sudo goldencloud user add --config /etc/goldencloud/config.yaml alice
```

*What it does:* creates the user, generates their folder under the storage root,
and prompts you to set a password. The password is only ever typed at the
prompt — never on the command line, because command lines are visible to every
account on the machine.

*Expected output:*

```
New password for alice:
Repeat password:
Added user "alice".
  folder: /mnt/wd/goldencloud/alice
  quota:  unlimited
Reload the running server with: systemctl reload goldencloud
```

Nothing appears as you type the password. Type it twice.

The password must be **at least 8 characters** and at most 72. Step 104
generates a 20-character one, which is what you should actually use.

*If you see* `goldencloud: user add: the two passwords do not match`: nothing
was created. Run the command again.

*If you see* `goldencloud: user add: the password must be at least 8
characters`: nothing was created either. Use a longer one.

*If you see* `goldencloud: user add: user "alice" already exists; use
"goldencloud user passwd alice" to change their password`: pick a different
name, or run the `user passwd` command it suggests.

*If you see* `goldencloud: user add: username "Alice" must be lowercase`:
usernames are lower case only, and may use `a-z`, `0-9`, dot, dash and
underscore, up to 32 characters.

**104.** Choose their password properly. This is the password a staff member
types into the tray app, and it is the only thing between the internet and
their files.

- Generate a good one on the Pi with:
  ```
  head -c 18 /dev/urandom | base64 | tr -d '/+=' | head -c 20; echo
  ```
  *Expected output:* twenty random characters like `k7Rm2xQpL9vT4nB6wYzA`.
- Give it to the staff member over a channel that is not email — in person, or
  by phone. Not in the same message as the server address.
- Tell them they can change it later only by asking you. There is no
  self-service password reset in v1.

**105.** Create the second user:

```
sudo goldencloud user add --config /etc/goldencloud/config.yaml bob
```

**106.** Create the third:

```
sudo goldencloud user add --config /etc/goldencloud/config.yaml carol
```

**107.** List them all:

```
sudo goldencloud user list --config /etc/goldencloud/config.yaml
```

*Expected output:*

```
USERNAME  QUOTA      FOLDER
alice     unlimited  /mnt/wd/goldencloud/alice
bob       unlimited  /mnt/wd/goldencloud/bob
carol     unlimited  /mnt/wd/goldencloud/carol
```

`unlimited` in the quota column means no quota was set, which is the v1
behaviour — quotas are reported but never enforced. See
[`../ROADMAP.md`](../ROADMAP.md).

**108.** Check the folders exist on the WD unit:

```
sudo ls -la /mnt/wd/goldencloud/
```

*Expected output:*

```
total 0
drwxr-x--- 5 goldencloud goldencloud 0 Aug  3 11:31 .
drwxrwx--- 4 goldencloud goldencloud 0 Aug  3 11:04 ..
drwxrwx--- 2 goldencloud goldencloud 0 Aug  3 11:29 alice
drwxrwx--- 2 goldencloud goldencloud 0 Aug  3 11:30 bob
drwxrwx--- 2 goldencloud goldencloud 0 Aug  3 11:31 carol
```

**109.** Tell the running server to re-read the user file:

```
sudo systemctl reload goldencloud
```

*What it does:* sends the server a signal telling it to reload `users.yaml`
without dropping anyone's file transfer.

*Expected output:* nothing.

*If you see* `Job for goldencloud.service failed`: use
`sudo systemctl restart goldencloud` instead. A restart briefly interrupts
transfers but is otherwise harmless.

**110.** Test that a real user can actually sign in:

```
curl -sS -u alice -o /dev/null -w 'HTTP status: %{http_code}\n' http://127.0.0.1:8080/
```

Type Alice's password when prompted.

*Expected output:*

```
Enter host password for user 'alice':
HTTP status: 405
```

**405 is the success condition here.** It means "Method Not Allowed": the
server accepted the password, then declined to answer a plain browser-style
`GET` for a folder, because a folder is not a file. A WebDAV client asks with
`PROPFIND` instead and gets `207`. Any of `405`, `207` or `200` means the
sign-in **succeeded**. What matters is that it is not 401.

*If you see* `HTTP status: 401`: the password is wrong, or the reload in step 109
did not happen. Try `sudo systemctl restart goldencloud` and repeat.

*If you see* `HTTP status: 429`: you have typed the wrong password five times
in a row and the server is deliberately slowing you down. Wait a minute and try
again — see [`../docs/SECURITY.md`](../docs/SECURITY.md).

**111.** Test that a **wrong** password is rejected:

```
curl -sS -u alice:definitely-not-the-password -o /dev/null \
  -w 'HTTP status: %{http_code}\n' http://127.0.0.1:8080/
```

*Expected output:*

```
HTTP status: 401
```

**401 is the success condition here.** If this returns anything else, stop and
raise an issue — authentication is not working and you must not publish the
server. (`429` is not a failure: it means you have already made several wrong
guesses and the rate limiter has kicked in. Wait a minute and run it once
more.)

> ### ✅ How to know it worked
>
> `goldencloud user list` shows three users, `/mnt/wd/goldencloud/` contains
> three folders, a correct password gets in, and a wrong password does not.

---

## 13. Publish it to the internet with Cloudflare Tunnel

This is where the server becomes reachable from outside the office. Read the
first part of [section 0](#0-how-to-read-this-runbook) again if you want the
explanation of why this needs no router changes.

**112.** Add Cloudflare's software repository key:

```
sudo mkdir -p --mode=0755 /usr/share/keyrings
curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | \
  sudo tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
```

*What it does:* installs the cryptographic key that lets your Pi verify that
software claiming to be from Cloudflare really is.

*Expected output:* nothing.

**113.** Add the repository itself:

```
echo 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main' | \
  sudo tee /etc/apt/sources.list.d/cloudflared.list
```

*Expected output:* the line you just typed, echoed back.

**114.** Install `cloudflared`:

```
sudo apt update && sudo apt install -y cloudflared
```

*Expected output:* ends with something like
`Setting up cloudflared (2026.7.0) ...`

**115.** Check it works:

```
cloudflared --version
```

*Expected output:*

```
cloudflared version 2026.7.0 (built 2026-07-14-1103 UTC)
```

**116.** Authorise this machine against your Cloudflare account:

```
cloudflared tunnel login
```

*What it does:* asks Cloudflare to issue this Pi a certificate that lets it
create and manage tunnels on your account.

*Expected output:*

```
Please open the following URL and log in with your Cloudflare account:

https://dash.cloudflare.com/argotunnel?aud=&callback=https%3A%2F%2Flogin.cloudflareaccess.org%2F...

Leave cloudflared running to download the cert automatically.
```

**117.** The Pi has no browser, so **copy that whole URL** — select it with the
mouse in your Terminal window, which copies it automatically in Windows
Terminal — and paste it into the address bar of a browser on your Windows PC.

**118.** In the browser, sign in to Cloudflare if asked, then you will see a list
of the domains in your account. Click your domain (worksheet row J), then click
**Authorize**.

**119.** Go back to your Terminal. Within a few seconds it should print:

```
You have successfully logged in.
If you wish to copy your credentials to a server, they have been saved to:
/home/gcadmin/.cloudflared/cert.pem
```

*If nothing happens after two minutes:* press Ctrl+C, run step 116 again, and
make sure you clicked Authorize on the correct domain.

**120.** Lock down the certificate you just received:

```
chmod 0600 ~/.cloudflared/cert.pem
ls -l ~/.cloudflared/cert.pem
```

*Expected output:*

```
-rw------- 1 gcadmin gcadmin 2427 Aug  3 11:48 /home/gcadmin/.cloudflared/cert.pem
```

**121.** Create the tunnel:

```
cloudflared tunnel create goldencloud
```

*What it does:* registers a new tunnel with Cloudflare and writes its private
credentials to a local file.

*Expected output:*

```
Tunnel credentials written to /home/gcadmin/.cloudflared/6ff42ae2-765d-4adf-8112-31c55c1551ef.json.
cloudflared chose this file based on where your origin certificate was found.
Keep this file secret. To revoke these credentials, delete the tunnel.

Created tunnel goldencloud with id 6ff42ae2-765d-4adf-8112-31c55c1551ef
```

**Write that UUID into worksheet row L.** You need it twice in the next few
steps.

*If you see* `tunnel with name goldencloud already exists`: you have run this
before. Get the existing UUID with `cloudflared tunnel list` and use that.

**122.** Move the credentials somewhere the system service can read. The service
runs as `root`, not as you, so it cannot see your home directory:

```
sudo mkdir -p /etc/cloudflared
sudo cp ~/.cloudflared/*.json /etc/cloudflared/
sudo chown root:root /etc/cloudflared/*.json
sudo chmod 0600 /etc/cloudflared/*.json
```

*Expected output:* nothing.

**123.** Verify:

```
sudo ls -l /etc/cloudflared/
```

*Expected output:*

```
total 4
-rw------- 1 root root 178 Aug  3 11:52 6ff42ae2-765d-4adf-8112-31c55c1551ef.json
```

`-rw-------` and owned by `root`. **This file is the tunnel's private key.**
Anyone holding it can impersonate your tunnel.

**124.** Write the tunnel configuration:

```
sudo nano /etc/cloudflared/config.yml
```

Type this in, replacing the UUID with worksheet row L (in **both** places) and
the hostname with worksheet row K (in **both** places):

```yaml
tunnel: 6ff42ae2-765d-4adf-8112-31c55c1551ef
credentials-file: /etc/cloudflared/6ff42ae2-765d-4adf-8112-31c55c1551ef.json

loglevel: info
metrics: 127.0.0.1:20241
protocol: quic
retries: 5

ingress:
  - hostname: cloud.yourcompany.com
    service: http://127.0.0.1:8080
    originRequest:
      connectTimeout: 30s
      keepAliveConnections: 16
      keepAliveTimeout: 90s
      httpHostHeader: cloud.yourcompany.com

  - service: http_status:404
```

Save with **Ctrl+O**, **Enter**, exit with **Ctrl+X**.

The full annotated version of this file, explaining every line, is
[`cloudflared/config.yml.template`](cloudflared/config.yml.template).

The last rule — `service: http_status:404` — is required and must be last. It
is the catch-all: anything arriving at your tunnel that is not the hostname you
expect gets "nothing here" rather than being forwarded. Your tunnel is reachable
from the whole internet and will be probed by scanners; this is the rule that
makes those probes boring.

**125.** Set permissions:

```
sudo chown root:root /etc/cloudflared/config.yml
sudo chmod 0644 /etc/cloudflared/config.yml
```

**126.** Check the configuration parses before you rely on it:

```
cloudflared tunnel ingress validate --config /etc/cloudflared/config.yml
```

*Expected output:*

```
Validating rules from /etc/cloudflared/config.yml
OK
```

*If you see* `parsing YAML in config file ... yaml: line N`: an indentation
mistake, almost always a tab character. Go back to step 124.

*If you see* `The last ingress rule must match all URLs`: you left off the
`http_status:404` rule.

**127.** Check a specific URL matches the rule you think it does (use row K):

```
cloudflared tunnel ingress rule --config /etc/cloudflared/config.yml https://cloud.yourcompany.com/
```

*Expected output:*

```
Using rules from /etc/cloudflared/config.yml
Matched rule #1
        hostname: cloud.yourcompany.com
        service: http://127.0.0.1:8080
```

**If it says `Matched rule #2` and shows `http_status:404`, your hostname has a
typo.** Fix it in step 124 before going further — this is by far the most common
mistake in this section, and it produces a confusing "404 from Cloudflare" later
that looks like a DNS problem.

**128.** Create the DNS record that points your hostname at this tunnel (use
row K):

```
cloudflared tunnel route dns goldencloud cloud.yourcompany.com
```

*What it does:* adds a DNS record in your Cloudflare account so the name
resolves to the tunnel. You do not have to touch the Cloudflare dashboard.

*Expected output:*

```
2026-08-03T11:58:12Z INF Added CNAME cloud.yourcompany.com which will route to this tunnel tunnelID=6ff42ae2-765d-4adf-8112-31c55c1551ef
```

*If you see* `Failed to add route: code: 1003, reason: An A, AAAA, or CNAME
record with that host already exists`: something already uses that name. Either
pick a different hostname, or delete the existing record in the Cloudflare
dashboard under **DNS → Records**, then run this again.

*If you see* `failed to parse quick Tunnel ID` *or an authentication error*:
your `cert.pem` is missing or unreadable. Repeat step 116.

**129.** Install `cloudflared` as a system service so it starts at boot:

```
sudo cloudflared service install
```

*What it does:* creates a systemd unit that runs the tunnel using
`/etc/cloudflared/config.yml`, and enables it.

*Expected output:*

```
2026-08-03T11:59:40Z INF Using Systemd
2026-08-03T11:59:40Z INF Linux service for cloudflared installed successfully
```

*If you see* `cloudflared service is already installed`: run
`sudo systemctl restart cloudflared` instead and carry on.

**130.** Start it and check:

```
sudo systemctl enable --now cloudflared
sudo systemctl status cloudflared
```

*Expected output:* `Loaded: loaded (...; enabled; ...)` and
`Active: active (running)`.

Press **q** to return to the prompt.

**131.** Confirm the tunnel actually connected:

```
sudo journalctl -u cloudflared -n 40 --no-pager | grep -i "registered tunnel connection"
```

*Expected output:* four lines, one per connection, to different Cloudflare
locations:

```
Aug 03 12:00:11 goldencloud cloudflared[2109]: INF Registered tunnel connection connIndex=0 connection=... location=lhr08 protocol=quic
Aug 03 12:00:11 goldencloud cloudflared[2109]: INF Registered tunnel connection connIndex=1 connection=... location=lhr01 protocol=quic
Aug 03 12:00:12 goldencloud cloudflared[2109]: INF Registered tunnel connection connIndex=2 connection=... location=cdg12 protocol=quic
Aug 03 12:00:12 goldencloud cloudflared[2109]: INF Registered tunnel connection connIndex=3 connection=... location=cdg04 protocol=quic
```

Four is what you want. One or two still works but is less resilient. **Zero
means the tunnel is not up** — read the full log with
`sudo journalctl -u cloudflared -n 60 --no-pager` and see
[section 16](#16-troubleshooting-by-symptom).

**132.** Test from the Pi, going out to the internet and back in through
Cloudflare (use row K):

```
curl -sS -o /dev/null -w 'HTTP status: %{http_code}\n' https://cloud.yourcompany.com/
```

*Expected output:*

```
HTTP status: 401
```

**401 is success.** The request left the Pi, went to Cloudflare, came back down
the tunnel, reached the server, and the server asked for a password. The entire
path works.

*If you see `530` or `1033`:* the tunnel is not connected. Step 131.

*If you see `502` or `1016`:* the tunnel is connected but cannot reach the
server. Check the port in `/etc/cloudflared/config.yml` matches `listen` in
`/etc/goldencloud/config.yaml`, and that `systemctl status goldencloud` is
`active (running)`.

*If you see `curl: (6) Could not resolve host`:* DNS has not propagated yet.
Wait five minutes and try again. If it persists, check the record exists in the
Cloudflare dashboard under **DNS → Records**.

**133.** Test a real sign-in over the public address:

```
curl -sS -u alice -o /dev/null -w 'HTTP status: %{http_code}\n' https://cloud.yourcompany.com/
```

Type Alice's password.

*Expected output:* `HTTP status: 405` (or `207`, or `200` — see step 110).

> ### ✅ How to know it worked
>
> `sudo systemctl status cloudflared` says `active (running)`, four tunnel
> connections are registered, and `curl https://cloud.yourcompany.com/` returns
> **401** without credentials and **200** with them. **You have not touched your
> router.**

---

## 14. Prove it works from outside the office

Everything so far was tested from inside your own network. Now prove it works
from somewhere you do not control.

**134.** Take out your phone. **Turn Wi-Fi off** so it is on mobile data only.
This matters — on office Wi-Fi you would be testing a shortcut, not the real
path.

**135.** Open a browser on the phone and go to `https://cloud.yourcompany.com`
(worksheet row K).

*Expected:* a padlock in the address bar, and a **username and password prompt**.

The padlock proves the connection is encrypted end to end. The prompt proves the
server is alive and demanding credentials.

*If you see a Cloudflare error page numbered 1033 or "Argo Tunnel error":* the
tunnel is down. Back to step 131.

*If you see "This site can't be reached":* DNS. Wait a few minutes, or check the
record in the Cloudflare dashboard.

**136.** Enter `alice` and her password.

*Expected:* either a plain directory listing, or a short technical message such
as "Method Not Allowed". **Either is a pass.** A web browser is not a WebDAV
client, so what it does after signing in varies. The thing being tested here is
that the password was accepted — you were not asked again and you did not get an
error page saying unauthorised.

*If you are asked for the password again and again:* the password is wrong, or
the reload in step 109 did not take. Back on the Pi:
`sudo systemctl restart goldencloud`.

**137.** Now the real test. On an iPhone or iPad, open the **Files** app:

- Tap **Browse**, then the **···** menu at the top right.
- Tap **Connect to Server**.
- Enter `https://cloud.yourcompany.com` and tap **Connect**.
- Choose **Registered User**, enter `alice` and her password, tap **Next**.

*Expected:* Alice's folder appears under **Shared** in the Files app, and you can
browse it. It will be empty — she has not put anything in it yet.

Full instructions for Mac and iPhone, including screenshots-in-words, are in
[`../docs/OTHER-PLATFORMS.md`](../docs/OTHER-PLATFORMS.md).

**138.** Upload something. In the Files app, copy a photo into Alice's folder.

**139.** Back on the Pi, confirm it landed on the WD unit and not somewhere
else:

```
sudo ls -la /mnt/wd/goldencloud/alice/
```

*Expected output:* your photo, with a real size:

```
total 2451
drwxrwx--- 2 goldencloud goldencloud       0 Aug  3 12:14 .
drwxr-x--- 5 goldencloud goldencloud       0 Aug  3 11:31 ..
-rwxrwx--- 1 goldencloud goldencloud 2508721 Aug  3 12:14 IMG_4821.HEIC
```

**This is the moment the whole system is proven.** A file went from a phone on
mobile data, through Cloudflare, down a tunnel into your office, through the
server, onto the WD unit — and your router was never configured.

**140.** Confirm the jail works. This is the most important security check in
the runbook. Connect the Files app as `bob` instead of `alice` (Connect to
Server again, same address, different credentials).

*Expected:* Bob sees an **empty folder**. He does not see Alice's photo. He does
not see a folder called `alice`. He has no way to navigate upwards to find one.

*If Bob can see Alice's files, stop immediately*, take the tunnel down with
`sudo systemctl stop cloudflared`, and raise an issue. Per-user isolation is the
single most security-critical property of the system — see
[`../DECISIONS.md`](../DECISIONS.md), D-002.

> ### ✅ How to know it worked
>
> A file uploaded from a phone on mobile data appeared on the WD unit inside
> Alice's folder, and Bob cannot see it.

---

## 15. Hand it to staff

**141.** Build or obtain the Windows installer, `GoldenCloudSetup.exe`. It comes
from the GitHub Release, and it needs your hostname compiled into it so staff
never type a server address:

- In the GitHub repository, go to **Settings → Secrets and variables → Actions
  → Variables → New repository variable**.
- Name: `GOLDENCLOUD_SERVER_URL`. Value: `https://cloud.yourcompany.com`
  (worksheet row K, with the `https://`).
- Re-run the Release workflow, or push a new tag.

This is human gate 2 in [`../PROGRESS.md`](../PROGRESS.md). Until it is set, the
installer still builds but the app shows an "unconfigured build" banner and
refuses to save credentials — deliberately, so an unconfigured installer can
never be handed to staff by mistake.

**142.** Before giving it to anyone, run it through the test checklist on one
machine: [`../docs/CLIENT-TEST.md`](../docs/CLIENT-TEST.md). It takes about
thirty minutes and it is the difference between a smooth rollout and three days
of support calls.

**143.** Give each staff member three things:

1. The installer, `GoldenCloudSetup.exe`.
2. Their username.
3. Their password — **by a different route from everything else**. In person or
   by phone. Never in the same email as the installer.

**144.** Give them the one-page guide:
[`../docs/STAFF-GUIDE.md`](../docs/STAFF-GUIDE.md). Print it, or paste it into
an email. It covers installing, signing in, where the drive is, and what to do
if it disappears.

> ### ✅ How to know it worked
>
> One staff member has `G:` in File Explorer, can save a file into it, and that
> file appears under their name in `/mnt/wd/goldencloud/` on the Pi.

---

## 16. Troubleshooting by symptom

Steps stop being numbered here, because you only do these if something is
wrong. Find your symptom, work down the checks in order.

### The one command to run first

```
systemctl status goldencloud cloudflared --no-pager ; \
findmnt /mnt/wd ; \
curl -sS -o /dev/null -w 'local: %{http_code}\n' http://127.0.0.1:8080/
```

This answers, in one go: is the server up, is the tunnel up, is the storage
mounted, and is the server answering. Almost every problem below is diagnosed
by which of those four is wrong.

---

### "Staff see an error page instead of a sign-in prompt"

| What they see | What it means | Fix |
| --- | --- | --- |
| Cloudflare **error 1033** / "Argo Tunnel error" | The tunnel is not connected to Cloudflare. | `sudo systemctl restart cloudflared`, then check step 131. |
| Cloudflare **error 502 Bad Gateway** | The tunnel is up but the server behind it is not answering. | `sudo systemctl status goldencloud`. Check the port in `/etc/cloudflared/config.yml` matches `listen:` in `/etc/goldencloud/config.yaml`. |
| Cloudflare **error 1016 / Origin DNS error** | The `service:` line in the tunnel config is malformed. | Re-read `/etc/cloudflared/config.yml` against step 124. It must be `http://127.0.0.1:8080`, with the `http://`. |
| A bare **404** | The request matched the catch-all rule, not your hostname. Usually a hostname typo. | `cloudflared tunnel ingress rule --config /etc/cloudflared/config.yml https://cloud.yourcompany.com/` — see step 127. |
| **"This site can't be reached" / DNS error** | The DNS record is missing. | `cloudflared tunnel route dns goldencloud cloud.yourcompany.com`, and check **DNS → Records** in the Cloudflare dashboard. |
| A **certificate warning** | You are not going through Cloudflare — check the address is spelled right and starts with `https://`. | |

---

### "The server will not start"

Read the reason first:

```
sudo systemctl status goldencloud --no-pager -l
sudo journalctl -u goldencloud -n 50 --no-pager
```

| Log says | Cause | Fix |
| --- | --- | --- |
| `Dependency failed for goldencloud.service` / `mnt-wd.mount` failed | The WD share is not mounted, and the unit correctly refuses to run without it. | See "the drive is empty" below. This is the guard doing its job. |
| `config.yaml: yaml: line N: ...` or `config.yaml: line N: <field>: ...` | A syntax error, an unknown key, or a bad value in `config.yaml`. The message names the line and the field. | `sudo nano /etc/goldencloud/config.yaml`, go to line N. Almost always a tab character, a missing quote, or a misspelled key — unknown keys are rejected, not ignored. |
| `preflight check failed: storage_root /mnt/wd/goldencloud does not exist; create it, or check that the share is mounted` | `storage_root` points somewhere that is not there. | `sudo mkdir -p /mnt/wd/goldencloud && sudo chown goldencloud:goldencloud /mnt/wd/goldencloud` |
| `users file: open /etc/goldencloud/users.yaml: no such file or directory` | The user database is missing. The server will not start without it, even to serve nobody. | `echo 'users: []' \| sudo tee /etc/goldencloud/users.yaml` then redo step 86. |
| `config: open ...: permission denied` or `users file: open ...: permission denied` | Ownership is wrong. | `sudo chown root:goldencloud /etc/goldencloud/*.yaml && sudo chmod 0640 /etc/goldencloud/*.yaml` |
| `cannot listen on 127.0.0.1:8080: listen tcp 127.0.0.1:8080: bind: address already in use` | Something else has port 8080. | `sudo ss -tlnp \| grep 8080` to see what. Stop it, or change the port in **both** config files. |
| `refusing to start: "0.0.0.0:8080" is not a loopback address and neither tls.enabled nor trusted_proxy is set` | `listen` was changed to a non-loopback address. | This is a safety refusal, not a bug — see [`../DECISIONS.md`](../DECISIONS.md), D-003. Put it back to `127.0.0.1:8080`. |
| Nothing at all in the log | The unit file is not installed or not loaded. | `sudo systemctl daemon-reload && sudo systemctl restart goldencloud` |

If the service is stuck in a restart loop and systemd has given up
(`start request repeated too quickly`):

```
sudo systemctl reset-failed goldencloud
sudo systemctl start goldencloud
```

---

### "The drive is empty, or files have vanished"

**Check the storage is actually mounted. This is the answer nine times out of
ten.**

```
findmnt /mnt/wd
df -h /mnt/wd
```

If `findmnt` prints nothing, or `df` shows about 30 GB (the SD card) rather
than the size of your WD unit, the share is not mounted and you have been
looking at an empty folder on the Pi.

```
sudo systemctl stop goldencloud
ls /mnt/wd                       # triggers the automount
findmnt /mnt/wd                  # confirm it came back
sudo systemctl start goldencloud
```

If it will not mount:

| Check | Command | What you want |
| --- | --- | --- |
| Is the WD unit powered and on the network? | `ping -c 3 192.168.1.50` | Replies, no packet loss. |
| Has its IP address changed? | Router's device list | Matches worksheet row F. If it changed, edit `/etc/fstab` and `sudo systemctl daemon-reload`. |
| Is Local Access still on? | `smbclient -L //192.168.1.50 -U 'YOURUSER'` | The share list from step 50. |
| Is the password still right? | `sudo cat /etc/goldencloud/wd.credentials` | Matches rows H and I. |
| What is the kernel complaining about? | `sudo dmesg \| grep -i cifs \| tail -20` | Names the specific error. |

**If files "vanished" while the server was running:** the share was unmounted
underneath it — usually a NAS reboot. Remount, restart the server, and the
files are all still there. Nothing was deleted; the server was looking at an
empty folder.

**Prevent it recurring:** reserve the WD unit's IP address in your router
(step 48), and put both the Pi and the WD unit on a UPS if power is unreliable.

---

### "A staff member cannot sign in"

| Check | How |
| --- | --- |
| Does the user exist? | `sudo goldencloud user list --config /etc/goldencloud/config.yaml` |
| Is the username spelled right? | Usernames are case-sensitive. `Alice` is not `alice`. |
| Does the password work at all? | `curl -sS -u alice -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/` on the Pi. 405/207/200 = fine, 401 = wrong password, 429 = too many recent wrong ones. |
| Have they been locked out by repeated failures? | Five wrong passwords in a row from one address start a backoff and the answer becomes `429`. Wait a minute; a single correct sign-in clears it. |
| Did the server reload after you added them? | `sudo systemctl reload goldencloud` |
| Is it just this one person? | If everybody is locked out, it is the server. If it is one person, it is their password. |
| What does the log say? | `sudo journalctl -u goldencloud -f`, then have them try again and watch for `msg="authentication failed"` or `msg="authentication rate limited"`. |

To reset a password, see [section 17](#17-maintenance), M-3.

---

### "It is slow"

| Check | What good looks like |
| --- | --- |
| Is the Pi on Wi-Fi? | Move it to ethernet. This is usually the whole answer. |
| Is the WD unit on Wi-Fi or a 100 Mbit port? | Gigabit ethernet, both devices. |
| Is your office upload speed the limit? | Test at <https://speedtest.net> on a machine in the office. Downloads from the drive are limited by your **upload** speed, which on many connections is a tenth of the download speed. |
| Is the Pi thermally throttling? | `vcgencmd measure_temp` — under 70°C is fine, over 80°C means it is throttling. Fit a fan. |
| Is the SD card dying? | `dmesg \| grep -i "mmc\|i/o error"` — any I/O errors mean replace the card. |
| Many small files? | WebDAV is chatty. Thousands of small files will always be slower than one large one. Zip them. |

---

### "It worked yesterday and not today"

Run the one command at the top of this section. Then, in order:

```
uptime                                        # did it reboot?
sudo journalctl -u goldencloud --since "24 hours ago" --no-pager | tail -50
sudo journalctl -u cloudflared --since "24 hours ago" --no-pager | tail -50
df -h                                         # is the SD card or the NAS full?
```

A full disk is a surprisingly common cause and produces very confusing symptoms.
If `/` is above 90%, clear the journal:

```
sudo journalctl --vacuum-size=100M
```

---

### "I have locked myself out of the Pi"

If SSH no longer works but the Pi is powered on:

1. Try the IP address instead of the name: `ssh gcadmin@192.168.1.42`.
2. Check the Pi is on the network from your router's device list.
3. If it is not there, power-cycle it: unplug for ten seconds, plug back in,
   wait two minutes.
4. If it still does not appear, plug in a monitor (micro-HDMI) and a USB
   keyboard and log in at the console with the same username and password.
5. As a last resort, put the SD card in your Windows PC. The small `bootfs`
   partition is readable, and you can re-run the Imager's customisation by
   re-flashing — but that loses everything on the Pi. **Your staff files are on
   the WD unit, not the Pi**, so re-flashing costs you this runbook's setup
   time and nothing else. That is by design.

---

### "I need to take it offline right now"

```
sudo systemctl stop cloudflared
```

That is it. The tunnel closes, the hostname stops resolving to anything useful,
and nobody outside the office can reach the server. Files are untouched. Bring
it back with `sudo systemctl start cloudflared`.

To revoke one individual instead, see [section 17](#17-maintenance), M-4.

---

## 17. Maintenance

Numbering continues from section 15. These are the things you do occasionally,
not in order.

### M-1 · Update the server binary

**145.** Check what you are running now:

```
goldencloud version
```

**146.** Download the new one (adjust `arm64`/`amd64` as in step 76):

```
cd /tmp
curl -fLO https://github.com/PV80/GoldenCloud/releases/latest/download/goldencloud-server-linux-arm64
curl -fLO https://github.com/PV80/GoldenCloud/releases/latest/download/goldencloud-server-linux-arm64.sha256
```

**147.** Verify it:

```
sha256sum -c goldencloud-server-linux-arm64.sha256
```

*Expected output:* `goldencloud-server-linux-arm64: OK`. **If it says `FAILED`,
stop.**

**148.** Keep the old one, so you can go back:

```
sudo cp /usr/local/bin/goldencloud /usr/local/bin/goldencloud.previous
```

**149.** Install the new one and restart:

```
sudo install -o root -g root -m 0755 goldencloud-server-linux-arm64 /usr/local/bin/goldencloud
sudo systemctl restart goldencloud
```

**150.** Confirm:

```
goldencloud version && systemctl status goldencloud --no-pager | head -4
```

*Expected output:* the new version number, and `Active: active (running)`.

**151.** If the new version misbehaves, roll straight back:

```
sudo install -o root -g root -m 0755 /usr/local/bin/goldencloud.previous /usr/local/bin/goldencloud
sudo systemctl restart goldencloud
```

**152.** Update the operating system at the same time, once a month or so:

```
sudo apt update && sudo apt full-upgrade -y && sudo apt autoremove --purge -y
sudo reboot
```

---

### M-2 · Add or remove a staff member

**153.** Add:

```
sudo goldencloud user add --config /etc/goldencloud/config.yaml dave
sudo systemctl reload goldencloud
```

**154.** Confirm and give them the installer and their password by separate
routes:

```
sudo goldencloud user list --config /etc/goldencloud/config.yaml
```

**155.** Remove someone who has left:

```
sudo goldencloud user remove --config /etc/goldencloud/config.yaml dave
sudo systemctl reload goldencloud
```

*What it does:* removes them from the user database so they can no longer sign
in. **It does not delete their files** — that is deliberate, so that "remove the
leaver's access immediately" and "decide what to do with the leaver's files" are
two separate decisions made at two different times.

*Expected output:*

```
Removed user "dave".
Their files were left in place at /mnt/wd/goldencloud/dave.
Delete them yourself, or re-run with --delete-data.
Reload the running server with: systemctl reload goldencloud
```

*If you see* `goldencloud: user remove: no such user "dave"`: nothing was
changed. Check the spelling against `goldencloud user list`.

**156.** Deal with their files when you are ready. Keep them:

```
sudo mv /mnt/wd/goldencloud/dave /mnt/wd/goldencloud/.archived-dave-2026-08-03
```

Or, once you are certain, delete them:

```
sudo rm -rf /mnt/wd/goldencloud/dave
```

You can also do both in one go, if you were certain at the time of step 155:
`sudo goldencloud user remove --config /etc/goldencloud/config.yaml
--delete-data dave`. It says `Deleted /mnt/wd/goldencloud/dave and everything in
it.` and there is no undo, which is why the two-step version above is the
default advice.

**157.** Confirm they cannot get back in:

```
curl -sS -u dave:whatever-their-password-was -o /dev/null \
  -w 'HTTP status: %{http_code}\n' https://cloud.yourcompany.com/
```

*Expected output:* `HTTP status: 401`.

---

### M-3 · Change or reset a password

**158.** Change it:

```
sudo goldencloud user passwd --config /etc/goldencloud/config.yaml alice
```

You are prompted for the new password twice. You do not need the old one.

*Expected output:*

```
New password for alice:
Repeat password:
Password for "alice" changed.
Reload the running server with: systemctl reload goldencloud
```

*If you see* `goldencloud: user passwd: no such user "alice"`: the name is
wrong. Check it with `goldencloud user list`.

**159.** Reload:

```
sudo systemctl reload goldencloud
```

**160.** Tell the staff member. Their tray app will stop working until they
sign out and back in with the new password — that is the correct behaviour, and
it is what makes password rotation actually effective.

**161.** Do this immediately if a laptop is lost or stolen, or if a password has
been shared over an insecure channel. It takes fifteen seconds and it locks the
old credential out everywhere at once. See
[`../docs/SECURITY.md`](../docs/SECURITY.md).

---

### M-4 · Revoke someone right now

**162.** The fastest possible removal of one person:

```
sudo goldencloud user remove --config /etc/goldencloud/config.yaml dave
sudo systemctl reload goldencloud
```

Their next request fails. Any transfer already in flight is cut when the
connection ends.

**163.** To cut **everybody** off instantly — a suspected compromise, say:

```
sudo systemctl stop cloudflared
```

The tunnel closes and the service is unreachable from outside. Nothing is
deleted. Investigate, then `sudo systemctl start cloudflared`.

---

### M-5 · Backups

Three things are worth backing up, and they are very different sizes.

**164.** **The user database and configuration** — tiny, and the only thing that
is genuinely irreplaceable about the Pi:

```
sudo cp /etc/goldencloud/users.yaml "/etc/goldencloud/users.yaml.$(date +%F)"
sudo tar czf "/mnt/wd/goldencloud-config-$(date +%F).tar.gz" \
  -C /etc goldencloud
```

*Expected output:* nothing, then a `.tar.gz` on the WD unit. Copy it somewhere
off the WD unit as well.

> That archive contains `wd.credentials`, which holds your NAS password in plain
> text. Treat it as a secret. Do not email it.

**165.** **The staff files** — everything under `/mnt/wd/goldencloud`. These
live on the WD unit, so use the WD unit's own backup features first (My Cloud
Home supports USB backup and cloud sync). For a second copy on a USB disk
plugged into the Pi:

```
sudo rsync -av --delete /mnt/wd/goldencloud/ /media/backup/goldencloud/
```

*What `--delete` does:* makes the backup an exact mirror, including removing
files that were deleted from the source. That is what you want for a mirror and
**not** what you want if you might need to recover something a staff member
deleted by accident. If in doubt, drop `--delete`.

**166.** **The Pi's operating system** — do not bother. Everything on it is
reproducible from this runbook in about an hour, and staff files are not on it.
Backing up an SD card image gives you a stale copy of something you can rebuild.

**167.** Test a restore once, now, before you need it:

```
sudo tar tzf /mnt/wd/goldencloud-config-*.tar.gz | head
```

*Expected output:* a list of the files inside the archive. An archive you have
never opened is not a backup.

---

### M-6 · Reading the logs

**168.** Watch what is happening right now:

```
sudo journalctl -u goldencloud -f
```

Press **Ctrl+C** to stop watching. Nothing is affected by watching.

**169.** Common views:

```
# Last 100 lines
sudo journalctl -u goldencloud -n 100 --no-pager

# Since a time
sudo journalctl -u goldencloud --since "2026-08-03 09:00" --no-pager

# Today only
sudo journalctl -u goldencloud --since today --no-pager

# Errors and warnings only
sudo journalctl -u goldencloud -p warning --no-pager

# One user's activity
sudo journalctl -u goldencloud --no-pager | grep alice

# Failed sign-in attempts, and anyone the rate limiter is holding off
sudo journalctl -u goldencloud --no-pager | grep -i "authentication"

# The tunnel
sudo journalctl -u cloudflared -n 100 --no-pager

# Everything since the last boot, all services
sudo journalctl -b --no-pager | tail -100
```

**170.** Keep the journal from filling the SD card. Check its size, and cap it:

```
journalctl --disk-usage
sudo journalctl --vacuum-time=30d
```

*Expected output:* something like
`Archived and active journals take up 184.0M in the file system.` Anything under
a few hundred megabytes is fine.

To cap it permanently, add `SystemMaxUse=200M` to `/etc/systemd/journald.conf`
and run `sudo systemctl restart systemd-journald`.

> **Passwords never appear in the log** at any level. If you ever see one,
> that is a bug — please report it.

---

## You are finished

You now have:

- A Raspberry Pi that boots unattended and mounts the WD unit by itself.
- A hardened WebDAV server running as an unprivileged account that cannot start
  without its storage and cannot write anywhere except that storage.
- Three users, each locked inside their own folder, unable to see each other.
- An HTTPS address reachable from anywhere in the world, built without touching
  your router, that works behind carrier-grade NAT.
- Logs, backups, and a way to revoke anybody in fifteen seconds.

Keep the worksheet from step 3 somewhere safe. Everything you need to change
later is in it.
