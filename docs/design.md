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

Current checks are deterministic repository checks: inventory parseability/status and schema diff parseability/status/generated_at/non-negative summary counts, generated DDL presence, manual review item clearance, conversion report presence, validation plan presence, review plan status validation, required field validation for committed row-count and query-based validation checks, and rejection of unresolved TODO predicates. `validate-repo` also requires file-schema-policy mappings for migration, export, import, CDC, and validation plans; checks cluster/project directory IDs against committed metadata; verifies source profile, cluster state, CDC checkpoint mode/phase/status/updated_at, worker lease ownership, active lease fields, active lease RFC3339 timestamps, project state phase/status/updated_at, export/import state phase/status/updated_at, validation status state/updated_at, schema diff ownership/status/generated_at/summary counts, evidence JSON ownership/status/generated_at, migration plan, and approval files belong to the same source cluster/project; requires export/import/CDC/validation plan status values to be present and one of `draft`, `reviewed`, or `approved`; validates executor evidence JSON structure and top-level `generated_at` when present; validates state, approval, and executor-evidence `payload_hash` fields use `sha256:<64 hex chars>` when present; validates approval action, status, approved reviewer presence, and `approved_at` RFC3339 timestamps when present, with `approved_at` required for approved approvals; checks export/import/CDC plan work-item fields when those plans contain work items; validates SQL Server source object names as `schema.table` or `database.schema.table` and TiDB target object names as `table` or `database.table`; validates export/import plan `compression` values as `none` or `gzip`, rejects gzip with `tidb-import-into`, validates export plan `null_encoding` values as `bitmap` or `backslash-n`, validates export chunk `output_uri` values against the included executor; validates import job `source_uri` values against the selected import engine; and rejects import job `fields` unless the selected import engine is `tidb-import-into`. For `tidb-import-into`, it also rejects empty fields, duplicate columns, duplicate user variables, and user variables outside the simple `@name` character set. For `tidb-lightning`, it validates that jobs resolve to one data source directory and that generated exports use Lightning-friendly `backslash-n` NULL encoding. S3 and GCS sources can derive fields from the remote CSV header during executor pre-audit. Empty draft plan lists remain valid during initialization. The validation worker itself still does not connect to SQL Server or TiDB; when the plan is structurally valid, its state/report message summarizes supported validation check counts by type. When `evidence/executor-validation-run.json` exists, the validation worker validates that evidence against the current approval hash and appends a deterministic executor-evidence summary; failed executor validation commands make the validation worker result failed. For real validation, `worker-executor --stage validation` can generate `sqlserver2tidb-executor validate-count` commands for row-count checks and `sqlserver2tidb-executor validate-query` commands for reviewed `checksum`, `sampled_hash`, `bucketed_count`, and `business_sql` scalar-query checks after validation approval and payload hash checks pass. `validate-count --execute` compares SQL Server and TiDB row counts for one reviewed source/target object pair; `validate-query --execute` compares one-row/one-column SQL Server and TiDB query results. Native row digest generators and bucketed sampled-hash strategies remain future executor capabilities.

Executor evidence validation rejects negative optional command-level data metrics. For export/import evidence these metrics are stored as `data_rows`, `data_bytes`, and optional `data_sha256` when the executor output includes complete non-negative row/byte metrics and a valid SHA256 digest line; `data_sha256` is valid only together with `data_rows` and `data_bytes`. Successful export evidence, successful `sql-insert` import evidence, successful local-path, `file://`, S3, or GCS `tidb-import-into` evidence, and successful `tidb-lightning` evidence must include all three audit fields (`data_rows`, `data_bytes`, and `data_sha256`) in both `validate-repo` and executor evidence PR generation.

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

The export worker reads a reviewed or approved `plan/export-plan.yaml` and writes planned chunk state to `state/export-chunks.yaml`, plus `evidence/precheck.json`. The import worker reads a reviewed or approved `plan/import-plan.yaml` and writes planned import job state to `state/import-jobs.yaml`, plus `evidence/import-summary.json`. Export and import workers fail fast when the approved plan has no work items, required work-item fields are missing, plan compression is unsupported, gzip compression is paired with `tidb-import-into`, export `null_encoding` is unsupported, `tidb-lightning` jobs do not share one data source directory, or import job `fields` are incompatible with the selected import engine or contain invalid `tidb-import-into` field tokens.

