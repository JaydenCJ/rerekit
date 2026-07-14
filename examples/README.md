# rerekit examples

Two runnable scripts, both offline and self-contained (they build the
binary from source on first use — any Go ≥1.22 — and need git):

- **`demo-rebase.sh <target-dir>`** — the full story in one run: builds
  a small git repo, merges two branches into a real conflict, records
  the hand-written resolution with `snap` + `record`, then hits the
  same conflict from the *opposite* merge direction and replays it with
  `apply`. Prints the committable `.res` file at the end. Use it to see
  the record-once/replay-forever loop before wiring rerekit into your
  own repository.

- **`ci-replay.sh <repo-root>`** — the pattern for automation: after
  `git rebase` (or `git merge`) stops on conflicts, run
  `rerekit apply --snap` as the first responder. Exit code 0 means every
  conflict was replayed from the committed store and the rebase can
  continue unattended; exit code 1 means a human is needed — and the
  leftovers are already snapshotted, so their resolution can be recorded
  the moment it is written.

```bash
bash examples/demo-rebase.sh /tmp/rerekit-demo
bash examples/ci-replay.sh /tmp/rerekit-demo/repo
```
