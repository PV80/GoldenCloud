# Using GoldenCloud on a Mac, iPhone or iPad

There is no GoldenCloud app for these devices, and there does not need to be.
macOS, iOS and iPadOS all speak **WebDAV** — the same protocol the Windows app
uses — natively, built into the operating system. You connect once and the drive
appears alongside everything else.

You need three things, which whoever set up the server will give you:

| | Example |
| --- | --- |
| The address | `https://cloud.yourcompany.com` |
| Your username | `alice` |
| Your password | given to you separately |

The address always starts with `https://`. If it does not, stop and ask —
without it your password would travel unencrypted.

> Android is not covered here. It has no built-in WebDAV support; you would need
> a third-party file manager, which is a decision about which app to trust with
> your company password rather than a technical step. Ask before installing one.

---

## macOS — Finder

### Connect

1. Click anywhere on the desktop, or on the Finder icon in the Dock, so that
   **Finder** is the active application. The menu bar at the top of the screen
   should read *Finder, File, Edit, View, Go, Window, Help*.
2. In the menu bar, click **Go**, then **Connect to Server**.
   Keyboard shortcut: **⌘K**.
3. In the **Server Address** box, type your address exactly:
   ```
   https://cloud.yourcompany.com
   ```
4. Click the **+** button to the right of the box. This saves the address in
   your Favourite Servers list so you never have to type it again.
5. Click **Connect**.
6. When asked how you want to connect, choose **Registered User** — not Guest.
7. Enter your **Name** (your username, e.g. `alice`) and **Password**.
8. Tick **Remember this password in my keychain**. macOS stores it in the
   Keychain, which is encrypted and tied to your Mac login.
9. Click **Connect**.

Your folder opens in a new Finder window and appears in the sidebar under
**Locations**, and as a drive icon on the desktop.

### Reconnect later

The Mac forgets network drives when it sleeps or restarts. To reconnect:

10. **Go → Connect to Server** (⌘K).
11. Your address is already in the **Favourite Servers** list — double-click it.
12. Because the password is in your keychain, it connects without asking.

### Reconnect automatically at login

13. Open **System Settings** → **General** → **Login Items**.
14. Under *Open at Login*, click **+**.
15. Navigate to the mounted GoldenCloud drive in the sidebar, select it, and
    click **Open**.

It now reconnects each time you log in. If the Mac is somewhere without
internet, it will simply fail quietly and you can connect manually later.

### If something goes wrong on a Mac

| What you see | What to do |
| --- | --- |
| *There was a problem connecting to the server* | Check the address is spelled correctly and starts with `https://`. Check you have internet — load any website. |
| It asks for the password over and over | Your username or password is wrong. Usernames are case-sensitive: `Alice` is not `alice`. Ask for a reset rather than guessing. |
| *The operation can't be completed because the original item can't be found* | The drive was disconnected. Reconnect with steps 10–12. |
| Files open but will not save | Some apps struggle with saving directly to a network drive. Copy the file to your Desktop, edit it there, and copy it back. |
| It is very slow with lots of small files | Normal. WebDAV makes one request per file. Zip a folder before copying it. |
| macOS creates `.DS_Store` files everywhere | Cosmetic, and harmless. They are how Finder remembers window positions. |

---

## iPhone and iPad — the Files app

### Connect

1. Open the **Files** app. It is grey with a blue folder — if you cannot find
   it, swipe down on the home screen and search for "Files".
2. Tap **Browse** at the bottom of the screen.
3. Tap the **···** (three dots) button at the top right.
4. Tap **Connect to Server**.
5. In the **Server** box, type your address:
   ```
   https://cloud.yourcompany.com
   ```
6. Tap **Connect**.
7. Choose **Registered User**.
8. Enter your **Name** (username) and **Password**.
9. Tap **Next**.

Your folder now appears under **Shared** in the Browse list. Tap it to open.

### Day to day

- **To save a file into it from another app:** tap the share button (a square
  with an arrow pointing up), tap **Save to Files**, then pick your GoldenCloud
  folder under Shared.
- **To open a file:** tap it. Photos, PDFs, and Office documents preview
  directly. Others offer to open in another app.
- **To keep a file available without internet:** press and hold it, then tap
  **Download Now**. Otherwise files stream on demand and need a connection.
- **To upload photos:** open **Photos**, select what you want, tap share, tap
  **Save to Files**, choose your GoldenCloud folder.

iOS remembers the connection. It reappears every time you open Files; you will
not be asked for the password again unless it changes.

### If something goes wrong on iOS

| What you see | What to do |
| --- | --- |
| *Connection failed* | Check your internet. Try loading a web page. If you are on Wi-Fi with a sign-in page (a hotel or café), complete that sign-in first. |
| It asks for the password repeatedly | Wrong username or password. They are case-sensitive. Ask for a reset. |
| The server disappeared from **Shared** | Add it again with steps 1–9. Nothing has been lost; only the shortcut is gone. |
| Large uploads fail | Stay in the Files app while a large upload runs. iOS suspends background apps and will cut the transfer. |
| Files look empty when you have no signal | Expected. Files stream on demand — see "Download Now" above. |

---

## What is the same everywhere

- **You only ever see your own folder.** There is no way to reach a colleague's
  files from any of these clients, on any platform. That is enforced by the
  server, not by the app you happen to be using.
- **Everything travels over HTTPS**, encrypted from your device all the way to
  the office.
- **Files are not copies.** They live on the office storage. Editing a file on
  your phone changes the same file your colleague sees on their PC — there is no
  syncing and no chance of two versions drifting apart.
- **Deletes are immediate.** There is no Recycle Bin or Trash on the server. If
  you delete something you needed, tell whoever runs the server straight away —
  it may be recoverable from a backup, but only if they know quickly.
- **Large files have two quirks on these platforms.** First, files that a
  Windows colleague stored through the GoldenCloud app may appear here as a
  set of numbered parts (`report.mp4.rclone_chunk.001`, `.002`, …) rather than
  one file — that is how the app fits big uploads under Cloudflare's per-upload
  cap. Leave the parts alone; on Windows the file looks and works normally. If
  you genuinely need such a file on a Mac, joining the parts in numeric order
  reconstructs it exactly (Terminal: `cat report.mp4.rclone_chunk.* > report.mp4`).
  Second, **uploading** a single file larger than 100 MB from Finder or the
  Files app fails with an error — Cloudflare refuses uploads that size outside
  the Windows app. Downloads of any size work everywhere.

---

## See also

- [`STAFF-GUIDE.md`](STAFF-GUIDE.md) — the Windows version of this page.
- [`SECURITY.md`](SECURITY.md) — what is protected, and how.
- [`../deploy/RUNBOOK.md`](../deploy/RUNBOOK.md) — for whoever runs the server.