These workers establish the approval and state write-back contract for future real executors. They intentionally mark reviewed items as `planned`; they do not mark chunks as exported or jobs as imported.

## Worker Executor Shell

`worker-executor` is the current external executor shell. It is deterministic and file-backed:

- Input: approved `ddl`, `export`, `import`, `cdc`, or `validation` stage metadata.
- Input: the matching schema files or plan file under `schema/` or `plan/`.
- Output: dry-run command lines by default.
- Optional execution: external command invocation only when `--execute` is explicitly set.

The command reuses `requireApprovedStage`, so it refuses to produce executor commands unless the stage approval is approved, has reviewers, and the payload hash matches the current repository files. The `cdc-enable` executor stage deliberately maps to the existing CDC approval and `plan/cdc-plan.yaml`; its evidence is still written as `evidence/executor-cdc-enable-run.json` so setup evidence is separate from LSN apply evidence. The default external binary is `sqlserver2tidb-executor`; operators can override it with `--executor-binary`. Operators can also pass `--source-connection-string-env`, `--target-connection-string-env`, `--import-batch-size`, `--require-empty-target`, `--command-timeout`, `--command-retries`, `--retry-backoff`, and `--resume`; these values are rendered into generated executor commands or local execution behavior instead of being stored in GitHub metadata. Export, import, validation, CDC enablement, and CDC apply executor preparation read `clusters/<source_cluster_id>/inventory/inventory.json` before rendering commands. They fail if a reviewed plan references a source table that is no longer in current inventory; when `schema/schema-diff.json` is reviewed, they also compare the reviewed source column baseline with current inventory column names/types. `tidb-import-into` import jobs with reviewed `fields` additionally require those fields, ignoring TiDB user variables, to match current inventory columns. CDC keeps its stricter captured-column and key-column checks: reviewed captured columns must match current non-computed source columns, and `key_columns` must still match a current primary key or non-filtered unique index. This is a GitHub-file schema drift gate, not a TiDB metadata-table dependency. `worker-executor --stage cdc-enable` generates one `sqlserver2tidb-executor cdc-enable` command per tracked table, passing reviewed `capture_instance`, `role_name`, `supports_net_changes`, and top-level `retention_hours_required`; it does not require `from_lsn` / `to_lsn`. `worker-executor --stage cdc` remains the LSN apply stage and does require reviewed LSN ranges. `--require-empty-target` is rendered only for `sql-insert` import commands and makes the executor fail before source CSV open when the target table already has rows. It is intentionally off by default because multi-chunk imports can legitimately append multiple CSV chunks into the same target table. Reviewed `compression: gzip` is rendered as executor `--compression gzip` for export, `sql-insert`, and `tidb-lightning` commands. Import job `fields` are rendered only for `tidb-import-into`; executor preparation rejects `fields` on `sql-insert` and `tidb-lightning` plans because those engines read CSV headers directly, and validates `tidb-import-into` field tokens before rendering `--fields`. `tidb-lightning` plans render as one aggregate import command with `--engine tidb-lightning`, `--source-uri <data_source_uri>`, and `--import-plan <path>`. In `worker-executor --execute` mode, the CLI invokes the external binary and injects the executor-level `--execute` flag immediately after the executor subcommand, so the included executor leaves dry-run mode. Execute mode writes `evidence/executor-<stage>-run.json` with the stage, payload hash, command args, output, exit codes, per-command start/end timestamps, per-command duration, optional retry `attempt_count` / `attempts`, optional command `error`, and for CDC apply commands the parsed `cdc_applied_changes` value from the required `applied changes: N` output line. Export, `sql-insert` import, local/file/S3/GCS `tidb-import-into`, and `tidb-lightning` import commands that exit successfully but omit complete `data_rows`, `data_bytes`, and `data_sha256` output are treated as failed command evidence immediately. `--command-timeout <duration>` caps each external executor command; the default `0` disables the timeout. `--command-retries <n>` retries failed external executor commands up to `n` times after `--retry-backoff`; command-level evidence records the final attempt and `attempts` preserves the per-attempt audit trail. `--resume` loads the existing executor evidence for the same stage and current approved payload hash, then skips only commands whose command ID, exact executor args, successful exit code, empty command error, and CDC applied-change evidence when relevant all match. Export, `sql-insert` import, local/file/S3/GCS `tidb-import-into`, and `tidb-lightning` import commands also require reusable evidence to include complete `data_rows`, `data_bytes`, and valid `data_sha256`, so older evidence without data audit is rerun after upgrading the executor. Timed-out commands are killed, recorded as failed command evidence with `error: command timed out after <duration>`, and then handled like other command failures after retries are exhausted. Validation execution is intentionally aggregate: `worker-executor --stage validation --execute` runs every generated validation command and then marks the evidence failed if any command failed, preserving the full mismatch set for review. DDL, export, import, CDC enablement, and CDC apply execution remain fail-fast because continuing after those stages fail can compound side effects. Operators can then use `generate-executor-evidence-pr-draft` and `create-executor-evidence-pr` to submit executor-only evidence, including DDL apply evidence and CDC enablement evidence, through GitHub review; evidence PR generation rejects evidence if the corresponding DDL schema diff or stage plan is no longer reviewed.

