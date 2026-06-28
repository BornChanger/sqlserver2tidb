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
- validates and includes `evidence/executor-<stage>-run.json` when executor run evidence exists, using the same approval, payload hash, reviewed instruction, status, command ID, args, exit-code, command-error, and timing checks as executor evidence PRs;
- includes source-cluster `state/cdc-checkpoint.yaml` for CDC state PRs;
- reports stale PR body file lists during dry-run and refreshes the body before commit in `--execute` mode;
- reconstructs deterministic `git switch`, `git add`, `git commit`, `git push`, and `gh pr create` commands;
- defaults to dry-run and only mutates the local checkout when `--execute` is explicitly set.

This command creates a local branch, commits files, pushes the branch, and opens a PR only in explicit execute mode. It still does not merge PRs, approve PRs, bypass branch protection, or decide whether a worker result is operationally correct.

`generate-executor-evidence-pr-draft` and `create-executor-evidence-pr` wrap executor-only evidence review. They:

- require `evidence/executor-<stage>-run.json` to exist;
- require the matching stage approval to be approved and payload-hash current;
- require the corresponding DDL schema diff or stage plan to still be reviewed;
- reject executor evidence whose source cluster, project, stage, or payload hash does not match the requested/current approved metadata;
- reject executor evidence whose status is not `succeeded` or `failed`, or whose top-level `generated_at` is present but not RFC3339;
- reject duplicate executor command IDs;
- reject executor evidence without command records, and require every command record to include `id`, non-empty `args`, shell-quoted `shell_command` matching those args, and `exit_code`;
- require every command record to include RFC3339Nano `started_at` and `completed_at`, plus non-negative `duration_ms`, reject completion timestamps earlier than start timestamps, and require optional `cdc_applied_changes` values to be non-negative;
- reject `succeeded` evidence when any command has a non-zero `exit_code`;
- reject `failed` evidence when no command has a non-zero `exit_code`;
- render command IDs, exit codes, command errors when present, timestamps, durations, and whitespace-normalized output summaries directly into the generated PR body for review;
- reject stale executor evidence PR bodies whose content no longer matches the current evidence and approval context;
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

Current checks are deterministic repository checks: inventory parseability/status and schema diff parseability/status/generated_at/non-negative summary counts, generated DDL presence, manual review item clearance, conversion report presence, validation plan presence, review plan status validation, required field validation for committed row-count and query-based validation checks, and rejection of unresolved TODO predicates. `validate-repo` also requires file-schema-policy mappings for migration, export, import, CDC, and validation plans; checks cluster/project directory IDs against committed metadata; verifies source profile, cluster state, CDC checkpoint mode/phase/status/updated_at, worker lease ownership, active lease fields, active lease RFC3339 timestamps, project state phase/status/updated_at, export/import state phase/status/updated_at, validation status state/updated_at, schema diff ownership/status/generated_at/summary counts, evidence JSON ownership/status/generated_at, migration plan, and approval files belong to the same source cluster/project; requires export/import/CDC/validation plan status values to be present and one of `draft`, `reviewed`, or `approved`; validates executor evidence JSON structure and top-level `generated_at` when present; validates state, approval, and executor-evidence `payload_hash` fields use `sha256:<64 hex chars>` when present; validates approval action, status, approved reviewer presence, and `approved_at` RFC3339 timestamps when present, with `approved_at` required for approved approvals; checks export/import/CDC plan work-item fields when those plans contain work items; validates export chunk `output_uri` values against the included executor; validates import job `source_uri` values against the selected import engine; and rejects import job `fields` unless the selected import engine is `tidb-import-into`. For `tidb-import-into`, it also rejects empty fields, duplicate columns, duplicate user variables, user variables outside the simple `@name` character set, and missing fields for `s3://` or `gs://` sources because remote header inspection is not implemented. Empty draft plan lists remain valid during initialization. The validation worker itself still does not connect to SQL Server or TiDB; when the plan is structurally valid, its state/report message summarizes supported validation check counts by type. When `evidence/executor-validation-run.json` exists, the validation worker validates that evidence against the current approval hash and appends a deterministic executor-evidence summary; failed executor validation commands make the validation worker result failed. For real validation, `worker-executor --stage validation` can generate `sqlserver2tidb-executor validate-count` commands for row-count checks and `sqlserver2tidb-executor validate-query` commands for reviewed `checksum`, `sampled_hash`, `bucketed_count`, and `business_sql` scalar-query checks after validation approval and payload hash checks pass. `validate-count --execute` compares SQL Server and TiDB row counts for one reviewed source/target object pair; `validate-query --execute` compares one-row/one-column SQL Server and TiDB query results. Native row digest generators and bucketed sampled-hash strategies remain future executor capabilities.

