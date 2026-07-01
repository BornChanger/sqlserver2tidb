# SQL Server to TiDB Migration Agent Design

## 1. Background

`sqlserver2tidb` already has the deterministic building blocks for a GitOps migration workflow:

- metadata repository initialization and validation
- SQL Server catalog discovery
- compatibility analysis
- schema, export/import, CDC, and validation draft generation
- deterministic PR draft generation
- GitHub PR creation and approval synchronization
- approval and payload-hash gates
- executor command generation and execution evidence
- metadata-only reconcile loop
- CDC orchestration and CDC health checks
- Feishu and Slack alerts
- advisory LLM commands

The missing layer is a migration agent that can coordinate these commands into a user-facing workflow without weakening the existing safety model.

The agent must not become an unrestricted autonomous database operator. It should be a GitOps state-machine orchestrator that reads repository state, chooses the next allowed action, invokes existing deterministic commands, and stops at explicit review or approval boundaries.

## 2. Problem Statement

Operators currently need to know the exact command sequence for each migration phase. This is usable for experts, but it is not yet a productized agent experience.

The agent should answer these operational questions:

- What is the current migration state?
- What is the next safe action?
- Which action is blocked, and why?
- Can the agent generate the next PR?
- Can the agent execute only already-approved work?
- Can the agent run CDC operational loops and alerts?
- Where does LLM assistance fit, and where is it forbidden?

The agent should reduce command choreography, not remove review gates.

## 3. Goals

- Provide a single top-level agent entrypoint for common migration workflows.
- Reuse existing deterministic CLI and `internal/gitops` functions instead of duplicating migration logic.
- Keep GitHub files as the source of truth for metadata, state, approvals, and evidence.
- Preserve human PR review as the approval boundary.
- Make LLM usage explicit, advisory, auditable, and non-executable.
- Support local binary, container, and GitHub Actions delivery.
- Make status and decisions machine-readable for Feishu, Slack, CI, and future UI integrations.

## 4. Non-Goals

- Do not add a TiDB metadata table.
- Do not add a long-lived external database for agent state.
- Do not let LLM output update `plan/`, `state/`, `approvals/`, or `evidence/`.
- Do not let the agent approve or merge migration PRs unless an explicit PR automation command is invoked and GitHub branch protection permits it.
- Do not bypass `worker-executor` approval and payload-hash gates.
- Do not switch production traffic, DNS, proxies, or application configuration directly.
- Do not replace DBA, app owner, or SRE review.

## 5. Design Principles

1. GitHub repository state is authoritative.
2. Agent memory is advisory and disposable.
3. Every database or object-storage side effect must pass deterministic approval gates.
4. Every agent decision must be explainable from repository files and command output.
5. LLM may propose text, candidates, and explanations, but it cannot become an execution authority.
6. Dry-run remains the default for PR creation and side-effecting flows unless `--execute` is explicitly set.
7. The first implementation should compose existing commands before adding new migration semantics.

## 6. Existing Capability Audit

The agent should build on these existing commands.

| Area | Existing command | Agent usage |
| --- | --- | --- |
| Repo setup | `init-repo`, `validate-repo`, `doctor` | Preflight and bootstrap |
| Source setup | `create-cluster` | Create source-cluster directory when requested |
| Discovery | `discover-sqlserver` | Generate inventory from SQL Server catalog |
| Compatibility | `analyze-compatibility` | Produce deterministic compatibility findings |
| Project setup | `create-project` | Create migration project directory |
| Schema plan | `generate-schema-draft` | Generate DDL and schema diff drafts |
| Data plan | `generate-data-plans` | Generate export/import plan drafts |
| CDC plan | `generate-cdc-plan`, `prepare-cdc-iteration`, `cdc-orchestrator` | Prepare CDC setup and range review |
| Validation plan | `generate-validation-plan` | Generate validation checks |
| Drift repair | `repair-schema-drift` | Regenerate impacted drafts after reviewed schema drift |
| PR | `generate-pr-draft`, `create-pr` | Stop at PR review boundary |
| PR closure | `complete-github-pr`, `sync-github-pr-approval` | Sync approved and merged PRs back to approval files |
| Execution | `worker-executor`, `worker-export`, `worker-import`, `worker-cdc`, `worker-validate`, `worker-cutover` | Execute only approved stages |
| Reconcile | `worker-reconcile`, `worker-agent` | Readiness scan and metadata-only loop |
| CDC ops | `cdc-health`, `advance-cdc-checkpoint` | Long-running health and checkpoint operations |
| Evidence PR | `generate-executor-evidence-pr-draft`, `create-executor-evidence-pr`, `create-worker-state-pr` | Review execution output and state write-back |
| LLM | `llm-*` commands | Advisory generation only |