For export/import commands, when executor output contains `exported rows: N` plus `output bytes: N` or `imported rows: N` plus `input bytes: N`, the CLI records those values as command-level `data_rows` and `data_bytes`. If output also contains `output sha256: sha256:<digest>` or `input sha256: sha256:<digest>`, the CLI records it as `data_sha256`. Local `file://` export writes to a same-directory temporary file and atomically renames it to the target path only after the output stream closes successfully. HTTP(S), S3, GCS, and Azure Blob export spool to local temporary files and only start the upload after the output stream closes successfully; abort removes the temporary file without starting a remote upload. HTTP(S)/S3/GCS/Azure Blob CSV downloads and uploads retry transient request errors and 408/429/5xx responses up to three attempts, replaying the complete temp-file payload for upload retries. HTTP(S), S3, GCS, and Azure Blob import request `Accept-Encoding: identity` so raw input byte and digest metrics are not changed by automatic decompression. Local/file, S3, and GCS `tidb-import-into` import pre-audit the CSV file before running `IMPORT INTO` and print the same `imported rows`, `input bytes`, and `input sha256` tuple after success. `tidb-lightning` import pre-audits every CSV source in the reviewed import plan, rejects sources that still contain the internal null bitmap column, prints aggregate data audit metrics, generates Lightning TOML, and then invokes the external Lightning binary. If only one row/byte data metric is present, a row/byte value is negative/non-numeric, the SHA256 value is malformed, or successful export / auditable import evidence is missing the complete data audit tuple, the command evidence is marked failed or rejected by GitOps validation.

`advance-cdc-checkpoint` turns successful CDC executor evidence into a source-cluster checkpoint snapshot. It validates `evidence/executor-cdc-run.json` through the same approval, payload hash, reviewed-plan, and command-structure checks as CDC executor evidence PRs, requires the evidence status to be `succeeded`, requires `cdc_applied_changes`, and verifies each evidence command's `--source-object`, `--target-object`, `--from-lsn`, and `--to-lsn` values match the current reviewed CDC plan. It then rewrites `clusters/<source_cluster_id>/state/cdc-checkpoint.yaml` with `status: running` by default, or `caught_up` when explicitly requested. This checkpoint command does not inspect SQL Server LSN bounds; `cdc-orchestrator` owns the long-running probe/approved-apply/plan loop.

`prepare-cdc-range` prepares the next reviewed LSN range without connecting to SQL Server. It reads the source-cluster checkpoint entries, uses each table's checkpoint `to_lsn` as the next plan `from_lsn`, accepts an operator-provided `--to-lsn`, validates `from_lsn <= to_lsn`, and rewrites `plan/cdc-plan.yaml`. If a table has no checkpoint entry, operators must pass `--from-lsn` for the initial range. Operators can also pass repeated `--min-lsn source.object=0x...` values from an external LSN probe; when provided, the command fails before mutating the plan if the derived `from_lsn` is older than SQL Server's retained `min_lsn`. The command always resets the CDC plan and tracked table statuses to `draft`, forcing the new range through GitHub review and stage approval before `worker-executor --stage cdc` can run.

