# Quickstart Example

This directory contains a small offline SQL Server inventory fixture used by `scripts/run-quickstart-example.sh`.

Run from the repository root:

```bash
make example-check
```

The script creates a temporary migration metadata repository, injects `inventory.json`, generates schema/data/CDC/validation drafts, runs `worker-reconcile --dry-run`, and validates the generated repository. It does not connect to SQL Server, TiDB, GitHub, or object storage.

To keep the generated repository for inspection, provide an empty output directory:

```bash
SQLSERVER2TIDB_QUICKSTART_ROOT=/tmp/sqlserver2tidb-quickstart make example-check
```
