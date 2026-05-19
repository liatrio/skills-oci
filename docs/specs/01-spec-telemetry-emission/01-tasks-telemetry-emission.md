# 01-tasks-telemetry-emission.md

Implementation plan for [`01-spec-telemetry-emission.md`](./01-spec-telemetry-emission.md). Five parent tasks, each a demoable end-to-end vertical slice.

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `pkg/telemetry/event.go` | New file. The `Event` struct, constructor `NewSkillDownloaded(...)`, ULID + RFC3339 helpers, and JSON marshaling for the wire body. Pure functions only — no IO. |
| `pkg/telemetry/event_test.go` | New file. Unit tests for `Event`: golden-file body, ULID/timestamp formats, "never sent" substring guard, missing-field rejection. |
| `pkg/telemetry/testdata/event-skill-downloaded.json` | New file. Canonical fixture body used for byte-equality golden tests. Synthetic values only (no real digests/namespaces). |
| `pkg/telemetry/testdata/event-v1.json` | New file. Vendored copy of the collector's JSON Schema (lockstep gate target). |
| `pkg/telemetry/config.go` | New file. `Config` struct, `LoadConfig()` that reads `SKILLS_OCI_TELEMETRY{,_ENDPOINT,_TOKEN}` once, package-level `DefaultEndpoint` and `DefaultToken` vars for `-ldflags` injection. |
| `pkg/telemetry/config_test.go` | New file. Env-var precedence, `off`-only off-value rule, ldflag-fallback tests. |
| `pkg/telemetry/transport.go` | New file. `Transport` (or `Emitter`) with `Emit(ctx, *Event) error`, single package-level `*http.Client` with 2s timeout, error classification (`permanentErr` vs `transientErr`), `last-error.log` writer. |
| `pkg/telemetry/transport_test.go` | New file. `httptest.Server`-driven tests for 2xx/4xx/5xx/timeout/off and header/body conformance. |
| `pkg/telemetry/buffer.go` | New file. NDJSON buffer: `Append([]byte)`, `Drain(emit, max)`, 1 MB cap with FIFO eviction, corruption tolerance, `0600`/`0700` perms. |
| `pkg/telemetry/buffer_test.go` | New file. Cap+eviction, order preservation, drain-cap-per-call, truncated-trailing-line, perms tests. |
| `pkg/telemetry/emitter.go` | New file. Thin orchestration: combine `Config` + `Transport` + buffer; `Emitter.Emit(ctx, evt)` runs the non-blocking send + drain; `Emitter.Wait()` for the root command to await in-flight goroutines. |
| `pkg/telemetry/emitter_test.go` | New file. End-to-end orchestration tests: success drains buffer, 5xx routes to buffer, `Wait()` blocks until goroutines finish. |
| `pkg/telemetry/schema_test.go` | New file. Validates `testdata/event-skill-downloaded.json` against `testdata/event-v1.json` using a Go JSON-schema validator. |
| `pkg/telemetry/doc.go` | New file. Package doc summarizing the contract and pointing to `docs/telemetry-data-contract.md`. |
| `pkg/oci/pull.go` | Modified. Add `Emitter` field to `PullOptions` (nil = no-op); call it on the success branch after extraction with the populated `Event`. No new cobra deps. |
| `pkg/oci/pull_telemetry_test.go` | New file. Integration test with in-memory OCI registry + `httptest` collector verifying one-event-per-success and zero-on-failure. |
| `cmd/root.go` | Modified. Construct a process-scoped `*telemetry.Emitter`, expose it via context to subcommands, call `emitter.Wait()` after `Execute()` returns. |
| `cmd/root_test.go` | New file. Asserts `emitter.Wait()` is called before process exit; uses a stub emitter to verify ordering. |
| `cmd/add.go` | Modified. Pull the emitter out of context, attach it to `oci.PullOptions` with `source.command="add"`, `source.trigger="user"`. Both TUI and `--plain` paths. |
| `cmd/install.go` | Modified. Same as `add` but `command="install"`, `trigger="manifest"`. Emit per pulled skill; do NOT emit for already-present skills. |
| `pkg/tui/load/model.go` | Modified. Plumb the emitter into `LoadSkills`/the per-skill pull call so the TUI path produces the same telemetry as `--plain`. |
| `cmd/install_test.go` | New file. N-events-for-N-pulls, zero-events-when-all-present, TUI/plain parity. |
| `.github/workflows/release.yml` | Modified. Extend `LDFLAGS` to include `-X github.com/salaboy/skills-oci/pkg/telemetry.DefaultEndpoint=${{ secrets.TELEMETRY_ENDPOINT }}` and `-X github.com/salaboy/skills-oci/pkg/telemetry.DefaultToken=${{ secrets.TELEMETRY_TOKEN }}`, with empty-string fallback documented in a comment. |
| `.github/workflows/ci.yml` | Modified. Add an explicit step to run the schema-lockstep test (`go test ./pkg/telemetry/... -run TestGolden_ValidatesAgainstSchema`) so drift is caught in PRs. |
| `README.md` | Modified. Add a "Telemetry" section: what's collected, opt-out env var, link to wire contract. |
| `go.mod` / `go.sum` | Modified. Add `github.com/oklog/ulid/v2` (ULID generation) and a JSON-schema validator dep used in `schema_test.go` (candidate: `github.com/santhosh-tekuri/jsonschema/v5`). |
| `docs/telemetry-data-contract.md` | Reference only (read-only here); the producer must remain byte-for-byte conformant. |

