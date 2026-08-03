# Progress

## ⚠️ Human action gates

Two things cannot be done without you. Everything else is built and green
before you touch either of these.

### Gate 1 — Cloudflare account + domain (blocks Phase 4 deploy, not the build)

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

---

## Phase status

| Phase | Name                     | Status         |
| ----- | ------------------------ | -------------- |
| 0     | Scaffold                 | 🟡 in progress |
| 1     | Server core              | ⬜ not started |
| 2     | Client                   | ⬜ not started |
| 3     | Packaging + deploy kit   | ⬜ not started |
| 4     | Handover                 | ⬜ not started |

---

## Phase 0 — Scaffold

**Acceptance criteria.** Repo structure, `DECISIONS.md`, `PROGRESS.md`, CI
skeleton green.

**Status.** In progress.

- [x] Repository structure created
- [x] `DECISIONS.md` seeded with D-001..D-008
- [x] `PROGRESS.md` with both human gates at the top
- [x] `ROADMAP.md` with v1 non-goals recorded
- [ ] CI skeleton green on GitHub Actions

Passed: —
Deferred: —
