# How skills-oci reports telemetry, and what an event looks like on the wire

**Source of truth**: an HTTP endpoint owned by the consumer of these events (today: skills-platform). The CLI does `POST <endpoint>/v1/events` with a JSON body. Endpoint URL, auth token, and on/off are configured via environment variables — see [Configuration](#configuration).

**Who emits**: the `skills-oci` CLI, on specific lifecycle events. Today the only event type is `skill.downloaded`. Emission is best-effort and non-blocking: failures never fail the user-facing command. Events that fail to send are buffered locally and flushed on the next successful call.

**Who consumes**: a collector service operated by whoever runs the endpoint. The collector's storage shape, query API, dashboards, retention, and operational details are out of scope here — this contract covers only the wire shape the CLI commits to. For the consumer-side contract used by Liatrio's hosted collector, see [`skills-platform/docs/telemetry-data-contract.md`](https://github.com/liatrio/skills-platform/blob/main/docs/telemetry-data-contract.md).

**What's in scope today**: skill downloads. Other lifecycle events (skill removed, skill invoked, install-loop summaries) are deferred. The envelope is designed so new `event_type` values are an additive change, not a breaking one.

## Wire shape

`POST /v1/events`
`Content-Type: application/json`
`Authorization: Bearer <token>`

Body — one envelope object containing one event:

```json
{
  "schema_version": 1,
  "event_id": "01HM3K9QZX7N8T6BVCQ2KX3RZA",
  "event_type": "skill.downloaded",
  "occurred_at": "2026-05-18T17:22:00Z",
  "client": {
    "name": "skills-oci",
    "version": "0.1.0",
    "os": "darwin",
    "arch": "arm64"
  },
  "actor": {
    "kind": "anonymous"
  },
  "skill": {
    "namespace": "liatrio-labs",
    "name": "example-skill",
    "version": "1.2.0",
    "digest": "sha256:abcd1234...",
    "registry": "ghcr.io",
    "oci_ref": "ghcr.io/liatrio-labs/skills/example-skill:1.2.0"
  },
  "source": {
    "command": "add",
    "trigger": "user"
  }
}
```

Response: `202 Accepted` with an empty body. `4xx` is a client bug (bad schema, bad token) and is not retried. `5xx` and network errors trigger local buffering and later retry.

## Field rules

### Envelope (every event, every type)

- **`schema_version`** — integer, currently `1`. Bumped only on breaking changes (see [Schema versioning](#schema-versioning)).
- **`event_id`** — [ULID](https://github.com/ulid/spec), client-generated. Doubles as the idempotency key; the collector MUST dedupe on `(client.name, event_id)` so buffered replays are safe.
- **`event_type`** — string, dotted `<noun>.<past-tense-verb>`. Today only `skill.downloaded`. Collectors MUST accept and store unknown types verbatim; consumers MAY ignore types they don't recognize. New types are added without bumping `schema_version`.
- **`occurred_at`** — RFC 3339, UTC, `Z` suffix, second precision (e.g. `2026-05-18T17:22:00Z`). The moment the event happened on the client, not when the server received it.
- **`client`** — what produced the event.
  - `name` (required) — fixed string `skills-oci` for events from this CLI.
  - `version` (required) — the CLI's own version string (matches `skills-oci --version`).
  - `os` (required) — `runtime.GOOS` value (`darwin`, `linux`, `windows`).
  - `arch` (required) — `runtime.GOARCH` value (`amd64`, `arm64`).
- **`actor`** — who triggered the action.
  - `kind` (required) — currently always `anonymous`. Reserved values for future use: `github_user`, `service_account`. When a non-anonymous kind ships, it adds an `id_hash` field (SHA-256 of the underlying identifier); the raw identifier is never sent.
- **`source`** — what part of the CLI emitted the event.
  - `command` (required) — the cobra subcommand that drove the action (e.g. `add`, `install`).
  - `trigger` (required) — one of `user` (the user typed the command directly) or `manifest` (the `install` loop pulled this skill while walking `skills.json`).

### Skill payload (for `event_type: skill.downloaded`)

All fields required and non-null.

- **`namespace`**, **`name`** — match `^[a-z0-9]+(?:-[a-z0-9]+)*$`, same rules as [catalog-data-contract.md](https://github.com/liatrio/skills-platform/blob/main/docs/catalog-data-contract.md).
- **`version`** — [semver 2.0.0](https://semver.org/), the resolved version actually pulled (never `latest` or a tag alias).
- **`digest`** — full manifest digest as `sha256:<hex>`. This is the only field that uniquely identifies *exactly which bytes* were downloaded; two events with the same `version` but different `digest` mean the tag was re-pushed.
- **`registry`** — registry hostname (today always `ghcr.io`).
- **`oci_ref`** — the fully-qualified reference as it resolved, tag included (e.g. `ghcr.io/<namespace>/skills/<name>:<version>`). Not necessarily what the user typed — if the user typed a short ref, this is the expanded form.

The `skill` object is omitted from event bodies whose `event_type` is not `skill.downloaded`.

### Catalog payload (for `event_type: catalog.synced`)

Emitted by `skills-oci catalog sync` once per per-entry outcome — every entry produces exactly one event regardless of whether it succeeded, failed, or was skipped. The envelope is identical to `skill.downloaded`; the `skill` object is omitted and a `catalog` object is included instead. `source.command` is always `catalog sync`.

```json
{
  "schema_version": 1,
  "event_id": "...",
  "event_type": "catalog.synced",
  "occurred_at": "2026-05-22T18:30:14Z",
  "client": { "...": "..." },
  "actor":  { "kind": "anonymous" },
  "catalog": {
    "name": "create-skill",
    "internal_ref": "ghcr.io/liatrio/skills/create-skill",
    "tag": "v1.0.0",
    "commit": "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
    "digest": "sha256:abcd1234...",
    "upstream_repo": "anthropics/skills",
    "outcome": "synced"
  },
  "source": { "command": "catalog sync", "trigger": "user" }
}
```

Field rules:

- **`name`** — local catalog entry name from `catalog.json` (need not match the upstream skill name).
- **`internal_ref`** — destination OCI ref without tag (matches the catalog entry's `internal_ref`).
- **`tag`** — version tag derived from `catalog.json`'s `version` field; the same string used in the pushed `<internal_ref>:<tag>`.
- **`commit`** — upstream 40-hex Git SHA-1 commit pinned in `catalog.json`. Immutable; the load-bearing audit property.
- **`digest`** — OCI manifest digest of the pushed artifact (`sha256:<hex>`). **Empty string for `outcome=failed` or `outcome=skipped`** since no manifest was written in those cases.
- **`upstream_repo`** — bare `<owner>/<repo>` slug consumed by Renovate.
- **`outcome`** — `synced` | `failed` | `skipped`. Failed and skipped entries still emit events so the analytics view of "what happened to this skill" is complete.
- **`source.trigger`** — `user` (interactive run) or `manifest` (CI / cron / scripted run from `catalog.json`).

The `catalog` object is omitted from event bodies whose `event_type` is not `catalog.synced`.

`skill.downloaded` semantics are unchanged by this addition.

## What is NEVER sent

The CLI is designed so the following can never appear in an event, even by accident:

- File paths, project directory names, working directory, hostname.
- Skill body, `SKILL.md` contents, manifest contents, or any user files.
- Environment variables (other than the explicit telemetry config), IP, MAC address.
- GitHub tokens, registry credentials, any secret.
- Raw GitHub login, email, or any direct identifier. The forward-looking `actor.id_hash` is a SHA-256 of the identifier; the underlying value is never transmitted.

If a future event type would require any of the above, that's a contract change — discussed and documented here first, not added ad-hoc.

## Configuration

The CLI is controlled by three environment variables. All are read once at startup.

| Variable | Default | Effect |
|---|---|---|
| `SKILLS_OCI_TELEMETRY` | `on` | `off` disables emission. Any other value (including unset) leaves it on. |
| `SKILLS_OCI_TELEMETRY_ENDPOINT` | *(release builds: baked-in via `-ldflags`; source builds: empty → no emission)* | Full URL of the collector, including `/v1/events`. Setting this env var overrides any compiled-in default (useful for self-hosted deployments or local testing). |
| `SKILLS_OCI_TELEMETRY_TOKEN` | *(release builds: baked-in via `-ldflags`; source builds: empty)* | Bearer token sent in the `Authorization` header. Shared anti-abuse value, not a secret — see [Auth model](#auth-model). Setting this env var overrides any compiled-in default. |

**Behavior depends on how the binary was built.**

- **Release builds** (downloaded from GitHub Releases) ship with `ENDPOINT` and `TOKEN` injected at build time via `-ldflags`. Telemetry is **opt-out / on by default**; emission goes to the project's hosted collector. Users set `SKILLS_OCI_TELEMETRY=off` to disable, or override either env var to redirect.
- **Source builds** (`go build`, `go install`, or `go run` without the release `-ldflags`) leave both `ENDPOINT` and `TOKEN` empty. The transport treats an empty endpoint as a no-op, so telemetry is **effectively off** unless the user explicitly sets `SKILLS_OCI_TELEMETRY_ENDPOINT` (and usually `SKILLS_OCI_TELEMETRY_TOKEN`).

A future `skills-oci config telemetry off` subcommand may persist the choice to user config; that surface is deferred and out of scope here.

### Auth model

The token gates "this request came from a real, configured CLI build" — it is shared, not per-user, and the collector treats it as anti-abuse, not identity. Identity (when added) goes in `actor`.

## Transport and reliability

- **Timeout**: hard 2-second timeout on the HTTP call. Events that exceed it are buffered locally.
- **Non-blocking**: emission happens after the user-facing operation succeeds; an event failure never fails the command and never delays its visible output by more than the timeout above.
- **Buffering**: failed events (`5xx`, network error, timeout) are appended as one JSON object per line to `<UserCacheDir>/skills-oci/telemetry/pending.ndjson` (path follows Go's `os.UserCacheDir()` — `~/Library/Caches/...` on macOS, `~/.cache/...` on Linux, `%LocalAppData%\...` on Windows).
- **Flush**: on each successful send, the CLI drains up to 50 buffered events before returning. Order is preserved.
- **Cap**: the buffer file is capped at 1 MB; when full, the oldest line is dropped per new write. Telemetry never grows unbounded on disk.
- **No retry-on-4xx**: a `400`/`401`/`403`/`422` means the collector rejected the schema or auth. The event is dropped and a single line is written to the CLI's debug log; this is a producer bug to fix, not a transient failure to retry.

## Schema versioning

- **Additive changes** do not bump `schema_version`. This includes: new optional envelope fields, new `event_type` values, new fields in a payload object, new enum values for `actor.kind` or `source.trigger`. Older collectors store the new fields verbatim and older consumers ignore them.
- **Breaking changes** bump `schema_version`: removed fields, renamed fields, changed types, changed semantics of an existing field. Producer and collector move together; the collector accepts the current version *and* the previous one for one deprecation window.

There is no `schema_version: 0`. The first published version of this contract is `1`.

## Reference: today's `skill.downloaded` event

Emitted exactly once per **successful** skill artifact pull, after extraction completes and before the user-facing command returns. Specifically:

- **Emitted from**: the pull path in `pkg/oci/pull.go`, on the success branch only.
- **Not emitted on**: failed pulls, dry-runs, cache hits where no network fetch occurred, or commands that don't pull (e.g. `verify`, `remove`).
- **`source.command`**: the cobra subcommand that drove the pull — `add` for one-off, `install` for manifest-driven.
- **`source.trigger`**: `user` when the user typed the command directly; `manifest` when the `install` loop pulled this skill while walking `skills.json`.

One pull = one event. The `install` command emits N events for N pulled skills, not one summary event.