`cdc-enable` is the explicit SQL Server setup executor path. It is reached through `worker-executor --stage cdc-enable` after the CDC plan has been reviewed and approved, and it reuses the same CDC payload hash instead of introducing a TiDB metadata table or a separate hidden state store. The executor checks SQL Server Agent status and CDC enable permissions before side effects, checks `sys.databases.is_cdc_enabled` and `cdc.change_tables` before calling `sys.sp_cdc_enable_db` or `sys.sp_cdc_enable_table`, and verifies the resulting capture job, cleanup job, and cleanup retention after enablement. Cleanup retention must be at least the reviewed `retention_hours_required`. Repeated runs are still idempotent because already-enabled DB/table state is detected before calling SQL Server CDC enable procedures. This setup stage is intentionally separate from `prepare-cdc-range` and `cdc-orchestrator`: range planning and long-running apply loops do not silently mutate SQL Server schema-level CDC settings.

`prepare-cdc-iteration` is the deterministic GitHub-file step that can be called by a CDC scheduler after `sqlserver2tidb-executor cdc-lsn --execute` or another trusted LSN probe has produced the current max LSN. It reads `state/cdc-checkpoint.yaml`, compares each tracked table checkpoint `to_lsn` with `--max-lsn`, optionally validates the derived `from_lsn` against a per-table SQL Server CDC `min_lsn`, and either reports `caught_up` without mutating files or rewrites `plan/cdc-plan.yaml` with the next range and optionally writes `prs/cdc-range-pr.md`. It deliberately keeps SQL Server probing, GitHub approval, and CDC execution as separate gates: the command accepts supplied LSN bounds, but it does not connect to SQL Server, approve the new plan, run `worker-executor --stage cdc`, or advance the checkpoint.

`cdc-orchestrator` is the long-running CDC probe/approved-apply/plan loop. It invokes `sqlserver2tidb-executor cdc-lsn --execute` through the same process-group-safe external command wrapper used by `worker-executor`, parses the global `max_lsn` output, then probes each tracked table with `--source-object` to read its capture instance `min_lsn`. The min-LSN guard is enabled by default and fails before writing a new range when a committed checkpoint is older than SQL Server's retained CDC window; `--skip-retention-check` exists only for integrations that perform an equivalent external guard. When `--apply-approved` is set, each iteration first attempts to consume the current CDC range only if the plan and approval already pass the approval/hash gate. If the committed checkpoint has not already covered the approved range, the orchestrator probes per-table `min_lsn` again and fails before starting `worker-executor --stage cdc --execute` when the approved `from_lsn` has expired while waiting for execution. Successful apply requires structured `cdc_applied_changes` evidence, advances the source-cluster checkpoint from that evidence, and skips ranges already covered by the committed checkpoint. `--min-applied-changes` turns this loop into a production soak/health-check primitive by failing the run when the observed applied-change total is below the required threshold. In `--poll` mode it keeps sleeping and probing while the project is `caught_up`; when a new range is prepared it writes the reviewed-plan draft and optional `prs/cdc-range-pr.md`, then stops at the PR boundary. This gives operators a stable daemon/scheduler entrypoint without letting the agent approve or merge PRs.

`cdc-health` is the long-running CDC operations check. It reads the committed project CDC plan and source-cluster checkpoint, optionally probes SQL Server through `sqlserver2tidb-executor cdc-lsn --execute`, and emits a JSON report that can be uploaded by `.github/workflows/cdc-ops-health.yml` or scraped by external monitors. The evaluator classifies failed, missing, stale, ahead-of-source, and retention-expired checkpoints as `critical`, lag behind the current max LSN as `warning`, and otherwise reports `ok`. It can also run without a database connection when operators supply `--max-lsn` and repeated `--min-lsn source.object=0x...` values. When `--history-file` is set, the command appends the same report as compact JSONL, normally under `clusters/<source_cluster_id>/projects/<project_id>/state/cdc-health-history.jsonl`, so scheduled checks have a durable trend/audit trail. Feishu and Slack alerting are output adapters: Feishu reads a custom bot webhook URL and optional signing secret from environment variables, Slack reads an incoming webhook URL from an environment variable, and each adapter sends a text alert only when the health status meets its configured minimum severity. Alert delivery failure returns non-zero because a missed production alarm is a failed health run. The command deliberately does not mutate `plan/cdc-plan.yaml`, `state/cdc-checkpoint.yaml`, approvals, evidence, or PR drafts; history and alerts stay outside the migration approval/hash gate, and migration state transitions remain separate.

