# Agent Wizard Design

## Goal

`sqlserver2tidb agent --mode wizard` provides a guided terminal workflow for operators who do not want to remember every migration command. It is an interaction layer over the existing agent modes, not a new migration state machine.

The wizard keeps GitHub repository metadata as the only durable source of truth. It reads and writes the same files as existing commands under `clusters/<source_cluster_id>/projects/<project_id>/`. It does not store conversational state, does not bypass PR approvals, and does not let LLM output become executable state.

## Non-Goals

- Do not add a chat runtime.
- Do not make Codex a required dependency.
- Do not add a second metadata store.
- Do not auto-approve PRs or bypass payload hash checks.
- Do not let natural language directly execute DDL, export, import, CDC, validation, or cutover.

## User Experience

The operator starts the wizard with the same project-scoping flags used by other agent modes:

```bash
sqlserver2tidb agent --mode wizard \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

The wizard prints the selected project, renders a stage dependency view, then shows a short menu and waits for a choice:

```text
migration agent wizard
selected project: prod-sqlserver-a/sales-db-to-tidb-prod-a

stage dependency view:
stage | status | dependency
schema | reviewed | depends on source inventory and project metadata
ddl | blocked | depends on reviewed schema diff and ddl approval
export | ready | depends on reviewed export plan and export approval
import | blocked | depends on export completed, reviewed import plan, and import approval
...
recommended next step: execute approved export
recommended command: sqlserver2tidb agent --mode execute-approved ...

1) Show status
2) Preview next safe action
3) Run safe auto planning
4) Generate stage PR draft
5) Execute approved stage
6) Run CDC ops
7) Run LLM review assist
q) Quit
```

Read-only options run immediately. Options that write files, call GitHub, call an LLM provider, or execute database work ask for explicit confirmation in the wizard before dispatching to the underlying mode.

## Command Mapping

| Wizard choice | Underlying behavior | Default safety |
| --- | --- | --- |
| Show status | `agent --mode status` | Read-only |
| Preview next safe action | `agent --mode auto --dry-run` | Read-only |
| Run safe auto planning | `agent --mode auto` | Asks before writing planning or PR draft files |
| Generate stage PR draft | `agent --mode plan-and-pr` | Draft only unless user confirms GitHub PR creation |
| Execute approved stage | `agent --mode execute-approved` | Dry-run unless user confirms `--execute` |
| Run CDC ops | `agent --mode cdc-ops` | Uses supplied flags; asks before `--apply-approved` and `--poll` |
| Run LLM review assist | `agent --mode review-assist` | Dry-run unless user confirms `--execute-llm` |

The wizard delegates all domain checks to the existing modes. Approval status, payload hashes, executor dry-runs, evidence PR creation, CDC health, and LLM redaction remain owned by existing code paths.

## Safety Model

The wizard must be conservative by default:

- It starts with no side effects.
- It never mutates state for status or preview choices.
- It asks for confirmation before `--execute`, `--execute-pr`, `--execute-llm`, `--apply-approved`, or `--poll`.
- It reuses `agentSecurityPolicy` before dispatching a chosen action.
- It passes secrets only as environment variable names, reusing existing CLI validation.
- It reports the underlying command output instead of inventing state.

The wizard can guide the operator, but GitHub PR approval and committed metadata remain the control plane.

## Error Handling

Invalid menu choices print an error and return to the menu. Empty input returns to the menu. EOF exits cleanly. If an underlying action returns non-zero, the wizard prints the exit code and returns it after the operator exits.

This keeps the interactive session usable while still making automation aware that a selected action failed.

## Test Strategy

The first implementation is covered by CLI-level tests:

- A wizard session can show status, preview the next action, and quit without mutating state.
- A wizard session can select `execute-approved`, decline execution, and verify the underlying mode remains a dry-run.
- Policy validation remains centralized and is reused for confirmed side-effect actions.

Future tests can cover `plan-and-pr`, `cdc-ops`, and `review-assist` prompts once the base wizard is stable.