## Export And Import Workers

The current export/import worker implementations are metadata-only. They do not connect to SQL Server, TiDB, or object storage. They execute only after the matching approval file has:

- `action: export` or `action: import`
- `status: approved`
- at least one `approved_by` entry
- `payload_hash` matching the current stage payload

The export and import workers also require their plan files to have `status: reviewed` or `status: approved`; `status: draft` is not executable even when an approval file has been edited to approved.

The export payload hash covers:

- `project.yaml`
- `plan/export-plan.yaml`

The import payload hash covers:

- `project.yaml`
- `schema/tidb-ddl/`
- `plan/export-plan.yaml`
- `plan/import-plan.yaml`

The export worker reads a reviewed or approved `plan/export-plan.yaml` and writes planned chunk state to `state/export-chunks.yaml`, plus `evidence/precheck.json`. The import worker reads a reviewed or approved `plan/import-plan.yaml` and writes planned import job state to `state/import-jobs.yaml`, plus `evidence/import-summary.json`. Export and import workers fail fast when the approved plan has no work items, required work-item fields are missing, or import job `fields` are incompatible with the selected import engine or contain invalid `tidb-import-into` field tokens.

These workers establish the approval and state write-back contract for future real executors. They intentionally mark reviewed items as `planned`; they do not mark chunks as exported or jobs as imported.

## Worker Executor Shell

`worker-executor` is the current external executor shell. It is deterministic and file-backed:

- Input: approved `ddl`, `export`, `import`, `cdc`, or `validation` stage metadata.
- Input: the matching schema files or plan file under `schema/` or `plan/`.
- Output: dry-run command lines by default.
- Optional execution: external command invocation only when `--execute` is explicitly set.

The command reuses `requireApprovedStage`, so it refuses to produce executor commands unless the stage approval is approved, has reviewers, and the payload hash matches the current repository files. The default external binary is `sqlserver2tidb-executor`; operators can override it with `--executor-binary`. Operators can also pass `--source-connection-string-env`, `--target-connection-string-env`, `--import-batch-size`, `--require-empty-target`, `--command-timeout`, `--command-retries`, `--retry-backoff`, and `--resume`; these values are rendered into generated executor commands or local execution behavior instead of being stored in GitHub metadata. `--require-empty-target` is rendered only for `sql-insert` import commands and makes the executor fail before source CSV open when the target table already has rows. It is intentionally off by default because multi-chunk imports can legitimately append multiple CSV chunks into the same target table. Import job `fields` are rendered only for `tidb-import-into`; executor preparation rejects `fields` on `sql-insert` plans because that engine reads CSV headers directly, and validates `tidb-import-into` field tokens before rendering `--fields`. In `worker-executor --execute` mode, the CLI invokes the external binary and injects the executor-level `--execute` flag immediately after the executor subcommand, so the included executor leaves dry-run mode. Execute mode writes `evidence/executor-<stage>-run.json` with the stage, payload hash, command args, output, exit codes, per-command start/end timestamps, per-command duration, optional retry `attempt_count` / `attempts`, optional command `error`, and for CDC commands the parsed `cdc_applied_changes` value from the required `applied changes: N` output line. `--command-timeout <duration>` caps each external executor command; the default `0` disables the timeout. `--command-retries <n>` retries failed external executor commands up to `n` times after `--retry-backoff`; command-level evidence records the final attempt and `attempts` preserves the per-attempt audit trail. `--resume` loads the existing executor evidence for the same stage and current approved payload hash, then skips only commands whose command ID, exact executor args, successful exit code, empty command error, and CDC applied-change evidence when relevant all match. Timed-out commands are killed, recorded as failed command evidence with `error: command timed out after <duration>`, and then handled like other command failures after retries are exhausted. Validation execution is intentionally aggregate: `worker-executor --stage validation --execute` runs every generated validation command and then marks the evidence failed if any command failed, preserving the full mismatch set for review. DDL, export, import, and CDC execution remain fail-fast because continuing after those stages fail can compound side effects. Operators can then use `generate-executor-evidence-pr-draft` and `create-executor-evidence-pr` to submit executor-only evidence, including DDL apply evidence, through GitHub review; evidence PR generation rejects evidence if the corresponding DDL schema diff or stage plan is no longer reviewed.

