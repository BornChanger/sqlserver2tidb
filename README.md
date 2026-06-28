# sqlserver2tidb

[![ci](https://github.com/BornChanger/sqlserver2tidb/actions/workflows/ci.yml/badge.svg)](https://github.com/BornChanger/sqlserver2tidb/actions/workflows/ci.yml)

`sqlserver2tidb` is a GitOps-oriented migration control toolkit for SQL Server to TiDB migrations.

The repository itself is the durable metadata store. Migration state, plans, approvals, checkpoints, and evidence are stored as YAML, JSON, and Markdown files under this repo. GitHub PRs provide the review and approval boundary.

## Current Scope

This MVP provides:

- A Go CLI named `sqlserver2tidb`.
- Initialization of the GitOps metadata repository structure.
- A local `doctor` preflight command for repository validation and optional `git`/`gh`/executor availability checks.
- Validation of the GitOps metadata repository structure, including schema policy mappings, inventory JSON parseability/status, cluster/project identity consistency, source profile/state/evidence ownership, schema diff status/timestamps/summary counts, evidence status/timestamps, review plan statuses, approval metadata/audit timestamps, export/import/CDC work-item fields, required row-count and query-based validation plan fields, and unresolved TODO predicates.
- SQL Server discovery dry-run planning without opening a database connection.
- SQL Server catalog discovery using a connection string supplied through an environment variable.
- Rule-based SQL Server compatibility analysis from `inventory/inventory.json`.
- Project-scoped TiDB schema draft generation from SQL Server inventory and project metadata.
- Project-scoped full export/import plan draft generation from SQL Server inventory and project metadata.
- Project-scoped CDC plan draft generation from SQL Server inventory and project metadata.
- Project-scoped row-count validation plan draft generation from SQL Server inventory and project metadata, with optional exact-numeric scalar-query checksum and sampled-hash draft checks.
- PR draft generation and a dry-run-by-default GitHub PR creation wrapper.
- DDL, export, import, CDC, and validation payload hash calculation.
- Approved metadata-only export/import/CDC/validation worker state write-back.
- Dry-run-by-default external executor command generation for approved DDL/export/import/CDC/validation plans.
- `sqlserver2tidb-executor` adapter for DDL/export/import/CDC plus row-count and query-based validation work items, including `apply-ddl --execute`, CSV `export --execute` to local file or HTTP(S), CSV `import --execute` from local file or HTTP(S), `validate-count --execute`, `validate-query --execute`, `cdc-lsn --execute`, and `cdc --execute` paths.
- Approved validation-only worker execution.
- Read-only worker reconcile dry-run planning across source clusters and projects.
- Lease-backed worker reconcile execute-next and bounded loop modes for approved metadata-only actions.
- A `worker-agent` command that packages the bounded reconcile loop as a stable local/container worker entrypoint.
- Worker state PR draft generation and a dry-run-by-default branch/commit/push/GitHub PR wrapper.
- An offline quickstart example that generates and validates a sample migration metadata repository without connecting to SQL Server, TiDB, GitHub, or object storage.
- A multi-stage Dockerfile for building a non-root CLI image with `sqlserver2tidb` and `sqlserver2tidb-executor`.
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
- Tests for repository initialization, validation, cluster/project metadata consistency, source profile/state/evidence/approval ownership checks, validation plan content checks, executor-supported data plan format checks, discovery planning and execution, compatibility analysis, schema draft generation, data movement, CDC, and validation plan generation, PR draft generation, GitHub PR create dry-runs, DDL/export/import/CDC/validation worker gates, external executor command dry-runs, executor binary dry-runs, DDL apply checks, CSV export/import execution checks, row-count and query-based validation command checks, worker reconcile dry-runs, execute-next state PR drafts, bounded loops, and worker-agent runs, worker state PR create dry-runs, executor evidence PR drafts and dry-runs, upstream SQL Server cluster creation, and migration project creation.

This MVP connects to SQL Server for read-only catalog discovery and, when `sqlserver2tidb-executor export --execute` is explicitly used, for a minimal CSV export path to local `file://` output or HTTP(S) output such as a presigned object storage URL. It connects to TiDB when `sqlserver2tidb-executor apply-ddl --execute` is explicitly used with a reviewed DDL file, or when `sqlserver2tidb-executor import --execute` is explicitly used with a TiDB/MySQL connection string environment variable. Import supports the default `sql-insert` engine for local `file://` or HTTP(S) CSV streaming with batched inserts, and the explicit `tidb-import-into` engine for TiDB `IMPORT INTO ... FROM FILE` over a local path, `file://`, `s3://`, or `gs://` file location. CSV export writes a header plus an internal `__sqlserver2tidb_null_bitmap` column so `sql-insert` import can restore SQL NULL values; `tidb-import-into` reads local/file CSV headers or uses reviewed import-plan `fields`, maps that internal tail column to a TiDB user variable so it is skipped, and preflights the target table with `COUNT(*)` before executing `IMPORT INTO`. It can also connect to both SQL Server and TiDB for explicit `sqlserver2tidb-executor validate-count --execute` row-count comparison and `sqlserver2tidb-executor validate-query --execute` reviewed scalar-query comparison for `checksum`, `sampled_hash`, and `business_sql` validation checks. The included `sqlserver2tidb-executor cdc-lsn --execute` path can query SQL Server CDC max LSN and, when a source object is provided, the capture instance min LSN. The included `sqlserver2tidb-executor cdc --execute` path can apply one explicit SQL Server CDC LSN range to TiDB after validating source/target connection strings, captured columns, and key columns. It does **not** execute native object storage export IO, TiDB Lightning, automated checkpoint-driven CDC streaming, cutover, cleanup, or native generated checksum/sample-hash strategies.

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

Pushing a tag like `v0.1.0` runs the release workflow, builds Linux/macOS/Windows archives, publishes checksums, and creates a GitHub Release. The container workflow also publishes `ghcr.io/bornchanger/sqlserver2tidb:v0.1.0` and updates `ghcr.io/bornchanger/sqlserver2tidb:latest`.

Build a local container image:

```bash
docker build \
  --build-arg VERSION=dev \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t sqlserver2tidb:dev .
```

Run the CLI against a mounted migration metadata repository:

```bash
docker run --rm \
  -v "$PWD:/workspace" \
  sqlserver2tidb:dev doctor --root /workspace
```

The image includes `git`, `sqlserver2tidb`, and `sqlserver2tidb-executor`, and runs as a non-root `sqlserver2tidb` user. It does not include GitHub CLI; use the host CLI or extend the image when `create-pr --execute`, `create-worker-state-pr --execute`, or `create-executor-evidence-pr --execute` must call `gh`.

## Test

```bash
make test
```

Run the same gate used by GitHub Actions:

```bash
make ci
```

Run the offline quickstart example:

```bash
make example-check
```

This creates a temporary metadata repository from `examples/quickstart/inventory.json`, generates schema/data/CDC/validation drafts, runs `worker-reconcile --dry-run`, and validates the generated repository. To keep the generated repository for inspection, provide an empty output directory:

```bash
SQLSERVER2TIDB_QUICKSTART_ROOT=/tmp/sqlserver2tidb-quickstart make example-check
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

Run local preflight checks:

```bash
go run ./cmd/sqlserver2tidb doctor --root .
```

By default, missing local tools are reported as warnings. Add `--require-tools` when the environment must already have `git`, `gh`, and `sqlserver2tidb-executor` on `PATH`.

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

This writes `plan/export-plan.yaml` and `plan/import-plan.yaml` under the project. The command estimates chunks from inventory `row_count`; single-chunk tables get a reviewed-safe `1 = 1` predicate, while multi-chunk tables still get `TODO` split predicates that must be reviewed before export execution. It generates executor-supported CSV plans with `sql-insert` imports over `file://`, `http://`, or `https://` URI prefixes by default. If `--import-engine tidb-import-into` is used, the URI prefix must be `file://`, `s3://`, or `gs://`, matching TiDB `IMPORT INTO ... FROM FILE` file locations, and each import job gets a `fields` list derived from inventory columns plus `@sqlserver2tidb_null_bitmap` so object-storage imports can skip the internal CSV tail column. It does not connect to SQL Server or TiDB and does not move data.

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

This writes `plan/cdc-plan.yaml` under the project. The command records tracked source/target table pairs, captured CDC columns, target apply key columns, and checkpoint policy. Captured columns come from discovered non-computed SQL Server table columns. It chooses key columns from the discovered SQL Server primary key first, then from a non-filtered unique index; tables without such an index produce an empty `key_columns` list that must be reviewed before execution. It does not start SQL Server CDC, Debezium, Kafka, or TiDB apply.

Prepare an explicit CDC LSN range for review:

```bash
go run ./cmd/sqlserver2tidb prepare-cdc-range \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --to-lsn 0x00000027000001f40003
```

This rewrites `plan/cdc-plan.yaml` and resets the plan and tracked table statuses to `draft`, so the new range must go through review and approval before execution. For tables that already have entries in `state/cdc-checkpoint.yaml`, the next `from_lsn` is the checkpoint `to_lsn`. For the first range, pass `--from-lsn` explicitly. The command does not connect to SQL Server or discover the current max LSN; use `sqlserver2tidb-executor cdc-lsn --execute` when an operator needs to read SQL Server CDC LSN bounds before preparing the reviewed range.

Generate a project-scoped validation draft plan from the current SQL Server inventory and project metadata:

```bash
go run ./cmd/sqlserver2tidb generate-validation-plan \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --include-checksum \
  --include-sampled-hash \
  --sample-modulo 100
```

This writes `plan/validation-plan.yaml` under the project with one `row_count` check per table in scope. When requested, it also adds `checksum` and `sampled_hash` scalar-query checks for tables that have exact numeric columns; sampled-hash checks require an integer sample column and use `--sample-modulo` to build the deterministic sample predicate. The command does not connect to SQL Server or TiDB and does not execute validation.

Compute payload hashes and run reviewed DDL/export/import/CDC actions after the matching approval files are marked approved. `worker-export`, `worker-import`, `worker-cdc`, and `worker-validate` also require their plan files to be `reviewed` or `approved`; draft export/import/CDC/validation plans are not executable even with approved approval files:

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

This command reuses the same approval/hash gate and is dry-run by default. It prints `sqlserver2tidb-executor` commands for reviewed DDL files, export chunks, import jobs, CDC table apply work, validation row-count checks, or validation scalar-query checks. Executor preparation fails when DDL schema diff is not `reviewed`, when an export/import/CDC/validation plan is still `draft`, when an approved export/import/CDC plan has no work items, when an approved CDC table has no `columns`, `key_columns`, `from_lsn`, or `to_lsn`, when CDC key columns are not present in `columns`, when an approved export plan still has `TODO` predicates, when export format is not `csv`, when import engine is not `sql-insert` or `tidb-import-into`, or when the approved validation plan contains no supported `row_count`, `checksum`, `sampled_hash`, or `business_sql` checks. Row-count validation checks can carry separate source `predicate` and target `target_predicate` filters. `checksum`, `sampled_hash`, and `business_sql` validation checks carry reviewed `source_sql` and `target_sql` scalar queries. Use `--source-connection-string-env`, `--target-connection-string-env`, and `--import-batch-size` to pass execution settings into generated executor commands; reviewed import job `fields` are passed as executor `--fields`, and reviewed CDC table `columns` / `key_columns` / `from_lsn` / `to_lsn` are passed as executor `--columns` / `--key-columns` / `--from-lsn` / `--to-lsn`. When `worker-executor --execute` is used, it invokes the external executor, passes the executor-level `--execute` flag, and writes `evidence/executor-<stage>-run.json` with command output, exit codes, per-command start/end timestamps, per-command duration, and for CDC commands the parsed `cdc_applied_changes` value when the executor reports `applied changes: N`. The included executor binary can apply reviewed TiDB DDL, run SQL Server to local `file://` or HTTP(S) CSV export, stream local `file://` or HTTP(S) CSV sources to TiDB import with `sql-insert`, execute TiDB `IMPORT INTO` for `tidb-import-into` local/file/S3/GCS file locations, preserve SQL NULLs through an internal CSV null bitmap column for local/file imports or reviewed `fields`, run approval-gated SQL Server/TiDB row-count comparison, run approval-gated scalar-query validation, query SQL Server CDC LSN bounds with `cdc-lsn`, and apply a reviewed SQL Server CDC LSN range to TiDB. Native object storage export IO, TiDB Lightning, checkpoint-driven CDC orchestration, and native generated checksum/sample-hash strategies are still not implemented.

Advance the source-cluster CDC checkpoint from successful CDC executor evidence:

```bash
go run ./cmd/sqlserver2tidb advance-cdc-checkpoint \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --status running
```

This validates `evidence/executor-cdc-run.json` against the current approval, payload hash, and reviewed CDC plan, requires succeeded CDC executor commands with `cdc_applied_changes`, verifies command source/target/LSN values match the current plan, and rewrites `clusters/<source_cluster_id>/state/cdc-checkpoint.yaml` with one checkpoint snapshot per applied table. Use `--status caught_up` only when the reviewed LSN range is known to represent catch-up. The command does not query SQL Server min/max LSNs or run a long-lived CDC loop; use `sqlserver2tidb-executor cdc-lsn --execute` to read SQL Server CDC bounds and `prepare-cdc-range` to derive the next reviewed plan range from the committed checkpoint and operator-provided `--to-lsn`.

Preview ready and blocked worker actions across the repository:

```bash
go run ./cmd/sqlserver2tidb worker-reconcile \
  --root . \
  --dry-run
```

This scans cluster/project metadata and reports which `worker-executor --stage ddl`, `worker-export`, `worker-import`, `worker-cdc`, and `worker-validate` actions are ready or blocked by approval/hash checks. DDL actions are blocked until `schema/schema-diff.json` is `reviewed`; export, import, CDC, and validation actions are blocked while their plan files are still `draft`. A metadata-only stage is also blocked when the same approved payload hash already has non-empty state such as `planned`, `passed`, or `failed`, preventing reconcile loops from repeatedly running the same action. It does not execute workers, acquire leases, or write state.

Execute the first ready metadata-only worker action with a source-cluster lease:

```bash
go run ./cmd/sqlserver2tidb worker-reconcile \
  --root . \
  --execute-next \
  --holder agent-a \
  --state-pr-draft
```

This acquires or renews `state/worker-lease.yaml` for the selected source cluster, runs exactly one ready metadata-only worker action (`export`, `import`, `cdc`, or `validation`), and writes the same state/evidence files that the explicit single-project worker would write. Active lease records include non-empty `holder`, `lease_id`, `project_id`, `expires_at`, and `renewed_at`; `project_id` must reference an existing project directory under the same source cluster; timestamps are RFC3339 and `expires_at` must not be before `renewed_at`; `phase: idle` remains the empty placeholder state. DDL is intentionally executor-only and must be run through `worker-executor --stage ddl`. With `--state-pr-draft`, it also writes a deterministic Markdown PR body under the project `prs/` directory for reviewing the state/evidence/lease changes. It does not create a branch or call GitHub. A different holder is blocked until the lease expires.

Run a bounded reconcile loop:

```bash
go run ./cmd/sqlserver2tidb worker-reconcile \
  --root . \
  --loop \
  --holder agent-a \
  --max-iterations 10 \
  --interval 5s
```

Loop mode repeatedly executes ready metadata-only actions with the same lease holder until no ready metadata action remains or `--max-iterations` is reached. `--max-iterations 0` means continue until the repository has no ready metadata-only actions. It still does not execute DDL, external executor commands, GitHub PR creation, or database IO.

Run the packaged worker agent entrypoint:

```bash
go run ./cmd/sqlserver2tidb worker-agent \
  --root . \
  --holder agent-a \
  --max-iterations 0 \
  --interval 5s \
  --poll \
  --idle-iterations 0 \
  --state-pr-draft
```

`worker-agent` is the same deterministic metadata-only loop packaged as a stable process entrypoint for local runs and containers. It requires a holder, uses the source-cluster lease, can emit state PR drafts, and stops only when no ready metadata-only action remains unless `--poll` is enabled. In poll mode, `--idle-iterations 0` means keep waiting for new ready metadata actions; set a positive value for bounded smoke tests or batch jobs.

Prepare the git and GitHub commands for a worker state write-back PR:

```bash
go run ./cmd/sqlserver2tidb create-worker-state-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export
```

This is a dry-run by default. It requires the state PR draft and state/evidence/lease files to exist, validates and includes `evidence/executor-<stage>-run.json` when present, then prints deterministic `git switch`, `git add`, `git commit`, `git push`, and `gh pr create` commands. Optional executor evidence must pass the same approval, payload hash, reviewed instruction, and command-structure checks used by executor evidence PRs. Dry-run reports whether the PR body needs a file-list refresh; `--execute` refreshes that body before commit and then runs the commands locally.

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

`generate-executor-evidence-pr-draft` validates the existing `evidence/executor-<stage>-run.json` against the current approved payload hash, rejects evidence when the corresponding DDL schema diff or stage plan is not reviewed, and rejects evidence that has no executor command records. Evidence status must be `succeeded` or `failed`, and command IDs must be unique. Each command record must include `id`, non-empty `args`, `shell_command`, `exit_code`, `started_at`, `completed_at`, and `duration_ms`; timestamps must parse as RFC3339Nano, `completed_at` must not be earlier than `started_at`, and duration must be non-negative. Optional `cdc_applied_changes` values must also be non-negative, and CDC evidence PR bodies include a CDC applied-changes column when the field is present. CDC evidence PR drafts also list the source-cluster `state/cdc-checkpoint.yaml` so evidence and checkpoint advancement can be reviewed together. `succeeded` evidence requires every command exit code to be `0`, and `failed` evidence requires at least one non-zero command exit code. The command then writes a Markdown PR body under `prs/`, including a command summary table with exit codes and timing. `create-executor-evidence-pr` is a dry-run by default and prints deterministic `git switch`, `git add`, `git commit`, `git push`, and `gh pr create` commands. Add `--execute` only when the local checkout should create the branch, commit the evidence/body, push it, and open a GitHub PR.

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

After `plan/validation-plan.yaml` is reviewed and `approvals/validation-approval.yaml` is set to `status: approved` with that hash, run the validation-only worker:

```bash
go run ./cmd/sqlserver2tidb worker-validate \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

This checks approved metadata, writes `state/validation-status.yaml`, and writes `evidence/validation-report.md`. The validation plan structural check message includes a supported check-type summary, for example `1 row_count, 1 checksum, 1 sampled_hash, 1 business_sql`.

## Documentation

- [User Manual](docs/user-manual.md): end-to-end operator guide for the target SQL Server to TiDB migration agent workflow.
- [Design Notes](docs/design.md): control-plane, metadata, and LLM responsibility boundaries.

## Design Principles

- `https://github.com/BornChanger/sqlserver2tidb` is the source of truth for migration control metadata.
- Metadata is organized by upstream SQL Server cluster.
- A source cluster can contain multiple migration projects.
- LLM output is never executed directly. It must become reviewed repository files first.
- Workers execute only approved and merged instructions.
- Project migration state phases are restricted to `planning`, `ddl`, `export`, `import`, `cdc`, `validation`, `cutover`, or `completed`; status is restricted to `not_started`, `planned`, `running`, `completed`, or `failed`; and `updated_at` must be RFC3339.
- Export/import state phase and status fields are optional during initialization, but phase must match `export`/`import` and status must be `planned` when present; their optional `updated_at` fields must be RFC3339.
- Validation status state is restricted to `pending`, `passed`, or `failed`; its optional phase must be `validation`, and when present `updated_at` must be RFC3339.
- State `payload_hash` fields are optional during initialization, but must use `sha256:<64 hex chars>` when present.
- High-frequency logs and per-event CDC offsets do not belong in GitHub. Periodic checkpoint snapshots do.
- CDC checkpoint snapshots stay source-cluster scoped; their mode must match the source cluster `cdc.mode`, optional phase must be `cdc`, status must be one of `not_started`, `planned`, `running`, `caught_up`, or `failed`, `updated_at` must be RFC3339, and table checkpoint entries must carry source/target objects, 10-byte hex `from_lsn` / `to_lsn` with `from_lsn <= to_lsn`, non-negative `applied_changes`, and RFC3339 `completed_at`.
- Plaintext credentials must never be committed. Use secret references only.

## Next Milestones

- Extend `sqlserver2tidb-executor export` beyond CSV over local file/HTTP(S) to native object storage clients and reviewed production formats.
- Extend `sqlserver2tidb-executor import` with more production import controls, including remote header inspection and richer `IMPORT INTO` options.
- Extend validation beyond exact-numeric scalar-query checksum/sample-hash checks to broader row digest generators and production-grade bucketed sampled-hash strategies.
