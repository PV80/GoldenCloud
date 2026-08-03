# GoldenCloud

A private cloud drive you own. Staff open Windows File Explorer, see a drive
letter (`G:` by default), sign in once with their own username and password, and
see **only their own folder** on your office storage.

No VPN client. No router changes. No third-party cloud accounts for staff. No
dependency on Western Digital's discontinued services.

```
Windows PC (staff)                Internet                 Office
┌──────────────────┐                                  ┌──────────────────────┐
│ GoldenCloud Tray │                                  │ Raspberry Pi 5 /     │
│  + rclone/WinFsp │──HTTPS──▶ Cloudflare ──tunnel──▶ │ mini-PC              │
│      ↓           │           (cloud.example.com)    │  goldencloud-server  │
│   G:\  drive     │                                  │      ↓               │
└──────────────────┘                                  │  /mnt/wd/<user>      │
                                                      │  (WD My Cloud Home   │
                                                      │   Local Access SMB)  │
                                                      └──────────────────────┘
```

## Repository layout

| Path                 | What it is                                                     |
| -------------------- | -------------------------------------------------------------- |
| `server/`            | Go WebDAV server + admin CLI. Single static binary.             |
| `deploy/`            | Cloudflare Tunnel templates, systemd unit, `RUNBOOK.md`.        |
| `client/`            | .NET 8 Windows tray app + Inno Setup installer.                 |
| `docs/`              | Operator and staff documentation, manual test checklists.       |
| `tests/`             | Cross-component integration tests.                              |
| `.github/workflows/` | CI: lint, unit tests, integration tests, release artefacts.     |

## Quick start (operator)

Read [`deploy/RUNBOOK.md`](deploy/RUNBOOK.md). It takes a blank SD card to an
internet-reachable server with three users, assuming no Linux knowledge.

## Quick start (developer)

```bash
cd server
go test ./...
go build -o goldencloud ./cmd/goldencloud
./goldencloud serve --config ../deploy/goldencloud.example.yaml
```

## Project status

See [`PROGRESS.md`](PROGRESS.md) for phase-by-phase status and the two
human-action gates. Design decisions and their rationale live in
[`DECISIONS.md`](DECISIONS.md). Deliberately deferred work is in
[`ROADMAP.md`](ROADMAP.md).

## Licence

MIT. See [`LICENSE`](LICENSE).