For DDL, it produces one `apply-ddl` command per SQL file under `schema/tidb-ddl/` after `schema/schema-diff.json` is `reviewed`. For export, it produces one command per export chunk when `plan/export-plan.yaml` uses `format: csv`, optionally passing reviewed `compression: gzip` and Lightning `null_encoding: backslash-n`. For import, it produces one command per import job when `plan/import-plan.yaml` uses `engine: sql-insert` or `engine: tidb-import-into`; `engine: import-into` is accepted as an alias and normalized to `tidb-import-into`; `engine: tidb-lightning` produces one aggregate command for the whole import plan. Gzip compression is rendered for `sql-insert` and `tidb-lightning` imports. For CDC, it produces one command per tracked source table and passes reviewed `columns` / `key_columns` / `from_lsn` / `to_lsn` as executor `--columns` / `--key-columns` / `--from-lsn` / `--to-lsn`. It fails fast if DDL schema diff is not `reviewed`, if an export/import/CDC/validation plan is still `draft`, if an approved export/import/CDC plan has no work items, if a source table referenced by export/import/validation/CDC has disappeared from inventory, if reviewed schema baselines or reviewed import fields have drifted from current inventory columns, if an approved CDC table has no captured columns, key columns, or LSN range, if CDC key columns are not present in captured columns, if CDC captured columns or key columns have drifted from the current inventory, if export format/import engine/compression/null encoding is not supported by the included executor, if an export chunk `output_uri` is not supported by the included executor, if an import job `source_uri` is not supported by the selected import engine, if a Lightning plan cannot resolve one data source directory, and export fails fast if any chunk predicate still contains `TODO`. For validation, it reads `plan/validation-plan.yaml` and produces one `validate-count` command for each check whose `type` is `row_count` or `row-count`, plus one `validate-query` command for each check whose `type` is `checksum`, `sampled_hash`, `bucketed_count`, or `business_sql`; row-count checks pass optional source `predicate` and target `target_predicate` values, and query-based checks pass reviewed `source_sql` and `target_sql` scalar queries. It fails fast if the approved validation plan has no supported row-count, checksum, sampled-hash, bucketed-count, or business-SQL checks. The executor shell does not itself connect to SQL Server, TiDB, Kafka, or object storage.

