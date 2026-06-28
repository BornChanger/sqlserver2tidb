# Integration Tests

This directory contains an optional Docker Compose environment for executor integration tests.

The default unit test path does not run these tests. To run the SQL Server to TiDB full-load executor flow:

```bash
make integration-test
```

The test starts SQL Server and TiDB, prepares a small `dbo.orders` table in SQL Server, exports it to a local CSV file, imports it into TiDB, and runs row-count validation.

Useful environment variables:

- `SQLSERVER2TIDB_INTEGRATION_SQLSERVER_PORT`: host SQL Server port, default `14333`
- `SQLSERVER2TIDB_INTEGRATION_TIDB_PORT`: host TiDB port, default `4000`
- `SQLSERVER2TIDB_INTEGRATION_SQLSERVER_PASSWORD`: SQL Server SA password, default `Sqlserver2tidb!2026`
- `SQLSERVER2TIDB_KEEP_INTEGRATION_ENV=1`: keep containers running after the test

You can also bypass Docker Compose and provide DSNs directly:

```bash
SQLSERVER2TIDB_INTEGRATION_SOURCE_DSN='sqlserver://sa:password@127.0.0.1:1433?database=tempdb&encrypt=disable&TrustServerCertificate=true' \
SQLSERVER2TIDB_INTEGRATION_TARGET_DSN='root@tcp(127.0.0.1:4000)/' \
go test -tags=integration ./internal/executor -run TestSQLServerToTiDBFullLoadExecutorFlow -count=1 -v
```
