#!/usr/bin/env bash
# Record the README's demo: docs/demo.cast (asciinema) and docs/demo.gif.
#
#   scripts/demo.sh            (or: make demo)
#
# Nothing here touches Apple, 1Password, the clipboard or the keychain:
# hme runs against the fake Apple from the tests, on localhost, through
# internal/demo. Needs asciinema and agg (brew install asciinema agg).

set -euo pipefail

cd "$(dirname "$0")/.."
for tool in asciinema agg; do
  command -v "$tool" >/dev/null || {
    echo "error: $tool is not installed (brew install $tool)" >&2
    exit 1
  }
done

work="$(mktemp -d)"
server_pid=""
cleanup() {
  [[ -n $server_pid ]] && kill "$server_pid" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

version="$(git describe --tags --abbrev=0 2>/dev/null || echo dev)"
go build -o "$work/bin/hme" ./internal/demo/hmedemo
go build -o "$work/fakeapple" ./internal/demo/fakeapple

port=8765
"$work/fakeapple" -listen "127.0.0.1:$port" >"$work/server.log" 2>&1 &
server_pid=$!
for _ in $(seq 50); do
  grep -q http "$work/server.log" 2>/dev/null && break
  sleep 0.1
done

export HME_DEMO_URL="http://127.0.0.1:$port"
export HME_DEMO_VERSION="${version#v}"
export HME_CONFIG_DIR="$work/config"
export PATH="$work/bin:$PATH"
export TERM=xterm-256color
mkdir -p docs

asciinema rec --headless --overwrite --quiet \
  --window-size 118x22 \
  --idle-time-limit 2 \
  --title "hme: iCloud Hide My Email from the command line" \
  --command "bash scripts/demo-session.sh" \
  docs/demo.cast

agg --theme monokai --font-size 15 --speed 1.2 --last-frame-duration 4 \
  docs/demo.cast docs/demo.gif

echo "wrote docs/demo.cast and docs/demo.gif ($(du -h docs/demo.gif | cut -f1))"
