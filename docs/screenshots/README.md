# Release screenshots

One picture per version, `vX.Y.Z.png`, showing what that version added. Each
is shown under its version in [CHANGELOG.md](../../CHANGELOG.md) and in the
GitHub release notes (`scripts/release-notes.sh`).

**Every new version gets one before it is tagged**: add a scene for it to
the scripts below, regenerate, look at it, and put it under the new
CHANGELOG heading.

All pictures are taken from real runs: real servers on scratch data,
real commands with their real output, real Chrome, a real Android emulator.
Demo data is made up (generated images, example text); nothing comes from
a real account or machine, and the folder tree of the machine taking the
picture is cropped out.

| versions | script | needs |
|---|---|---|
| all web pages and terminal sessions | `scripts/screenshots/run.sh [VERSION…]` | Node ≥ 22, Chrome, ImageMagick, ffmpeg, jq, Docker |
| 0.0.15 Chrome extension | `scripts/screenshots/extension.sh` | Node, Chrome, ImageMagick |
| 0.0.16 Android | `scripts/screenshots/android.sh` | a booted emulator, adb, the Android SDK |
| 0.0.17 ACDSeeN | `scripts/screenshots/acdseen.sh` | an ACDSeeN checkout (`ACDSEEN=…`) with its virtualenv |
| 0.1.2 Homebrew | `scripts/screenshots/brew.sh` | brew ≥ 7 (Linux), `baojie/tap` not tapped; uses a local build, nothing is downloaded |

Pictures show the current code, not the code of the old tag: each one is
about the feature that version introduced, as it works today.
