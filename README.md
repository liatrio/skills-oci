# skills-oci

A CLI tool for packaging, pushing, and managing AI agent skills as OCI artifacts, following the [Agent Skills OCI Artifacts Specification](https://github.com/ThomasVitale/agents-skills-oci-artifacts-spec).

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) for an interactive terminal experience.

## Installation

### Homebrew (macOS and Linux)

```bash
cd ~ && brew install liatrio/taproom/skills-oci && cd -
```

`brew upgrade skills-oci` picks up new releases automatically.

### Go install

```bash
go install github.com/liatrio/skills-oci@latest
```

### Build from source

```bash
git clone https://github.com/liatrio/skills-oci.git
cd skills-oci
go build -o skills-oci .
```

## What is a Skill?

A skill is a directory containing a `SKILL.md` file with YAML frontmatter that describes what the skill does, along with optional supporting files like scripts and references. Here is an example skill directory:

```
my-skill/
  SKILL.md
  scripts/
    create-pr.sh
  references/
    REFERENCE.md
```

The `SKILL.md` file uses YAML frontmatter to declare metadata:

```markdown
---
name: manage-pull-requests
version: 1.0.0
description: A skill for managing pull requests using the forgejo-cli.
license: Apache-2.0
compatibility: |
  Requires forgejo-cli.
  Agent must have network access to the Forgejo API.
metadata:
  category: development-tools
  tags: [git, forgejo, pull-requests, automation]
---

# Manage Pull Requests

Instructions and documentation for the skill go here...
```

## Packaging and Pushing Skills

The `push` command packages a skill directory into an OCI artifact and pushes it to a container registry. The CLI reads the `SKILL.md` frontmatter to build the artifact config and annotations automatically.