The current `worker-agent` is not the full migration agent. It is a packaged metadata-only reconcile loop. The new migration agent sits above it.

## 7. Target Architecture

```text
User / Feishu / Slack / GitHub Issue / CI
        |
        v
sqlserver2tidb agent
        |
        +--> Preflight and repository validation
        |
        +--> State reader
        |       - cluster metadata
        |       - inventory
        |       - project metadata
        |       - plans
        |       - approvals
        |       - state
        |       - evidence
        |
        +--> Decision engine
        |       - next safe action
        |       - blocked reason
        |       - required review boundary
        |       - optional LLM advisory actions
        |
        +--> Command runner
        |       - existing sqlserver2tidb subcommands
        |       - existing sqlserver2tidb-executor through worker-executor
        |
        +--> GitHub integration
        |       - PR draft
        |       - PR create
        |       - approval sync
        |       - evidence PR
        |
        +--> Observability
                - JSON status
                - Markdown summary
                - CDC health metrics
                - Feishu / Slack alerts
```

## 8. Agent Modes

The agent should expose one top-level command with explicit modes.

```bash
sqlserver2tidb agent --mode <mode> [flags]
```

### 8.1 `status`

Read-only status report.

```bash
sqlserver2tidb agent \
  --mode status \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --json
```

Responsibilities:

- run repository validation
- summarize cluster/project existence
- summarize inventory, plans, approvals, state, and evidence
- call the same readiness logic as `worker-reconcile --dry-run`
- report next action and blocked reason
- never write files
- never call GitHub
- never connect to SQL Server or TiDB

### 8.1.1 `wizard`

Interactive terminal guidance for operators.

```bash
sqlserver2tidb agent \
  --mode wizard \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

Responsibilities:

- present a concise menu for status, next-step preview, safe planning, PR drafting, approved execution, CDC ops, and LLM review assist
- call existing agent modes instead of adding a second state machine
- run read-only status and preview actions immediately
- ask before write, GitHub, LLM, CDC apply/polling, or database execution actions
- keep GitHub metadata as the only durable state source
- reuse `agentSecurityPolicy`, approval gates, payload hashes, executor evidence, and LLM redaction from existing modes

Forbidden behavior:

- no conversational state as migration metadata
- no automatic approval or PR merge
- no execution based only on terminal input
- no LLM-generated executable plan/state/evidence

### 8.2 `plan-and-pr`

Generate missing deterministic migration drafts and open PRs when explicitly requested.

```bash
sqlserver2tidb agent \
  --mode plan-and-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage schema \
  --execute-pr
```

Responsibilities:

- generate schema/data/CDC/validation drafts according to selected stage
- generate deterministic PR draft
- optionally call `create-pr --execute`
- print successful GitHub output, including the created PR URL when `gh` returns one
- stop at PR review boundary
- never execute DDL, export, import, CDC apply, validation, or cutover

### 8.3 `execute-approved`

Execute only work that is already approved and hash-current.

```bash
sqlserver2tidb agent \
  --mode execute-approved \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export \
  --use-executor \
  --source-connection-string-env SQLSERVER2TIDB_SQLSERVER_DSN \
  --execute \
  --create-evidence-pr
```

Responsibilities:

- require explicit `--execute`
- call `worker-executor --stage <stage> --execute` for always-executor stages (`ddl`, `cdc-enable`)
- call `worker-executor --stage <stage> --execute` for executor-supported worker stages (`export`, `import`, `cdc`, `validation`) when `--use-executor` is set
- call metadata-only workers when appropriate
- support `--resume`, `--command-timeout`, `--command-retries`, and `--retry-backoff`
- generate evidence PR draft after executor-backed execution when `--evidence-pr-draft` is set
- preview evidence PR creation when `--create-evidence-pr` is set
- create the evidence PR only when `--execute-evidence-pr` is explicitly set

Forbidden behavior:

- no execution when approval is missing, pending, or stale
- no execution when plan status is still `draft`
- no mutation of approval files

### 8.4 `pr-close`

Close a reviewed stage PR and sync the resulting GitHub approval back to the project approval file.

```bash
sqlserver2tidb agent \
  --mode pr-close \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export \
  --pr 42 \
  --repo BornChanger/sqlserver2tidb \
  --execute