`advance-cdc-checkpoint` turns successful CDC executor evidence into a source-cluster checkpoint snapshot. It validates `evidence/executor-cdc-run.json` through the same approval, payload hash, reviewed-plan, and command-structure checks as CDC executor evidence PRs, requires the evidence status to be `succeeded`, requires `cdc_applied_changes`, and verifies each evidence command's `--source-object`, `--target-object`, `--from-lsn`, and `--to-lsn` values match the current reviewed CDC plan. It then rewrites `clusters/<source_cluster_id>/state/cdc-checkpoint.yaml` with `status: running` by default, or `caught_up` when explicitly requested. It does not inspect SQL Server min/max LSNs or run a long-lived streaming loop.

`prepare-cdc-range` prepares the next reviewed LSN range without connecting to SQL Server. It reads the source-cluster checkpoint entries, uses each table's checkpoint `to_lsn` as the next plan `from_lsn`, accepts an operator-provided `--to-lsn`, validates `from_lsn <= to_lsn`, and rewrites `plan/cdc-plan.yaml`. If a table has no checkpoint entry, operators must pass `--from-lsn` for the initial range. The command always resets the CDC plan and tracked table statuses to `draft`, forcing the new range through GitHub review and stage approval before `worker-executor --stage cdc` can run.

`prepare-cdc-iteration` is the deterministic GitHub-file step that can be called by a CDC scheduler after `sqlserver2tidb-executor cdc-lsn --execute` or another trusted LSN probe has produced the current max LSN. It reads `state/cdc-checkpoint.yaml`, compares each tracked table checkpoint `to_lsn` with `--max-lsn`, and either reports `caught_up` without mutating files or rewrites `plan/cdc-plan.yaml` with the next range and optionally writes `prs/cdc-range-pr.md`. It deliberately keeps SQL Server probing, GitHub approval, and CDC execution as separate gates: the command accepts a supplied max LSN, but it does not connect to SQL Server, approve the new plan, run `worker-executor --stage cdc`, or advance the checkpoint.

For DDL, it produces one `apply-ddl` command per SQL file under `schema/tidb-ddl/` after `schema/schema-diff.json` is `reviewed`. For export, it produces one command per export chunk when `plan/export-plan.yaml` uses `format: csv`. For import, it produces one command per import job when `plan/import-plan.yaml` uses `engine: sql-insert` or `engine: tidb-import-into`; `engine: import-into` is accepted as an alias and normalized to `tidb-import-into`. For CDC, it produces one command per tracked source table and passes reviewed `columns` / `key_columns` / `from_lsn` / `to_lsn` as executor `--columns` / `--key-columns` / `--from-lsn` / `--to-lsn`. It fails fast if DDL schema diff is not `reviewed`, if an export/import/CDC/validation plan is still `draft`, if an approved export/import/CDC plan has no work items, if an approved CDC table has no captured columns, key columns, or LSN range, if CDC key columns are not present in captured columns, if export format or import engine is not supported by the included executor, if an export chunk `output_uri` is not supported by the included executor, if an import job `source_uri` is not supported by the selected import engine, and export fails fast if any chunk predicate still contains `TODO`. For validation, it reads `plan/validation-plan.yaml` and produces one `validate-count` command for each check whose `type` is `row_count` or `row-count`, plus one `validate-query` command for each check whose `type` is `checksum`, `sampled_hash`, `bucketed_count`, or `business_sql`; row-count checks pass optional source `predicate` and target `target_predicate` values, and query-based checks pass reviewed `source_sql` and `target_sql` scalar queries. It fails fast if the approved validation plan has no supported row-count, checksum, sampled-hash, bucketed-count, or business-SQL checks. The executor shell does not itself connect to SQL Server, TiDB, Kafka, or object storage.

