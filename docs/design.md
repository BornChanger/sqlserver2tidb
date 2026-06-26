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

`create-worker-state-pr` wraps worker state PR drafts. It:

- requires `clusters/<source_cluster_id>/projects/<project_id>/prs/reconcile-<stage>-state-pr.md`;
- requires the worker-written state/evidence files and source-cluster `state/worker-lease.yaml`;
- includes `evidence/executor-<stage>-run.json` when executor run evidence exists;
- includes source-cluster `state/cdc-checkpoint.yaml` for CDC state PRs;
- reports stale PR body file lists during dry-run and refreshes the body before commit in `--execute` mode;
- reconstructs deterministic `git switch`, `git add`, `git commit`, `git push`, and `gh pr create` commands;
- defaults to dry-run and only mutates the local checkout when `--execute` is explicitly set.

This command creates a local branch, commits files, pushes the branch, and opens a PR only in explicit execute mode. It still does not merge PRs, approve PRs, bypass branch protection, or decide whether a worker result is operationally correct.

`generate-executor-evidence-pr-draft` and `create-executor-evidence-pr` wrap executor-only evidence review. They:

- require `evidence/executor-<stage>-run.json` to exist;
- require the matching stage approval to be approved and payload-hash current;
- reject executor evidence whose source cluster, project, stage, or payload hash does not match the requested/current approved metadata;
- reject executor evidence without command records, and require every command record to include `id`, `shell_command`, and `exit_code`;
- write or consume `clusters/<source_cluster_id>/projects/<project_id>/prs/executor-<stage>-evidence-pr.md`;
- reconstruct deterministic `git switch`, `git add`, `git commit`, `git push`, and `gh pr create` commands.

This path is especially important for DDL, because DDL is executor-only and does not produce metadata worker state. The command creates a local branch, commits the evidence and PR body, pushes the branch, and opens a PR only in explicit execute mode. It does not merge PRs, approve PRs, bypass branch protection, or decide whether executor output is operationally correct.

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

The DDL executor payload hash covers:

- `project.yaml`
- `schema/conversion-report.md`
- `schema/schema-diff.json`
- `schema/tidb-ddl/`

When approved and the CDC plan contains reviewed tracked tables, the worker writes:

- `state/validation-status.yaml`
- `evidence/validation-report.md`

Current checks are deterministic repository checks: schema diff parseability, generated DDL presence, manual review item clearance, conversion report presence, validation plan presence, required field validation for committed row-count checks, and rejection of unresolved TODO predicates. `validate-repo` also requires file-schema-policy mappings for migration, export, import, CDC, and validation plans, and checks export/import/CDC plan work-item fields when those plans contain work items. Empty draft plan lists remain valid during initialization. The validation worker itself still does not connect to SQL Server or TiDB. For real row-count validation, `worker-executor --stage validation` can generate `sqlserver2tidb-executor validate-count` commands after validation approval and payload hash checks pass. `validate-count --execute` compares SQL Server and TiDB row counts for one reviewed source/target object pair when operators provide connection strings through environment variables. Checksum validation, sampled hash validation, and reviewed business SQL validation remain future executor capabilities.

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

The export worker reads `plan/export-plan.yaml` and writes planned chunk state to `state/export-chunks.yaml`, plus `evidence/precheck.json`. The import worker reads `plan/import-plan.yaml` and writes planned import job state to `state/import-jobs.yaml`, plus `evidence/import-summary.json`. Export and import workers fail fast when the approved plan has no work items or required work-item fields are missing.

These workers establish the approval and state write-back contract for future real executors. They intentionally mark reviewed items as `planned`; they do not mark chunks as exported or jobs as imported.

## Worker Executor Shell

`worker-executor` is the current external executor shell. It is deterministic and file-backed:

- Input: approved `ddl`, `export`, `import`, `cdc`, or `validation` stage metadata.
- Input: the matching schema files or plan file under `schema/` or `plan/`.
- Output: dry-run command lines by default.
- Optional execution: external command invocation only when `--execute` is explicitly set.

The command reuses `requireApprovedStage`, so it refuses to produce executor commands unless the stage approval is approved, has reviewers, and the payload hash matches the current repository files. The default external binary is `sqlserver2tidb-executor`; operators can override it with `--executor-binary`. Operators can also pass `--source-connection-string-env`, `--target-connection-string-env`, and `--import-batch-size`; these values are rendered into the generated executor commands instead of being stored in GitHub metadata. In `worker-executor --execute` mode, the CLI invokes the external binary and injects the executor-level `--execute` flag immediately after the executor subcommand, so the included executor leaves dry-run mode. Execute mode writes `evidence/executor-<stage>-run.json` with the stage, payload hash, command args, output, exit codes, per-command start/end timestamps, and per-command duration; failed executor commands are recorded before the CLI returns non-zero. Operators can then use `generate-executor-evidence-pr-draft` and `create-executor-evidence-pr` to submit executor-only evidence, including DDL apply evidence, through GitHub review.

