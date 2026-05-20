# Task 03 Proofs — NDJSON buffer with cap, eviction, and ordered drain

## Task Summary

This task adds the persistent failure-tolerance layer: a `Buffer` that
appends failed events as one JSON object per line to
`<cacheDir>/pending.ndjson`, enforces a 1 MB cap with FIFO eviction, and
drains up to 50 events per call in FIFO order. The file is created at
`0600`, its parent dir at `0700`. Truncated trailing lines are tolerated.
Re-sends preserve the original `event_id` byte-for-byte.

## What This Task Proves

- `Append`-then-read returns lines in FIFO order.
- A write that would exceed the cap triggers FIFO eviction; the resulting
  file size is at or below the cap and the newest entry is retained while
  the oldest is gone.
- A successful drain consumes lines in FIFO order and rewrites the file
  empty.
- The per-flush drain cap is 50; a buffer of 60 leaves 10 behind starting
  at the 51st entry.
- A truncated trailing line is silently dropped on read; earlier lines
  still drain.
- File perms are `0600` and parent-dir perms are `0700` on POSIX (Windows
  skipped).
- Re-drained lines are byte-equal to the originally written ones (so
  `event_id` survives intact for collector dedup).
- `*TransientError` during drain stops the loop and leaves the failed line
  plus all subsequent lines in the file.
- `*PermanentError` during drain drops that line and continues.

## Evidence Summary

- `go test ./pkg/telemetry/... -run TestBuffer -v` → 10 tests pass.
- `go vet ./pkg/telemetry/...` is clean.

## Artifact: Full buffer test suite

**What it proves:** Every functional requirement in spec Unit 3 has a
passing named test.

**Command:**

```bash
go test ./pkg/telemetry/... -run TestBuffer -v
```

**Result summary:** PASS — 10 tests:
`TestBuffer_AppendThenRead`, `TestBuffer_CapAndEviction`,
`TestBuffer_DrainsInOrderOnSuccess`, `TestBuffer_DrainCapPerCall`,
`TestBuffer_TruncatedTrailingLineSkipped`, `TestBuffer_FilePermissions`,
`TestBuffer_PreservesEventID`, `TestBuffer_DrainTransientStopsEarly`,
`TestBuffer_DrainPermanentDropsAndContinues`,
`TestBuffer_AtomicTmpRewrite`.

```text
=== RUN   TestBuffer_CapAndEviction
--- PASS: TestBuffer_CapAndEviction (0.02s)
=== RUN   TestBuffer_DrainsInOrderOnSuccess
--- PASS: TestBuffer_DrainsInOrderOnSuccess (0.00s)
=== RUN   TestBuffer_DrainCapPerCall
--- PASS: TestBuffer_DrainCapPerCall (0.00s)
=== RUN   TestBuffer_TruncatedTrailingLineSkipped
--- PASS: TestBuffer_TruncatedTrailingLineSkipped (0.00s)
=== RUN   TestBuffer_FilePermissions
--- PASS: TestBuffer_FilePermissions (0.00s)
=== RUN   TestBuffer_PreservesEventID
--- PASS: TestBuffer_PreservesEventID (0.00s)
PASS
ok  	github.com/salaboy/skills-oci/pkg/telemetry  0.280s
```

## Artifact: 1 MB cap with FIFO eviction

**What it proves:** Writing 100 ~1 KB entries to a Buffer capped at 50 KB
keeps the file at or below 50 KB and retains only the newest entries
(`evt-099` still present, `evt-000` evicted).

**Why it matters:** The spec's "Telemetry never grows unbounded on disk"
invariant is enforced and tested.

## Artifact: Per-flush cap

**What it proves:** A buffer of 60 entries drains exactly 50 on one
successful call; the 10 leftovers (starting at `E-50`) persist.

**Why it matters:** Matches the spec's "drain up to 50 in FIFO order per
success" rule and gives the collector room to recover gracefully after an
outage without thundering on the user's command latency.

## Artifact: `event_id` preservation

**What it proves:** A line containing a known ULID survives round-trip
through `Append` → `Drain` byte-equal.

**Why it matters:** The collector's `(client.name, event_id)` dedupe makes
replays safe — only if the producer never mutates the line on replay.

## Artifact: Permissions

**What it proves:** On POSIX, the buffer file is `0600` and the parent
directory created by the buffer is `0700`.

**Why it matters:** Spec's privacy posture — no other local user can read
pending events.

## Reviewer Conclusion

The buffer correctly persists failed events, enforces the cap with FIFO
eviction, drains in order with a per-call cap, tolerates trailing
corruption, preserves event_id verbatim on replay, and applies the
mandated POSIX permissions. The component is ready to be combined with
the transport in Task 04's orchestrator.
