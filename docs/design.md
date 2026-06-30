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
- require every command record to include RFC3339Nano `started_at` and `completed_at`, plus non-negative `duration_ms`, reject completion timestamps earlier than start timestamps, require optional `cdc_applied_changes`, `data_rows`, and `data_bytes` values to be non-negative, and require optional `data_sha256` values to use `sha256:<64 hex chars>`;
- reject `succeeded` evidence when any command has a non-zero `exit_code`;
- reject `failed` evidence when no command has a non-zero `exit_code`;
- render command IDs, exit codes, command errors when present, timestamps, durations, optional data metrics, and whitespace-normalized output summaries directly into the generated PR body for review;
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

Current checks are deterministic repository checks. The validation worker checks:

- inventory parseability and status;
- schema diff parseability, status, `generated_at`, and non-negative summary counts;
- generated DDL presence;
- manual review item clearance;
- conversion report presence;
- validation plan presence and status;
- required fields for committed row-count and query-based validation checks;
- unresolved `TODO` predicates.

`validate-repo` performs the broader repository checks. It requires file-schema-policy mappings for migration, export, import, CDC, and validation plans. It also verifies that cluster/project directory IDs, source profiles, state files, evidence files, migration plans, and approval files belong to the same source cluster/project.

State and approval fields are validated before execution. This includes CDC checkpoint mode/phase/status/updated_at, worker lease ownership, active lease fields, active lease RFC3339 timestamps, project state, export/import state, validation state, schema diff ownership/status/generated_at/summary counts, and evidence JSON ownership/status/generated_at.

Plan and approval metadata must also be structurally safe:

- export/import/CDC/validation plan statuses must be `draft`, `reviewed`, or `approved`;
- executor evidence JSON must have valid structure and top-level `generated_at` when present;
- state, approval, and executor-evidence `payload_hash` values must use `sha256:<64 hex chars>` when present;
- approval action, status, reviewers, and `approved_at` must be valid, with `approved_at` required for approved approvals;
- export/import/CDC work items must include their required execution fields when plans contain work items.

Object and data-plan checks stay executor-aware:

- SQL Server source objects must be `schema.table` or `database.schema.table`;
- TiDB target objects must be `table` or `database.table`;
- export/import `compression` must be `none` or `gzip`;
- gzip with `tidb-import-into` is rejected;
- export `null_encoding` must be `bitmap` or `backslash-n`;
- export chunk `output_uri` must be supported by the included executor;
- import job `source_uri` must be compatible with the selected import engine;
- import job `fields` are rejected unless the selected import engine is `tidb-import-into`.

For `tidb-import-into`, validation also rejects empty fields, duplicate columns, duplicate user variables, and user variables outside the simple `@name` character set. For `tidb-lightning`, it validates that jobs resolve to one data source directory and that generated exports use Lightning-friendly `backslash-n` NULL encoding. S3 and GCS sources can derive fields from the remote CSV header during executor pre-audit.

Empty draft plan lists remain valid during initialization. The validation worker itself still does not connect to SQL Server or TiDB. When the plan is structurally valid, its state/report message summarizes supported validation check counts by type.

When `evidence/executor-validation-run.json` exists, the validation worker validates that evidence against the current approval hash and appends a deterministic executor-evidence summary. Failed executor validation commands make the validation worker result failed.

For real validation, `worker-executor --stage validation` can generate:

- `sqlserver2tidb-executor validate-count` commands for row-count checks;
- `sqlserver2tidb-executor validate-query` commands for reviewed `checksum`, `sampled_hash`, `bucketed_count`, and `business_sql` scalar-query checks.

Those commands are generated only after validation approval and payload hash checks pass. `validate-count --execute` compares SQL Server and TiDB row counts for one reviewed source/target object pair. `validate-query --execute` compares one-row/one-column SQL Server and TiDB query results. Native row digest generators and bucketed sampled-hash strategies remain future executor capabilities.

Executor evidence validation rejects negative optional command-level data metrics. For export/import evidence, these metrics are stored as `data_rows`, `data_bytes`, and optional `data_sha256` when executor output includes complete non-negative row/byte metrics and a valid SHA256 digest line.

`data_sha256` is valid only together with `data_rows` and `data_bytes`. Successful export evidence, successful `sql-insert` import evidence, successful local-path, `file://`, S3, or GCS `tidb-import-into` evidence, and successful `tidb-lightning` evidence must include all three audit fields in both `validate-repo` and executor evidence PR generation.

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

The export worker reads a reviewed or approved `plan/export-plan.yaml`. It writes planned chunk state to `state/export-chunks.yaml` plus `evidence/precheck.json`.

