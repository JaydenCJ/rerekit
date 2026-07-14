# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- Conflict parser for git-style markers: merge and diff3 styles, CRLF
  marker lines, empty sides, marker lookalikes (setext `=======`
  underlines, 8-char rulers) kept as content, and byte-identical
  Parse→Render round-trips.
- Content fingerprints (`rerekit-fp-v1`) that identify a conflict by its
  two sides only — label-blind, diff3-base-blind, CRLF-normalized, and
  symmetric under ours/theirs swap, so the same conflict matches from
  either rebase direction and either checkout style.
- The committable `rerekit-resolution-v1` text format: `key: value`
  headers plus `|`-prefixed payload sections, deterministic bytes (no
  timestamps), tolerant of unknown headers, and integrity-checked on
  load (an edited conflict body fails loudly; the resolution section is
  freely editable).
- `snap` and `record`: snapshot conflicted files while markers are in
  place, then recover exactly what replaced each hunk by anchoring on
  the untouched context between conflicts — refusing (never guessing)
  when context was also edited or hunks are adjacent.
- `apply` with `--dry-run` and `--snap`: replays recorded resolutions
  onto fresh conflicts, preserves the target file's CRLF endings and
  final-newline state, leaves unmatched hunks untouched, and exits 1
  when manual work remains.
- `status`, `list`, `show`, `forget`, and `init`; `--format json` on
  status and list with a stable `schema_version: 1` envelope.
- Store discovery (`.rerekit/` upward, then the git root, including
  worktree `.git` files) with an auto-written `.gitignore` so pending
  snapshots stay local while `resolutions/*.res` get committed.
- Runnable examples (`examples/demo-rebase.sh`, `examples/ci-replay.sh`)
  and a format reference (`docs/format.md`).
- 90 deterministic offline tests (unit + in-process CLI integration
  against real temp-dir trees) and `scripts/smoke.sh` driving the built
  binary through real git merges in both directions.

[0.1.0]: https://github.com/JaydenCJ/rerekit/releases/tag/v0.1.0
