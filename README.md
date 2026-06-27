# sqlserver2tidb

[![ci](https://github.com/BornChanger/sqlserver2tidb/actions/workflows/ci.yml/badge.svg)](https://github.com/BornChanger/sqlserver2tidb/actions/workflows/ci.yml)

`sqlserver2tidb` is a GitOps-oriented migration control toolkit for SQL Server to TiDB migrations.

The repository itself is the durable metadata store. Migration state, plans, approvals, checkpoints, and evidence are stored as YAML, JSON, and Markdown files under this repo. GitHub PRs provide the review and approval boundary.

## Current Scope

This MVP provides:

- A Go CLI named `sqlserver2tidb`.
- Initialization of the GitOps metadata repository structure.
- Validation of the GitOps metadata repository structure, including schema policy mappings, inventory JSON parseability, cluster/project identity consistency, source profile/state/evidence ownership, approval metadata, export/import/CDC work-item fields, required row-count validation plan fields, and unresolved TODO predicates.
- SQL Server discovery dry-run planning without opening a database connection.
- SQL Server catalog discovery using a connection string supplied through an environment variable.
- Rule-based SQL Server compatibility analysis from `inventory/inventory.json`.
- Project-scoped TiDB schema draft generation from SQL Server inventory and project metadata.
- Project-scoped full export/import plan draft generation from SQL Server inventory and project metadata.
- Project-scoped CDC plan draft generation from SQL Server inventory and project metadata.
- Project-scoped row-count validation plan draft generation from SQL Server inventory and project metadata.
- PR draft generation and a dry-run-by-default GitHub PR creation wrapper.
- DDL, export, import, CDC, and validation payload hash calculation.
- Approved metadata-only export/import/CDC worker state write-back.
- Dry-run-by-default external executor command generation for approved DDL/export/import/CDC/validation plans.
- `sqlserver2tidb-executor` adapter for DDL/export/import/CDC and row-count validation work items, including `apply-ddl --execute`, CSV `export --execute` to local file or HTTP(S), CSV `import --execute` from local file or HTTP(S), and `validate-count --execute` paths.
- Approved validation-only worker execution.
- Read-only worker reconcile dry-run planning across source clusters and projects.
- Worker state PR draft generation and a dry-run-by-default branch/commit/push/GitHub PR wrapper.
- Source-cluster-first metadata organization:

  ```text
  clusters/<source_cluster_id>/
    cluster.yaml
    inventory/
    state/
      cdc-checkpoint.yaml
      worker-lease.yaml
    prs/
    projects/<project_id>/
      project.yaml
      schema/
      plan/
      state/
      evidence/
      approvals/
      prs/
  ```

- JSON Schema files for core metadata, including cluster, project, migration, export, import, CDC, and validation plan metadata.
- Tests for repository initialization, validation, cluster/project metadata consistency, source profile/state/evidence/approval ownership checks, validation plan content checks, executor-supported data plan format checks, discovery planning and execution, compatibility analysis, schema draft generation, data movement, CDC, and validation plan generation, PR draft generation, GitHub PR create dry-runs, DDL/export/import/CDC/validation worker gates, external executor command dry-runs, executor binary dry-runs, DDL apply checks, CSV export/import execution checks, row-count validation command checks, worker reconcile dry-runs and execute-next state PR drafts, worker state PR create dry-runs, executor evidence PR drafts and dry-runs, upstream SQL Server cluster creation, and migration project creation.

This MVP connects to SQL Server for read-only catalog discovery and, when `sqlserver2tidb-executor export --execute` is explicitly used, for a minimal CSV export path to local `file://` output or HTTP(S) output such as a presigned object storage URL. It connects to TiDB when `sqlserver2tidb-executor apply-ddl --execute` is explicitly used with a reviewed DDL file, or when `sqlserver2tidb-executor import --execute` is explicitly used with a local `file://` CSV source or HTTP(S) CSV source and a TiDB/MySQL connection string environment variable; CSV rows are streamed and committed in batches. CSV export writes a header plus an internal `__sqlserver2tidb_null_bitmap` column so import can restore SQL NULL values instead of turning them into empty strings. It can also connect to both SQL Server and TiDB for an explicit `sqlserver2tidb-executor validate-count --execute` row-count comparison. It does **not** execute native `s3://` object storage IO, TiDB Lightning or `IMPORT INTO`, CDC streaming/apply, cutover, cleanup, checksum validation, sampled hash validation, or business SQL validation. The included `sqlserver2tidb-executor cdc --execute` path intentionally returns an explicit not-implemented error.

## Build

```bash
make build
```

The binary is written to:

```text
bin/sqlserver2tidb
bin/sqlserver2tidb-executor
```

Both binaries expose build metadata:

```bash
bin/sqlserver2tidb version
bin/sqlserver2tidb-executor version
```

`make build` injects the current Git commit and UTC build time. Direct `go test` and unlinked development builds report `dev`, `unknown`, and `unknown`.

Install both binaries into `PREFIX/bin`:

```bash
make install PREFIX="$HOME/.local"
```

Build local release archives under `dist/`:

```bash
make dist VERSION=v0.1.0
```

Each archive includes both binaries, `README.md`, `LICENSE`, and the core documents under `docs/`.

Limit local release builds to selected `GOOS/GOARCH` targets:

```bash
DIST_TARGETS="linux/amd64 darwin/arm64" make dist VERSION=v0.1.0
```

Pushing a tag like `v0.1.0` runs the release workflow, builds Linux/macOS/Windows archives, publishes checksums, and creates a GitHub Release.

## Test

```bash
make test
```

Run the same gate used by GitHub Actions:

```bash
make ci
```

## Quick Start

Initialize the repository metadata layout:

```bash
go run ./cmd/sqlserver2tidb init-repo --root .
```

Validate the repository metadata layout:

```bash
go run ./cmd/sqlserver2tidb validate-repo --root .
```

Create an upstream SQL Server cluster:

```bash
go run ./cmd/sqlserver2tidb create-cluster \
  --root . \
  --cluster-id prod-sqlserver-a \
  --display-name "prod SQL Server A" \
  --listener sqlserver-a.internal \
  --port 1433 \
  --secret-ref vault://migration/prod-sqlserver-a/readonly \
  --owner dba-team,sre-team
```

The command refuses to overwrite an existing `clusters/<source_cluster_id>/` directory.

Preview the SQL Server discovery scope without connecting to SQL Server or writing inventory files:

```bash
go run ./cmd/sqlserver2tidb discover-sqlserver \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --dry-run
```

Run SQL Server catalog discovery. The connection string must come from the environment and must not be committed:

```bash
go run ./cmd/sqlserver2tidb discover-sqlserver \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --connection-string-env SQLSERVER2TIDB_SQLSERVER_DSN
```

Analyze SQL Server compatibility findings from the current inventory file:

```bash
go run ./cmd/sqlserver2tidb analyze-compatibility \
  --root . \
  --source-cluster-id prod-sqlserver-a
```

Create a migration project under that upstream cluster:

```bash
go run ./cmd/sqlserver2tidb create-project \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --display-name "sales DB to TiDB prod A" \
  --source-database sales \
  --source-schema dbo \
  --target-name tidb-prod-a \
  --target-database app \
  --target-secret-ref vault://migration/tidb-prod-a/migrate-user \
  --owner dba-team,app-team
```

The command refuses to overwrite an existing `clusters/<source_cluster_id>/projects/<project_id>/` directory.

Generate project-scoped TiDB DDL drafts from the current SQL Server inventory and project metadata:

```bash
go run ./cmd/sqlserver2tidb generate-schema-draft \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

This writes `schema/tidb-ddl/`, `schema/conversion-report.md`, and `schema/schema-diff.json` under the project. Manual-review mappings are marked in both the DDL comments and schema diff.

Generate project-scoped full export/import draft plans from the current SQL Server inventory and project metadata:

```bash
go run ./cmd/sqlserver2tidb generate-data-plans \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --object-uri-prefix https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full
```

This writes `plan/export-plan.yaml` and `plan/import-plan.yaml` under the project. The command estimates chunks from inventory `row_count`; single-chunk tables get a reviewed-safe `1 = 1` predicate, while multi-chunk tables still get `TODO` split predicates that must be reviewed before export execution. It only generates executor-supported CSV plans with `file://`, `http://`, or `https://` URI prefixes and `sql-insert` imports. It does not connect to SQL Server or TiDB and does not move data.

Generate a project-scoped CDC draft plan from the current SQL Server inventory and project metadata:

```bash
go run ./cmd/sqlserver2tidb generate-cdc-plan \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --mode sqlserver-cdc \
  --retention-hours 168 \
  --apply-batch-size 1000
```

This writes `plan/cdc-plan.yaml` under the project. The command records tracked source/target table pairs and checkpoint policy; it does not start SQL Server CDC, Debezium, Kafka, or TiDB apply.

Generate a project-scoped row-count validation draft plan from the current SQL Server inventory and project metadata:

```bash
go run ./cmd/sqlserver2tidb generate-validation-plan \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

This writes `plan/validation-plan.yaml` under the project with one `row_count` check per table in scope. The command does not connect to SQL Server or TiDB and does not execute validation.

Compute payload hashes and run reviewed DDL/export/import/CDC actions after the matching approval files are marked approved:

```bash
go run ./cmd/sqlserver2tidb compute-payload-hash \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage ddl

go run ./cmd/sqlserver2tidb worker-executor \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage ddl \
  --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING

go run ./cmd/sqlserver2tidb compute-payload-hash \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export

go run ./cmd/sqlserver2tidb worker-export \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a

go run ./cmd/sqlserver2tidb compute-payload-hash \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage import

go run ./cmd/sqlserver2tidb worker-import \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a

go run ./cmd/sqlserver2tidb compute-payload-hash \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage cdc

go run ./cmd/sqlserver2tidb worker-cdc \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

These workers only convert approved non-empty plan files into planned state/evidence files. They do not export data, import data, start CDC, connect to databases, or write object storage.

Preview external executor commands for an approved stage:

```bash
go run ./cmd/sqlserver2tidb worker-executor \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export \
  --source-connection-string-env SQLSERVER_READONLY_DSN
```

This command reuses the same approval/hash gate and is dry-run by default. It prints `sqlserver2tidb-executor` commands for reviewed DDL files, export chunks, import jobs, CDC table apply work, or validation row-count checks. Executor preparation fails when an approved export/import/CDC plan has no work items, when an approved export plan still has `TODO` predicates, when export format is not `csv`, when import engine is not `sql-insert`, or when the approved validation plan contains no supported `row_count` checks. Validation checks can carry separate source `predicate` and target `target_predicate` filters. Use `--source-connection-string-env`, `--target-connection-string-env`, and `--import-batch-size` to pass execution settings into generated executor commands. When `worker-executor --execute` is used, it invokes the external executor, passes the executor-level `--execute` flag, and writes `evidence/executor-<stage>-run.json` with command output, exit codes, per-command start/end timestamps, and per-command duration. The included executor binary can apply reviewed TiDB DDL, run SQL Server to local `file://` or HTTP(S) CSV export, stream local `file://` or HTTP(S) CSV sources to TiDB import, preserve SQL NULLs through an internal CSV null bitmap column, and run approval-gated SQL Server/TiDB row-count comparison after connection strings are supplied through environment variables. Native `s3://` object storage IO, TiDB Lightning or `IMPORT INTO`, CDC apply side effects, checksum validation, sampled hash validation, and business SQL validation are still not implemented.

Preview ready and blocked worker actions across the repository:

```bash
go run ./cmd/sqlserver2tidb worker-reconcile \
  --root . \
  --dry-run
```

This scans cluster/project metadata and reports which `worker-executor --stage ddl`, `worker-export`, `worker-import`, `worker-cdc`, and `worker-validate` actions are ready or blocked by approval/hash checks. It does not execute workers, acquire leases, or write state.

Execute the first ready metadata-only worker action with a source-cluster lease:

```bash
go run ./cmd/sqlserver2tidb worker-reconcile \
  --root . \
  --execute-next \
  --holder agent-a \
  --state-pr-draft
```

This acquires or renews `state/worker-lease.yaml` for the selected source cluster, runs exactly one ready metadata-only worker action (`export`, `import`, `cdc`, or `validation`), and writes the same state/evidence files that the explicit single-project worker would write. Active lease records include non-empty `holder`, `lease_id`, `expires_at`, and `renewed_at`; timestamps are RFC3339 and `expires_at` must not be before `renewed_at`; `phase: idle` remains the empty placeholder state. DDL is intentionally executor-only and must be run through `worker-executor --stage ddl`. With `--state-pr-draft`, it also writes a deterministic Markdown PR body under the project `prs/` directory for reviewing the state/evidence/lease changes. It does not create a branch or call GitHub. A different holder is blocked until the lease expires.

Prepare the git and GitHub commands for a worker state write-back PR:

```bash
go run ./cmd/sqlserver2tidb create-worker-state-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export
```

This is a dry-run by default. It requires the state PR draft and state/evidence/lease files to exist, includes `evidence/executor-<stage>-run.json` when present, then prints deterministic `git switch`, `git add`, `git commit`, `git push`, and `gh pr create` commands. Dry-run reports whether the PR body needs a file-list refresh; `--execute` refreshes that body before commit and then runs the commands locally.

Generate and prepare a PR for executor-only evidence such as DDL apply evidence:

```bash
go run ./cmd/sqlserver2tidb generate-executor-evidence-pr-draft \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage ddl

go run ./cmd/sqlserver2tidb create-executor-evidence-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage ddl
```

`generate-executor-evidence-pr-draft` validates the existing `evidence/executor-<stage>-run.json` against the current approved payload hash and rejects evidence that has no executor command records. Evidence status must be `succeeded` or `failed`, and command IDs must be unique. Each command record must include `id`, non-empty `args`, `shell_command`, `exit_code`, `started_at`, `completed_at`, and `duration_ms`; timestamps must parse as RFC3339Nano, `completed_at` must not be earlier than `started_at`, and duration must be non-negative. `succeeded` evidence requires every command exit code to be `0`, and `failed` evidence requires at least one non-zero command exit code. The command then writes a Markdown PR body under `prs/`, including a command summary table with exit codes and timing. `create-executor-evidence-pr` is a dry-run by default and prints deterministic `git switch`, `git add`, `git commit`, `git push`, and `gh pr create` commands. Add `--execute` only when the local checkout should create the branch, commit the evidence/body, push it, and open a GitHub PR.

Generate a project-scoped PR draft for schema review:

```bash
go run ./cmd/sqlserver2tidb generate-pr-draft \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage schema
```

This writes a Markdown PR body under `prs/` and prints the suggested `gh pr create` command. It does not call the GitHub API.

Prepare a GitHub PR create command from the generated draft:

```bash
go run ./cmd/sqlserver2tidb create-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage schema
```

This is a dry-run by default. Add `--execute` to call `gh pr create`.

Compute the validation payload hash before approving validation:

```bash
go run ./cmd/sqlserver2tidb compute-payload-hash \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage validation
```

After `approvals/validation-approval.yaml` is set to `status: approved` with that hash, run the validation-only worker:

```bash
go run ./cmd/sqlserver2tidb worker-validate \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

This checks approved metadata, writes `state/validation-status.yaml`, and writes `evidence/validation-report.md`.

## Documentation

- [User Manual](docs/user-manual.md): end-to-end operator guide for the target SQL Server to TiDB migration agent workflow.
- [Design Notes](docs/design.md): control-plane, metadata, and LLM responsibility boundaries.

## Design Principles

- `https://github.com/BornChanger/sqlserver2tidb` is the source of truth for migration control metadata.
- Metadata is organized by upstream SQL Server cluster.
- A source cluster can contain multiple migration projects.
- LLM output is never executed directly. It must become reviewed repository files first.
- Workers execute only approved and merged instructions.
- Project migration state phases are restricted to `planning`, `ddl`, `export`, `import`, `cdc`, `validation`, `cutover`, or `completed`, and `updated_at` must be RFC3339.
- Validation status state is restricted to `pending`, `passed`, or `failed`.
- High-frequency logs and per-event CDC offsets do not belong in GitHub. Periodic checkpoint snapshots do.
- CDC checkpoint snapshots stay source-cluster scoped; their mode must match the source cluster `cdc.mode`, status must be one of `not_started`, `planned`, `running`, `caught_up`, or `failed`, and `updated_at` must be RFC3339.
- Plaintext credentials must never be committed. Use secret references only.

## Next Milestones

- Extend `sqlserver2tidb-executor export` beyond CSV over local file/HTTP(S) to native object storage clients and reviewed production formats.
- Extend `sqlserver2tidb-executor import` beyond row-by-row CSV inserts to reviewed production import engines.
- Extend validation beyond direct row-count checks to checksum, sampled hash, and reviewed business SQL checks.