The import worker reads a reviewed or approved `plan/import-plan.yaml`. It writes planned import job state to `state/import-jobs.yaml` plus `evidence/import-summary.json`.

Export and import workers fail fast when:

- the approved plan has no work items;
- required work-item fields are missing;
- plan compression is unsupported;
- gzip compression is paired with `tidb-import-into`;
- export `null_encoding` is unsupported;
- `tidb-lightning` jobs do not share one data source directory;
- import job `fields` are incompatible with the selected import engine;
- `tidb-import-into` field tokens are invalid.

These workers establish the approval and state write-back contract for future real executors. They intentionally mark reviewed items as `planned`; they do not mark chunks as exported or jobs as imported.

## Worker Executor Shell

`worker-executor` is the current external executor shell. It is deterministic and file-backed:

- Input: approved `ddl`, `export`, `import`, `cdc`, or `validation` stage metadata.
- Input: the matching schema files or plan file under `schema/` or `plan/`.
- Output: dry-run command lines by default.
- Optional execution: external command invocation only when `--execute` is explicitly set.

The command reuses `requireApprovedStage`. It refuses to produce executor commands unless the stage approval is approved, has reviewers, and the payload hash matches the current repository files.

`cdc-enable` deliberately maps to the existing CDC approval and `plan/cdc-plan.yaml`. Its evidence is still written as `evidence/executor-cdc-enable-run.json`, so setup evidence stays separate from LSN apply evidence.

The default external binary is `sqlserver2tidb-executor`; operators can override it with `--executor-binary`. Runtime flags such as `--source-connection-string-env`, `--target-connection-string-env`, `--import-batch-size`, `--require-empty-target`, `--command-timeout`, `--command-retries`, `--retry-backoff`, and `--resume` are rendered into generated executor commands or local execution behavior. They are not stored in GitHub metadata.

Before rendering export, import, validation, CDC enablement, or CDC apply commands, executor preparation reads `clusters/<source_cluster_id>/inventory/inventory.json`. It fails when a reviewed plan references a source table that no longer exists in inventory.

Schema drift checks are GitHub-file checks, not TiDB metadata-table dependencies:

- if `schema/schema-diff.json` is reviewed, source column baselines must still match current inventory column names/types;
- `tidb-import-into` reviewed `fields`, ignoring TiDB user variables, must still match current inventory columns;
- reviewed CDC captured columns must match current non-computed source columns;
- CDC `key_columns` must still match a current primary key or non-filtered unique index.

CDC setup and apply stay separate:

- `worker-executor --stage cdc-enable` generates one `sqlserver2tidb-executor cdc-enable` command per tracked table;
- the setup command passes reviewed `capture_instance`, `role_name`, `supports_net_changes`, and top-level `retention_hours_required`;
- `cdc-enable` does not require `from_lsn` / `to_lsn`;
- `worker-executor --stage cdc` remains the LSN apply stage and does require reviewed LSN ranges.

Import and compression behavior is rendered from reviewed plans:

- `--require-empty-target` is rendered only for `sql-insert` import commands;
- the empty-target check is off by default because multi-chunk imports can append multiple CSV chunks into the same target table;
- reviewed `compression: gzip` becomes executor `--compression gzip` for export, `sql-insert`, and `tidb-lightning`;
- import job `fields` are rendered only for `tidb-import-into`;
- `fields` on `sql-insert` and `tidb-lightning` plans are rejected because those engines read CSV headers directly;
- `tidb-lightning` plans render as one aggregate import command with `--engine tidb-lightning`, `--source-uri <data_source_uri>`, and `--import-plan <path>`.

In `worker-executor --execute` mode, the CLI invokes the external binary and injects the executor-level `--execute` flag immediately after the executor subcommand. This makes the included executor leave dry-run mode.

Execute mode writes `evidence/executor-<stage>-run.json` with:

- stage and payload hash;
- command args and output;
- exit codes;
- per-command start/end timestamps and duration;
- optional retry `attempt_count` / `attempts`;
- optional command `error`;
- parsed `cdc_applied_changes` for CDC apply commands.

Export, `sql-insert` import, local/file/S3/GCS `tidb-import-into`, and `tidb-lightning` import commands must emit complete `data_rows`, `data_bytes`, and `data_sha256` audit output when they succeed. A successful command that omits the complete audit tuple is recorded as failed command evidence immediately.

Timeout, retry, and resume behavior is explicit:

