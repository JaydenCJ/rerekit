# rerekit on-disk formats

Everything rerekit writes is line-oriented plain text, designed to be
committed, diffed, and reviewed. This document is the reference for the
two file formats and the fingerprint that ties them together.

## Store layout

```
.rerekit/
├── .gitignore        # written on init: ignores pending/
├── resolutions/      # committed — one <id>.res file per conflict
└── pending/          # per-checkout preimage snapshots, never committed
```

The store root is found by walking upward from the working directory to
the first ancestor containing `.rerekit/`, falling back to the first
ancestor containing `.git` (a directory, or the file linked worktrees
use). `rerekit snap` auto-creates the store inside a git repository;
outside one, run `rerekit init` explicitly.

## Resolution files (`rerekit-resolution-v1`)

One file per conflict, named `<id>.res` where `<id>` is the first 12 hex
characters of the fingerprint. Example (real output):

```text
rerekit-resolution-v1
fingerprint: 817c3a89d026c30103028aea14f4ffa61a00b503076fcf43e3c6e7bd8c445946
path: src/config.go
ours-label: HEAD
theirs-label: feature/retry

--- ours ---
|	return Options{Retries: 3, Timeout: 30}
--- theirs ---
|	return Options{Retries: 5}
--- resolution ---
|	return Options{Retries: 5, Timeout: 30}
--- end ---
```

Rules:

- **Line 1** is the magic/version string, exactly `rerekit-resolution-v1`.
- **Headers** are `key: value` lines until the first blank line.
  `fingerprint` is required (64 lowercase hex). `path`, `ours-label` and
  `theirs-label` are informational — they record where and under which
  refs the conflict was first seen. Unknown headers are ignored, so the
  format can grow additively without breaking v1 readers.
- **Sections** appear in fixed order: `--- ours ---`, optional
  `--- base ---` (present when the conflict was recorded from a diff3
  checkout), `--- theirs ---`, `--- resolution ---`, `--- end ---`.
- **Every payload line is prefixed with `|`**. The prefix makes the file
  unambiguous no matter what the payload contains — conflict markers,
  section headers, even the magic string — while keeping one payload
  line per file line so diffs stay readable. Payload is stored
  LF-normalized; `apply` re-adds `\r` when the target file uses CRLF.
- **No timestamps, hostnames, or absolute paths.** Marshalling is
  deterministic: the same resolution always produces the same bytes, so
  committed files never churn.

### Editing resolutions by hand

The `--- resolution ---` section is meant to be edited in review — fix
the merged text, commit, done. The `ours`/`theirs` sections are the
conflict's identity: on load, rerekit recomputes the fingerprint from
them and rejects the file if it no longer matches the header. That makes
a corrupted or hand-mangled conflict body a loud error instead of a
resolution that silently never matches again.

## Fingerprints (`rerekit-fp-v1`)

The fingerprint is `sha256("rerekit-fp-v1" \0 sideA \0 sideB)` in hex,
where each side is its lines, trailing `\r` stripped, joined with `\n`,
and `(sideA, sideB)` is the lexicographically sorted pair of the two
conflicting sides. Consequences, each deliberate:

| Property | Why |
|---|---|
| Labels excluded | `<<<<<<< HEAD` / `>>>>>>> 4f2a91c` change on every rebase |
| diff3 base excluded | the same conflict must match from merge- and diff3-style checkouts |
| Sides sorted before hashing | rebasing the other way swaps ours/theirs; it is still the same conflict |
| CRLF stripped | a Windows checkout must match the resolution a Linux colleague recorded |
| Context excluded | the same conflicting change matches wherever in the file it lands |
| Line structure preserved | `["ab"]` and `["a","b"]` hash differently (`\n`-joined before hashing) |

Note that hunks are fingerprinted individually — unlike git's rerere,
which hashes whole-file preimages, moving one conflict in a file does
not invalidate the others.

## Pending snapshots (`rerekit-pending-v1`)

`snap` stores the exact content of each conflicted file under
`pending/<sha256(path)[:16]>.pre`, framed the same way (`path` and
optional `final-newline: false` headers, then a `|`-prefixed
`--- content ---` section). Snapshots are consumed by `record`, which
re-parses the preimage, anchors the context segments between hunks
against the resolved file, and extracts what replaced each hunk. They
are per-checkout state and are ignored via the store's `.gitignore`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | `apply` left unresolved conflicts (scripting signal) |
| 2 | usage error |
| 3 | runtime error (I/O, malformed store file, bad input) |