### Notes

- All tests follow strict TDD per `CLAUDE.md`: write a failing test first, then minimum code to pass, then refactor. Table-driven where branchy.
- Tests live alongside the code (`event.go` + `event_test.go`, etc.) — same convention used by the rest of the repo's Go code.
- Run with `go test ./...` and `go vet ./...` before each commit; CI gates on both.
- All new tests use `t.TempDir()` and `httptest` — no live registry calls.
- Error wrapping uses `fmt.Errorf("...: %w", err)` to match the codebase.
- Conventional-commit subjects (e.g., `feat(telemetry): emit skill.downloaded events`) are expected per recent log style.

## Tasks

### [x] 1.0 Build the `pkg/telemetry` event model and `skill.downloaded` envelope

Establish the pure, IO-free core of the package: a `telemetry.Event` Go type whose JSON marshaling produces a wire-conformant body for `event_type: skill.downloaded` at `schema_version: 1`. This task adds the only new direct dependency expected (`github.com/oklog/ulid/v2`) and ships under strict TDD — every field rule from `docs/telemetry-data-contract.md` §"Field rules" maps to a failing test before the corresponding constructor/marshaler code is written. No HTTP, no filesystem, no cobra here; this layer must be exercisable from a `_test.go` file with zero external state. Maps to spec Unit 1 and to functional requirements in Unit 1.

#### 1.0 Proof Artifact(s)

- Test: `pkg/telemetry/event_test.go` golden-file test that marshals a fixed-input `Event` and asserts byte-equality with `pkg/telemetry/testdata/event-skill-downloaded.json` — demonstrates the envelope matches the documented wire shape exactly.
- Test: table-driven `TestEvent_IDAndTimestampFormats` asserts `event_id` matches `^[0-9A-HJKMNP-TV-Z]{26}$` (ULID) and `occurred_at` matches `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$` (RFC 3339 UTC, second precision) — demonstrates contract-conformant ID and timestamp formats.
- Test: `TestEvent_NeverContainsForbiddenSubstrings` seeds path, hostname, and env values into a captured fixture, marshals the event, and greps the body for any of them — demonstrates the privacy guarantee at the model layer.
- Test: `TestNewSkillDownloaded_RejectsMissingFields` asserts that a constructor called with empty `namespace`, `name`, `version`, `digest`, `registry`, `oci_ref`, `command`, or `trigger` returns a typed error — demonstrates the "all required" rule from the contract.
- CLI: `go test ./pkg/telemetry/... -run TestEvent -v` shows all envelope tests passing.

#### 1.0 Tasks