- `--command-timeout <duration>` caps each external executor command; `0` disables the timeout;
- `--command-retries <n>` retries failed commands up to `n` times after `--retry-backoff`;
- command-level evidence records the final attempt, while `attempts` preserves the retry audit trail;
- `--resume` skips only matching successful evidence for the same stage, approved payload hash, command ID, exact executor args, empty command error, and CDC applied-change evidence when relevant;
- auditable data commands also require reusable evidence to include complete `data_rows`, `data_bytes`, and valid `data_sha256`.

Timed-out commands are killed and recorded as failed evidence with `error: command timed out after <duration>`. They are then handled like other command failures after retries are exhausted.

Validation execution is intentionally aggregate. `worker-executor --stage validation --execute` runs every generated validation command and then marks the evidence failed if any command failed, preserving the full mismatch set for review. DDL, export, import, CDC enablement, and CDC apply remain fail-fast because continuing after those stages fail can compound side effects.

Operators can submit executor-only evidence through `generate-executor-evidence-pr-draft` and `create-executor-evidence-pr`. This covers DDL apply evidence and CDC enablement evidence. Evidence PR generation rejects evidence if the corresponding DDL schema diff or stage plan is no longer reviewed.

For export/import commands, the CLI promotes executor audit output into command-level evidence. `exported rows: N` plus `output bytes: N` becomes `data_rows` and `data_bytes`; `imported rows: N` plus `input bytes: N` does the same for import. A valid `output sha256: sha256:<digest>` or `input sha256: sha256:<digest>` line becomes `data_sha256`.

Local `file://` export writes to a same-directory temporary file and atomically renames it only after the output stream closes successfully. HTTP(S), S3, GCS, and Azure Blob export also spool to local temporary files, then upload only after the output stream closes. Abort removes the temporary file without starting a remote upload.

HTTP(S), S3, GCS, and Azure Blob CSV downloads and uploads retry transient request errors and 408/429/5xx responses up to three attempts. Upload retries replay the complete temp-file payload. Imports request `Accept-Encoding: identity` so raw input byte and digest metrics are not changed by automatic decompression.

Local/file, S3, and GCS `tidb-import-into` import pre-audits the CSV before running `IMPORT INTO` and prints the same imported row/byte/SHA tuple after success. `tidb-lightning` import pre-audits every CSV source in the reviewed import plan, rejects sources that still contain the internal null bitmap column, prints aggregate data audit metrics, generates Lightning TOML, and then invokes the external Lightning binary.

Evidence is marked failed or rejected by GitOps validation when row/byte metrics are incomplete, negative, non-numeric, or paired with malformed SHA256 output. Successful export and auditable import evidence must include the complete data audit tuple.

`advance-cdc-checkpoint` turns successful CDC executor evidence into a source-cluster checkpoint snapshot. It validates `evidence/executor-cdc-run.json` through the same approval, payload hash, reviewed-plan, and command-structure checks as CDC executor evidence PRs.

The command requires succeeded evidence, requires `cdc_applied_changes`, and verifies each evidence command's `--source-object`, `--target-object`, `--from-lsn`, and `--to-lsn` values against the current reviewed CDC plan. It then rewrites `clusters/<source_cluster_id>/state/cdc-checkpoint.yaml` with `status: running` by default, or `caught_up` when explicitly requested.

This checkpoint command does not inspect SQL Server LSN bounds. `cdc-orchestrator` owns the long-running probe/approved-apply/plan loop.

`prepare-cdc-range` prepares the next reviewed LSN range without connecting to SQL Server. It reads the source-cluster checkpoint entries, uses each table's checkpoint `to_lsn` as the next plan `from_lsn`, accepts an operator-provided `--to-lsn`, validates `from_lsn <= to_lsn`, and rewrites `plan/cdc-plan.yaml`.

If a table has no checkpoint entry, operators must pass `--from-lsn` for the initial range. Operators can also pass repeated `--min-lsn source.object=0x...` values from an external LSN probe. When provided, the command fails before mutating the plan if the derived `from_lsn` is older than SQL Server's retained `min_lsn`.

The command always resets the CDC plan and tracked table statuses to `draft`. That forces the new range through GitHub review and stage approval before `worker-executor --stage cdc` can run.

`cdc-enable` is the explicit SQL Server setup executor path. It is reached through `worker-executor --stage cdc-enable` after the CDC plan has been reviewed and approved. It reuses the same CDC payload hash instead of introducing a TiDB metadata table or a separate hidden state store.

The executor checks SQL Server Agent status and CDC enable permissions before side effects. It checks `sys.databases.is_cdc_enabled` and `cdc.change_tables` before calling `sys.sp_cdc_enable_db` or `sys.sp_cdc_enable_table`. After enablement, it verifies the capture job, cleanup job, and cleanup retention. Cleanup retention must be at least the reviewed `retention_hours_required`.

