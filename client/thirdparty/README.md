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
| `rclone.exe`          | rclone    | 1.68.2      | `https://downloads.rclone.org/v1.68.2/rclone-v1.68.2-windows-amd64.zip`                        | MIT          |
| `rclone-LICENSE.txt`  | rclone    | 1.68.2      | extracted from the same archive (`COPYING`)                                                    | MIT          |
| `winfsp.msi`          | WinFsp    | 2.0.23075   | `https://github.com/winfsp/winfsp/releases/download/v2.0/winfsp-2.0.23075.msi`                 | GPLv3 + FLOSS exception |

## Pinned hashes

| Component | SHA256 |
| --------- | ------ |
| rclone 1.68.2 (zip) | _not yet pinned_ |
| WinFsp 2.0.23075 (msi) | _not yet pinned_ |

### Why they are blank, and how to fill them in

The client was authored in an environment with no outbound network access, so
the real hashes could not be computed here, and inventing them would be worse
than leaving them empty — a wrong hash fails the build for the wrong reason, and
a made-up hash that happens to be checked in looks verified when it is not.

`thirdparty.ps1` therefore treats an empty `sha256` as **"not yet pinned"**: it
downloads the file, prints the SHA256 it actually saw as a loud warning
(`::warning::` in GitHub Actions), and carries on. A non-empty `sha256` is
enforced strictly and a mismatch aborts the build.

To pin them, once:

```powershell
cd client
./build.ps1 -Configuration Release      # prints both hashes as warnings
```

Cross-check the values against the publishers before pasting them in:

- rclone publishes `https://downloads.rclone.org/v1.68.2/SHA256SUMS`
- WinFsp publishes the SHA256 of each MSI in its GitHub release notes

Then put them into `../thirdparty.pins.json` and commit that one file. From that
point on the build refuses to package an unexpected binary.

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