- [x] 1.1 (RED) Add `pkg/telemetry/event_test.go` with `TestEvent_GoldenBody` that constructs an `Event` from a fixed `SkillDownloadedInput` (deterministic `event_id`, `occurred_at`, all fields) and asserts `json.Marshal(evt)` is byte-equal to `pkg/telemetry/testdata/event-skill-downloaded.json`. Author the golden file by hand from the §"Wire shape" example in `docs/telemetry-data-contract.md`. Test must fail (file doesn't compile).
- [x] 1.2 (GREEN) Create `pkg/telemetry/event.go` with the `Event` struct, nested types (`ClientInfo`, `Actor`, `SkillPayload`, `SourceInfo`), and JSON tags matching the §"Wire shape" example exactly (snake_case keys, field order). Add `pkg/telemetry/doc.go` with a one-line package comment.
- [x] 1.3 (GREEN) Add `go.mod` dep `github.com/oklog/ulid/v2` (`go get github.com/oklog/ulid/v2`); commit `go.sum` in the same change.
- [x] 1.4 (GREEN) Implement `NewSkillDownloaded(input SkillDownloadedInput) (*Event, error)` that generates a ULID via `ulid.Make()` (using `time.Now()` + a monotonic entropy source), sets `OccurredAt = time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)`, fills `Client.{Name="skills-oci",Version,OS,Arch}` from inputs and `runtime.GOOS`/`runtime.GOARCH`, sets `Actor.Kind="anonymous"`, and copies the skill + source fields from input. Return error if any required input string is empty.
- [x] 1.5 (RED→GREEN) Add `TestEvent_IDAndTimestampFormats` (table-driven over 50 generated events) asserting `event_id` matches `^[0-9A-HJKMNP-TV-Z]{26}$` and `occurred_at` matches `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`. Make pass.
- [x] 1.6 (RED→GREEN) Add `TestNewSkillDownloaded_RejectsMissingFields` as a table-driven test that empties each of `namespace`, `name`, `version`, `digest`, `registry`, `oci_ref`, `command`, `trigger` in turn and asserts a typed `*FieldRequiredError` is returned with the offending field name.
- [x] 1.7 (RED→GREEN) Add `TestEvent_NeverContainsForbiddenSubstrings` that builds an `Event`, marshals it, and asserts the body does NOT contain `/Users/`, `\\`, the test machine's hostname (`os.Hostname()`), the value of `$HOME`, or any user-provided env var the test seeds.
- [x] 1.8 (REFACTOR) Inject the clock and ULID entropy via small unexported function-typed fields (or constructor params) so 1.1's golden test can pin deterministic values; default to wall-clock + crypto/rand in production.
- [x] 1.9 Run `go vet ./pkg/telemetry/...` and `go test ./pkg/telemetry/... -run TestEvent -v`; assert all pass. Commit with `feat(telemetry): add skill.downloaded event model`.

### [x] 2.0 Implement the best-effort HTTP transport with config and timeout

Add the smallest correct HTTP emitter: a package-level `*http.Client` with a 2-second timeout, an `Emit(ctx, event)` entry point that does `POST /v1/events` with `Authorization: Bearer <token>`, and a `Config` struct loaded once from `SKILLS_OCI_TELEMETRY{,_ENDPOINT,_TOKEN}` env vars (with compiled-in `-ldflags` fallbacks injected from `pkg/telemetry/config.go` package-level `var`s). Treat `SKILLS_OCI_TELEMETRY=off` as the only off value. Map `2xx` → success, `4xx` → drop (no retry, log to `last-error.log`), `5xx`/network/timeout → return a `transientErr` for Unit 3 to buffer. This task does NOT yet wire into `oci.Pull`; the proof is purely via `httptest.Server`. Maps to spec Unit 2.

#### 2.0 Proof Artifact(s)

- Test: `pkg/telemetry/transport_test.go::TestEmit_PostsExpectedBody` uses `httptest.NewServer` to assert one successful pull-simulation produces exactly one `POST /v1/events` with `Content-Type: application/json`, `Authorization: Bearer <token>`, and body byte-equal to the Unit 1 golden — demonstrates wire conformance end-to-end.
- Test: `TestEmit_4xxDropsNoRetry` — server returns `400`; emitter returns a typed `permanentErr`, does NOT buffer, and writes a single line to `<UserCacheDir>/skills-oci/telemetry/last-error.log`.
- Test: `TestEmit_5xxReturnsTransient` — server returns `500`; emitter returns a typed `transientErr` that Unit 3 buffer code can recognize.
- Test: `TestEmit_TimeoutBounded` — server sleeps 3s; emitter returns within ≤ 4.5s wall-clock with `transientErr` (the 2s emitter timeout plus generous CI scheduling slack).
- Test: `TestEmit_OffMakesNoNetworkCall` — with `SKILLS_OCI_TELEMETRY=off`, the `httptest.Server` is configured to `t.Fatal` if hit; no buffer file is created in `t.TempDir()`-rooted cache.
- Test: `TestConfig_LdflagFallback` — when env vars are unset, `LoadConfig` returns the compiled-in defaults set via package-level `var DefaultEndpoint` / `var DefaultToken`.
- CLI: `go vet ./pkg/telemetry/... && go test ./pkg/telemetry/... -v` shows all transport tests passing.

#### 2.0 Tasks

- [ ] 2.1 (RED) Add `pkg/telemetry/config_test.go::TestLoadConfig_EnvOverrides` (table-driven over env permutations including `SKILLS_OCI_TELEMETRY=off`, `=on`, unset, empty, arbitrary strings) asserting `Enabled`, `Endpoint`, and `Token` resolve per the spec. Use `t.Setenv` for isolation.
- [ ] 2.2 (GREEN) Create `pkg/telemetry/config.go` with `type Config struct { Enabled bool; Endpoint, Token string }`, package-level `var DefaultEndpoint, DefaultToken string` (set via `-ldflags`), and `LoadConfig() Config` that reads env vars exactly once into a `sync.Once`-guarded package-level cached value. Off only when `SKILLS_OCI_TELEMETRY=="off"`.
- [ ] 2.3 (RED→GREEN) Add `TestConfig_LdflagFallback`: unset all env vars, assign `DefaultEndpoint = "http://built-in"` and `DefaultToken = "builtin"` via test helper, call `LoadConfig`, assert returned values match. (Note: tests will overwrite the package vars; reset in `t.Cleanup`.)
- [ ] 2.4 (RED) Add `pkg/telemetry/transport_test.go::TestEmit_PostsExpectedBody` using `httptest.NewServer` whose handler captures method, path, headers, and body, then asserts `POST /v1/events`, `Content-Type: application/json`, `Authorization: Bearer <token>`, and a body matching the Unit 1 golden file byte-for-byte.
- [ ] 2.5 (GREEN) Create `pkg/telemetry/transport.go` with `type Transport struct { cfg Config; client *http.Client }`, constructor `NewTransport(cfg Config)` that sets `client.Timeout = 2*time.Second`, and `Emit(ctx context.Context, evt *Event) error` that marshals, posts to `cfg.Endpoint`, and returns `nil` on 2xx.
- [ ] 2.6 (RED→GREEN) Add `TestEmit_4xxDropsNoRetry` — server returns `400`. Assert: emitter returns `*PermanentError`, no retries occur (single hit), and after the call `<UserCacheDir>/skills-oci/telemetry/last-error.log` exists with one line containing the status code and event ID. Use `t.Setenv("XDG_CACHE_HOME", t.TempDir())` (Linux) or override `os.UserCacheDir()` via a small package-level test seam to redirect the cache dir.
- [ ] 2.7 (RED→GREEN) Add `TestEmit_5xxReturnsTransient` — server returns `500`; assert `errors.As(err, &transient)` where `transient *TransientError`. No retry inside `Emit`; buffer routing is Unit 3's concern.
- [ ] 2.8 (RED→GREEN) Add `TestEmit_TimeoutBounded` — server handler does `time.Sleep(3*time.Second)` (long enough to exceed the 2s emitter timeout but short enough to keep the test fast). Assert `Emit` returns with a `*TransientError` wrapping a deadline-exceeded error AND that the measured wall-clock elapsed time is ≤ 4.5s (2s emitter timeout + 2.5s CI scheduling slack). Use `time.Now()` deltas; the upper bound is a regression safety net, not a tight clock — avoid asserting a tight lower bound so a fast machine doesn't false-fail.
- [ ] 2.9 (RED→GREEN) Add `TestEmit_OffMakesNoNetworkCall` — `t.Setenv("SKILLS_OCI_TELEMETRY","off")`. The `httptest.Server`'s handler calls `t.Fatal("must not be hit")`. Assert: `Emit` returns nil immediately; no file under the redirected cache dir exists.
- [ ] 2.10 (REFACTOR) Extract error classification (`classifyHTTPStatus(code int) error`) and the `last-error.log` writer into small unexported helpers; cover with their own table-driven tests.
- [ ] 2.11 Run `go vet ./pkg/telemetry/... && go test ./pkg/telemetry/... -v`; assert all pass. Commit with `feat(telemetry): add HTTP transport and env-var config`.

### [ ] 3.0 Implement the local NDJSON buffer with cap, eviction, ordered flush

Add the persistent failure-tolerance layer: a `buffer` component that appends failed events as one JSON object per line to `<UserCacheDir>/skills-oci/telemetry/pending.ndjson`, enforces a 1 MB hard cap with FIFO eviction (oldest line dropped on overflow), tolerates a corrupt trailing line on read, and provides a `Drain(emit func(line []byte) error, max int)` API that drains up to 50 events in FIFO order on each call. File mode `0600`, parent mode `0700`. Re-sends preserve the original `event_id` so the collector's `(client_name, event_id)` dedup makes replays safe. This task uses `t.TempDir()` for all I/O tests and skips perm checks on Windows. Maps to spec Unit 3.

#### 3.0 Proof Artifact(s)

- Test: `pkg/telemetry/buffer_test.go::TestBuffer_CapAndEviction` writes 100 ~1 KB events to a forced-failure transport, then asserts `pending.ndjson` size ≤ 1 MB and that the *newest* events are retained (oldest evicted) — demonstrates cap + FIFO eviction.
- Test: `TestBuffer_DrainsInOrderOnSuccess` — 3 failed sends followed by a successful one drains all 3 buffered events in their original order, verified by collecting the `event_id` sequence at a `httptest` recorder.
- Test: `TestBuffer_DrainCapPerCall` — buffer of 60 entries drains exactly 50 on one successful call; the remaining 10 persist for the next.
- Test: `TestBuffer_TruncatedTrailingLineSkipped` — buffer file whose last line is truncated mid-JSON loads earlier lines and drops the bad trailing line without erroring.
- Test: `TestBuffer_FilePermissions` (Unix-only via `runtime.GOOS != "windows"` guard) asserts `pending.ndjson` mode is `0600` and parent directory is `0700`.
- Test: `TestBuffer_PreservesEventID` — a buffered event re-sent later has the same `event_id` as when it was first generated.
- CLI: `go test ./pkg/telemetry/... -run TestBuffer -v` shows all buffer tests passing.

#### 3.0 Tasks

- [ ] 3.1 (RED) Add `pkg/telemetry/buffer_test.go::TestBuffer_AppendThenRead` that creates a `Buffer` rooted at `t.TempDir()`, appends 3 distinct lines, then reads them back in order via an iterator. Test fails until 3.2 lands.
- [ ] 3.2 (GREEN) Create `pkg/telemetry/buffer.go` with `type Buffer struct { dir string; maxBytes int64; perFlush int }`, constructor `NewBuffer(dir string) *Buffer` defaulting to `maxBytes=1<<20` and `perFlush=50`. Implement `Append(line []byte) error` (creates dir at `0700`, file at `0600`, `O_APPEND|O_WRONLY|O_CREATE`, ensures trailing `\n`).
- [ ] 3.3 (RED→GREEN) Implement `iterLines() ([]line, error)` returning entries in FIFO order; tolerate a truncated/invalid final line by dropping it without error.
- [ ] 3.4 (RED) Add `TestBuffer_CapAndEviction` that writes 100 ~1 KB entries (so total > 1 MB). Assert post-write file size ≤ 1 MB AND newest entry's `event_id` is present, oldest is not.
- [ ] 3.5 (GREEN) Implement the cap+eviction in `Append`: if `currentSize + len(line)+1 > maxBytes`, read all lines, drop oldest-first until the new line fits, rewrite file atomically (write to `pending.ndjson.tmp`, `os.Rename`).
- [ ] 3.6 (RED) Add `TestBuffer_DrainsInOrderOnSuccess` and `TestBuffer_DrainCapPerCall`. Both use a stub `emit` callback that records calls; assert order and per-call cap of 50.
- [ ] 3.7 (GREEN) Implement `Drain(ctx, emit func(ctx, line []byte) error, max int) (drained int, err error)`: read in order, call `emit` for each, stop after `max` successes; if `emit` returns a `*TransientError`, stop and keep undrained entries (and the current one) in the file; if it returns `*PermanentError`, drop the bad line and continue (it's a producer bug, not a transient failure).
- [ ] 3.8 (RED→GREEN) Add `TestBuffer_TruncatedTrailingLineSkipped`: write 3 valid lines then append a partial JSON byte sequence with no `\n`; assert `Drain` processes the 3 valid lines and removes the partial bytes on rewrite.
- [ ] 3.9 (RED→GREEN) Add `TestBuffer_FilePermissions` (skipped on Windows): assert `os.Stat(filepath.Join(dir,"pending.ndjson")).Mode().Perm() == 0o600` and parent dir is `0o700`.
- [ ] 3.10 (RED→GREEN) Add `TestBuffer_PreservesEventID`: append an event line containing a known ULID, re-read via `Drain`, assert the received line is byte-equal to what was written (so the original `event_id` survives intact).
- [ ] 3.11 (REFACTOR) Extract atomic-rewrite helper (`rewriteAtomic(path string, lines [][]byte) error`); share between eviction and drain paths.
- [ ] 3.12 Run `go vet ./pkg/telemetry/... && go test ./pkg/telemetry/... -v`. Commit with `feat(telemetry): add pending.ndjson buffer with cap and ordered drain`.

### [ ] 4.0 Wire emission into `oci.Pull`, `cmd/add`, and `cmd/install`

Connect the telemetry package to the actual success branch of `pkg/oci/pull.go` without introducing cobra coupling. Approach: add an `Emitter` interface (or function-typed field) to `oci.PullOptions` that defaults to a no-op when `nil`, and call it from the success branch after extraction completes. `cmd/add.go` constructs the emitter with `source.command="add"`, `source.trigger="user"`. `cmd/install.go` constructs the emitter with `source.command="install"`, `source.trigger="manifest"` and emits one event per skill it pulls — NOT for skills that are already present and NOT for failed pulls. A `sync.WaitGroup` (or equivalent) in `cmd/root.go`'s post-`Execute` path waits for any in-flight emission goroutines to settle (success, buffered, or 2s timeout) before the process exits. Both the TUI and `--plain` paths of `add` and `install` get the same wiring. Maps to spec Unit 4.

#### 4.0 Proof Artifact(s)

- Test: `pkg/oci/pull_telemetry_test.go::TestPull_EmitsOneEventOnSuccess` uses an in-memory OCI registry (`oras-go` memory store-backed) plus an `httptest.Server` acting as the collector, runs `oci.Pull` with an `Emitter` configured, and asserts exactly one event arrives with the expected `oci_ref`, `digest`, `source.command="add"`, `source.trigger="user"`.
- Test: `TestPull_EmitsZeroEventsOnFailure` — a registry that returns 404 produces zero events on the collector.
- Test: `cmd/install_test.go::TestInstall_EmitsPerPulledSkill` — 3 missing skills produce 3 events with `source.trigger="manifest"`; a re-run where all 3 are already present produces 0 events (cache-hit no-op).
- Test: `cmd/install_test.go::TestInstall_PlainAndTUIParity` — running `install` with `--plain` and without produces the same set of events on the collector (TUI vs plain parity per repo standard).
- Test: `cmd/root_test.go::TestRoot_WaitsForEmissionBeforeExit` — a stub emitter that takes ~1s to complete is awaited by the process exit path.
- CLI: `SKILLS_OCI_TELEMETRY_ENDPOINT=http://127.0.0.1:PORT skills-oci add localhost:5000/example-skill:1.0.0 --plain-http --plain` against a locally-run debug HTTP echo server prints one captured event.
- CLI: `go test ./... -v` (full test suite) passes and `go vet ./...` is clean.

#### 4.0 Tasks

- [ ] 4.1 (RED) Add `pkg/telemetry/emitter_test.go::TestEmitter_SuccessDrainsBuffer`: construct an `Emitter` with a buffer pre-seeded with 2 entries and a `httptest.Server` returning 202; call `Emitter.Emit(ctx, evt)`, then `Emitter.Wait()`; assert the server received exactly 3 events (the new one + 2 drained) and the buffer is empty.
- [ ] 4.2 (GREEN) Create `pkg/telemetry/emitter.go` with `type Emitter struct { cfg Config; tx *Transport; buf *Buffer; wg sync.WaitGroup }`, constructor `New(cfg Config) *Emitter`. `Emit(evt)` returns immediately when `!cfg.Enabled`; otherwise launches a goroutine guarded by `wg.Add(1)` that calls `tx.Emit`, on `*TransientError` calls `buf.Append`, then calls `buf.Drain(ctx, tx.Emit, 50)`. `Wait()` blocks on `wg.Wait()`.
- [ ] 4.3 (RED→GREEN) Add `TestEmitter_TransientRoutesToBuffer`: server returns `500`; after `Emit`+`Wait`, assert one new line in `pending.ndjson` with the expected `event_id`.
- [ ] 4.4 (RED→GREEN) Add `TestEmitter_OffIsNoOp`: with `SKILLS_OCI_TELEMETRY=off`, the server's handler fails the test if hit; assert `Emit`+`Wait` return without I/O and no buffer file is created.
- [ ] 4.5 (RED) Add `pkg/oci/pull_telemetry_test.go::TestPull_EmitsOneEventOnSuccess` using oras-go's `memory.Store`-backed registry plus `httptest`. The test constructs an `Emitter` pointed at the test server, passes it into `PullOptions`, runs `Pull`, and after `emitter.Wait()` asserts the server received exactly one event with the expected `oci_ref`, `digest`, `source.command="add"`, `source.trigger="user"`.
- [ ] 4.6 (GREEN) Modify `pkg/oci/pull.go`: add `Emitter telemetry.Emitter` field (interface type `Emitter interface { Emit(SkillDownloadedInput) }`) to `PullOptions`. On the success branch (after extraction and after computing `extractPath`), if `opts.Emitter != nil`, build a `telemetry.SkillDownloadedInput` from the `PullResult` plus the caller-supplied `Command` and `Trigger` fields (also added to `PullOptions`), and call `opts.Emitter.Emit(input)`. Ensure no new cobra import in `pkg/oci`.
- [ ] 4.7 (RED→GREEN) Add `TestPull_EmitsZeroEventsOnFailure`: registry returns 404. Assert server received zero requests after `emitter.Wait()`.
- [ ] 4.8 (RED) Add `cmd/root_test.go::TestRoot_WaitsForEmissionBeforeExit` with a stub `Emitter` whose `Emit` increments a counter inside a 50 ms `time.Sleep`. Run a no-op subcommand that calls `Emit` once; assert `Wait()` was called before the test's observed exit point and the counter reflects the completion.
- [ ] 4.9 (GREEN) Modify `cmd/root.go`: in `NewRootCmd`, construct `emitter := telemetry.New(telemetry.LoadConfig())`. Stash it in the cobra command context (`context.WithValue`). Add a `PersistentPostRunE` (or equivalent) that calls `emitter.Wait()`. Update `main.go` only if needed to pass `cmd.Context()` through.
- [ ] 4.10 (GREEN) Modify `cmd/add.go`: in both `runAddPlain` and the TUI `runAdd` path, fetch the emitter from `cmd.Context()`, attach to `oci.PullOptions` with `Command:"add"`, `Trigger:"user"`.
- [ ] 4.11 (RED) Add `cmd/install_test.go::TestInstall_EmitsPerPulledSkill` and `TestInstall_NoEventsWhenAlreadyPresent`. Use a `skills.json` fixture with 3 entries and an `httptest`-backed in-memory registry; collect events on a second `httptest.Server`. Assert per-pulled count and zero-on-cache-hit.
- [ ] 4.12 (GREEN) Modify `cmd/install.go` and `pkg/tui/load/model.go`: thread the emitter through `LoadSkills` (new last param), set `Command:"install"`, `Trigger:"manifest"`, and ensure the cache-hit branch (skill dir already present) does NOT call `Emit`. Both `--plain` and TUI paths must produce identical event sets.
- [ ] 4.13 (RED→GREEN) Add `TestInstall_PlainAndTUIParity` that runs the install flow twice — once via `runInstallPlain`, once by driving `load.LoadSkills` directly with the same project dir state — and asserts the captured event sets are equal.
- [ ] 4.14 (REFACTOR) Extract the `(PullResult, command, trigger) -> SkillDownloadedInput` mapping into a small helper in `pkg/oci` or `pkg/telemetry` so `add` and `install` share it.
- [ ] 4.15 Run `go vet ./... && go test ./... -v` from repo root; assert clean. Commit with `feat(telemetry): wire skill.downloaded emission into add and install`.

### [ ] 5.0 Release pipeline, README docs, and schema-lockstep CI gate

Productionize the feature: extend `.github/workflows/release.yml` to inject `pkg/telemetry.DefaultEndpoint` and `pkg/telemetry.DefaultToken` via `-ldflags -X` from release-time secrets (with empty-string fallback when secrets are unset, which keeps stock builds effectively off until the collector is stood up — see spec Open Question #1). Vendor the collector's canonical JSON Schema as `pkg/telemetry/testdata/event-v1.json` and add a CI step that validates the Unit 1 golden body against this schema, failing the build on drift (spec Open Question #2). Update `README.md` with a "Telemetry" section documenting what's collected, the `SKILLS_OCI_TELEMETRY=off` opt-out, and a link to the wire contract. Maps to spec Goals and the spec's three Open Questions.

#### 5.0 Proof Artifact(s)

- Diff: `.github/workflows/release.yml` shows new `-X .../pkg/telemetry.DefaultEndpoint=...` and `-X .../pkg/telemetry.DefaultToken=...` ldflags reading from `secrets.TELEMETRY_ENDPOINT` / `secrets.TELEMETRY_TOKEN`.
- Diff: `.github/workflows/ci.yml` shows a new step running `go test ./pkg/telemetry/... -run TestGolden_ValidatesAgainstSchema`.
- Test: `pkg/telemetry/schema_test.go::TestGolden_ValidatesAgainstSchema` loads `testdata/event-v1.json` and `testdata/event-skill-downloaded.json` and asserts the golden validates.
- File: `pkg/telemetry/testdata/event-v1.json` exists and matches the collector's canonical schema (sourced from the skills-platform repo).
- Diff: `README.md` has a new "Telemetry" section.
- CLI: `SKILLS_OCI_TELEMETRY=off skills-oci add ghcr.io/myorg/skills/example:1.0.0 --plain-http` produces zero outbound TCP connections and zero filesystem mutations under `<UserCacheDir>/skills-oci/telemetry/`.

#### 5.0 Tasks

- [ ] 5.1 (RED) Add `pkg/telemetry/schema_test.go::TestGolden_ValidatesAgainstSchema` that uses `github.com/santhosh-tekuri/jsonschema/v5` to compile `testdata/event-v1.json` and assert `testdata/event-skill-downloaded.json` validates against it. Test fails because the schema file doesn't exist yet.
- [ ] 5.2 (GREEN) Add `pkg/telemetry/testdata/event-v1.json` — minimum viable JSON Schema covering the fields documented in §"Field rules" (string formats, regex for `event_type`, `namespace`, `name`, required keys, length caps). Source from the skills-platform contract doc (which references `collector/schemas/event-v1.json`); copy any available canonical schema. Document the upstream source in a sibling `README.md` under `pkg/telemetry/testdata/`.
- [ ] 5.3 (GREEN) Add `go.mod` dep `github.com/santhosh-tekuri/jsonschema/v5`; run `go mod tidy`; commit `go.sum`.
- [ ] 5.4 Modify `.github/workflows/ci.yml`: add an explicit step `- name: Validate event schema lockstep` running `go test ./pkg/telemetry/... -run TestGolden_ValidatesAgainstSchema -v`. (Note: this is technically covered by the existing `go test ./...` step, but the explicit name makes drift readable in PR CI logs.)
- [ ] 5.5 Modify `.github/workflows/release.yml`: extend `LDFLAGS` to:
      `-s -w -X main.version=${VERSION} -X github.com/salaboy/skills-oci/pkg/telemetry.DefaultEndpoint=${TELEMETRY_ENDPOINT:-} -X github.com/salaboy/skills-oci/pkg/telemetry.DefaultToken=${TELEMETRY_TOKEN:-}`
      reading `TELEMETRY_ENDPOINT: ${{ secrets.TELEMETRY_ENDPOINT }}` and `TELEMETRY_TOKEN: ${{ secrets.TELEMETRY_TOKEN }}` as `env` on the Build step. Add a comment documenting that empty defaults keep stock builds effectively off until the collector is stood up.
- [ ] 5.6 Modify `README.md`: append a "Telemetry" section after "Authentication" covering: what's collected (`skill.downloaded` events per the wire contract), what's never sent (verbatim from the contract's "What is NEVER sent" list), the three env vars and their defaults, the opt-out one-liner `SKILLS_OCI_TELEMETRY=off`, and a link to `docs/telemetry-data-contract.md`.
- [ ] 5.7 Manual verification (CLI proof artifact): run `SKILLS_OCI_TELEMETRY=off ./skills-oci add localhost:5000/example:1.0.0 --plain-http --plain` against a local registry while observing with `lsof -i -P -nP -p $(pgrep skills-oci)` (or capture the command transcript). Confirm no outbound connection except the registry pull and no creation of `<UserCacheDir>/skills-oci/telemetry/` directory. Paste the transcript under `docs/specs/01-spec-telemetry-emission/proofs/optout-cli.txt`.
- [ ] 5.8 Run `go vet ./... && go test ./... -v && go build ./...`. Commit with `feat(telemetry): release-time defaults, schema lockstep CI, README opt-out docs`.

---

**Coverage map (each spec functional requirement → parent task):**

| Spec Unit | Functional Requirement (abbreviated) | Parent Task |
|---|---|---|
| 1 | `Event` type marshals to wire-conformant body | 1.0 |
| 1 | `schema_version: 1`, `event_type: "skill.downloaded"`, ULID `event_id` | 1.0 |
| 1 | `occurred_at` RFC 3339 UTC second precision | 1.0 |
| 1 | `client.{name,version,os,arch}` populated correctly | 1.0 |
| 1 | `actor.kind: "anonymous"` | 1.0 |
| 1 | `skill.{namespace,name,version,digest,registry,oci_ref}` populated | 1.0 |
| 1 | `source.{command,trigger}` populated | 1.0 / 4.0 (wiring) |
| 1 | Reject construction with missing required field | 1.0 |
| 2 | Read env vars once at startup | 2.0 |
| 2 | `off` is the only off value | 2.0 |
| 2 | Compiled-in defaults via `-ldflags` | 2.0 (var) + 5.0 (release wiring) |
| 2 | `POST /v1/events` with bearer auth | 2.0 |
| 2 | 2-second hard timeout | 2.0 |
| 2 | Non-blocking relative to command return | 4.0 (WaitGroup) |
| 2 | 2xx success / 4xx drop+log / 5xx+network buffer | 2.0 (classify) + 4.0 (route) |
| 2 | Off → no HTTP, no buffer file | 2.0 |
| 3 | Persist failed events to `pending.ndjson` | 3.0 |
| 3 | Buffer file `0600`, parent `0700` | 3.0 |
| 3 | 1 MB hard cap, oldest-line eviction | 3.0 |
| 3 | Drain up to 50 in FIFO order per success | 3.0 |
| 3 | Preserve `event_id` on re-send | 3.0 |
| 3 | Tolerate truncated trailing line | 3.0 |
| 4 | One event per successful pull from `oci.Pull` success branch | 4.0 |
| 4 | No events on failure / cache-hit / non-pull commands | 4.0 |
| 4 | `add` → `command=add, trigger=user` | 4.0 |
| 4 | `install` → `command=install, trigger=manifest`, N events for N pulls | 4.0 |
| 4 | `oci_ref` is the fully-qualified expanded form | 4.0 |
| 4 | No new cobra dep in `pkg/oci/pull.go` | 4.0 |
| Cross | Release-time ldflags injection | 5.0 |
| Cross | Schema lockstep CI gate | 5.0 |
| Cross | README opt-out documentation | 5.0 |
| Cross | TUI/`--plain` parity for emission | 4.0 |

Every functional requirement maps to at least one parent task with at least one planned test or CLI proof artifact.
