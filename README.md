# sqlserver2tidb

[![ci](https://github.com/BornChanger/sqlserver2tidb/actions/workflows/ci.yml/badge.svg)](https://github.com/BornChanger/sqlserver2tidb/actions/workflows/ci.yml)

`sqlserver2tidb` is a GitOps-oriented migration control toolkit for SQL Server to TiDB migrations.

The repository itself is the durable metadata store. Migration state, plans, approvals, checkpoints, and evidence are stored as YAML, JSON, and Markdown files under this repo. GitHub PRs provide the review and approval boundary.

## Current Scope

This MVP provides:

- A Go CLI named `sqlserver2tidb`.
- Initialization of the GitOps metadata repository structure.
- A local `doctor` preflight command for repository validation and optional `git`/`gh`/executor availability checks.
- Validation of the GitOps metadata repository structure, including schema policy mappings, inventory JSON parseability/status, cluster/project identity consistency, source profile/state/evidence ownership, schema diff status/timestamps/summary counts, evidence status/timestamps, review plan statuses, approval metadata/audit timestamps, export/import/CDC work-item fields, SQL Server source object and TiDB target object name shape, required row-count and query-based validation plan fields, and unresolved TODO predicates.
- SQL Server discovery dry-run planning without opening a database connection.
- SQL Server catalog discovery using a connection string supplied through an environment variable.
- Rule-based SQL Server compatibility analysis from `inventory/inventory.json`.
- Project-scoped TiDB schema draft generation from SQL Server inventory and project metadata.
- Project-scoped full export/import plan draft generation from SQL Server inventory and project metadata.
- Project-scoped CDC plan draft generation from SQL Server inventory and project metadata.
- Project-scoped row-count validation plan draft generation from SQL Server inventory and project metadata, with optional exact-numeric scalar-query checksum, sampled-hash, and bucketed-count draft checks.
- PR draft generation and a dry-run-by-default GitHub PR creation wrapper.
- DDL, export, import, CDC, validation, and cutover payload hash calculation.
- Approved metadata-only export/import/CDC/validation/cutover worker state write-back.
- Dry-run-by-default external executor command generation for approved DDL/export/import/CDC/validation plans, with structured executor evidence for timing, retries, CDC applied-change counts, and export/import data-volume metrics.
- `sqlserver2tidb-executor` adapter for DDL/export/import/CDC plus row-count and query-based validation work items, including `apply-ddl --execute`, CSV `export --execute` to local file, HTTP(S), S3, GCS, or Azure Blob, CSV `import --execute` from local file, HTTP(S), S3, GCS, or Azure Blob, `validate-count --execute`, `validate-query --execute`, `cdc-lsn --execute`, and `cdc --execute` paths.
- A `cdc-orchestrator` command that repeatedly probes SQL Server CDC max LSN through the executor, prepares the next reviewed CDC range from committed checkpoints, writes a range PR draft, and can explicitly execute already-approved CDC ranges while preserving GitHub approval gates.
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
- Tests for repository initialization, validation, cluster/project metadata consistency, source profile/state/evidence/approval ownership checks, validation plan content and object-name checks, executor-supported data plan format checks, discovery planning and execution, compatibility analysis, schema draft generation, data movement, CDC, and validation plan generation, PR draft generation, GitHub PR create dry-runs, DDL/export/import/CDC/validation/cutover worker gates, external executor command dry-runs, executor binary dry-runs, DDL apply checks, CSV export/import execution checks, executor evidence metrics, row-count and query-based validation command checks, worker reconcile dry-runs, execute-next state PR drafts, bounded loops, and worker-agent runs, worker state PR create dry-runs, executor evidence PR drafts and dry-runs, upstream SQL Server cluster creation, and migration project creation.

This MVP connects to SQL Server for read-only catalog discovery and, when `sqlserver2tidb-executor export --execute` is explicitly used, for a CSV export path to local `file://`, HTTP(S), native `s3://`, native `gs://`, or native `azblob://` output. Executor dry-runs validate object names through the same SQL builders used by execute mode. Executor export dry-runs validate output URI compatibility and reject `TODO` predicates without opening SQL Server or writing CSV output. It connects to TiDB when `sqlserver2tidb-executor apply-ddl --execute` is explicitly used with a reviewed DDL file, or when `sqlserver2tidb-executor import --execute` is explicitly used with a TiDB/MySQL connection string environment variable; apply-DDL dry-runs read the DDL file and reject unresolved `TODO` markers or empty SQL without opening TiDB. Import supports the default `sql-insert` engine for local `file://`, HTTP(S), S3, GCS, or Azure Blob CSV streaming with batched inserts, the explicit `tidb-import-into` engine for TiDB `IMPORT INTO ... FROM FILE` over an absolute local path, local `file://`, `s3://`, or `gs://` file location, and the explicit `tidb-lightning` engine that generates Lightning-friendly CSV plans and invokes an external `tidb-lightning -config <toml>` process over local `file://`, `s3://`, `gs://`, or `azblob://` data-source directories. Executor import dry-runs validate source URI compatibility for the selected engine without opening TiDB or the CSV source. HTTP(S) import URIs must include a host, S3/GCS URIs must include both a bucket and object path, and Azure Blob URIs use `azblob://<container>/<blob>`. Default CSV export writes a header plus an internal `__sqlserver2tidb_null_bitmap` column so `sql-insert` and `tidb-import-into` can restore SQL NULL values; `tidb-lightning` data plans instead write `null_encoding: backslash-n`, omit the internal bitmap column, encode SQL NULL as `\N`, and generate Lightning TOML with `null = '\N'`. `--compression gzip` writes/reads gzip-compressed CSV for the `sql-insert` and `tidb-lightning` paths and sets `Content-Encoding: gzip` for HTTP(S), S3, GCS, and Azure Blob export uploads. S3 export/import uses AWS Signature V4 with `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION` or `AWS_DEFAULT_REGION`, optional `AWS_SESSION_TOKEN`, and optional `AWS_ENDPOINT_URL` / `AWS_S3_FORCE_PATH_STYLE`. GCS export/import uses HMAC credentials from `GCS_ACCESS_KEY_ID` / `GCS_SECRET_ACCESS_KEY` (or `GOOG_ACCESS_KEY_ID` / `GOOG_SECRET_ACCESS_KEY`) and optional `GCS_ENDPOINT_URL`. Azure Blob export/import uses Shared Key auth from `AZURE_STORAGE_ACCOUNT`, base64 `AZURE_STORAGE_KEY`, and optional `AZURE_BLOB_ENDPOINT_URL`. HTTP(S)/S3/GCS/Azure Blob CSV downloads and uploads retry transient request errors and 408/429/5xx responses up to three attempts; remote uploads are sent from local temporary files so retries replay the complete payload. `sql-insert` can optionally preflight the target table with `COUNT(*)` when `--require-empty-target` is set, but that check is disabled by default so multi-chunk import jobs can load the same target table. Import-plan `fields` are only valid with `tidb-import-into`; `sql-insert` and `tidb-lightning` plans rely on CSV headers and are rejected if reviewed jobs carry `fields`. `tidb-import-into` reads local/file/S3/GCS CSV headers or uses reviewed import-plan `fields`, maps that internal tail column to a TiDB user variable so it is skipped, and always preflights the target table with `COUNT(*)` before executing `IMPORT INTO`; reviewed fields must be non-empty, duplicate-free, and user variables must use simple `@name` syntax. Azure Blob is currently supported for the `sql-insert` and `tidb-lightning` CSV paths, not for TiDB `IMPORT INTO`. Gzip compression is not enabled for `tidb-import-into` in this agent yet. It can also connect to both SQL Server and TiDB for explicit `sqlserver2tidb-executor validate-count --execute` row-count comparison and `sqlserver2tidb-executor validate-query --execute` reviewed scalar-query comparison for `checksum`, `sampled_hash`, `bucketed_count`, and `business_sql` validation checks; validation dry-runs reject unresolved `TODO` predicates or scalar SQL before any database connection is opened. The included `sqlserver2tidb-executor cdc-lsn --execute` path can query SQL Server CDC max LSN and, when a source object is provided, the capture instance min LSN. The included `sqlserver2tidb-executor cdc --execute` path can apply one explicit SQL Server CDC LSN range to TiDB after validating source/target connection strings, captured columns, and key columns; CDC dry-runs validate captured columns, key-column membership, and LSN format/range without starting a CDC reader or TiDB apply worker. `worker-cutover` is a GitHub-file gate that records completed cutover state only after reviewed runbook, successful export/import/validation executor evidence, passed validation worker state, and for non-offline projects a caught-up CDC checkpoint. It does **not** switch application traffic or perform DNS/proxy changes. It does **not** perform automatic CDC PR approval/merge, cleanup, or native row-digest/bucketed sampled-hash strategies.

Successful CSV export execution prints `exported rows: N`, `output bytes: N`, and `output sha256: sha256:<digest>`. Local `file://` CSV export writes a same-directory temporary file and atomically publishes it to the target path only after the CSV writer closes successfully. HTTP(S), S3, GCS, and Azure Blob export spool to local temporary files and only start the upload after the CSV writer closes successfully; abort removes the temporary file without starting a remote upload. Successful `sql-insert` import execution prints `imported rows: N`, `input bytes: N`, and `input sha256: sha256:<digest>`; HTTP(S), S3, GCS, and Azure Blob imports explicitly request `Accept-Encoding: identity` so byte and digest metrics describe the stored object bytes. Local-path, `file://`, S3, and GCS `tidb-import-into` imports pre-audit the CSV and print the same row/byte/SHA tuple after successful `IMPORT INTO`. `tidb-lightning` imports pre-audit all import-plan CSV sources, reject files that still contain the internal null bitmap column, print aggregate row/byte/SHA metrics, write a temporary or requested Lightning TOML config, and then invoke the external Lightning binary. `worker-executor --execute` records those values as command-level `data_rows`, `data_bytes`, and `data_sha256` in `evidence/executor-<stage>-run.json`, and fails export / auditable import commands whose successful output omits the complete data audit tuple. Executor evidence PRs render these metrics for review. `validate-repo` and executor evidence PR generation require complete data audit for successful export, `sql-insert` import, local/file/S3/GCS `tidb-import-into` evidence, and `tidb-lightning` evidence.

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

By default, missing local tools are reported as warnings. Add `--require-tools` when the environment must already have `git`, `gh`, and `sqlserver2tidb-executor` on `PATH`. Add `--json` to emit repository and tool status for CI/CD or monitoring integrations.

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
  --object-uri-prefix https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full \
  --compression gzip
```

This writes `plan/export-plan.yaml` and `plan/import-plan.yaml` under the project. The command estimates chunks from inventory `row_count`; single-chunk tables get a reviewed-safe `1 = 1` predicate, while multi-chunk tables still get `TODO` split predicates that must be reviewed before export execution. It generates executor-supported CSV plans with `sql-insert` imports over local `file://`, `http://`, `https://`, `s3://`, `gs://`, or `azblob://` URI prefixes by default; HTTP(S) prefixes must include a host, S3/GCS prefixes must include a bucket, and Azure Blob prefixes use the container as the URI host. `--compression gzip` records `compression: gzip` in both plans and generates `.csv.gz` object names; the worker executor then passes `--compression gzip` to export and import commands. If `--import-engine tidb-import-into` is used, the executable URI prefix must be a local absolute `file://` path, `s3://`, or `gs://`; S3/GCS prefixes must include a bucket, and compression must stay `none`. If `--import-engine tidb-lightning` is used, the executable URI prefix must be local `file://`, `s3://`, `gs://`, or `azblob://`; generated export files are named from TiDB target objects, the export plan records `null_encoding: backslash-n`, and the import plan records a top-level `data_source_uri` so one Lightning command can load the whole data directory. Only `tidb-import-into` import jobs get a `fields` list derived from inventory columns plus `@sqlserver2tidb_null_bitmap` so object-storage imports can skip the internal CSV tail column; `sql-insert` and `tidb-lightning` jobs do not carry `fields`. It does not connect to SQL Server or TiDB and does not move data.

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

Prepare one CDC iteration from the committed checkpoint and a known SQL Server max LSN:

```bash
go run ./cmd/sqlserver2tidb prepare-cdc-iteration \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --max-lsn 0x00000027000001f40004 \
  --pr-draft
```

This is the deterministic GitHub-file part of a continuous CDC loop. It compares each tracked table checkpoint `to_lsn` with the supplied `--max-lsn`; if work remains, it rewrites `plan/cdc-plan.yaml` with the next range and can write `prs/cdc-range-pr.md` for review. If all tables are already at `--max-lsn`, it reports `caught_up` and leaves the plan unchanged. It still does not query SQL Server itself; use `sqlserver2tidb-executor cdc-lsn --execute` or an external scheduler to supply the max LSN.

Run the CDC orchestrator as the long-running probe-and-plan loop:

```bash
go run ./cmd/sqlserver2tidb cdc-orchestrator \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --apply-approved \
  --poll \
  --pr-draft
```

The orchestrator invokes `sqlserver2tidb-executor cdc-lsn --execute`, parses `max_lsn`, probes each tracked table's SQL Server CDC `min_lsn`, and then calls the same deterministic `prepare-cdc-iteration` path. If a table's next `from_lsn` is older than its `min_lsn`, the orchestrator fails before mutating `plan/cdc-plan.yaml` because the CDC retention window no longer covers the requested range. Use `--skip-retention-check` only when another scheduler has already enforced the same min-LSN guard. When `--apply-approved` is set, each iteration first checks whether the current CDC plan and approval already pass the approval/hash gate; if so, it runs `worker-executor --stage cdc --execute`, records `evidence/executor-cdc-run.json`, advances `clusters/<source_cluster_id>/state/cdc-checkpoint.yaml` from that evidence, and skips reapplying a range that the checkpoint already covers. When the project is caught up it can keep polling; when a new range is prepared it writes `plan/cdc-plan.yaml` and optional `prs/cdc-range-pr.md`, then stops at the PR boundary. It does not approve or merge PRs.

Generate a project-scoped validation draft plan from the current SQL Server inventory and project metadata:

```bash
go run ./cmd/sqlserver2tidb generate-validation-plan \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --include-checksum \
  --include-sampled-hash \
  --sample-modulo 100 \
  --include-bucketed-count \
  --bucket-count 16
```

This writes `plan/validation-plan.yaml` under the project with one `row_count` check per table in scope. When requested, it also adds `checksum` and `sampled_hash` scalar-query checks for tables that have exact numeric columns; sampled-hash checks require an integer sample column and use `--sample-modulo` to build the deterministic sample predicate. With `--include-bucketed-count`, it adds one `bucketed_count` scalar-query check per modulo bucket for tables that have a non-computed integer bucket column; `--bucket-count` defaults to `16` and is capped at `1024`. The command does not connect to SQL Server or TiDB and does not execute validation.

Compute payload hashes and run reviewed DDL/export/import/CDC/validation/cutover actions after the matching approval files are marked approved. `worker-export`, `worker-import`, `worker-cdc`, and `worker-validate` also require their plan files to be `reviewed` or `approved`; draft export/import/CDC/validation plans are not executable even with approved approval files:

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

go run ./cmd/sqlserver2tidb compute-payload-hash \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage cutover

go run ./cmd/sqlserver2tidb worker-cutover \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

These workers only convert approved repository files and evidence into state/evidence files. They do not export data, import data, start CDC, switch application traffic, connect to databases, or write object storage.

Preview external executor commands for an approved stage:

```bash
go run ./cmd/sqlserver2tidb worker-executor \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export \
  --source-connection-string-env SQLSERVER_READONLY_DSN
```

This command reuses the same approval/hash gate and is dry-run by default. It prints `sqlserver2tidb-executor` commands for reviewed DDL files, export chunks, import jobs, CDC table apply work, validation row-count checks, or validation scalar-query checks. Executor preparation fails when DDL schema diff is not `reviewed`, when an export/import/CDC/validation plan is still `draft`, when an approved export/import/CDC plan has no work items, when a reviewed export/import/validation/CDC source table is no longer present in current SQL Server inventory, when reviewed schema-diff baselines or `tidb-import-into` fields no longer match current inventory columns, when an approved CDC table has no `columns`, `key_columns`, `from_lsn`, or `to_lsn`, when CDC key columns are not present in `columns`, when reviewed CDC columns or key columns no longer match the current inventory, when an approved export plan still has `TODO` predicates, when export format is not `csv`, when export/import plan compression is not `none` or `gzip`, when gzip compression is combined with `tidb-import-into`, when an export plan `null_encoding` is not `bitmap` or `backslash-n`, when an export chunk `output_uri` is not supported by the included executor, when import engine is not `sql-insert`, `tidb-import-into`, or `tidb-lightning`, when an import job `source_uri` is not supported by the selected import engine, when import job `fields` are present for any engine other than `tidb-import-into`, when `tidb-import-into` fields are empty, duplicated, or contain unsafe user variables, when `tidb-lightning` import jobs do not resolve to one data source directory, or when the approved validation plan contains no supported `row_count`, `checksum`, `sampled_hash`, `bucketed_count`, or `business_sql` checks. The source schema drift checks read `clusters/<source_cluster_id>/inventory/inventory.json` and optional reviewed `schema/schema-diff.json`; they do not use a TiDB metadata table. Row-count validation checks can carry separate source `predicate` and target `target_predicate` filters. `checksum`, `sampled_hash`, `bucketed_count`, and `business_sql` validation checks carry reviewed `source_sql` and `target_sql` scalar queries. Use `--source-connection-string-env`, `--target-connection-string-env`, and `--import-batch-size` to pass execution settings into generated executor commands; use `--require-empty-target` only when `sql-insert` import commands should fail before opening the CSV source if the target table is already non-empty. Reviewed plan `compression: gzip` is passed as executor `--compression gzip` for export, `sql-insert`, and `tidb-lightning` commands. Reviewed `tidb-import-into` import job `fields` are passed as executor `--fields`; reviewed `tidb-lightning` import plans are rendered as one `--engine tidb-lightning` command with `--import-plan` and `--source-uri <data_source_uri>`; and reviewed CDC table `columns` / `key_columns` / `from_lsn` / `to_lsn` are passed as executor `--columns` / `--key-columns` / `--from-lsn` / `--to-lsn`. When `worker-executor --execute` is used, it invokes the external executor, passes the executor-level `--execute` flag, and writes `evidence/executor-<stage>-run.json` with command output, exit codes, per-command start/end timestamps, per-command duration, optional retry `attempt_count` / `attempts`, and for CDC commands the parsed `cdc_applied_changes` value from the required `applied changes: N` output line. Use `--command-timeout <duration>` to cap each external executor command; the default `0` disables the timeout. Use `--command-retries <n>` and `--retry-backoff <duration>` for bounded retries of failed executor commands; the command-level evidence records the final attempt, while `attempts` preserves every retry attempt for review. Use `--resume` after a partial run to skip commands that already have matching successful evidence for the same stage, payload hash, command ID, and exact executor args; non-matching, failed, or CDC commands without `cdc_applied_changes` are executed again. Timed-out commands are killed, recorded as failed evidence with a command `error`, and cause non-validation stages to fail fast after retries are exhausted while validation continues aggregating command results. If a CDC executor command omits the applied-changes line or prints a non-numeric value, execute mode records failed evidence before returning non-zero. For `validation`, execute mode runs all generated validation commands before writing failed evidence, so one mismatch does not hide later check results; non-validation stages remain fail-fast. The included executor binary can apply reviewed TiDB DDL, run SQL Server to local `file://`, HTTP(S), S3, GCS, or Azure Blob CSV/gzip CSV export, stream local `file://`, HTTP(S), S3, GCS, or Azure Blob CSV/gzip CSV sources to TiDB import with `sql-insert`, optionally preflight non-empty `sql-insert` targets with `--require-empty-target`, execute TiDB `IMPORT INTO` for `tidb-import-into` local/file/S3/GCS file locations, invoke an external TiDB Lightning binary for `tidb-lightning` local/file/S3/GCS/Azure Blob data-source directories, preserve SQL NULLs through an internal CSV null bitmap column for `sql-insert` imports or local/file/S3/GCS derived or reviewed `tidb-import-into` fields, use `\N` null encoding for Lightning CSV, run approval-gated SQL Server/TiDB row-count comparison, run approval-gated scalar-query validation, query SQL Server CDC LSN bounds with `cdc-lsn`, and apply a reviewed SQL Server CDC LSN range to TiDB. Automatic CDC PR approval/merge and native generated row-digest/sample-hash strategies are still not implemented.

Advance the source-cluster CDC checkpoint from successful CDC executor evidence:

```bash
go run ./cmd/sqlserver2tidb advance-cdc-checkpoint \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --status running
```

This validates `evidence/executor-cdc-run.json` against the current approval, payload hash, and reviewed CDC plan, requires succeeded CDC executor commands with `cdc_applied_changes`, verifies command source/target/LSN values match the current plan, and rewrites `clusters/<source_cluster_id>/state/cdc-checkpoint.yaml` with one checkpoint snapshot per applied table. Use `--status caught_up` only when the reviewed LSN range is known to represent catch-up. The command does not query SQL Server min/max LSNs; use `cdc-orchestrator` for the long-running probe/approved-apply/plan loop, or `sqlserver2tidb-executor cdc-lsn --execute` plus `prepare-cdc-range` for manual operation.

Preview ready and blocked worker actions across the repository:

```bash
go run ./cmd/sqlserver2tidb worker-reconcile \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --dry-run
```

This scans cluster/project metadata and reports which `worker-executor --stage ddl`, `worker-export`, `worker-import`, `worker-cdc`, `worker-validate`, and `worker-cutover` actions are ready or blocked by approval/hash checks. Use `--source-cluster-id` to scope the scan to one upstream SQL Server cluster directory. DDL actions are blocked until `schema/schema-diff.json` is `reviewed`; export, import, CDC, and validation actions are blocked while their plan files are still `draft`; cutover is blocked until the runbook is reviewed, cutover approval matches, export/import/validation executor evidence succeeded, validation worker state passed, and non-offline projects have a caught-up CDC checkpoint. A metadata-only stage is also blocked when the same approved payload hash already has non-empty state such as `planned`, `passed`, `completed`, or `failed`, preventing reconcile loops from repeatedly running the same action. It does not execute workers, acquire leases, or write state.

Add `--json` to emit the same dry-run report as machine-readable JSON with `projects`, `ready_actions`, `blocked_actions`, and `actions` fields.

Execute the first ready metadata-only worker action with a source-cluster lease:

```bash
go run ./cmd/sqlserver2tidb worker-reconcile \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --execute-next \
  --holder agent-a \
  --state-pr-draft
```

This acquires or renews `state/worker-lease.yaml` for the selected source cluster, runs exactly one ready metadata-only worker action (`export`, `import`, `cdc`, `validation`, or `cutover`), and writes the same state/evidence files that the explicit single-project worker would write. Active lease records include non-empty `holder`, `lease_id`, `project_id`, `expires_at`, and `renewed_at`; `project_id` must reference an existing project directory under the same source cluster; timestamps are RFC3339 and `expires_at` must not be before `renewed_at`; `phase: idle` remains the empty placeholder state. DDL is intentionally executor-only and must be run through `worker-executor --stage ddl`. With `--state-pr-draft`, it also writes a deterministic Markdown PR body under the project `prs/` directory for reviewing the state/evidence/lease changes. It does not create a branch or call GitHub. A different holder is blocked until the lease expires.

Run a bounded reconcile loop:

```bash
go run ./cmd/sqlserver2tidb worker-reconcile \
  --root . \
  --source-cluster-id prod-sqlserver-a \
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
  --source-cluster-id prod-sqlserver-a \
  --holder agent-a \
  --max-iterations 0 \
  --interval 5s \
  --poll \
  --idle-iterations 0 \
  --state-pr-draft
```

`worker-agent` is the same deterministic metadata-only loop packaged as a stable process entrypoint for local runs and containers. It requires a holder, uses the source-cluster lease, can be scoped with `--source-cluster-id`, can emit state PR drafts, and stops only when no ready metadata-only action remains unless `--poll` is enabled. In poll mode, `--idle-iterations 0` means keep waiting for new ready metadata actions; set a positive value for bounded smoke tests or batch jobs.

Run optional SQL Server + TiDB integration tests:

```bash
make integration-test
```

This starts the Docker Compose environment in `tests/integration/` unless `SQLSERVER2TIDB_INTEGRATION_SOURCE_DSN` and `SQLSERVER2TIDB_INTEGRATION_TARGET_DSN` are already set. It runs both the direct executor full-load flow and the CLI/GitOps E2E flow: real SQL Server catalog discovery, schema/data/validation plan generation, approval hash checks, `worker-executor --execute` DDL/export/import/validation, `worker-validate`, and `validate-repo`. The target is intentionally outside default `make ci` because the default path pulls database images and requires Docker.

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

`generate-executor-evidence-pr-draft` validates the existing `evidence/executor-<stage>-run.json` against the current approved payload hash, rejects evidence when the corresponding DDL schema diff or stage plan is not reviewed, and rejects evidence that has no executor command records. Evidence status must be `succeeded` or `failed`, top-level `generated_at` must be RFC3339 when present, and command IDs must be unique. Each command record must include `id`, non-empty `args`, shell-quoted `shell_command` matching those args, `exit_code`, `started_at`, `completed_at`, and `duration_ms`; command timestamps must parse as RFC3339Nano, `completed_at` must not be earlier than `started_at`, and duration must be non-negative. Optional command `attempt_count` must be at least 1, and when `attempts` is present its length and per-attempt timing fields must match `attempt_count`. Optional command `error` values are rendered into the PR body when present. Optional `cdc_applied_changes` values must also be non-negative, and CDC evidence PR bodies include a CDC applied-changes column when the field is present. CDC evidence PR drafts also list the source-cluster `state/cdc-checkpoint.yaml` so evidence and checkpoint advancement can be reviewed together. `succeeded` evidence requires every final command exit code to be `0` and every final command error to be empty; `failed` evidence requires at least one non-zero final command exit code or command error. The command then writes a Markdown PR body under `prs/`, including a command summary table with exit codes, attempt counts when present, timing, command errors when present, and a whitespace-normalized output summary capped for review. `create-executor-evidence-pr` is a dry-run by default, rejects stale PR bodies whose content no longer matches the current evidence, and prints deterministic `git switch`, `git add`, `git commit`, `git push`, and `gh pr create` commands. Add `--execute` only when the local checkout should create the branch, commit the evidence/body, push it, and open a GitHub PR.

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

This checks approved metadata, writes `state/validation-status.yaml`, and writes `evidence/validation-report.md`. The validation plan structural check message includes a supported check-type summary, for example `1 row_count, 1 checksum, 1 sampled_hash, 16 bucketed_count, 1 business_sql`. If `evidence/executor-validation-run.json` exists, the worker also validates that executor evidence against the current approval hash and adds an executor-evidence summary to the validation status/report; failed validation commands make the worker result `failed`.

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
- CDC checkpoint snapshots stay source-cluster scoped; their mode must match the source cluster `cdc.mode`, optional phase must be `cdc`, status must be one of `not_started`, `planned`, `running`, `caught_up`, or `failed`, `updated_at` must be RFC3339, and table checkpoint entries must carry SQL Server source objects as `schema.table` or `database.schema.table`, TiDB target objects as `table` or `database.table`, 10-byte hex `from_lsn` / `to_lsn` with `from_lsn <= to_lsn`, non-negative `applied_changes`, and RFC3339 `completed_at`.
- Plaintext credentials must never be committed. Use secret references only.

## Next Milestones

- Extend `sqlserver2tidb-executor export` beyond CSV over local file/HTTP(S)/S3/GCS/Azure Blob to reviewed production formats.
- Extend `sqlserver2tidb-executor import` with more production import controls, including richer `IMPORT INTO` / Lightning options, TLS/security settings, and provider-specific Lightning credential validation.
- Extend validation beyond exact-numeric scalar-query checksum/sample-hash and integer bucketed-count checks to broader row digest generators and production-grade bucketed sampled-hash strategies.