See [`examples/package-and-push/`](examples/package-and-push/) for a complete walkthrough using the popular `pdf` skill from [skills.sh](https://skills.sh/hot).

### Push to a registry

```bash
skills-oci push ghcr.io/myorg/skills/my-skill:1.0.0 ./my-skill
```

### Push to a local registry (plain HTTP)

```bash
skills-oci push localhost:5000/my-skill:1.0.0 ./my-skill --plain-http
```

If no tag is provided in `NAME[:TAG]`, the artifact is tagged as `latest`.

### What gets pushed

The CLI creates a standard OCI artifact with:

- **Config blob** (`application/vnd.agentskills.skill.config.v1+json`) — JSON metadata extracted from the SKILL.md frontmatter (name, version, description, license, compatibility, etc.)
- **Content layer** (`application/vnd.agentskills.skill.content.v1.tar+gzip`) — A deterministic tar.gz archive of the skill directory, rooted at `<skill-name>/`
- **Annotations** — Standard OCI annotations (`org.opencontainers.image.title`, `.version`, `.created`, `.licenses`) plus skill-specific ones (`io.agentskills.skill.name`)

The artifact is compatible with any OCI-compliant registry (GHCR, ECR, GAR, ACR, Docker Hub, Harbor, etc.).

## Installing Skills

The `add` command pulls a skill artifact from a registry, extracts it into `.agents/skills/`, creates symlinks in `.claude/skills/`, `.codex/skills/`, `.cursor/skills/`, and `.gemini/skills/`, and updates the project manifest files.

### Install a skill

```bash
skills-oci add ghcr.io/myorg/skills/my-skill:1.0.0
```

### Install from a local registry

```bash
skills-oci add localhost:5000/my-skill:1.0.0 --plain-http
```

### Install to a custom directory

```bash
skills-oci add ghcr.io/myorg/skills/my-skill:1.0.0 --output ./custom/skills
```

After installation, the skill is extracted and ready for use:

```
my-project/
  .agents/
    skills/
      manage-pull-requests/
        SKILL.md
        scripts/
          create-pr.sh
  skills.json
  skills.lock.json
```

## Managing Skills with skills.json

The CLI automatically manages two manifest files in your project directory:

### skills.json

A declarative manifest that declares which skills your project requires. It is created and updated automatically when you run `skills-oci add` or `skills-oci remove`.

```json
{
  "skills": [
    {
      "name": "manage-pull-requests",
      "source": "ghcr.io/myorg/skills/manage-pull-requests",
      "version": "1.0.0"
    },
    {
      "name": "go-pro-skills",
      "source": "ghcr.io/myorg/skills/go-pro-skills",
      "version": "2.0.0"
    }
  ]
}
```

Each entry contains:

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Skill identifier used for local references |
| `source` | Yes | OCI repository reference (without tag or digest) |
| `version` | No | OCI tag to install (should follow semver) |

### skills.lock.json

A lock file that records the exact OCI digests and metadata of installed skills, ensuring reproducible installs across environments. This file should be committed to version control.

```json
{
  "lockfileVersion": 1,
  "generatedAt": "2026-04-02T08:11:09Z",
  "skills": [
    {
      "name": "manage-pull-requests",
      "path": ".agents/skills/manage-pull-requests",
      "source": {
        "registry": "ghcr.io",
        "repository": "myorg/skills/manage-pull-requests",
        "tag": "1.0.0",
        "digest": "sha256:bc6708cbbc37adb919157f04d31e601e68f4b9c24b35c655079da87ad0e30f86",
        "ref": "ghcr.io/myorg/skills/manage-pull-requests:1.0.0@sha256:bc6708cb..."
      },
      "installedAt": "2026-04-02T08:11:09Z"
    }
  ]
}
```

The lock file pins each skill to an immutable digest, so installations are reproducible regardless of whether mutable tags (like `latest` or `1.0`) have been updated.

### Removing a skill

```bash
skills-oci remove --name manage-pull-requests
```

This removes the skill from `skills.json`, `skills.lock.json`, and deletes the extracted directory.

## Vendoring third-party skills (`catalog add`)

`skills-oci catalog add` records one or more third-party skills in a `vendored.json` desired-state file. It resolves an upstream GitHub reference to an **immutable commit SHA**, verifies that the upstream content actually contains a `SKILL.md`, and upserts an entry per skill. It never contacts the destination registry.

**Single skill vs. discovery.** If the URL/subpath points directly at a skill directory (one containing `SKILL.md`), exactly that skill is vendored — the classic single-skill case. If it points at a **container** (a directory or whole repo with no `SKILL.md` of its own), `catalog add` recursively discovers every directory that contains a `SKILL.md` and vendors each one, auto-naming it from its directory. Discovery stops descending once it finds a skill, so a skill's own nested example skills are not vendored separately. In discovery mode, `--name` does not apply (each skill is named from its directory).

**Accepted URL forms.** A directory at a ref (`.../tree/<ref>/<subpath>`), a repo root at a ref (`.../tree/<ref>`), or a bare repo (`https://github.com/<owner>/<repo>`). For a bare repo the **default branch** is resolved to its head commit and that SHA is recorded as the entry's commit pin.

**Recorded fields.** Each entry pins `name`, `namespace`, `repo`, `subpath`, and `commit` (the resolved 40-hex SHA). When the upstream `SKILL.md` declares a `license`, it is copied verbatim into an optional `license` field; entries whose upstream skill declares no license omit it. The destination OCI ref is **not** stored: the registry host and base namespace are owned by the consumer (skills-platform), which derives the ref as `<writeRepo>/<repo>/<name>` from its own config at processing time.

**Source-qualified namespacing.** Vendored skills are namespaced by their source repository so two upstreams that ship a skill with the same name never collide. The catalog `namespace` = `<owner>-<repo>` normalized to a single kebab token, e.g. `mattpocock-skills`. If two distinct source repos normalize to the same `(namespace, name)` but a different source repo, the add fails rather than silently overwriting the other source.

Re-adding a skill that is already listed **overwrites** its pin in place (matched by `(namespace, name)`). On an interactive terminal you are prompted per skill before each overwrite; new skills are added without prompting. In a non-interactive context (`--plain`, piped/no TTY) an overwrite requires `-y`/`--yes` — otherwise the command exits non-zero, naming the conflicts, without writing.

```bash
# Single skill, URL form (tag)
skills-oci catalog add https://github.com/anthropics/skills/tree/v1.0.0/skills/skill-creator

# Single skill, URL form (branch) — the branch head is resolved and the resulting SHA is recorded as the row's commit pin
skills-oci catalog add https://github.com/anthropics/skills/tree/main/skills/skill-creator

# Many skills — repo root at a commit: discovers every SKILL.md directory and vendors each
skills-oci catalog add https://github.com/anthropics/skills/tree/da20c92503b2e8ff1cf28ca81a0df4673debdbf7

# Many skills — bare repo: resolves the default branch, then discovers all skills
skills-oci catalog add https://github.com/vercel-labs/agent-skills

# Many skills under a container subpath
skills-oci catalog add https://github.com/anthropics/skills/tree/v1.0.0/skills

# Flag form (single skill)
skills-oci catalog add --repo anthropics/skills --subpath skills/skill-creator --version v1.0.0

# Overwrite existing entries non-interactively (CI / --plain) — overwrites every discovered skill
skills-oci catalog add --plain -y https://github.com/anthropics/skills/tree/v1.1.0

# Dry run prints the would-be entries without writing vendored.json
skills-oci catalog add <URL> --dry-run
```

| Flag | Description |
|------|-------------|
| `--repo` | Upstream `<owner>/<repo>` slug (flag form; mutually exclusive with the positional URL) |
| `--subpath` | Path within the upstream repo. A skill directory vendors that one skill; a container directory discovers and vendors every skill beneath it |
| `--version` | Upstream tag, branch, or 40-hex commit SHA to vendor. Resolved to an immutable commit SHA, which is recorded as the entry's commit pin. Omit (bare-repo URL only) to resolve the default branch |
| `--name` | Local entry name (single-skill only; default: last segment of the upstream subpath). Ignored — and rejected — in discovery mode |
| `--vendored` | Path to `vendored.json` (default `vendored.json`) |
| `-y`, `--yes` | Overwrite existing entries without prompting (required to overwrite under `--plain` / non-interactive) |
| `--timeout` | Maximum time for the network-bound resolve + checkout steps (default `60s`) |
| `--dry-run` | Print the would-be entries (and whether each would add or overwrite) and exit without writing |

## Using with Claude Code (Hook Integration)

`skills-oci` can be configured as a Claude Code `SessionStart` hook so that skills are automatically installed every time a Claude Code session starts. This means your project's skills are always present without any manual steps.

### How it works

1. **Declare skills** in `skills.json` at the root of your project.
2. **Register the hook** using `skills-oci register`. This writes a `SessionStart` hook into `.claude/settings.json` that runs `skills-oci install --plain` on every session start.
3. **Start Claude Code** — the hook fires, reads `skills.json`, and pulls any missing skills into `.claude/skills/`. Skills already present are skipped, so subsequent starts are fast.

```
Project start → Claude Code launches
                      │
                      ▼
              SessionStart hook fires
                      │
                      ▼
          skills-oci install --plain
                      │
                 reads skills.json
                      │
            ┌─────────┴──────────┐
            ▼                    ▼
     skill missing?        already present?
    pull from registry         skip
            │
     extract to .claude/skills/
```

### Setup

**Step 1 — Install skills-oci**

```bash
cd ~ && brew install liatrio/taproom/skills-oci && cd -
skills-oci --help
```

See [Installation](#installation) for alternative install methods (`go install`, source).

**Step 2 — Declare your skills**

Create a `skills.json` in your project root (or add skills interactively via `skills-oci add <NAME[:TAG]>`):

```json
{
  "skills": [
    {
      "name": "manage-pull-requests",
      "source": "ghcr.io/liatrio/skills/manage-pull-requests",
      "version": "1.0.0"
    }
  ]
}
```

**Step 3 — Register the hook**

```bash
skills-oci register
```

This creates or updates `.claude/settings.json`:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/skills-oci install --plain",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

**Step 4 — Commit both files**

```bash
git add skills.json skills.lock.json .claude/settings.json
git commit -m "add skills-oci hook for Claude Code"
```

Any team member who clones the repo and opens Claude Code will automatically get the skills installed on their first session start.

### Example

See [`examples/claude-code-hooks/`](examples/claude-code-hooks/) for a minimal project showing the `skills.json` and the resulting `.claude/settings.json`.

### Updating skills

To add or update a skill, run `skills-oci add <NAME[:TAG]>` (or edit `skills.json` directly) and commit the updated manifest. The hook will install the new skill on the next session start.

To remove a skill:

```bash
skills-oci remove --name manage-pull-requests
```

## Interactive TUI

By default, the CLI runs with an interactive terminal UI that shows progress through each phase with spinners and styled output. To disable the TUI (for CI/CD pipelines or scripting), use the `--plain` flag:

```bash
skills-oci push ghcr.io/myorg/skills/my-skill:1.0.0 ./my-skill --plain
skills-oci add ghcr.io/myorg/skills/my-skill:1.0.0 --plain
```

## Global Flags

| Flag | Description |
|------|-------------|
| `--plain` | Disable interactive TUI, use plain text output |
| `--plain-http` | Use HTTP instead of HTTPS for registry connections |

## Authentication

The CLI uses your existing Docker credentials from `~/.docker/config.json` and any configured credential helpers. Log in to your registry before pushing or pulling:

```bash
# GitHub Container Registry
echo $GITHUB_TOKEN | docker login ghcr.io -u USERNAME --password-stdin

# Docker Hub
docker login

# AWS ECR
aws ecr get-login-password | docker login --username AWS --password-stdin <account>.dkr.ecr.<region>.amazonaws.com
```

## Telemetry

`skills-oci` reports a single `skill.downloaded` event after every
successful skill pull from `add` or `install` so the project can see real
adoption signal. Emission is best-effort and non-blocking: failures never
fail your command, time out beyond 2 seconds, or print errors to your
terminal.

### What is sent

| Field | Example | Why |
|---|---|---|
| `skill.namespace`, `name`, `version`, `digest`, `oci_ref` | the skill you pulled | adoption per skill |
| `client.{name,version,os,arch}` | `skills-oci`, `0.1.0`, `darwin`, `arm64` | which CLI build is in use |
| `source.{command,trigger}` | `add`/`install`, `user`/`manifest` | how the pull was initiated |
| `event_id`, `occurred_at` | a ULID, RFC 3339 UTC | idempotency + timing |

See [`docs/telemetry-data-contract.md`](docs/telemetry-data-contract.md)
for the canonical wire shape.

### What is **never** sent

- File paths, working directory, hostname, file contents, `SKILL.md`
  bodies.
- Environment variables (other than the explicit telemetry config below).
- Registry credentials, GitHub tokens, or any other secret.
- Raw user identifiers (GitHub login, email). The forward-looking
  `actor.id_hash` is a SHA-256 of the underlying value; the raw value is
  never transmitted.

### Opting out

To disable telemetry, set the env var to the exact value `off`:

```bash
export SKILLS_OCI_TELEMETRY=off
```

Any other value (including unset) leaves telemetry on.

### Configuration

| Variable | Default | Effect |
|---|---|---|
| `SKILLS_OCI_TELEMETRY` | `on` | `off` disables emission. Any other value (including unset) leaves it on. |
| `SKILLS_OCI_TELEMETRY_ENDPOINT` | compiled-in via `-ldflags` (empty in stock builds) | Full URL of the collector, including `/v1/events`. Overrides the compiled-in default. |
| `SKILLS_OCI_TELEMETRY_TOKEN` | compiled-in via `-ldflags` (empty in stock builds) | Bearer token sent in the `Authorization` header. Overrides the compiled-in default. |

Failed sends are appended to
`<UserCacheDir>/skills-oci/telemetry/pending.ndjson` (capped at 1 MB) and
drained on the next successful call, so transient collector outages do not
lose events.

### Local testing

A stdlib-only Python collector lives at
[`scripts/dev-collector.py`](scripts/dev-collector.py) for verifying the
producer end-to-end against the wire contract. Run it in one terminal,
point `SKILLS_OCI_TELEMETRY_ENDPOINT` at it from another, and exercise
`add`/`install`:

```bash
# terminal A
python3 scripts/dev-collector.py

# terminal B
export SKILLS_OCI_TELEMETRY_ENDPOINT=http://127.0.0.1:8787/v1/events
export SKILLS_OCI_TELEMETRY_TOKEN=dev-token
./skills-oci add <registry>/<namespace>/<skill>:<tag> --plain
```

Flags on the collector exercise specific paths:
`--fail-first N` (transient → buffer → drain), `--status 400` (4xx →
`last-error.log`, no buffer growth), `--require-bearer TOKEN` (auth
header). See the script's docstring for the full list.

## License

[Apache License 2.0](LICENSE)