Repeated runs are idempotent because already-enabled DB/table state is detected before calling SQL Server CDC enable procedures. This setup stage is intentionally separate from `prepare-cdc-range` and `cdc-orchestrator`: range planning and long-running apply loops do not silently mutate SQL Server schema-level CDC settings.

`prepare-cdc-iteration` is the deterministic GitHub-file step called by a CDC scheduler after `sqlserver2tidb-executor cdc-lsn --execute` or another trusted LSN probe has produced the current max LSN.

It reads `state/cdc-checkpoint.yaml`, compares each tracked table checkpoint `to_lsn` with `--max-lsn`, and optionally validates the derived `from_lsn` against a per-table SQL Server CDC `min_lsn`. It then either reports `caught_up` without mutating files, or rewrites `plan/cdc-plan.yaml` with the next range and optionally writes `prs/cdc-range-pr.md`.

The command deliberately keeps SQL Server probing, GitHub approval, and CDC execution as separate gates. It accepts supplied LSN bounds, but it does not connect to SQL Server, approve the new plan, run `worker-executor --stage cdc`, or advance the checkpoint.

`cdc-orchestrator` is the long-running CDC probe/approved-apply/plan loop. It invokes `sqlserver2tidb-executor cdc-lsn --execute` through the same process-group-safe external command wrapper used by `worker-executor`. It parses the global `max_lsn` output, then probes each tracked table with `--source-object` to read its capture instance `min_lsn`.

The min-LSN guard is enabled by default. It fails before writing a new range when a committed checkpoint is older than SQL Server's retained CDC window. `--skip-retention-check` exists only for integrations that perform an equivalent external guard.

When `--apply-approved` is set, each iteration first attempts to consume the current CDC range only if the plan and approval already pass the approval/hash gate. If the committed checkpoint has not already covered the approved range, the orchestrator probes per-table `min_lsn` again. It fails before starting `worker-executor --stage cdc --execute` when the approved `from_lsn` has expired while waiting for execution.

Successful apply requires structured `cdc_applied_changes` evidence. It advances the source-cluster checkpoint from that evidence and skips ranges already covered by the committed checkpoint.

`--min-applied-changes` turns this loop into a production soak/health-check primitive by failing the run when the observed applied-change total is below the required threshold.

In `--poll` mode it keeps sleeping and probing while the project is `caught_up`. When a new range is prepared, it writes the reviewed-plan draft and optional `prs/cdc-range-pr.md`, then stops at the PR boundary. This gives operators a stable daemon/scheduler entrypoint without letting the agent approve or merge PRs.

`cdc-health` is the long-running CDC operations check. It reads the committed project CDC plan and source-cluster checkpoint, optionally probes SQL Server through `sqlserver2tidb-executor cdc-lsn --execute`, and emits a JSON report for GitHub Actions or external monitors.

The evaluator classifies failed, missing, stale, ahead-of-source, and retention-expired checkpoints as `critical`. Lag behind the current max LSN is `warning`; otherwise the report is `ok`.

It can also run without a database connection when operators supply `--max-lsn` and repeated `--min-lsn source.object=0x...` values. When `--history-file` is set, the command appends the same report as compact JSONL, normally under `clusters/<source_cluster_id>/projects/<project_id>/state/cdc-health-history.jsonl`.

Feishu and Slack alerting are output adapters. Feishu reads a custom bot webhook URL and optional signing secret from environment variables. Slack reads an incoming webhook URL from an environment variable. Each adapter sends a text alert only when the health status meets its configured minimum severity.

Alert delivery failure returns non-zero because a missed production alarm is a failed health run. The command deliberately does not mutate `plan/cdc-plan.yaml`, `state/cdc-checkpoint.yaml`, approvals, evidence, or PR drafts. History and alerts stay outside the migration approval/hash gate, and migration state transitions remain separate.

For DDL, it produces one `apply-ddl` command per SQL file under `schema/tidb-ddl/` after `schema/schema-diff.json` is `reviewed`.

For export, it produces one command per export chunk when `plan/export-plan.yaml` uses `format: csv`. It can also pass reviewed `compression: gzip` and Lightning `null_encoding: backslash-n`.

For import, command generation depends on the reviewed engine:

- `sql-insert` produces one command per import job;
- `tidb-import-into` produces one command per import job;
- `import-into` is accepted as an alias and normalized to `tidb-import-into`;
- `tidb-lightning` produces one aggregate command for the whole import plan.

Gzip compression is rendered for `sql-insert` and `tidb-lightning` imports.

For CDC, it produces one command per tracked source table. Reviewed `columns`, `key_columns`, `from_lsn`, and `to_lsn` become executor `--columns`, `--key-columns`, `--from-lsn`, and `--to-lsn`.