`sqlserver2tidb-executor` is currently included as a narrow execution adapter. It parses `apply-ddl`, `export`, `import`, `validate-count`, `validate-query`, `cdc-lsn`, and `cdc` work-item arguments and prints the work-item context by default. Executor dry-runs validate object names through the same SQL builders used by execute mode. Apply-DDL dry-runs read the DDL file and reject unresolved `TODO` markers or empty SQL without opening TiDB; export dry-runs validate output URI compatibility and reject `TODO` predicates without opening SQL Server or writing CSV output; import dry-runs validate the selected engine's source URI compatibility and reviewed `fields` without opening TiDB or the CSV source; validation dry-runs reject unresolved `TODO` predicates and scalar SQL without opening SQL Server or TiDB; CDC dry-runs validate captured columns, key-column membership, and LSN format/range without starting a CDC reader or TiDB apply worker. `apply-ddl --execute` reads a reviewed DDL file, rejects files that still contain `TODO`, and applies the SQL statements to TiDB/MySQL using a connection string read from `SQLSERVER2TIDB_TARGET_CONNECTION_STRING` or `--target-connection-string-env`. `export --execute` supports SQL Server query execution into local `file://`, HTTP(S), native `s3://`, native `gs://`, or native `azblob://` CSV output, using a connection string read from `SQLSERVER2TIDB_SOURCE_CONNECTION_STRING` or `--source-connection-string-env`. Remote export spools the CSV/gzip payload to a local temporary file and uploads it only after the CSV writer closes successfully. S3 export/import uses AWS Signature V4 and `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION` or `AWS_DEFAULT_REGION`, optional `AWS_SESSION_TOKEN`, and optional `AWS_ENDPOINT_URL` / `AWS_S3_FORCE_PATH_STYLE` for compatible object stores. GCS export/import uses HMAC credentials from `GCS_ACCESS_KEY_ID` / `GCS_SECRET_ACCESS_KEY` or `GOOG_ACCESS_KEY_ID` / `GOOG_SECRET_ACCESS_KEY`, plus optional `GCS_ENDPOINT_URL`. Azure Blob export/import uses `AZURE_STORAGE_ACCOUNT`, base64 `AZURE_STORAGE_KEY`, and optional `AZURE_BLOB_ENDPOINT_URL`. HTTP(S)/S3/GCS/Azure Blob CSV downloads and uploads retry transient request errors and 408/429/5xx responses up to three attempts, replaying the complete temp-file payload for upload retries. The default CSV format writes the source header plus an internal `__sqlserver2tidb_null_bitmap` tail column so import can restore SQL NULL values; Lightning exports use `--null-encoding backslash-n`, omit the bitmap column, and encode NULL as `\N`. `import --execute` defaults to `--engine sql-insert`, supports local `file://`, HTTP(S), S3, GCS, and Azure Blob CSV input, then streams row-by-row inserts into TiDB/MySQL using the same target connection string mechanism; it excludes the internal null bitmap column from the target insert. Import commits rows in batches controlled by `--import-batch-size` to avoid loading the full CSV into memory. When `--require-empty-target` is set for `sql-insert`, the executor opens TiDB first, runs a quoted `COUNT(*)` preflight against the target table, and fails before opening the CSV source if the target is non-empty. With `--engine tidb-import-into`, it builds and executes TiDB `IMPORT INTO <table> FROM <fileLocation> FORMAT 'csv' WITH skip_rows=1` using an absolute local path, local `file://`, `s3://`, or `gs://` file locations; S3/GCS locations must include both a bucket and object path. Before executing `IMPORT INTO`, it always runs a quoted `COUNT(*)` preflight against the target table and fails if the table is not empty; this catches the documented empty-table prerequisite but does not replace the operational requirement to avoid concurrent DDL/DML during the import. For local path, `file://`, `s3://`, and `gs://` sources, it reads the CSV header and maps a trailing internal null bitmap column to a TiDB user variable so that field is skipped; local path and `file://` sources still reject relative local paths, while S3/GCS sources are read with signed GET. Azure Blob is supported by `sql-insert` and `tidb-lightning`, not by TiDB `IMPORT INTO` in this agent. With `--engine tidb-lightning`, the executor reads the reviewed import plan, pre-audits every CSV source, rejects CSV files that still contain the internal null bitmap column, generates a TiDB Lightning TOML config with local backend and `null = '\N'`, and invokes an external `tidb-lightning -config <toml>` binary. The target TiDB connection string must be a TCP MySQL/TiDB DSN; PD address is supplied by `--lightning-pd-addr` or `SQLSERVER2TIDB_LIGHTNING_PD_ADDR`; sorted KV directory defaults to `/tmp/sqlserver2tidb-lightning-sorted-kv`. TiDB `IMPORT INTO` requires an existing empty target table and follows the restrictions in the official TiDB documentation: https://docs.pingcap.com/tidb/stable/sql-statement-import-into/. `validate-count --execute` opens SQL Server and TiDB/MySQL connections, runs `COUNT(*)` against the quoted source and target objects, applies source and target predicates independently when provided, and returns non-zero when the counts differ. `validate-query --execute` opens SQL Server and TiDB/MySQL connections, runs reviewed one-row/one-column source and target SQL, normalizes the scalar values, and returns non-zero when they differ; generated `checksum`, `sampled_hash`, `bucketed_count`, and manually reviewed `business_sql` checks use this path. `cdc-lsn --execute` opens SQL Server, queries `sys.fn_cdc_get_max_lsn()`, and when a source object is provided also derives the capture instance and queries `sys.fn_cdc_get_min_lsn()`. `cdc --execute` validates reviewed captured columns, key columns, source/target connection strings, and explicit `--from-lsn` / `--to-lsn` binary LSN boundaries, then queries SQL Server CDC and applies upsert/delete changes to TiDB. Parquet formats, broader row digest generators, production-grade bucketed sampled-hash strategies, and fully automatic CDC PR approval/merge should be implemented incrementally behind the same approval/hash gate.

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

