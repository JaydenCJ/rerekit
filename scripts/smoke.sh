#!/usr/bin/env bash
# End-to-end smoke test for rerekit: builds the binary, manufactures a
# real merge conflict with git in a temp repo, records the human
# resolution, then replays it when the same conflict comes back from the
# opposite merge direction. No network, idempotent, finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/rerekit"
git() { command git -c user.name=smoke -c user.email=smoke@example.test -c commit.gpgsign=false "$@"; }

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/rerekit) || fail "go build failed"

echo "2. version matches manifest"
"$BIN" --version | grep -qx "rerekit 0.1.0" || fail "--version mismatch"

echo "3. manufacture a real merge conflict with git"
REPO="$WORKDIR/repo"
mkdir "$REPO" && cd "$REPO"
git init -q -b main
printf 'package config\n\nfunc Defaults() Options {\n\treturn Options{Retries: 3}\n}\n' > config.go
git add config.go && git commit -qm base
git checkout -qb feature
printf 'package config\n\nfunc Defaults() Options {\n\treturn Options{Retries: 5}\n}\n' > config.go
git commit -qam "raise retries"
git checkout -q main
printf 'package config\n\nfunc Defaults() Options {\n\treturn Options{Retries: 3, Timeout: 30}\n}\n' > config.go
git commit -qam "add timeout"
git tag premerge
if git merge -q feature >/dev/null 2>&1; then fail "merge should conflict"; fi
grep -q "<<<<<<<" config.go || fail "no conflict markers in config.go"

echo "4. snap the conflict (auto-initializes .rerekit/)"
"$BIN" snap | grep -q "snapped config.go (1 conflict)" || fail "snap did not find the conflict"
[ -f .rerekit/.gitignore ] || fail ".rerekit/.gitignore not created"
grep -qx "pending/" .rerekit/.gitignore || fail "pending/ not ignored"

echo "5. resolve by hand, record a committable .res file"
printf 'package config\n\nfunc Defaults() Options {\n\treturn Options{Retries: 5, Timeout: 30}\n}\n' > config.go
OUT="$("$BIN" record)"
echo "$OUT" | grep -q "config.go (new)" || fail "record did not create a resolution"
echo "$OUT" | grep -q "rerekit record: 1 resolution recorded from 1 file" || fail "record summary wrong"
RES_FILE="$(ls .rerekit/resolutions/*.res)" || fail "no .res file written"
grep -qx "rerekit-resolution-v1" "$RES_FILE" || fail ".res magic line missing"
grep -q "^|	return Options{Retries: 5, Timeout: 30}$" "$RES_FILE" || fail "resolution body not human-readable"
git add --dry-run .rerekit | grep -q "resolutions" || fail ".res files must be committable"
# (left untracked here so the store follows us across branch switches)
git add config.go && git commit -qm "merge feature (resolution recorded)"

echo "6. the same conflict returns from the opposite direction"
git checkout -q feature
if git merge -q premerge >/dev/null 2>&1; then fail "reverse merge should conflict"; fi
grep -q "<<<<<<<" config.go || fail "no markers on reverse merge"

echo "7. status sees it as replayable"
"$BIN" status | grep -q "config.go: 1 conflict, 1 replayable" || fail "status not replayable"
"$BIN" status --format json | grep -q '"schema_version": 1' || fail "status json envelope missing"

echo "8. apply --dry-run reports without writing"
"$BIN" apply --dry-run | grep -q "would resolve config.go: 1 of 1 conflicts" || fail "dry-run wrong"
grep -q "<<<<<<<" config.go || fail "dry-run must not write"

echo "9. apply replays the recorded resolution byte-identically"
"$BIN" apply | grep -q "rerekit apply: 1 conflict resolved, 0 remaining" || fail "apply summary wrong"
printf 'package config\n\nfunc Defaults() Options {\n\treturn Options{Retries: 5, Timeout: 30}\n}\n' > expected.go
cmp -s config.go expected.go || fail "replayed file differs from the recorded resolution"
rm expected.go
git add config.go && git commit -qm "merge main (replayed)" || fail "replayed merge did not commit"

echo "10. list / show / forget round-trip"
ID="$("$BIN" list | awk 'NR==2 {print $1}')"
[ -n "$ID" ] || fail "list did not print an ID"
"$BIN" show "$ID" | grep -q -- "--- resolution ---" || fail "show missing resolution section"
"$BIN" list --format json | grep -q '"tool": "rerekit"' || fail "list json envelope missing"
"$BIN" forget "$ID" | grep -q "1 resolution removed" || fail "forget failed"
"$BIN" list | grep -q "no resolutions recorded" || fail "store not empty after forget"

echo "11. unmatched conflicts exit 1; usage errors exit 2"
printf '<<<<<<< a\nnever recorded\n=======\nanywhere\n>>>>>>> b\n' > fresh.txt
set +e
"$BIN" apply >/dev/null 2>&1
[ $? -eq 1 ] || fail "apply with unmatched conflicts should exit 1"
"$BIN" list --format yaml >/dev/null 2>&1
[ $? -eq 2 ] || fail "bad --format should exit 2"
"$BIN" frobnicate >/dev/null 2>&1
[ $? -eq 2 ] || fail "unknown command should exit 2"
set -e

echo "SMOKE OK"
