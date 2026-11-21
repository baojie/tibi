#!/bin/bash
# The 0.1.2 screenshot: `brew install baojie/tap/tibi` (Homebrew on Linux).
#
#   scripts/screenshots/brew.sh
#
# Runs before the release exists, so it installs a local build: a cask
# shaped like the one GoReleaser pushes to baojie/homebrew-tap, in a local
# tap registered as baojie/tap, with the archive pre-placed in a throwaway
# HOMEBREW_CACHE (brew then reports it as already downloaded). The tap and
# the cask are removed again at the end. Needs brew ≥ 7 (postflight_steps),
# Node ≥ 22 and Chrome. Refuses to run when baojie/tap is already tapped.
set -euo pipefail
repo=$(cd "$(dirname "$0")/../.." && pwd)
cd "$repo"
out=$repo/docs/screenshots
V=$(sed -n 's/^var Version = "\(.*\)"/\1/p' internal/app/app.go)
command -v brew >/dev/null || { echo "brew not found" >&2; exit 1; }
if brew tap | grep -qx baojie/tap; then
  echo "baojie/tap is already tapped; untap it first (brew untap baojie/tap)" >&2
  exit 1
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/tibi-brew.XXXXXX")
export HOMEBREW_CACHE=$work/cache HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 \
  HOMEBREW_NO_ENV_HINTS=1 HOMEBREW_NO_INSTALL_CLEANUP=1
cleanup() {
  brew uninstall --cask baojie/tap/tibi >/dev/null 2>&1 || true
  brew untrust --cask baojie/tap/tibi >/dev/null 2>&1 || true
  brew untap baojie/tap >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

# The archive, built like the release (.goreleaser.yaml).
mkdir -p "$work/pkg" "$work/tap/Casks"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/baojie/tibi/internal/app.Version=$V" \
  -o "$work/pkg/tibi" ./cmd/tibi
cp LICENSE README.md "$work/pkg/"
archive=tibi_${V}_linux_amd64.tar.gz
tar -C "$work/pkg" -czf "$work/$archive" tibi LICENSE README.md
sum=$(sha256sum "$work/$archive" | cut -d' ' -f1)

cat > "$work/tap/Casks/tibi.rb" <<EOF
cask "tibi" do
  version "$V"
  on_linux do
    on_intel do
      sha256 "$sum"
      url "https://github.com/baojie/tibi/releases/download/v#{version}/tibi_#{version}_linux_amd64.tar.gz"
    end
  end
  name "tibi"
  desc "Self-hosted image host and pastebin — client and server"
  homepage "https://github.com/baojie/tibi"
  binary "tibi"
end
EOF
git -C "$work/tap" init -q -b main
git -C "$work/tap" add -A
git -C "$work/tap" -c user.email=shots@example.com -c user.name=shots commit -qm tap
brew tap baojie/tap "$work/tap" >/dev/null 2>&1
# Homebrew ≥ 6 loads non-official casks only once trusted; installing by
# the full name (what the transcript shows) would trust it anyway.
brew trust --cask baojie/tap/tibi >/dev/null
dl=$(brew --cache --cask baojie/tap/tibi)
mkdir -p "$(dirname "$dl")"
cp "$work/$archive" "$dl"

T=$work/v$V.txt
: > "$T"
say() { printf '$ %s\n' "$1" >> "$T"; (cd "$work" && PATH="$(brew --prefix)/bin:$PATH" eval "$1") >> "$T" 2>&1 || true; }
say 'brew install baojie/tap/tibi'
say 'tibi version'
say 'brew uninstall tibi'
# Keep only what concerns tibi: drop brew's warnings about this machine's
# other untrusted taps and about the kernel's Landlock version.
python3 - "$T" <<'PY'
import re, sys
p = sys.argv[1]
t = open(p).read()
t = re.sub(r"Warning: The following taps are not trusted:.*?https://docs\.brew\.sh/Tap-Trust\n\n", "", t, flags=re.S)
t = re.sub(r"Warning: Landlock .*?kernel\.\n", "", t, flags=re.S)
open(p, "w").write(t)
PY

echo "[{\"out\":\"$out/v$V.png\",\"term\":\"$T\",\"title\":\"brew install baojie/tap/tibi\"}]" > "$work/scenes.json"
node "$repo/scripts/screenshots/shot.mjs" "$work/scenes.json" "${CHROME:-google-chrome}"
echo "wrote $out/v$V.png"
