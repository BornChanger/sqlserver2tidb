# sqlserver2tidb

`sqlserver2tidb` is a GitOps-oriented migration control toolkit for SQL Server to TiDB migrations.

The repository itself is the durable metadata store. Migration state, plans, approvals, checkpoints, and evidence are stored as YAML, JSON, and Markdown files under this repo. GitHub PRs provide the review and approval boundary.

## Current Scope

This MVP provides:

- A Go CLI named `sqlserver2tidb`.
- Initialization of the GitOps metadata repository structure.
- Validation of the GitOps metadata repository structure, including required row-count validation plan fields.
- SQL Server discovery dry-run planning without opening a database connection.
- SQL Server catalog discovery using a connection string supplied through an environment variable.
- Rule-based SQL Server compatibility analysis from `inventory/inventory.json`.
- Project-scoped TiDB schema draft generation from SQL Server inventory and project metadata.
- Project-scoped full export/import plan draft generation from SQL Server inventory and project metadata.
- Project-scoped CDC plan draft generation from SQL Server inventory and project metadata.
- Project-scoped row-count validation plan draft generation from SQL Server inventory and project metadata.
- PR draft generation and a dry-run-by-default GitHub PR creation wrapper.
- Export, import, CDC, and validation payload hash calculation.
- Approved metadata-only export/import/CDC worker state write-back.
- Dry-run-by-default external executor command generation for approved export/import/CDC/validation plans.
- `sqlserver2tidb-executor` adapter for export/import/CDC and row-count validation work items, including minimal local CSV `export --execute`, `import --execute`, and `validate-count --execute` paths.
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

- JSON Schema files for core metadata.
- Tests for repository initialization, validation, validation plan content checks, discovery planning and execution, compatibility analysis, schema draft generation, data movement, CDC, and validation plan generation, PR draft generation, GitHub PR create dry-runs, export/import/CDC/validation worker gates, external executor command dry-runs, executor binary dry-runs, local CSV export/import execution checks, row-count validation command checks, worker reconcile dry-runs and execute-next state PR drafts, worker state PR create dry-runs, upstream SQL Server cluster creation, and migration project creation.

This MVP connects to SQL Server for read-only catalog discovery and, when `sqlserver2tidb-executor export --execute` is explicitly used, for a minimal local CSV export path. It connects to TiDB when `sqlserver2tidb-executor import --execute` is explicitly used with a local `file://` CSV source and a TiDB/MySQL connection string environment variable; CSV rows are streamed and committed in batches. It can also connect to both SQL Server and TiDB for an explicit `sqlserver2tidb-executor validate-count --execute` row-count comparison. It does **not** execute generated DDL, object storage export/import, TiDB Lightning or `IMPORT INTO`, CDC streaming/apply, cutover, cleanup, checksum validation, sampled hash validation, or business SQL validation. The included `sqlserver2tidb-executor cdc --execute` path intentionally returns an explicit not-implemented error.

## Build

```bash
make build
```

The binary is written to:

```text
bin/sqlserver2tidb
bin/sqlserver2tidb-executor
```

## Test

```bash
make test
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
  --object-uri-prefix s3://migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full
```

This writes `plan/export-plan.yaml` and `plan/import-plan.yaml` under the project. The command estimates chunks from inventory `row_count`; it does not connect to SQL Server or TiDB and does not move data.

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

Compute payload hashes and run metadata-only export/import/CDC workers after the matching approval files are marked approved:

```bash
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

These workers only convert approved plan files into planned state/evidence files. They do not export data, import data, start CDC, connect to databases, or write object storage.

Preview external executor commands for an approved stage:

```bash
go run ./cmd/sqlserver2tidb worker-executor \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export \
  --source-connection-string-env SQLSERVER_READONLY_DSN
```

This command reuses the same approval/hash gate and is dry-run by default. It prints `sqlserver2tidb-executor` commands for export chunks, import jobs, CDC table apply work, or validation row-count checks. Validation executor preparation fails when the approved validation plan contains no supported `row_count` checks. Validation checks can carry separate source `predicate` and target `target_predicate` filters. Use `--source-connection-string-env`, `--target-connection-string-env`, and `--import-batch-size` to pass execution settings into generated executor commands. When `worker-executor --execute` is used, it invokes the external executor and passes the executor-level `--execute` flag so the included executor leaves dry-run mode. The included executor binary can run SQL Server to local `file://` CSV export, streaming local `file://` CSV to TiDB import, and approval-gated SQL Server/TiDB row-count comparison after connection strings are supplied through environment variables. Object storage export/import, TiDB Lightning or `IMPORT INTO`, CDC apply side effects, checksum validation, sampled hash validation, and business SQL validation are still not implemented.

Preview ready and blocked worker actions across the repository:

```bash
go run ./cmd/sqlserver2tidb worker-reconcile \
  --root . \
  --dry-run
```

This scans cluster/project metadata and reports which `worker-export`, `worker-import`, `worker-cdc`, and `worker-validate` actions are ready or blocked by approval/hash checks. It does not execute workers, acquire leases, or write state.

Execute the first ready metadata-only worker action with a source-cluster lease:

```bash
go run ./cmd/sqlserver2tidb worker-reconcile \
  --root . \
  --execute-next \
  --holder agent-a \
  --state-pr-draft
```

This acquires or renews `state/worker-lease.yaml` for the selected source cluster, runs exactly one ready metadata-only worker action, and writes the same state/evidence files that the explicit single-project worker would write. With `--state-pr-draft`, it also writes a deterministic Markdown PR body under the project `prs/` directory for reviewing the state/evidence/lease changes. It does not create a branch or call GitHub. A different holder is blocked until the lease expires.

Prepare the git and GitHub commands for a worker state write-back PR:

```bash
go run ./cmd/sqlserver2tidb create-worker-state-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export
```

This is a dry-run by default. It requires the state PR draft and state/evidence/lease files to exist, then prints deterministic `git switch`, `git add`, `git commit`, `git push`, and `gh pr create` commands. Add `--execute` to run those commands locally.

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
- High-frequency logs and per-event CDC offsets do not belong in GitHub. Periodic checkpoint snapshots do.
- Plaintext credentials must never be committed. Use secret references only.

## Next Milestones

- Extend `sqlserver2tidb-executor export` beyond local CSV to reviewed object storage formats.
- Extend `sqlserver2tidb-executor import` beyond row-by-row local CSV inserts to reviewed production import engines.
- Extend validation beyond direct row-count checks to checksum, sampled hash, and reviewed business SQL checks.
