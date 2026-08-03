# Roadmap

## v1 non-goals

Explicitly out of scope for v1, recorded here so they are not re-litigated
mid-build. Each is a real want, not a bad idea — just not this release.

| Not in v1                 | Why deferred                                                                                                                   | What v1 does instead                                                                     |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------- |
| **File versioning**       | Needs a storage layout decision and a retention policy; interacts with quotas. A half-built version history is worse than none. | The WD unit's own snapshot feature, plus ordinary backups. Documented in `RUNBOOK.md` §9. |
| **Sync / offline mode**   | A correct sync engine (conflict resolution, partial-write recovery) is a larger project than everything else here combined.     | The drive is a live mount. Files are on the server, not mirrored locally.                 |
| **macOS / mobile clients** | macOS mounts WebDAV natively; iOS/Android need their own app each.                                                             | `docs/OTHER-PLATFORMS.md` shows the native macOS and iOS WebDAV steps — no app required.  |
| **Web UI**                | A browser file manager is a second front-end and a second auth surface.                                                        | Drive letter in File Explorer, which is what staff asked for.                             |

## Post-v1 candidates, roughly in order

1. **Per-user quota enforcement** — `users.yaml` already carries the field and
   the server reports it; enforcement on write is not wired up.
2. **Audit log** — who read/wrote what, append-only, rotated. Nothing today
   beyond ordinary access logs.
3. **Group folders** — a shared `/company` root visible to named users, on top
   of the existing per-user jail.
4. **Client auto-update** — the tray app checks GitHub Releases and offers an
   in-place upgrade.
5. **Server-side trash** — deletes move to a per-user `.trash` for 30 days,
   which is also the cheapest possible version of "undo".
6. **macOS tray client** — same shape as the Windows one, once there is demand.