`sqlserver2tidb-executor` is currently included as a narrow execution adapter. It parses `apply-ddl`, `export`, `import`, `validate-count`, `validate-query`, `cdc-lsn`, and `cdc` work-item arguments and prints the work-item context by default. Executor dry-runs validate object names through the same SQL builders used by execute mode. Apply-DDL dry-runs read the DDL file and reject unresolved `TODO` markers or empty SQL without opening TiDB; export dry-runs validate output URI compatibility and reject `TODO` predicates without opening SQL Server or writing CSV output; import dry-runs validate the selected engine's source URI compatibility and reviewed `fields` without opening TiDB or the CSV source; validation dry-runs reject unresolved `TODO` predicates and scalar SQL without opening SQL Server or TiDB; CDC dry-runs validate captured columns, key-column membership, and LSN format/range without starting a CDC reader or TiDB apply worker. `apply-ddl --execute` reads a reviewed DDL file, rejects files that still contain `TODO`, and applies the SQL statements to TiDB/MySQL using a connection string read from `SQLSERVER2TIDB_TARGET_CONNECTION_STRING` or `--target-connection-string-env`. `export --execute` supports SQL Server query execution into local `file://` CSV output or HTTP(S) CSV output such as a presigned object storage URL, using a connection string read from `SQLSERVER2TIDB_SOURCE_CONNECTION_STRING` or `--source-connection-string-env`. It rejects export predicates that still contain `TODO` and refuses unsupported output URI schemes such as native `s3://`. The CSV format writes the source header plus an internal `__sqlserver2tidb_null_bitmap` tail column so import can restore SQL NULL values. `import --execute` defaults to `--engine sql-insert`, supports local `file://` CSV input and HTTP(S) CSV input, then streams row-by-row inserts into TiDB/MySQL using the same target connection string mechanism; HTTP(S) sources must include a host, and it excludes the internal null bitmap column from the target insert. Import commits rows in batches controlled by `--import-batch-size` to avoid loading the full CSV into memory. When `--require-empty-target` is set for `sql-insert`, the executor opens TiDB first, runs a quoted `COUNT(*)` preflight against the target table, and fails before opening the CSV source if the target is non-empty. With `--engine tidb-import-into`, it builds and executes TiDB `IMPORT INTO <table> FROM <fileLocation> FORMAT 'csv' WITH skip_rows=1` using an absolute local path, local `file://`, `s3://`, or `gs://` file locations; S3/GCS locations must include both a bucket and object path. Before executing `IMPORT INTO`, it always runs a quoted `COUNT(*)` preflight against the target table and fails if the table is not empty; this catches the documented empty-table prerequisite but does not replace the operational requirement to avoid concurrent DDL/DML during the import. For local path and `file://` sources, it rejects relative local paths, reads the CSV header, and maps a trailing internal null bitmap column to a TiDB user variable so that field is skipped. For `s3://` and `gs://` sources, operators should use reviewed import-plan `fields` generated by `generate-data-plans --import-engine tidb-import-into`, because remote header inspection is not implemented. TiDB `IMPORT INTO` requires an existing empty target table and follows the restrictions in the official TiDB documentation: https://docs.pingcap.com/tidb/stable/sql-statement-import-into/. `validate-count --execute` opens SQL Server and TiDB/MySQL connections, runs `COUNT(*)` against the quoted source and target objects, applies source and target predicates independently when provided, and returns non-zero when the counts differ. `validate-query --execute` opens SQL Server and TiDB/MySQL connections, runs reviewed one-row/one-column source and target SQL, normalizes the scalar values, and returns non-zero when they differ; generated `checksum`, `sampled_hash`, `bucketed_count`, and manually reviewed `business_sql` checks use this path. `cdc-lsn --execute` opens SQL Server, queries `sys.fn_cdc_get_max_lsn()`, and when a source object is provided also derives the capture instance and queries `sys.fn_cdc_get_min_lsn()`. `cdc --execute` validates reviewed captured columns, key columns, source/target connection strings, and explicit `--from-lsn` / `--to-lsn` binary LSN boundaries, then queries SQL Server CDC and applies upsert/delete changes to TiDB. Native object storage export clients, Parquet formats, TiDB Lightning, broader row digest generators, production-grade bucketed sampled-hash strategies, and checkpoint-driven CDC orchestration should be implemented inside this binary incrementally behind the same approval/hash gate.