Command generation fails fast when stage metadata is not executable:

- DDL schema diff is not `reviewed`;
- export/import/CDC/validation plan is still `draft`;
- approved export/import/CDC plan has no work items;
- export chunk predicate still contains `TODO`;
- approved validation plan has no supported row-count, checksum, sampled-hash, bucketed-count, or business-SQL checks.

It also fails when the reviewed instruction has drifted or is unsupported:

- a referenced source table has disappeared from inventory;
- reviewed schema baselines or reviewed import fields have drifted from current inventory columns;
- an approved CDC table has no captured columns, key columns, or LSN range;
- CDC key columns are not present in captured columns;
- CDC captured columns or key columns have drifted from current inventory;
- export format, import engine, compression, or null encoding is not supported by the included executor;
- export `output_uri`, import `source_uri`, or Lightning data-source layout is unsupported.

For validation, it reads `plan/validation-plan.yaml` and produces:

- one `validate-count` command for each `row_count` or `row-count` check;
- one `validate-query` command for each `checksum`, `sampled_hash`, `bucketed_count`, or `business_sql` check.

Row-count checks pass optional source `predicate` and target `target_predicate` values. Query-based checks pass reviewed `source_sql` and `target_sql` scalar queries.

The executor shell does not itself connect to SQL Server, TiDB, Kafka, or object storage.

`sqlserver2tidb-executor` is currently included as a narrow execution adapter. It parses `apply-ddl`, `export`, `import`, `validate-count`, `validate-query`, `cdc-lsn`, and `cdc` work-item arguments, then prints the work-item context by default.

Dry-runs validate command shape without opening database or object-storage connections:

- apply-DDL reads the DDL file and rejects unresolved `TODO` markers or empty SQL;
- export validates output URI compatibility and rejects `TODO` predicates;
- import validates source URI compatibility and reviewed `fields`;
- validation rejects unresolved `TODO` predicates and scalar SQL;
- CDC validates captured columns, key-column membership, and LSN format/range.

`apply-ddl --execute` reads a reviewed DDL file, rejects files that still contain `TODO`, and applies the SQL statements to TiDB/MySQL. The target connection string is read from `SQLSERVER2TIDB_TARGET_CONNECTION_STRING` or `--target-connection-string-env`.

`export --execute` runs SQL Server queries and writes local `file://`, HTTP(S), native `s3://`, native `gs://`, or native `azblob://` CSV output. The source connection string is read from `SQLSERVER2TIDB_SOURCE_CONNECTION_STRING` or `--source-connection-string-env`. Remote export spools CSV/gzip payloads to local temporary files and uploads only after the CSV writer closes successfully.

Cloud credentials are environment-driven:

- S3 uses AWS Signature V4 and `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION` or `AWS_DEFAULT_REGION`, optional `AWS_SESSION_TOKEN`, and optional `AWS_ENDPOINT_URL` / `AWS_S3_FORCE_PATH_STYLE`;
- GCS uses HMAC credentials from `GCS_ACCESS_KEY_ID` / `GCS_SECRET_ACCESS_KEY` or `GOOG_ACCESS_KEY_ID` / `GOOG_SECRET_ACCESS_KEY`, plus optional `GCS_ENDPOINT_URL`;
- Azure Blob uses `AZURE_STORAGE_ACCOUNT`, base64 `AZURE_STORAGE_KEY`, and optional `AZURE_BLOB_ENDPOINT_URL`.

HTTP(S), S3, GCS, and Azure Blob CSV downloads/uploads retry transient request errors and 408/429/5xx responses up to three attempts. Upload retries replay the complete temp-file payload.

The default CSV format writes the source header plus an internal `__sqlserver2tidb_null_bitmap` tail column so import can restore SQL NULL values. Lightning exports use `--null-encoding backslash-n`, omit the bitmap column, and encode NULL as `\N`.

`import --execute` defaults to `--engine sql-insert`. It supports local `file://`, HTTP(S), S3, GCS, and Azure Blob CSV input, then streams row-by-row inserts into TiDB/MySQL. It excludes the internal null bitmap column from the target insert and commits rows in batches controlled by `--import-batch-size`.

When `--require-empty-target` is set for `sql-insert`, the executor opens TiDB first and runs a quoted `COUNT(*)` preflight. It fails before opening the CSV source if the target table is non-empty.

With `--engine tidb-import-into`, the executor builds and runs TiDB `IMPORT INTO <table> FROM <fileLocation> FORMAT 'csv' WITH skip_rows=1`. Supported file locations are absolute local paths, local `file://`, `s3://`, and `gs://`. S3/GCS locations must include both bucket and object path.

