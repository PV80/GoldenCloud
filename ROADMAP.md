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

1. **Drive-letter collision handling in the tray app** — the default `G:` is
   simply taken on some machines (the owner's laptop being example one) and the
   mount fails with "mount point in use". The app should detect this and fall
   back to the next free letter, or surface a friendly picker. Small, but it is
   the difference between "works everywhere" and a support call per office.
2. **Per-user quota enforcement** — `users.yaml` already carries the field and
   the server reports it; enforcement on write is not wired up.
3. **Audit log** — who read/wrote what, append-only, rotated. Nothing today
   beyond ordinary access logs.
4. **Group folders** — a shared `/company` root visible to named users, on top
   of the existing per-user jail.
5. **Client auto-update** — the tray app checks GitHub Releases and offers an
   in-place upgrade.
6. **Server-side trash** — deletes move to a per-user `.trash` for 30 days,
   which is also the cheapest possible version of "undo".
7. **macOS tray client** — same shape as the Windows one, once there is demand.

## Known packaging gaps

- **rclone's MIT licence is not shipped with the installer.** rclone's Windows
  zip contains no `COPYING` entry (only `rclone.exe`, `rclone.1`, `README.txt`,
  `README.html`, `git-log.txt`), so `thirdparty.ps1` finds nothing to copy and
  skips silently. Redistributing an MIT binary should carry its licence text.
  Fix: fetch `COPYING` from the rclone source tree at the pinned tag, or vendor
  the licence text into `client/installer/`. Not a build failure — a compliance
  loose end.