## CDC Plan And Worker

`generate-cdc-plan` is deterministic and file-backed:

- Input: `clusters/<source_cluster_id>/inventory/inventory.json`.
- Input: `clusters/<source_cluster_id>/projects/<project_id>/project.yaml`.
- Input: operator-supplied CDC mode, required retention hours, and apply batch size.
- Output: project-local `plan/cdc-plan.yaml`.

It filters inventory by the project source database and source schemas, then records tracked source/target table pairs, non-computed captured columns, target apply key columns, and the source-cluster checkpoint file. Key columns are chosen from the discovered SQL Server primary key first, then from a non-filtered unique index. If no such key exists, the draft records `key_columns: []`; reviewed or approved CDC plans are not executable until key columns are filled in and reviewed. It does not connect to SQL Server or TiDB, enable SQL Server CDC, create Debezium connectors, read LSNs, write Kafka offsets, or start TiDB apply.

The current `worker-cdc` implementation is metadata-only. It executes only after `approvals/cdc-approval.yaml` has:

- `action: cdc`
- `status: approved`
- at least one `approved_by` entry
- `payload_hash` matching the current CDC payload

It also requires `plan/cdc-plan.yaml` to have `status: reviewed` or `status: approved`; draft CDC plans are not executable even when the approval file is already approved.

The CDC payload hash covers:

- `project.yaml`
- `plan/cdc-plan.yaml`

When approved, the worker writes:

- project-local `state/migration-state.yaml`
- source-cluster-level `state/cdc-checkpoint.yaml`
- project-local `evidence/cdc-catchup.json`

It marks the CDC phase as `planned`; it does not assert that CDC is enabled, caught up, or safe for cutover. It fails fast when the approved CDC plan has no tracked tables, no reviewed captured columns, no reviewed key columns, key columns outside captured columns, or required tracked-table fields are missing.

## Validation Plan Generation

`generate-validation-plan` is deterministic and file-backed:

- Input: `clusters/<source_cluster_id>/inventory/inventory.json`.
- Input: `clusters/<source_cluster_id>/projects/<project_id>/project.yaml`.
- Output: project-local `plan/validation-plan.yaml`.

It filters inventory by the project source database and source schemas. For each in-scope table, it writes one `row_count` check with a reviewed source object and target object pair. The source object uses `<source_database>.<schema>.<table>`. The target object uses `<target_database>.<table>` for single-schema projects and `<target_database>.<schema>_<table>` for multi-schema projects, matching schema, export/import, and CDC plan generation. With `--include-checksum`, it also writes exact-numeric `checksum` scalar-query checks for tables that have non-computed exact numeric columns. With `--include-sampled-hash`, it writes exact-numeric `sampled_hash` scalar-query checks for tables that also have an integer sample column; `--sample-modulo` controls the deterministic modulo sample predicate. With `--include-bucketed-count`, it writes one `bucketed_count` scalar-query check per modulo bucket for tables that have a non-computed integer bucket column; `--bucket-count` controls the number of generated buckets and is capped at `1024`.

The command does not connect to SQL Server or TiDB and does not execute validation. It only creates a draft plan that must be reviewed through GitHub before `compute-payload-hash --stage validation`, `worker-validate`, or `worker-executor --stage validation` can use it. The generated checksum/sampled-hash/bucketed-count checks are scalar-query drafts, not a universal row digest engine; operators can edit or replace them before review. The repository includes `global/schemas/export-plan.schema.json`, `global/schemas/import-plan.schema.json`, `global/schemas/cdc-plan.schema.json`, and `global/schemas/validation-plan.schema.json`, and maps them from `global/policies/file-schema-policy.yaml`. `validate-repo` also checks committed migration/export/import/CDC/validation plan metadata, schema diff metadata, state/evidence ownership, approval metadata, `row_count` / `row-count` checks, and query-based `checksum` / `sampled_hash` / `bucketed_count` / `business_sql` checks, reporting an invalid repository when required execution fields or ownership fields are missing or inconsistent.

