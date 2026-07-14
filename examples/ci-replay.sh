#!/usr/bin/env bash
# ci-replay.sh <repo-root> — the automation pattern: after a rebase or
# merge stops on conflicts, try the committed resolution store first.
# Exit 0: everything replayed, continue unattended. Exit 1: a human is
# needed, and the leftovers are already snapshotted for recording.
set -euo pipefail

REPO="${1:?usage: ci-replay.sh <repo-root>}"
REPO="$(cd "$REPO" && pwd)"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${REREKIT_BIN:-$ROOT/rerekit}"

if [ ! -x "$BIN" ]; then
  echo "==> building rerekit"
  (cd "$ROOT" && go build -o "$BIN" ./cmd/rerekit)
fi

cd "$REPO"
echo "==> replaying recorded resolutions"
if "$BIN" apply --snap; then
  echo "==> all conflicts replayed from the store; continue the rebase:"
  echo "    git add -u && git rebase --continue"
else
  status=$?
  if [ "$status" -eq 1 ]; then
    echo "==> some conflicts have no recorded resolution yet."
    echo "    Resolve them by hand, then run:  $BIN record"
    echo "    (the leftovers were snapshotted by --snap, so record is ready)"
    exit 1
  fi
  exit "$status"
fi
