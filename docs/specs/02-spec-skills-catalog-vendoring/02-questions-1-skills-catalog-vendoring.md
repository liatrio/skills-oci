# 02 Questions Round 1 - Skills Catalog Vendoring

Please answer each question below (select one or more options, or add your own notes). Feel free to add additional context under any question.

These are the only items I could not infer from the PRD. Everything else in the spec will come straight from `02-spec-skills-catalog-vendoring.md` without re-elicitation.

## 1. Telemetry emission ordering with parallel sync

`catalog sync` runs up to N entries in parallel (default 4). The existing telemetry pipeline (`pkg/telemetry/`) was designed around sequential per-pull emission. When 4 entries finish near-simultaneously, how should `catalog.synced` events be emitted?

- [ ] (A) Emit each event immediately as the entry completes — accepts up to N concurrent HTTP POSTs from one CLI process; simplest code, matches the "best-effort" telemetry philosophy
- [ ] (B) Serialize emissions through a single channel after each entry completes — one POST at a time, preserves arrival order on the collector, slightly more code, no concurrent network on the telemetry path
- [ ] (C) Buffer all events in memory until the run completes, then flush sequentially — clean ordering by `outcome` if desired, but events lost on SIGINT mid-run
- [ ] (D) Other (describe)

## 2. `pkg/tui/catalog/` scope for v1

The PRD says the TUI "mirrors `pkg/tui/add/`" with one row per entry across states `queued / cloning / pushing / done / failed / skipped`. The existing `pkg/tui/add/` is a single-skill flow. A multi-row TUI is meaningfully more work than a single-row one. How much TUI polish do you want in v1?

- [ ] (A) Minimum viable: one line per entry, plain-text status updates ("create-skill: cloning"), no spinners, no fancy layout. Plain-mode (`--plain`) output is the canonical UX; TUI is a nice-to-have wrapper
- [ ] (B) Full per-row TUI: spinner per active row, color-coded states, summary footer — feature-parity with `pkg/tui/add/`'s polish level
- [ ] (C) Defer the TUI entirely — `catalog sync` is plain-text only in v1; add a TUI in a follow-up PRD when there's real adoption signal
- [ ] (D) Other (describe)

## 3. Test fixture strategy for `pkg/scm/`

The PRD mentions both options for upstream-repo test fixtures. The choice affects test infrastructure size and how reliably tests reproduce GitHub's behavior.

- [ ] (A) `httptest.Server` serving the smart-HTTP protocol — fully in-memory, faithful to real GitHub fetches over HTTPS, more code to set up
- [ ] (B) Temp on-disk repo accessed via `file://` — simpler setup (just `git init` + commit fixture files), but doesn't exercise the HTTP path, so won't catch HTTP-specific bugs (auth headers, redirects)
- [ ] (C) Both — use `file://` for happy-path correctness tests and `httptest.Server` for HTTP-edge-case tests (auth, 404, timeout)
- [ ] (D) Other (describe)

## 4. User-level `.skills-oci.yaml` fallback

The PRD specifies a project-level `.skills-oci.yaml` (next to `catalog.json`). Some Go CLIs also support a user-level override at `$XDG_CONFIG_HOME/skills-oci/config.yaml` or `~/.skills-oci.yaml` so a developer's personal defaults flow across all their projects. Should v1 support this?

- [ ] (A) Project-level only — keeps the model simple, all settings live in version control, no surprises from per-developer config
- [ ] (B) Project-level + user-level fallback — user-level fills in keys the project file omits; precedence is `--flag` > env var > project file > user file > error
- [ ] (C) User-level only — there is no `.skills-oci.yaml` in the project; all defaults are personal
- [ ] (D) Other (describe)