Before executing `IMPORT INTO`, the executor always runs a quoted `COUNT(*)` preflight and fails if the target table is not empty. This catches the documented empty-table prerequisite, but it does not replace the operational requirement to avoid concurrent DDL/DML during import.

For local path, `file://`, `s3://`, and `gs://` sources, the executor reads the CSV header and maps a trailing internal null bitmap column to a TiDB user variable so that field is skipped. Local path and `file://` sources still reject relative local paths; S3/GCS sources are read with signed GET. Azure Blob is supported by `sql-insert` and `tidb-lightning`, not by TiDB `IMPORT INTO` in this agent.

With `--engine tidb-lightning`, the executor reads the reviewed import plan, pre-audits every CSV source, rejects CSV files that still contain the internal null bitmap column, generates a TiDB Lightning TOML config with local backend and `null = '\N'`, and invokes an external `tidb-lightning -config <toml>` binary.

Lightning requires a TCP MySQL/TiDB DSN for the target connection. PD address is supplied by `--lightning-pd-addr` or `SQLSERVER2TIDB_LIGHTNING_PD_ADDR`; sorted KV directory defaults to `/tmp/sqlserver2tidb-lightning-sorted-kv`.

TiDB `IMPORT INTO` requires an existing empty target table and follows the official TiDB restrictions: https://docs.pingcap.com/tidb/stable/sql-statement-import-into/.

Validation execute paths are narrow and deterministic:

- `validate-count --execute` opens SQL Server and TiDB/MySQL connections, runs `COUNT(*)` against quoted source and target objects, applies source and target predicates independently, and returns non-zero when counts differ;
- `validate-query --execute` opens SQL Server and TiDB/MySQL connections, runs reviewed one-row/one-column source and target SQL, normalizes scalar values, and returns non-zero when they differ.

Generated `checksum`, `sampled_hash`, `bucketed_count`, and manually reviewed `business_sql` checks use `validate-query`.

CDC execute paths are also explicit:

- `cdc-lsn --execute` opens SQL Server, queries `sys.fn_cdc_get_max_lsn()`, and optionally derives a capture instance to query `sys.fn_cdc_get_min_lsn()`;
- `cdc --execute` validates reviewed captured columns, key columns, connection strings, and explicit binary LSN boundaries, then queries SQL Server CDC and applies upsert/delete changes to TiDB.

Parquet formats, broader row digest generators, production-grade bucketed sampled-hash strategies, and fully automatic CDC PR approval/merge should be implemented incrementally behind the same approval/hash gate.

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

It filters inventory by the project source database and source schemas. For each in-scope table, it writes one `row_count` check with a reviewed source object and target object pair.

Source and target names follow existing planning conventions:

- source object: `<source_database>.<schema>.<table>`;
- target object for single-schema projects: `<target_database>.<table>`;
- target object for multi-schema projects: `<target_database>.<schema>_<table>`.

Optional generated checks are scalar-query drafts:

- `--include-checksum` writes exact-numeric `checksum` checks for tables with non-computed exact numeric columns;
- `--include-sampled-hash` writes exact-numeric `sampled_hash` checks for tables that also have an integer sample column;
- `--sample-modulo` controls the sampled-hash modulo predicate;
- `--include-bucketed-count` writes one `bucketed_count` check per modulo bucket for tables with a non-computed integer bucket column;
- `--bucket-count` controls generated bucket count and is capped at `1024`.

The command does not connect to SQL Server or TiDB and does not execute validation. It only creates a draft plan that must be reviewed through GitHub before `compute-payload-hash --stage validation`, `worker-validate`, or `worker-executor --stage validation` can use it.

The generated checksum, sampled-hash, and bucketed-count checks are not a universal row digest engine. Operators can edit or replace them before review.

The repository includes JSON schemas for export, import, CDC, and validation plans, and maps them from `global/policies/file-schema-policy.yaml`. `validate-repo` also checks committed plan metadata, schema diff metadata, state/evidence ownership, approval metadata, object-name shape, row-count checks, and query-based checks. It reports an invalid repository when required execution fields or ownership fields are missing or inconsistent.

## Worker Reconcile

`worker-reconcile` is the current bridge between explicit one-project workers and a future reconcile loop. In `--dry-run` mode, it scans:

- `clusters/<source_cluster_id>/projects/<project_id>/project.yaml`
- project approval files for `ddl`, `export`, `import`, `cdc`, `validation`, and `cutover`
- the payload files covered by each stage hash

For each project/stage pair, it reports:

