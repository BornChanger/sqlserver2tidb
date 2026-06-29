# Integration Tests

This directory contains an optional Docker Compose environment for real SQL Server to TiDB integration tests.

The default unit test path does not run these tests. To run the SQL Server to TiDB integration suite:

```bash
make integration-test
```

The suite starts SQL Server and TiDB unless external DSNs are provided, then runs:

- executor full-load flow: prepare `dbo.orders`, export to local CSV, import into TiDB, and run row-count validation.
- CLI/GitOps E2E flow: run real SQL Server catalog discovery, generate schema/data/validation plans, approve payload hashes, execute DDL/export/import/validation through `worker-executor --execute`, run `worker-validate`, and finish with `validate-repo`.

Useful environment variables:

- `SQLSERVER2TIDB_INTEGRATION_SQLSERVER_PORT`: host SQL Server port, default `14333`
- `SQLSERVER2TIDB_INTEGRATION_TIDB_PORT`: host TiDB port, default `4000`
- `SQLSERVER2TIDB_INTEGRATION_SQLSERVER_PASSWORD`: SQL Server SA password, default `Sqlserver2tidb!2026`
- `SQLSERVER2TIDB_KEEP_INTEGRATION_ENV=1`: keep containers running after the test

You can also bypass Docker Compose and provide DSNs directly. In this mode `scripts/run-integration-tests.sh` does not require Docker:

```bash
SQLSERVER2TIDB_INTEGRATION_SOURCE_DSN='sqlserver://sa:password@127.0.0.1:1433?database=tempdb&encrypt=disable&TrustServerCertificate=true' \
SQLSERVER2TIDB_INTEGRATION_TARGET_DSN='root@tcp(127.0.0.1:4000)/' \
make integration-test
```