## Worker Reconcile

`worker-reconcile` is the current bridge between explicit one-project workers and a future reconcile loop. In `--dry-run` mode, it scans:

- `clusters/<source_cluster_id>/projects/<project_id>/project.yaml`
- project approval files for `ddl`, `export`, `import`, `cdc`, and `validation`
- the payload files covered by each stage hash

For each project/stage pair, it reports:

- `ready` when approval is approved, `approved_by` is non-empty, the payload hash matches, DDL schema diff is reviewed, export/import/CDC/validation plan status gates pass, and the same approved payload hash does not already have non-empty stage state
- `blocked` with the deterministic reason returned by the approval/hash gate
- the exact single-project worker command for ready metadata actions
- `worker-executor --stage ddl` for ready DDL executor actions

With `--dry-run --json`, the same report is written as JSON for automation and monitoring integrations. JSON output is intentionally read-only and is not accepted with execute modes.

In `--execute-next` mode, it selects the first ready metadata-only action in source-cluster/project/stage order, acquires or renews the source-cluster `state/worker-lease.yaml`, and runs exactly one metadata-only worker action: `export`, `import`, `cdc`, or `validation`. DDL is intentionally executor-only and must be run through `worker-executor --stage ddl`, so a ready DDL action is reported by `--dry-run` but is not selected by `--execute-next`. The same holder can renew its own unexpired lease. A different holder is blocked until the lease expires. This mode writes the same state/evidence files that the selected explicit worker writes, plus the source-cluster lease file. Active lease records must carry non-empty `holder`, `lease_id`, `project_id`, `expires_at`, and `renewed_at`; `project_id` must reference an existing project directory under the same source cluster; timestamps must be RFC3339 and `expires_at` must not be before `renewed_at`; `phase: idle` remains the empty placeholder state.

In `--loop` mode, it repeatedly runs the same execute-next selection with one holder until no ready metadata-only action remains or `--max-iterations` is reached. `--max-iterations 0` means the loop stops only when the repository has no ready metadata-only action. The loop uses the same approval, payload hash, plan status, state dedupe, and lease rules as `--execute-next`; it does not select DDL executor actions and it does not run external executor commands.

`worker-agent` is the delivery-oriented entrypoint for the same loop. It accepts the same holder, optional source-cluster scope, lease TTL, interval, max iteration, and state PR draft options, prints an agent header, and then delegates to the deterministic reconcile loop. It also supports explicit `--poll` mode, where no-ready scans sleep and retry instead of exiting; `--idle-iterations` can bound idle polling for tests or batch jobs. It exists so local process managers and container runtimes can run `sqlserver2tidb worker-agent ...` without depending on the lower-level `worker-reconcile --loop` spelling.

When `--state-pr-draft` is enabled together with `--execute-next`, the command also writes a project-local PR body at `clusters/<source_cluster_id>/projects/<project_id>/prs/reconcile-<stage>-state-pr.md`. The draft records the selected stage, status, payload hash, lease id, branch naming convention, and files to review. For CDC, it includes the source-cluster `state/cdc-checkpoint.yaml` in addition to project state/evidence and the worker lease.

The command still does not connect to SQL Server, TiDB, Kafka, or object storage. It also does not create branches, open PRs, inspect GitHub merge state, or push bot commits by itself. `create-worker-state-pr` is the explicit follow-up step that can turn the generated state PR draft into a branch, commit, push, and GitHub PR.

## Metadata Boundary

Metadata is organized by upstream SQL Server cluster:

```text
clusters/<source_cluster_id>/
```