```

Responsibilities:

- wrap `complete-github-pr`
- dry-run by default and print the exact GitHub/git closure operations
- optionally approve the PR unless `--skip-approve` is set
- merge with the selected method after GitHub state, review, checks, and file gates pass
- run approval synchronization for the selected stage
- commit and push the updated approval file to the base branch when execution changes it
- record automation audit metadata when supplied by GitHub Actions environment variables or explicit flags

Forbidden behavior:

- no GitHub or git mutation without `--execute`
- no bypass of branch protection, required checks, or review gates
- no direct approval-file mutation outside the `complete-github-pr` approval sync path
- no database execution

### 8.5 `cdc-ops`

Run long-period CDC operation and health workflows.

```bash
sqlserver2tidb agent \
  --mode cdc-ops \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --poll \
  --apply-approved \
  --metrics-file artifacts/cdc-health.json \
  --history-file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/cdc-health-history.jsonl
```

Responsibilities:

- run `cdc-health`
- run `cdc-orchestrator`
- prepare next CDC range PR when needed
- apply only already-approved ranges when `--apply-approved` is set
- send Feishu or Slack alerts according to existing webhook flags/env vars
- never auto-approve CDC ranges

### 8.6 `review-assist`

Generate advisory LLM outputs for human review.

```bash
sqlserver2tidb agent \
  --mode review-assist \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage validation \
  --provider-config global/llm-providers.yaml \
  --execute-llm
```

Responsibilities:

- route to the relevant `llm-*` command
- write advisory output under `ai/`
- write audit JSON
- redact secrets before provider calls
- never edit executable plans, approvals, state, or evidence

### 8.7 `auto`

Bounded autopilot to the next review or approval boundary.

```bash
sqlserver2tidb agent \
  --mode auto \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --max-steps 5
```

Responsibilities:

- repeatedly compute next action
- run only non-destructive deterministic planning steps
- create PR drafts
- optionally create PRs with `--execute-pr`
- stop at:
  - missing required operator input
  - PR review boundary
  - stale or blocked approval
  - executor action requiring `--execute`
  - cutover boundary

`auto` must not be a fully autonomous production migration.

## 9. State Machine

The agent state machine is derived from GitHub files.

```text
repo_missing
  -> init_required
  -> repo_ready

repo_ready
  -> source_cluster_missing
  -> source_cluster_ready

source_cluster_ready
  -> inventory_missing
  -> discovery_ready

discovery_ready
  -> compatibility_missing
  -> compatibility_ready

compatibility_ready
  -> project_missing
  -> project_ready

project_ready
  -> schema_draft_missing
  -> schema_review_required
  -> schema_approved
  -> ddl_execution_ready
  -> ddl_evidence_review_required

ddl_evidence_review_required
  -> data_plan_missing
  -> export_review_required
  -> export_execution_ready
  -> import_review_required
  -> import_execution_ready

import_execution_ready
  -> cdc_plan_missing
  -> cdc_review_required
  -> cdc_enable_ready
  -> cdc_range_review_required
  -> cdc_apply_ready
  -> cdc_caught_up

cdc_caught_up
  -> validation_plan_missing
  -> validation_review_required
  -> validation_execution_ready
  -> validation_passed

validation_passed
  -> cutover_runbook_review_required
  -> cutover_ready
  -> completed
