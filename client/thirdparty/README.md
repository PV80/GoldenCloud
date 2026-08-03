# Third-party binaries

This folder is **generated at build time and gitignored**. Only this README is
committed.

`rclone.exe` is around 60 MB and the WinFsp redistributable is around 2 MB;
neither belongs in a source repository, and committing them would make every
clone pay for them forever. Instead `../thirdparty.ps1` downloads them from the
projects' official release URLs and verifies a pinned SHA256 before anything is
packaged.

The pins live in [`../thirdparty.pins.json`](../thirdparty.pins.json).

## What gets fetched

| File                  | Component | Version     | Source                                                                                       | Licence      |
| --------------------- | --------- | ----------- | -------------------------------------------------------------------------------------------- | ------------ |
| `rclone.exe`          | rclone    | 1.68.2      | `https://github.com/rclone/rclone/releases/download/v1.68.2/rclone-v1.68.2-windows-amd64.zip`  | MIT          |
| `rclone-LICENSE.txt`  | rclone    | 1.68.2      | _not present in the Windows zip — see below_                                                   | MIT          |
| `winfsp.msi`          | WinFsp    | 2.0.23075   | `https://github.com/winfsp/winfsp/releases/download/v2.0/winfsp-2.0.23075.msi`                 | GPLv3 + FLOSS exception |

## Pinned hashes

| Component | SHA256 |
| --------- | ------ |
| rclone 1.68.2 (zip) | `812bf76cc02c04cf6327f3683f3d5a88e47d36c39db84c1a745777496be7d993` |
| WinFsp 2.0.23075 (msi) | `6324dc81194a6a08f97b6aeca303cf5c2325c53ede153bae9fc4378f0838c101` |

Both are pinned, so `thirdparty.ps1` enforces them strictly and a mismatch
aborts the build. Nothing here needs a human step.

### How these were verified

- **rclone** — the hash is the one rclone itself publishes in the release's
  `SHA256SUMS` file, and it was independently confirmed by downloading the zip
  and hashing it. Both agreed.
- **WinFsp** — the MSI was downloaded from the GitHub release and hashed
  directly (2,207,744 bytes).

The rclone URL points at the GitHub release rather than `downloads.rclone.org`.
Both hosts serve byte-identical archives — the hash above matches rclone's own
published sum either way — but the GitHub host is reachable from restricted
build networks where `downloads.rclone.org` is not.

If `thirdparty.ps1` ever encounters an empty `sha256`, it treats it as "not yet
pinned": it downloads, prints the hash it observed as a `::warning::`, and
carries on. That path is the fallback for a version bump, not the steady state.

### A note on `rclone-LICENSE.txt`

The Windows zip does **not** contain a `COPYING` entry — its files are
`rclone.exe`, `rclone.1`, `README.txt`, `README.html` and `git-log.txt`. The
licence copy in `thirdparty.ps1` is therefore guarded and silently skips, which
is why the build does not fail. rclone is MIT-licensed and redistributing the
binary should carry that licence, so this is tracked as a packaging gap rather
than a build problem: see `ROADMAP.md`.

## Bumping a version

1. Change `version` and `url` in `../thirdparty.pins.json`.
2. Blank the matching `sha256`.
3. Run `./build.ps1`, read the warning, paste the new hash in, re-run.
4. Update the table above.
5. Add a `D-0xx` entry to `../../DECISIONS.md` if the reason is not obvious.

## Offline and air-gapped builds

Set either or both of these to local paths and no download happens:

```powershell
$env:GOLDENCLOUD_RCLONE_EXE = 'D:\vendor\rclone.exe'
$env:GOLDENCLOUD_WINFSP_MSI = 'D:\vendor\winfsp-2.0.23075.msi'
./build.ps1
```