The command does not connect to SQL Server or TiDB and does not execute validation. It only creates a draft plan that must be reviewed through GitHub before `compute-payload-hash --stage validation`, `worker-validate`, or `worker-executor --stage validation` can use it. The generated checksum/sampled-hash/bucketed-count checks are scalar-query drafts, not a universal row digest engine; operators can edit or replace them before review. The repository includes `global/schemas/export-plan.schema.json`, `global/schemas/import-plan.schema.json`, `global/schemas/cdc-plan.schema.json`, and `global/schemas/validation-plan.schema.json`, and maps them from `global/policies/file-schema-policy.yaml`. `validate-repo` also checks committed migration/export/import/CDC/validation plan metadata, schema diff metadata, state/evidence ownership, approval metadata, object-name shape, `row_count` / `row-count` checks, and query-based `checksum` / `sampled_hash` / `bucketed_count` / `business_sql` checks, reporting an invalid repository when required execution fields or ownership fields are missing or inconsistent.

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

In `--execute-next` mode, it selects the first ready metadata-only action in source-cluster/project/stage order, acquires or renews the source-cluster `state/worker-lease.yaml`, and runs exactly one metadata-only worker action: `export`, `import`, `cdc`, `validation`, or `cutover`. DDL and CDC enablement are intentionally executor-only and must be run through `worker-executor --stage ddl` or `worker-executor --stage cdc-enable`, so those ready actions are reported by `--dry-run` but are not selected by `--execute-next`. The same holder can renew its own unexpired lease. A different holder is blocked until the lease expires. This mode writes the same state/evidence files that the selected explicit worker writes, plus the source-cluster lease file. Active lease records must carry non-empty `holder`, `lease_id`, `project_id`, `expires_at`, and `renewed_at`; `project_id` must reference an existing project directory under the same source cluster; timestamps must be RFC3339 and `expires_at` must not be before `renewed_at`; `phase: idle` remains the empty placeholder state.

In `--loop` mode, it repeatedly runs the same execute-next selection with one holder until no ready metadata-only action remains or `--max-iterations` is reached. `--max-iterations 0` means the loop stops only when the repository has no ready metadata-only action. The loop uses the same approval, payload hash, plan status, state dedupe, and lease rules as `--execute-next`; it does not select DDL executor actions and it does not run external executor commands.

`worker-agent` is the delivery-oriented entrypoint for the same loop. It accepts the same holder, optional source-cluster scope, lease TTL, interval, max iteration, and state PR draft options, prints an agent header, and then delegates to the deterministic reconcile loop. It also supports explicit `--poll` mode, where no-ready scans sleep and retry instead of exiting; `--idle-iterations` can bound idle polling for tests or batch jobs. It exists so local process managers and container runtimes can run `sqlserver2tidb worker-agent ...` without depending on the lower-level `worker-reconcile --loop` spelling.

When `--state-pr-draft` is enabled together with `--execute-next`, the command also writes a project-local PR body at `clusters/<source_cluster_id>/projects/<project_id>/prs/reconcile-<stage>-state-pr.md`. The draft records the selected stage, status, payload hash, lease id, branch naming convention, and files to review. For CDC, it includes the source-cluster `state/cdc-checkpoint.yaml` in addition to project state/evidence and the worker lease. For cutover, it includes `state/migration-state.yaml`, `evidence/cutover-evidence.md`, `evidence/post-cutover-report.md`, and the worker lease.

`worker-cutover` is deterministic and metadata-only. It requires `approvals/cutover-approval.yaml` to approve the current hash over `project.yaml` and `plan/cutover-runbook.md`, rejects runbooks that still contain `TODO` or the initialization placeholder, requires successful export/import/validation executor evidence, requires `state/validation-status.yaml` to be `passed`, and for non-offline projects requires successful CDC executor evidence plus a source-cluster `state/cdc-checkpoint.yaml` with `status: caught_up` for the same project. When all gates pass, it rewrites project `state/migration-state.yaml` to `phase: completed` / `status: completed`, writes `evidence/cutover-evidence.md`, and writes `evidence/post-cutover-report.md`. It does not switch application traffic, update DNS, modify proxies, or perform cleanup.

The command still does not connect to SQL Server, TiDB, Kafka, or object storage. It also does not create branches, open PRs, inspect GitHub merge state, or push bot commits by itself. `create-worker-state-pr` is the explicit follow-up step that can turn the generated state PR draft into a branch, commit, push, and GitHub PR.

## Metadata Boundary