- `ready` when approval is approved, `approved_by` is non-empty, the payload hash matches, DDL schema diff is reviewed, export/import/CDC/validation plan status gates pass, cutover runbook and prerequisite evidence gates pass for cutover, and the same approved payload hash does not already have non-empty stage state
- `blocked` with the deterministic reason returned by the approval/hash gate
- the exact single-project worker command for ready metadata actions
- `worker-executor --stage ddl` and `worker-executor --stage cdc-enable` for ready executor-only actions

With `--dry-run --json`, the same report is written as JSON for automation and monitoring integrations. JSON output is intentionally read-only and is not accepted with execute modes.

In `--execute-next` mode, it selects the first ready metadata-only action in source-cluster/project/stage order. It acquires or renews the source-cluster `state/worker-lease.yaml`, then runs exactly one metadata-only worker action: `export`, `import`, `cdc`, `validation`, or `cutover`.

DDL and CDC enablement are intentionally executor-only. They must be run through `worker-executor --stage ddl` or `worker-executor --stage cdc-enable`, so those ready actions are reported by `--dry-run` but are not selected by `--execute-next`.

The same holder can renew its own unexpired lease. A different holder is blocked until the lease expires. This mode writes the same state/evidence files that the selected explicit worker writes, plus the source-cluster lease file.

Active lease records must carry non-empty `holder`, `lease_id`, `project_id`, `expires_at`, and `renewed_at`. `project_id` must reference an existing project directory under the same source cluster. Timestamps must be RFC3339 and `expires_at` must not be before `renewed_at`; `phase: idle` remains the empty placeholder state.

In `--loop` mode, it repeatedly runs the same execute-next selection with one holder until no ready metadata-only action remains or `--max-iterations` is reached. `--max-iterations 0` means the loop stops only when the repository has no ready metadata-only action. The loop uses the same approval, payload hash, plan status, state dedupe, and lease rules as `--execute-next`; it does not select DDL executor actions and it does not run external executor commands.

`worker-agent` is the delivery-oriented entrypoint for the same loop. It accepts the same holder, optional source-cluster scope, lease TTL, interval, max iteration, and state PR draft options.

The command prints an agent header and delegates to the deterministic reconcile loop. It also supports explicit `--poll` mode, where no-ready scans sleep and retry instead of exiting. `--idle-iterations` can bound idle polling for tests or batch jobs.

It exists so local process managers and container runtimes can run `sqlserver2tidb worker-agent ...` without depending on the lower-level `worker-reconcile --loop` spelling.

When `--state-pr-draft` is enabled together with `--execute-next`, the command writes a project-local PR body at `clusters/<source_cluster_id>/projects/<project_id>/prs/reconcile-<stage>-state-pr.md`.

The draft records the selected stage, status, payload hash, lease id, branch naming convention, and files to review. For CDC, it includes the source-cluster `state/cdc-checkpoint.yaml` in addition to project state/evidence and the worker lease. For cutover, it includes `state/migration-state.yaml`, `evidence/cutover-evidence.md`, `evidence/post-cutover-report.md`, and the worker lease.

`worker-cutover` is deterministic and metadata-only. It requires `approvals/cutover-approval.yaml` to approve the current hash over `project.yaml` and `plan/cutover-runbook.md`.

Cutover rejects runbooks that still contain `TODO` or the initialization placeholder. It requires successful export/import/validation executor evidence and `state/validation-status.yaml` with `status: passed`. For non-offline projects, it also requires successful CDC executor evidence plus a source-cluster `state/cdc-checkpoint.yaml` with `status: caught_up` for the same project.

When all gates pass, it rewrites project `state/migration-state.yaml` to `phase: completed` / `status: completed`, writes `evidence/cutover-evidence.md`, and writes `evidence/post-cutover-report.md`. It does not switch application traffic, update DNS, modify proxies, or perform cleanup.

The command still does not connect to SQL Server, TiDB, Kafka, or object storage. It also does not create branches, open PRs, inspect GitHub merge state, or push bot commits by itself. `create-worker-state-pr` is the explicit follow-up step that can turn the generated state PR draft into a branch, commit, push, and GitHub PR.

## Metadata Boundary

Metadata is organized by upstream SQL Server cluster:

```text
clusters/<source_cluster_id>/
```

This is the right boundary for source inventory, SQL Server CDC, source permissions, and worker leases.
The source-cluster `state/cdc-checkpoint.yaml` must stay aligned with `cluster.yaml`. Initialization uses `capture_mode`; CDC worker write-back uses `mode`. Either field must match the cluster `cdc.mode` when present.

Checkpoint phase is optional during initialization but must be `cdc` when present. Checkpoint status is restricted to `not_started`, `planned`, `running`, `caught_up`, or `failed`; `updated_at` must be non-empty RFC3339.