```

The actual order may vary by project type. Offline or full-load-only projects may skip CDC gates only when the project metadata and cutover gate allow it.

## 10. Decision Model

The first implementation should use a deterministic rule engine.

### 10.1 Inputs

- `validate-repo` result
- source-cluster metadata
- inventory presence and status
- compatibility report presence
- project metadata
- schema diff status
- export/import/CDC/validation plan status
- approval files and payload hashes
- worker state
- executor evidence
- `worker-reconcile --dry-run` equivalent report
- selected mode and stage

### 10.2 Outputs

Agent decision output should be serializable as JSON:

```json
{
  "status": "blocked",
  "source_cluster_id": "prod-sqlserver-a",
  "project_id": "sales-db-to-tidb-prod-a",
  "phase": "schema",
  "next_action": "generate-pr-draft",
  "blocked_reason": "",
  "requires_review": true,
  "requires_execute": false,
  "commands": [
    "sqlserver2tidb generate-pr-draft --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage schema"
  ],
  "files": [
    "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/schema-diff.json"
  ]
}
```

### 10.3 Status Values

- `ready`: the agent can run the next action in the selected mode.
- `blocked`: the agent cannot continue without human input, approval, or missing prerequisite.
- `waiting_review`: a PR or review file exists and must be reviewed.
- `waiting_approval_sync`: PR is expected to be merged, but approval file is not current.
- `executing`: an action is currently running.
- `completed`: no more action for the selected mode.
- `failed`: command failed and evidence/logs should be reviewed.

## 11. LLM Responsibility Boundary

LLM usage must be explicit.

| Phase | LLM allowed? | Output path | Worker consumes output? |
| --- | --- | --- | --- |
| Compatibility explanation | Yes | `clusters/<source_cluster_id>/ai/` | No |
| Schema rewrite advice | Yes | `projects/<project_id>/ai/` | No |
| Migration strategy advice | Yes | `projects/<project_id>/ai/` | No |
| PR summary | Yes | `projects/<project_id>/ai/` | No |
| Validation mismatch analysis | Yes | `projects/<project_id>/ai/` | No |
| Cutover risk notes | Yes | `projects/<project_id>/ai/` | No |
| DDL execution | No | N/A | N/A |
| Export/import execution | No | N/A | N/A |
| CDC enable/apply | No | N/A | N/A |
| Approval file generation from merged PR | No LLM | `approvals/*.yaml` | Yes |
| State/evidence write-back | No LLM | `state/`, `evidence/` | Yes |

The agent may ask an LLM to draft text, explain findings, or suggest candidate fixes. Any candidate that changes executable behavior must be committed as normal metadata and reviewed through GitHub PR before execution.

## 12. GitHub and PR Boundary

The agent should distinguish three PR operations:

1. Draft PR body generation.
2. PR creation.
3. PR completion or approval sync.

Draft generation is safe and local. PR creation requires explicit `--execute-pr` or equivalent. PR completion requires `complete-github-pr --execute`, GitHub permissions, green checks, and branch protection compliance.

The agent must never treat an LLM-generated summary, a local draft file, or an unmerged branch as approval.

## 13. Configuration

The agent reads a repository-local policy file:

```text
global/policies/agent-policy.yaml
```

Current implemented shape:

```yaml
version: 1
allow_execute: true
allow_execute_pr: true
allow_execute_evidence_pr: true
allow_execute_llm: true
max_auto_steps: 0
```

Policy values narrow behavior, not expand it beyond compiled safety gates. `allow_execute: false` blocks all agent `--execute` flows before mode dispatch. `allow_execute_pr: false` blocks `--execute-pr`; `allow_execute_evidence_pr: false` blocks `--execute-evidence-pr`; `allow_execute_llm: false` blocks `--execute-llm`; `max_auto_steps` caps `agent --mode auto --max-steps`, with `0` meaning no policy cap beyond CLI validation.

## 14. CLI Interface

Initial command:

```bash
sqlserver2tidb agent \
  --mode status|wizard|plan-and-pr|execute-approved|pr-close|cdc-ops|review-assist|auto \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

Common flags:

- `--stage <stage>`
- `--json`
- `--dry-run`
- `--max-steps <n>`
- `--execute-pr`
- `--execute`
- `--execute-llm`
- `--provider-config <path>`
- `--source-connection-string-env <name>`
- `--target-connection-string-env <name>`
- `--holder <agent-id>`
- `--poll`
- `--interval <duration>`

The first code slice implements `--mode status` and `--mode auto --dry-run`. Current implementation also allows bounded safe non-dry-run `auto` planning steps: schema draft generation, schema PR draft generation, plan PR draft generation, and optional schema/plan PR creation with `--execute-pr`. Database/object-storage execution remains behind `execute-approved`.

## 15. Delivery Model

The agent should be usable in three ways.

### 15.1 Local CLI

Operator runs the binary from a checkout:

```bash
sqlserver2tidb agent --mode status --root .
```

### 15.2 Container

The existing image already includes:

- `git`
- `gh`
- `sqlserver2tidb`
- `sqlserver2tidb-executor`

The agent should run inside the same image with mounted metadata repo and environment-based secrets.

### 15.3 GitHub Actions

GitHub Actions should run status, PR creation, approval sync, and CDC health with scoped tokens. Database execution should remain opt-in and environment-protected.

The repository ships runtime templates under `examples/agent-runtime/`:

- `github-actions/migration-agent.yml` for manual `workflow_dispatch` orchestration.
- `kubernetes/migration-agent-cronjob.yaml` for periodic containerized status or CDC operations.
- `systemd/sqlserver2tidb-agent-status.*` for host-level status polling.
- `systemd/sqlserver2tidb-agent-cdc-ops.*` for host-level CDC health/orchestration polling.

These templates must keep status/read-only behavior as the default. PR creation, database execution, and LLM provider calls stay behind explicit runtime switches and the repository policy file.

## 16. Security Model

- Secrets are passed by environment variable names or secret references.
- Plaintext credentials must not be written into repo files.
- Existing redaction must be used for logs, evidence summaries, and LLM prompts.
- GitHub token permissions must be least privilege.
- PR automation must respect branch protection.
- Database execution requires explicit `--execute`.
- Cutover remains a file gate and cannot change production traffic directly.

The CLI now centralizes agent-side policy checks before dispatching into a specific mode. This policy layer rejects:

- unsafe external binary flag values that contain whitespace or control characters;
- connection-string and alert webhook flags unless they are environment variable names;
- negative retry, timeout, or polling controls;
- executor evidence PR flows that have not passed their required gates before repository mutation begins.

Mode implementations still keep their deterministic approval/hash gates. New agent entrypoints should add side-effect policy rules to the central agent policy layer instead of scattering one-off checks in each mode.

## 17. Failure Handling

- Planning commands should be idempotent or fail if they would overwrite existing reviewed work.
- Execution commands must rely on existing approval and payload-hash gates.
- `worker-executor --resume` should be used for retrying partial execution.
- CDC loops must preserve retention checks.
- Agent `auto` mode must stop on unknown or ambiguous states.
- All failed commands should return non-zero and print the next manual recovery command.

## 18. Observability

Agent output should support:

- human-readable summary
- `--json` machine-readable status
- command plan list
- blocked reason list
- stage matrix with approval, plan, state, evidence, and PR artifact paths
- active agent policy summary
- latest CDC health history summary for a scoped project
- touched file list
- optional Markdown run summary under `ai/` or `prs/` only when explicitly requested

Long-running CDC status should continue to use:

- `cdc-health --metrics-file`
- `cdc-health --history-file`
- Feishu alert webhook
- Slack alert webhook

## 19. Implementation Plan

### Phase 1: Design and Read-Only Agent Status

Deliver:

- `docs/migration-agent-design.md`
- `sqlserver2tidb agent --mode status`
- JSON and text status output
- tests for repository validation, missing project, ready action, and blocked action

No file writes except optional test fixtures.

### Phase 2: Bounded Autopilot

Deliver:

- `sqlserver2tidb agent --mode auto --dry-run`
- next-action planning
- command list rendering
- stop conditions
- tests for schema PR boundary and approved executor boundary
- non-dry-run schema draft / schema PR draft generation
- `--max-steps` bounded planning loop
- plan PR draft generation at the post-schema planning boundary
- optional schema/plan PR creation with `--execute-pr`

No database or object-storage side effects. GitHub PR creation is allowed only when `--execute-pr` is explicitly set.

### Phase 3: Planning and PR Mode

Deliver:

- `--mode plan-and-pr`
- stage-specific draft generation
- optional `--execute-pr`
- tests for PR draft creation and dry-run PR command output

### Phase 4: Execute-Approved Mode

Deliver:

- `--mode execute-approved`
- explicit `--execute` requirement
- wrapper around `worker-executor` and metadata-only workers
- evidence PR draft generation
- tests for approval/hash gate pass-through and dry-run safety

### Phase 5: PR Close Mode

Deliver:

- `--mode pr-close`
- dry-run wrapper around `complete-github-pr`
- explicit `--execute` requirement for GitHub and git mutations
- tests for PR closure command pass-through

### Phase 6: CDC Ops Mode

Deliver:

- `--mode cdc-ops`
- wrapper around `cdc-health` and `cdc-orchestrator`
- Feishu/Slack alert flag pass-through
- tests with dry-run/probed LSN stubs

### Phase 7: Review Assist Mode

Deliver:

- `--mode review-assist`
- LLM command routing
- explicit `--execute-llm`
- tests that advisory files stay under `ai/`

## 20. Acceptance Criteria

- The agent never executes database work without `--execute`.
- The agent never creates plan/review GitHub PRs without `--execute-pr`.
- The agent never approves, merges, or syncs stage PR approval files without `--execute`.
- The agent never creates executor evidence GitHub PRs without `--execute-evidence-pr`.
- The agent never treats LLM output as approval or execution input.
- The agent status is derived from repository files and existing readiness logic.
- The agent can explain blocked states with deterministic reasons.
- The agent can be run from the existing container image.
- Existing `make ci` passes.

## 21. First Implementation Slice

The first implementation should be deliberately small:

```bash
sqlserver2tidb agent --mode status --root . --source-cluster-id prod-sqlserver-a --json
```

It should:

- validate the repo
- call the existing reconcile planning logic
- summarize project actions
- return non-zero only for invalid repository or invalid CLI input
- never write files

This gives users the agent surface without changing migration semantics. Later modes can be added behind the same structure.
