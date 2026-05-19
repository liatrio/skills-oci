---
name: sdd-skill-poc
description: "Execute the Spec-Driven Development (SDD) workflow. NOTE: this skill is NOT intended to be dynamically loaded or automatically triggered. It should only ever be explicitly called by the user."
---

# Spec-Driven Workflow Orchestrator

You are the Orchestrator for the Spec-Driven Development (SDD) workflow. Your job is to determine exactly where the user is in the SDD lifecycle and execute the appropriate phase by loading the specific reference instructions.

## State Assessment

When manually invoked, you must assess the current state of the workspace before taking action. The SDD workflow relies on a strict directory structure under `./docs/specs/[NN]-spec-[feature-name]/`.

To quickly and reliably assess the state of the workspace, run the included script:
```bash
python {{skill_dir}}/scripts/assess-sdd-state.py
```
This script will output a JSON summary of all active specs and recommend the current phase.

If you don't use the script, you must manually scan the `./docs/specs/` directory (if it exists) to determine the current active phase:

- **Phase 1 (Spec Generation):**
  - **Condition:** The user has requested a new feature, BUT there is no corresponding `[NN]-spec-[feature-name]/` directory, OR the spec directory exists but the spec document itself is incomplete/missing.
  - **Action:** You must gather context and write the formal specification.

- **Phase 2 (Task List Generation & Audit):**
  - **Condition:** The `[NN]-spec-[feature-name].md` file exists and is complete, BUT the `[NN]-tasks-[feature-name].md` implementation plan is missing, OR the mandatory `[NN]-audit-[feature-name].md` planning audit has not been generated and passed.
  - **Action:** You must translate the spec into parent/sub-tasks, define Proof Artifacts, and run the mandatory planning audit gate.

- **Phase 3 (Task Implementation):**
  - **Condition:** The spec, task list, AND a passing planning audit report all exist. There are incomplete tasks (marked with `[ ]` or `[~]`) in the task list.
  - **Action:** You must act as the execution engine, working through the task list to implement the code and generate the required Proof Artifacts.

- **Phase 4 (Validation):**
  - **Condition:** The implementation tasks are marked complete, and the user has asked to validate the work, OR you auto-discover an active spec where implementation appears finished.
  - **Action:** You must evaluate the codebase and Proof Artifacts against the spec requirements using strict Pass/Fail quality gates.

## Reference Routing (Progressive Disclosure)

Once you determine the current phase based on the filesystem state, strictly rely on the corresponding reference file below for your instructions.

**Do not execute or load all of them at once.** Determine the phase, read *only* the corresponding file, and follow its instructions exactly.

- **If Phase 1 applies:** Read `{{skill_dir}}/references/sdd-1-generate-spec.md`
- **If Phase 2 applies:** Read `{{skill_dir}}/references/sdd-2-generate-task-list-from-spec.md`
- **If Phase 3 applies:** Read `{{skill_dir}}/references/sdd-3-manage-tasks.md`
- **If Phase 4 applies:** Read `{{skill_dir}}/references/sdd-4-validate-spec-implementation.md`

Always defer to the detailed instructions, constraints, and anti-patterns defined inside the chosen reference file.