After `advance-cdc-checkpoint`, checkpoint entries are source-cluster snapshots derived from reviewed executor evidence. They should be committed in the same PR as the CDC executor evidence.

`validate-repo` checks each checkpoint entry has:

- a SQL Server source object shaped as `schema.table` or `database.schema.table`;
- a TiDB target object shaped as `table` or `database.table`;
- 10-byte hex LSN boundaries with `from_lsn <= to_lsn`;
- non-negative `applied_changes`;
- RFC3339 `completed_at`.

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

It filters inventory by the project source database and source schemas. It estimates full-load export chunks from inventory `row_count`, writes executor-supported CSV URIs for each chunk, and creates matching import jobs that depend on those export chunks. The generator rejects unsupported export formats and unsupported import engines.

With the default `sql-insert` import engine, URI prefixes may be local `file://`, `http://`, `https://`, `s3://`, `gs://`, or `azblob://`. HTTP(S) prefixes must include a host, S3/GCS prefixes must include a bucket, and Azure Blob prefixes use the URI host as the container.

With `tidb-import-into`, executable end-to-end URI prefixes must be local absolute `file://`, `s3://`, or `gs://`. S3/GCS prefixes must include a bucket. S3 and GCS jobs may omit reviewed `fields` because the executor can inspect the remote CSV header before running `IMPORT INTO`.

Generated `tidb-import-into` jobs still include a reviewed `fields` list derived from inventory columns plus `@sqlserver2tidb_null_bitmap`. This lets TiDB skip the internal CSV tail column even when the source file is remote object storage.

With `tidb-lightning`, URI prefixes may be local `file://`, `s3://`, `gs://`, or `azblob://`. Output filenames are derived from TiDB target objects, export plans use `null_encoding: backslash-n`, and import plans carry one `data_source_uri` for a single aggregate Lightning command.

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

The implemented LLM entrypoints are advisory commands:

| Command | Reads | Writes |
| --- | --- | --- |
| `llm-compatibility-advice` | `schema-issues.yaml` and `compatibility-report.md` | `ai/compatibility-advice.md` plus audit JSON |
| `llm-schema-advice` | `schema/schema-diff.json`, `schema/conversion-report.md`, generated TiDB DDL | `ai/schema-rewrite-candidates.md` plus audit JSON |
| `llm-migration-strategy` | cluster/project metadata, migration plan, and available migration artifacts | `ai/migration-strategy-advice.md` plus audit JSON |
| `llm-validation-analysis` | reviewed validation plan, validation state/report, executor validation evidence, and schema context | `ai/validation-mismatch-analysis.md` plus audit JSON |
| `llm-cutover-risk` | reviewed cutover runbook, migration state, validation state, CDC checkpoint, approvals, and evidence | `ai/cutover-risk-summary.md` plus audit JSON |
| `llm-pr-summary` | deterministic `prs/<stage>-pr.md` plus relevant metadata and plan/schema artifacts | `ai/pr-summary.md` plus audit JSON |

LLM prompts redact common secrets before provider calls. The `ai/` directories are deliberately outside `approvals/`, `state/`, `plan/`, and `evidence/`. Workers do not consume these files as execution instructions or gate decisions.

LLMs are not required for deterministic repository commands. The deterministic path includes repository validation, dry-run discovery, compatibility analysis, schema/data/CDC/validation plan generation, PR draft generation, PR creation wrappers, evidence PR wrappers, payload hashing, metadata-only workers, worker-executor, CDC checkpoint advancement, worker reconcile, and worker-agent.

LLM usage is scoped by artifact type:

- Schema: the LLM may read `conversion-report.md` and `schema-diff.json` to propose rewrites. The candidate must be committed as reviewed files before any worker can use it.
- Export/import: the LLM may propose split keys or risk notes. Generated predicates and execution settings still need PR review before workers can use them.
- CDC: the LLM may explain retention or connector risks. LSN, offset, catch-up, and cutover gates must come from deterministic runtime checks and GitHub approvals.
- Validation: the LLM may suggest extra checksum, sampled-hash, bucketed-count, or business SQL checks. The committed `plan/validation-plan.yaml` and pass/fail results must be produced and reviewed through deterministic files.
- PR prose: the LLM may refine descriptions and summaries. File lists, approval files, state/evidence files, lease files, git commands, GitHub CLI arguments, executor args, and stage gates remain deterministic.

LLMs must not decide:

- whether DDL can run
- whether import can start
- whether CDC has caught up
- whether validation passed
- whether cutover can proceed
- whether destructive cleanup can run

Those gates are handled by deterministic rules, observed runtime state, and GitHub approvals.
