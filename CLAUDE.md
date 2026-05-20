# AI Agent Development Guide

This document provides essential guidance for AI agents working in the skills-oci repo.

## Project Context

`skills-oci` is a Go CLI tool for packaging, pushing, and managing AI agent skills as OCI artifacts. It follows the [Agent Skills OCI Artifacts Specification](https://github.com/ThomasVitale/agents-skills-oci-artifacts-spec) and ships an interactive terminal UI built with [Bubble Tea](https://github.com/charmbracelet/bubbletea).

Major components:

- `cmd/` — Cobra commands (`push`, `add`, `install`, `remove`, `register`, `verify`, `clean`, `collection`)
- `pkg/skill/` — SKILL.md parsing, validation, manifest, and archive logic
- `pkg/oci/` — OCI registry interactions (push, pull, auth, verify, media types)
- `pkg/tui/` — Bubble Tea TUI flows for each command
- `docs/` — Specs and data contracts (e.g. `telemetry-data-contract.md`)

## Critical Requirement: Strict TDD

**MANDATORY**: All feature implementations must follow **Strict Test-Driven Development (TDD)** methodology:

1. **RED Phase**: Write a failing test that defines the desired behavior
2. **GREEN Phase**: Write the minimum code required to make the test pass
3. **REFACTOR Phase**: Improve the code while maintaining test coverage

**Never write production code before a failing test.**

## TDD Standards

### Coverage Requirements

- **Minimum 90% line coverage** for new code
- **100% branch coverage** for critical business logic (SKILL.md frontmatter parsing/validation, OCI manifest assembly, deterministic tar.gz archiving, lockfile resolution, digest verification, etc.)
- All edge cases must be explicitly tested (missing fields, invalid semver, registry auth failures, partial pulls, symlink targets, etc.)

### Test Organization

- Use Go's standard `testing` package; prefer table-driven tests for branchy logic
- Follow **Arrange-Act-Assert** pattern
- Use descriptive test names (`TestParseFrontmatter_RejectsMissingName`) that document behavior
- Tests must be **fast, isolated, and repeatable** — no live registry calls in unit tests; use `testdata/` fixtures and in-memory or `httptest` registries for integration tests
- Run `go test ./...` and `go vet ./...` before committing

### Quality Gates

- Tests written before implementation (RED phase)
- All tests pass before commit (`go test ./...`)
- Code coverage meets standards before merge

## Code Standards

- **Clean Code**: SOLID principles, DRY, single responsibility
- **Pure functions at the core**: keep parsing, validation, manifest assembly, and archive shaping in plain functions that are easy to test in isolation; keep IO (filesystem, registry HTTP, Docker credential helpers) at the edges
- **One concern per package**: `pkg/skill` does not talk to registries, `pkg/oci` does not parse SKILL.md, `pkg/tui` does not perform IO directly — it dispatches to the core packages
- **TUI vs. plain output**: every command must work correctly under `--plain` (CI/scripting mode). TUI is a presentation layer over the same underlying operations
- **Deterministic artifacts**: tar.gz layers must be byte-for-byte reproducible (sorted entries, zeroed mtimes, fixed uid/gid) — any change here must be covered by tests
- **No premature abstraction**: three similar lines is better than a wrong abstraction
- **Error handling**: wrap errors with context (`fmt.Errorf("pushing layer: %w", err)`); never swallow errors silently

## Version Control

- Use Git with **conventional commits** (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`)
- Keep commits focused; changes that span `pkg/skill`, `pkg/oci`, and `cmd/` should land together when they depend on each other
- Lockfile changes (`skills-lock.json`) belong in the same commit as the change that produced them

## Review Checklist

Before committing code:

- Tests written before implementation
- `go test ./...` passes
- `go vet ./...` clean; `gofmt` applied
- Follows SOLID principles
- No code duplication
- Works under both TUI and `--plain` modes (if touching a command)
- Conforms to the relevant data contract (OCI spec, telemetry, lockfile) if applicable
- Documentation (`README.md`, `docs/`) updated when behavior or contract changes

