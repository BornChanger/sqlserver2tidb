# sqlserver2tidb

`sqlserver2tidb` is a GitOps-oriented migration control toolkit for SQL Server to TiDB migrations.

The repository itself is the durable metadata store. Migration state, plans, approvals, checkpoints, and evidence are stored as YAML, JSON, and Markdown files under this repo. GitHub PRs provide the review and approval boundary.

## Current Scope

This MVP provides:

- A Go CLI named `sqlserver2tidb`.
- Initialization of the GitOps metadata repository structure.
- Validation of the GitOps metadata repository structure.
- SQL Server discovery dry-run planning without opening a database connection.
- SQL Server catalog discovery using a connection string supplied through an environment variable.
- Rule-based SQL Server compatibility analysis from `inventory/inventory.json`.
- Source-cluster-first metadata organization:

  ```text
  clusters/<source_cluster_id>/
    cluster.yaml
    inventory/
    state/
      cdc-checkpoint.yaml
      worker-lease.yaml
    projects/<project_id>/
      project.yaml
      schema/
      plan/
      state/
      evidence/
      approvals/
  ```

- JSON Schema files for core metadata.
- Tests for repository initialization, validation, discovery planning and execution, compatibility analysis, upstream SQL Server cluster creation, and migration project creation.

This MVP connects to SQL Server only for read-only catalog discovery when a connection string is supplied through an environment variable. It does **not** connect to TiDB or execute export, import, CDC, cutover, cleanup, or other migration actions yet.

## Build

```bash
make build
```

The binary is written to:

```text
bin/sqlserver2tidb
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

- Add schema conversion draft generator.
- Add PR generation helpers.
- Add deterministic worker execution for approved validation steps.