For DDL, it produces one `apply-ddl` command per SQL file under `schema/tidb-ddl/`. For export, it produces one command per export chunk. For import, it produces one command per import job. For CDC, it produces one command per tracked source table. It fails fast if an approved export/import/CDC plan has no work items, and export fails fast if any chunk predicate still contains `TODO`. For validation, it reads `plan/validation-plan.yaml` and produces one `validate-count` command for each check whose `type` is `row_count` or `row-count`; it passes optional source `predicate` and target `target_predicate` values through to the executor, and fails fast if the approved validation plan has no supported row-count checks. The executor shell does not itself connect to SQL Server, TiDB, Kafka, or object storage.

`sqlserver2tidb-executor` is currently included as a narrow execution adapter. It parses `apply-ddl`, `export`, `import`, `validate-count`, and `cdc` work-item arguments and prints the work-item context by default. `apply-ddl --execute` reads a reviewed DDL file, rejects files that still contain `TODO`, and applies the SQL statements to TiDB/MySQL using a connection string read from `SQLSERVER2TIDB_TARGET_CONNECTION_STRING` or `--target-connection-string-env`. `export --execute` supports SQL Server query execution into a local `file://` CSV file, using a connection string read from `SQLSERVER2TIDB_SOURCE_CONNECTION_STRING` or `--source-connection-string-env`. It rejects export predicates that still contain `TODO` and refuses non-`file://` output URIs. The local CSV format writes the source header plus an internal `__sqlserver2tidb_null_bitmap` tail column so import can restore SQL NULL values. `import --execute` supports local `file://` CSV input and streaming row-by-row inserts into TiDB/MySQL, using the same target connection string mechanism; it excludes the internal null bitmap column from the target insert. Import commits rows in batches controlled by `--import-batch-size` to avoid loading the full CSV into memory. `validate-count --execute` opens SQL Server and TiDB/MySQL connections, runs `COUNT(*)` against the quoted source and target objects, applies source and target predicates independently when provided, and returns non-zero when the counts differ. Object storage export/import formats, TiDB Lightning or `IMPORT INTO`, checksum validation, sampled hash validation, business SQL validation, and CDC apply logic should be implemented inside this binary incrementally behind the same approval/hash gate. `cdc --execute` still returns an explicit not-implemented error.

## CDC Plan And Worker

`generate-cdc-plan` is deterministic and file-backed:

- Input: `clusters/<source_cluster_id>/inventory/inventory.json`.
- Input: `clusters/<source_cluster_id>/projects/<project_id>/project.yaml`.
- Input: operator-supplied CDC mode, required retention hours, and apply batch size.
- Output: project-local `plan/cdc-plan.yaml`.

It filters inventory by the project source database and source schemas, then records tracked source/target table pairs and the source-cluster checkpoint file. It does not connect to SQL Server or TiDB, enable SQL Server CDC, create Debezium connectors, read LSNs, write Kafka offsets, or start TiDB apply.

The current `worker-cdc` implementation is metadata-only. It executes only after `approvals/cdc-approval.yaml` has:

- `action: cdc`
- `status: approved`
- at least one `approved_by` entry
- `payload_hash` matching the current CDC payload

The CDC payload hash covers:

- `project.yaml`
- `plan/cdc-plan.yaml`

When approved, the worker writes:

- project-local `state/migration-state.yaml`
- source-cluster-level `state/cdc-checkpoint.yaml`
- project-local `evidence/cdc-catchup.json`

It marks the CDC phase as `planned`; it does not assert that CDC is enabled, caught up, or safe for cutover. It fails fast when the approved CDC plan has no tracked tables or required tracked-table fields are missing.

## Validation Plan Generation

`generate-validation-plan` is deterministic and file-backed:

- Input: `clusters/<source_cluster_id>/inventory/inventory.json`.
- Input: `clusters/<source_cluster_id>/projects/<project_id>/project.yaml`.
- Output: project-local `plan/validation-plan.yaml`.

It filters inventory by the project source database and source schemas. For each in-scope table, it writes one `row_count` check with a reviewed source object and target object pair. The source object uses `<source_database>.<schema>.<table>`. The target object uses `<target_database>.<table>` for single-schema projects and `<target_database>.<schema>_<table>` for multi-schema projects, matching schema, export/import, and CDC plan generation.

