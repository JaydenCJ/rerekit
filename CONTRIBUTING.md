# Contributing to rerekit

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22, and git for the smoke test; nothing else — the tool
and its unit tests are pure standard library and never touch the network.

```bash
git clone https://github.com/JaydenCJ/rerekit && cd rerekit
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, manufactures a real merge conflict
with git in a temp repo, records the hand-written resolution, and then
replays it when the same conflict returns from the opposite merge
direction; it must finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (90 deterministic tests, no network, no git).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (parsing, fingerprinting, extraction, and replay never touch
   the filesystem — only the store and the CLI do).

## Ground rules

- Keep dependencies at zero; adding one needs strong justification in
  the PR.
- No network calls, ever, and no telemetry — rerekit only reads and
  writes files under the store root you run it in.
- The `.res` format and the fingerprint normalization are contracts:
  changing either needs a new version string (`rerekit-resolution-v2`,
  `rerekit-fp-v2`), a migration note in `docs/format.md`, and tests for
  both old and new forms.
- Code comments and doc comments are written in English.
- Determinism first: identical input must produce byte-identical output,
  including all orderings — resolution files get committed and reviewed.

## Reporting bugs

Include the output of `rerekit version`, the full command you ran, and a
minimal conflicted file (before and after resolving, for record bugs)
that reproduces the problem — the engine sees nothing but those files,
so a repro is usually two small text snippets.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
