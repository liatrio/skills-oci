# pkg/telemetry/testdata

Files under this directory are reference fixtures for the telemetry wire
contract documented in [`docs/telemetry-data-contract.md`](../../../docs/telemetry-data-contract.md).

| File | Purpose |
| --- | --- |
| `event-skill-downloaded.json` | Canonical golden body for `event_type: skill.downloaded` at `schema_version: 1`. Used by `TestEvent_GoldenBody` to assert byte-equality of `json.Marshal(*Event)`. Synthetic values only — no real digests, namespaces, or registries. |
| `event-v1.json` | JSON Schema (draft 2020-12) describing the v1 envelope. Used by `TestGolden_ValidatesAgainstSchema` to assert that the producer-side golden continues to validate against the published contract; CI fails on drift. Kept in lockstep with the collector's canonical schema at `liatrio/skills-platform:collector/schemas/event-v1.json` (reference only). |

If the wire contract changes, update **both** files together and bump the
contract version (`schema_version`) in lockstep with the collector.