The command does not connect to SQL Server or TiDB and does not execute validation. It only creates a draft plan that must be reviewed through GitHub before `compute-payload-hash --stage validation`, `worker-validate`, or `worker-executor --stage validation` can use it. The repository includes `global/schemas/export-plan.schema.json`, `global/schemas/import-plan.schema.json`, `global/schemas/cdc-plan.schema.json`, and `global/schemas/validation-plan.schema.json`, and maps them from `global/policies/file-schema-policy.yaml`. `validate-repo` also checks committed export/import/CDC work items and `row_count` or `row-count` checks, reporting an invalid repository when required execution fields are missing.

## Worker Reconcile

`worker-reconcile` is the current bridge between explicit one-project workers and a future reconcile loop. In `--dry-run` mode, it scans:

- `clusters/<source_cluster_id>/projects/<project_id>/project.yaml`
- project approval files for `ddl`, `export`, `import`, `cdc`, and `validation`
- the payload files covered by each stage hash

For each project/stage pair, it reports:

- `ready` when approval is approved, `approved_by` is non-empty, and the payload hash matches
- `blocked` with the deterministic reason returned by the approval/hash gate
- the exact single-project worker command for ready metadata actions
- `worker-executor --stage ddl` for ready DDL executor actions

In `--execute-next` mode, it selects the first ready metadata-only action in source-cluster/project/stage order, acquires or renews the source-cluster `state/worker-lease.yaml`, and runs exactly one metadata-only worker action: `export`, `import`, `cdc`, or `validation`. DDL is intentionally executor-only and must be run through `worker-executor --stage ddl`, so a ready DDL action is reported by `--dry-run` but is not selected by `--execute-next`. The same holder can renew its own unexpired lease. A different holder is blocked until the lease expires. This mode writes the same state/evidence files that the selected explicit worker writes, plus the source-cluster lease file.

When `--state-pr-draft` is enabled together with `--execute-next`, the command also writes a project-local PR body at `clusters/<source_cluster_id>/projects/<project_id>/prs/reconcile-<stage>-state-pr.md`. The draft records the selected stage, status, payload hash, lease id, branch naming convention, and files to review. For CDC, it includes the source-cluster `state/cdc-checkpoint.yaml` in addition to project state/evidence and the worker lease.

The command still does not connect to SQL Server, TiDB, Kafka, or object storage. It also does not create branches, open PRs, inspect GitHub merge state, or push bot commits by itself. `create-worker-state-pr` is the explicit follow-up step that can turn the generated state PR draft into a branch, commit, push, and GitHub PR.

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

The generated split predicate is intentionally a `TODO` placeholder because a safe split key must be reviewed per table. After export approval, `worker-export` and `worker-executor --stage export` reject export plans whose predicates still contain `TODO`. The command does not connect to SQL Server or TiDB, does not read data, does not write object storage, and does not start `IMPORT INTO` or TiDB Lightning.

## LLM Boundary

LLMs may generate:

- compatibility explanations
- schema rewrite candidates
- migration plan drafts
- export/import chunking or split-key recommendations
- CDC risk notes, retention recommendations, and connector configuration candidates
- PR descriptions
- validation report narratives
- incident diagnosis suggestions

LLMs are not required for deterministic repository commands such as `validate-repo`, `discover-sqlserver --dry-run`, `analyze-compatibility`, `generate-schema-draft`, `generate-data-plans`, `generate-cdc-plan`, `generate-validation-plan`, `generate-pr-draft`, `create-pr`, `create-worker-state-pr`, `generate-executor-evidence-pr-draft`, `create-executor-evidence-pr`, `compute-payload-hash`, `worker-export`, `worker-import`, `worker-cdc`, `worker-validate`, `worker-executor`, `worker-reconcile --dry-run`, or `worker-reconcile --execute-next`. For schema work, the LLM may read `conversion-report.md` and `schema-diff.json` to propose candidate rewrites, but the candidate must be committed as reviewed files before any worker can use it. For export/import planning, the LLM may propose split keys or risk notes, but generated predicates and execution settings still need PR review before workers can use them. For CDC planning, the LLM may explain retention or connector risks, but LSN, offset, catch-up, and cutover gates must come from deterministic runtime checks and GitHub approvals. For validation planning, the LLM may suggest extra checksum, sampled-hash, or business SQL checks, but the committed `plan/validation-plan.yaml` and pass/fail results must be produced and reviewed through deterministic files. For PR work, the LLM may refine prose, but file lists, approval files, state/evidence files, lease files, git commands, GitHub CLI arguments, executor command arguments, and stage gates must remain deterministic.

LLMs must not decide:

- whether DDL can run
- whether import can start
- whether CDC has caught up
- whether validation passed
- whether cutover can proceed
- whether destructive cleanup can run

Those gates are handled by deterministic rules, observed runtime state, and GitHub approvals.