Metadata is organized by upstream SQL Server cluster:

```text
clusters/<source_cluster_id>/
```

This is the right boundary for source inventory, SQL Server CDC, source permissions, and worker leases.
The source-cluster `state/cdc-checkpoint.yaml` must stay aligned with `cluster.yaml`: initialization uses `capture_mode`, CDC worker write-back uses `mode`, and either field must match the cluster `cdc.mode` when present. Checkpoint phase is optional during initialization but must be `cdc` when present. Checkpoint status is restricted to `not_started`, `planned`, `running`, `caught_up`, or `failed`; `updated_at` must be non-empty RFC3339. After `advance-cdc-checkpoint`, checkpoint entries are source-cluster snapshots derived from reviewed executor evidence and should be committed in the same PR as the CDC executor evidence. `validate-repo` checks each checkpoint entry has a SQL Server source object shaped as `schema.table` or `database.schema.table`, a TiDB target object shaped as `table` or `database.table`, 10-byte hex LSN boundaries with `from_lsn <= to_lsn`, non-negative `applied_changes`, and RFC3339 `completed_at`.

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

It filters inventory by the project source database and source schemas. It estimates full-load export chunks from inventory `row_count`, writes executor-supported CSV URIs for each chunk, and creates matching import jobs that depend on those export chunks. The generator rejects unsupported export formats and unsupported import engines. With the default `sql-insert` import engine, URI prefixes may be local `file://`, `http://`, `https://`, `s3://`, `gs://`, or `azblob://`; HTTP(S) prefixes must include a host, S3/GCS prefixes must include a bucket, and Azure Blob prefixes use the URI host as the container. With `tidb-import-into`, executable end-to-end URI prefixes must be local absolute `file://`, `s3://`, or `gs://`, and S3/GCS prefixes must include a bucket. S3 and GCS `tidb-import-into` jobs may omit reviewed `fields` because the executor can inspect the remote CSV header before running `IMPORT INTO`; generated `tidb-import-into` jobs still include a reviewed `fields` list derived from inventory columns plus `@sqlserver2tidb_null_bitmap` so TiDB can skip the internal CSV tail column even when the source file is remote object storage. With `tidb-lightning`, URI prefixes may be local `file://`, `s3://`, `gs://`, or `azblob://`; output filenames are derived from TiDB target objects, export plans use `null_encoding: backslash-n`, and import plans carry one `data_source_uri` for a single aggregate Lightning command.

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

LLMs are not required for deterministic repository commands such as `validate-repo`, `discover-sqlserver --dry-run`, `analyze-compatibility`, `generate-schema-draft`, `generate-data-plans`, `generate-cdc-plan`, `prepare-cdc-range`, `prepare-cdc-iteration`, `generate-validation-plan`, `generate-pr-draft`, `create-pr`, `create-worker-state-pr`, `generate-executor-evidence-pr-draft`, `create-executor-evidence-pr`, `compute-payload-hash`, `worker-export`, `worker-import`, `worker-cdc`, `worker-validate`, `worker-cutover`, `worker-executor`, `advance-cdc-checkpoint`, `worker-reconcile --dry-run`, `worker-reconcile --execute-next`, `worker-reconcile --loop`, or `worker-agent`. For schema work, the LLM may read `conversion-report.md` and `schema-diff.json` to propose candidate rewrites, but the candidate must be committed as reviewed files before any worker can use it. For export/import planning, the LLM may propose split keys or risk notes, but generated predicates and execution settings still need PR review before workers can use them. For CDC planning, the LLM may explain retention or connector risks, but LSN, offset, catch-up, and cutover gates must come from deterministic runtime checks and GitHub approvals. For validation planning, the LLM may suggest extra checksum, sampled-hash, bucketed-count, or business SQL checks, but the committed `plan/validation-plan.yaml` and pass/fail results must be produced and reviewed through deterministic files. For PR work, the LLM may refine prose, but file lists, approval files, state/evidence files, lease files, git commands, GitHub CLI arguments, executor command arguments, and stage gates must remain deterministic.

LLMs must not decide:

- whether DDL can run
- whether import can start
- whether CDC has caught up
- whether validation passed
- whether cutover can proceed
- whether destructive cleanup can run

Those gates are handled by deterministic rules, observed runtime state, and GitHub approvals.
