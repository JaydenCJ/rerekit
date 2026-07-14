#!/usr/bin/env bash
# demo-rebase.sh <target-dir> — build a demo repo, record a conflict
# resolution once, and watch it replay when the same conflict returns
# from the opposite merge direction. Offline; needs git and Go >=1.22.
set -euo pipefail

TARGET="${1:?usage: demo-rebase.sh <target-dir>}"
mkdir -p "$TARGET"
TARGET="$(cd "$TARGET" && pwd)"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$TARGET/rerekit"
git() { command git -c user.name=demo -c user.email=demo@example.test -c commit.gpgsign=false "$@"; }

echo "==> building rerekit"
(cd "$ROOT" && go build -o "$BIN" ./cmd/rerekit)

REPO="$TARGET/repo"
rm -rf "$REPO" && mkdir "$REPO" && cd "$REPO"
git init -q -b main
printf 'retries = 3\ntls = on\n' > service.conf
git add service.conf && git commit -qm base
git checkout -qb feature
printf 'retries = 5\ntls = on\n' > service.conf
git commit -qam "raise retries"
git checkout -q main
printf 'retries = 3\ntimeout = 30\ntls = on\n' > service.conf
git commit -qam "add timeout"
git tag premerge

echo "==> merging feature into main (this conflicts)"
git merge -q feature >/dev/null 2>&1 || true

echo "==> snap while the markers are in place"
"$BIN" snap

echo "==> a human resolves it, then record"
printf 'retries = 5\ntimeout = 30\ntls = on\n' > service.conf
"$BIN" record
git add service.conf && git commit -qm "merge feature"

echo "==> the same conflict returns, opposite direction"
git checkout -q feature
git merge -q premerge >/dev/null 2>&1 || true

echo "==> replay instead of re-resolving"
"$BIN" apply
git add service.conf && git commit -qm "merge main (replayed)"

echo
echo "==> the committable resolution file:"
cat "$REPO"/.rerekit/resolutions/*.res
echo
echo "demo complete — repo left at $REPO for exploring"