This is the right boundary for source inventory, SQL Server CDC, source permissions, and worker leases.
The source-cluster `state/cdc-checkpoint.yaml` must stay aligned with `cluster.yaml`: initialization uses `capture_mode`, CDC worker write-back uses `mode`, and either field must match the cluster `cdc.mode` when present. Checkpoint phase is optional during initialization but must be `cdc` when present. Checkpoint status is restricted to `not_started`, `planned`, `running`, `caught_up`, or `failed`; `updated_at` must be non-empty RFC3339. After `advance-cdc-checkpoint`, checkpoint entries are source-cluster snapshots derived from reviewed executor evidence and should be committed in the same PR as the CDC executor evidence. `validate-repo` checks each checkpoint entry has source/target objects, 10-byte hex LSN boundaries with `from_lsn <= to_lsn`, non-negative `applied_changes`, and RFC3339 `completed_at`.

Migration projects live below the source cluster:

```text
clusters/<source_cluster_id>/projects/<project_id>/
```

A project represents one independently planned, approved, validated, and cut-over migration unit.
The project-local `state/migration-state.yaml` phase is restricted to `planning`, `ddl`, `export`, `import`, `cdc`, `validation`, `cutover`, or `completed`; status is restricted to `not_started`, `planned`, `running`, `completed`, or `failed`; `updated_at` must be non-empty RFC3339.
The project-local `state/export-chunks.yaml` and `state/import-jobs.yaml` phase and status fields are optional during initialization. When present, phase must match `export` and `import` respectively, status must be `planned`, and `updated_at` must be RFC3339.
The project-local `state/validation-status.yaml` status is restricted to `pending`, `passed`, or `failed`; its optional phase must be `validation`, and when present `updated_at` must be RFC3339.
State `payload_hash` fields are optional during initialization, but must use `sha256:<64 hex chars>` when present.

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
- Input: operator-supplied CSV URI prefix and chunk sizing flags.
- Output: project-local `plan/export-plan.yaml`.
- Output: project-local `plan/import-plan.yaml`.

It filters inventory by the project source database and source schemas. It estimates full-load export chunks from inventory `row_count`, writes executor-supported CSV URIs for each chunk, and creates matching import jobs that depend on those export chunks. The generator rejects unsupported export formats and unsupported import engines. With the default `sql-insert` import engine, URI prefixes must be local `file://`, `http://`, or `https://`, and HTTP(S) prefixes must include a host; with `tidb-import-into`, URI prefixes must be local absolute `file://`, `s3://`, or `gs://`, and S3/GCS prefixes must include a bucket. Only `tidb-import-into` import jobs include a reviewed `fields` list derived from inventory columns plus `@sqlserver2tidb_null_bitmap` so TiDB can skip the internal CSV tail column even when the source file is remote object storage.

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

LLMs are not required for deterministic repository commands such as `validate-repo`, `discover-sqlserver --dry-run`, `analyze-compatibility`, `generate-schema-draft`, `generate-data-plans`, `generate-cdc-plan`, `prepare-cdc-range`, `prepare-cdc-iteration`, `generate-validation-plan`, `generate-pr-draft`, `create-pr`, `create-worker-state-pr`, `generate-executor-evidence-pr-draft`, `create-executor-evidence-pr`, `compute-payload-hash`, `worker-export`, `worker-import`, `worker-cdc`, `worker-validate`, `worker-executor`, `advance-cdc-checkpoint`, `worker-reconcile --dry-run`, `worker-reconcile --execute-next`, `worker-reconcile --loop`, or `worker-agent`. For schema work, the LLM may read `conversion-report.md` and `schema-diff.json` to propose candidate rewrites, but the candidate must be committed as reviewed files before any worker can use it. For export/import planning, the LLM may propose split keys or risk notes, but generated predicates and execution settings still need PR review before workers can use them. For CDC planning, the LLM may explain retention or connector risks, but LSN, offset, catch-up, and cutover gates must come from deterministic runtime checks and GitHub approvals. For validation planning, the LLM may suggest extra checksum, sampled-hash, bucketed-count, or business SQL checks, but the committed `plan/validation-plan.yaml` and pass/fail results must be produced and reviewed through deterministic files. For PR work, the LLM may refine prose, but file lists, approval files, state/evidence files, lease files, git commands, GitHub CLI arguments, executor command arguments, and stage gates must remain deterministic.

LLMs must not decide:

- whether DDL can run
- whether import can start
- whether CDC has caught up
- whether validation passed
- whether cutover can proceed
- whether destructive cleanup can run

Those gates are handled by deterministic rules, observed runtime state, and GitHub approvals.
