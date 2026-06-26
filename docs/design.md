# Design Notes

## Control Plane

The migration control plane is GitHub-based. This repository stores low-frequency durable state:

- source cluster profile
- inventory snapshots
- schema conversion output
- migration plans
- export/import state
- CDC checkpoint snapshots
- validation evidence
- approval files

GitHub PRs are used for review and approval.

## PR Draft Generation

The current PR helper is deterministic and local-only. It writes Markdown PR bodies into the metadata tree:

- Cluster-level drafts: `clusters/<source_cluster_id>/prs/<stage>-pr.md`.
- Project-level drafts: `clusters/<source_cluster_id>/projects/<project_id>/prs/<stage>-pr.md`.

Supported stages are `discovery`, `schema`, `plan`, `export`, `import`, `cdc`, `validation`, and `cutover`. The helper records:

- PR title.
- Suggested branch name.
- Files to review.
- Required reviewer roles.
- Approval files when the stage has one.
- Operator checklist.
- Suggested `gh pr create` command.

It does not call the GitHub API, open a PR, merge a PR, or infer approval state.

## PR Creation Wrapper

`create-pr` wraps generated PR drafts with a guarded `gh pr create` command. It:

- requires an existing PR draft body file;
- reconstructs the deterministic title, branch, and body-file arguments;
- defaults to dry-run and prints the exact command;
- calls `gh pr create` only when `--execute` is explicitly set.

The wrapper does not merge PRs, approve PRs, bypass branch protection, or inspect GitHub approval state. GitHub branch protection and CODEOWNERS remain the approval boundary.

## Validation Worker

The current validation worker implementation is metadata-only. It does not connect to SQL Server or TiDB. It executes only after `approvals/validation-approval.yaml` has:

- `action: validation`
- `status: approved`
- at least one `approved_by` entry
- `payload_hash` matching the current validation payload

The validation payload hash covers:

- `project.yaml`
- `schema/conversion-report.md`
- `schema/schema-diff.json`
- `schema/tidb-ddl/`
- `plan/validation-plan.yaml`

The approval file is not included in the hash, avoiding self-referential approvals. If the approval is missing, pending, or has a hash mismatch, the worker exits without changing state or evidence.

When approved, the worker writes:

- `state/validation-status.yaml`
- `evidence/validation-report.md`

Current checks are deterministic repository checks: schema diff parseability, generated DDL presence, manual review item clearance, conversion report presence, and validation plan presence. Source/target row-count or checksum validation is intentionally out of scope until import and target connection support exist.

## Export And Import Workers

The current export/import worker implementations are metadata-only. They do not connect to SQL Server, TiDB, or object storage. They execute only after the matching approval file has:

- `action: export` or `action: import`
- `status: approved`
- at least one `approved_by` entry
- `payload_hash` matching the current stage payload

The export payload hash covers:

- `project.yaml`
- `plan/export-plan.yaml`

The import payload hash covers:

- `project.yaml`
- `schema/tidb-ddl/`
- `plan/export-plan.yaml`
- `plan/import-plan.yaml`

The export worker reads `plan/export-plan.yaml` and writes planned chunk state to `state/export-chunks.yaml`, plus `evidence/precheck.json`. The import worker reads `plan/import-plan.yaml` and writes planned import job state to `state/import-jobs.yaml`, plus `evidence/import-summary.json`.

These workers establish the approval and state write-back contract for future real executors. They intentionally mark items as `planned`; they do not mark chunks as exported or jobs as imported.

## Metadata Boundary

Metadata is organized by upstream SQL Server cluster:

```text
clusters/<source_cluster_id>/
```

This is the right boundary for source inventory, SQL Server CDC, source permissions, and worker leases.

Migration projects live below the source cluster:

```text
clusters/<source_cluster_id>/projects/<project_id>/
```

A project represents one independently planned, approved, validated, and cut-over migration unit.

## Schema Draft Generation

The current schema draft generator is deterministic and file-backed:

- Input: `clusters/<source_cluster_id>/inventory/inventory.json`.
- Input: `clusters/<source_cluster_id>/projects/<project_id>/project.yaml`.
- Output: project-local `schema/tidb-ddl/*.sql`.
- Output: project-local `schema/conversion-report.md`.
- Output: project-local `schema/schema-diff.json`.

It filters inventory by the project source database and source schemas. It generates TiDB DDL drafts with rule-based type mappings and marks unsupported or risky source types as manual-review items. It does not connect to TiDB and does not execute DDL.

## Data Movement Plan Generation

`generate-data-plans` is deterministic and file-backed:

- Input: `clusters/<source_cluster_id>/inventory/inventory.json`.
- Input: `clusters/<source_cluster_id>/projects/<project_id>/project.yaml`.
- Input: operator-supplied object URI prefix and chunk sizing flags.
- Output: project-local `plan/export-plan.yaml`.
- Output: project-local `plan/import-plan.yaml`.

It filters inventory by the project source database and source schemas. It estimates full-load export chunks from inventory `row_count`, writes object storage URIs for each chunk, and creates matching import jobs that depend on those export chunks.

The generated split predicate is intentionally a `TODO` placeholder because a safe split key must be reviewed per table. The command does not connect to SQL Server or TiDB, does not read data, does not write object storage, and does not start `IMPORT INTO` or TiDB Lightning.

## LLM Boundary

LLMs may generate:

- compatibility explanations
- schema rewrite candidates
- migration plan drafts
- export/import chunking or split-key recommendations
- PR descriptions
- validation report narratives
- incident diagnosis suggestions

LLMs are not required for deterministic repository commands such as `validate-repo`, `discover-sqlserver --dry-run`, `analyze-compatibility`, `generate-schema-draft`, `generate-data-plans`, `generate-pr-draft`, `create-pr`, `compute-payload-hash`, `worker-export`, `worker-import`, or `worker-validate`. For schema work, the LLM may read `conversion-report.md` and `schema-diff.json` to propose candidate rewrites, but the candidate must be committed as reviewed files before any worker can use it. For export/import planning, the LLM may propose split keys or risk notes, but generated predicates and execution settings still need PR review before workers can use them. For PR work, the LLM may refine prose, but file lists, approval files, and stage gates must remain deterministic.

LLMs must not decide:

- whether DDL can run
- whether import can start
- whether CDC has caught up
- whether validation passed
- whether cutover can proceed
- whether destructive cleanup can run

Those gates are handled by deterministic rules, observed runtime state, and GitHub approvals.
