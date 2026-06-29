# Integration Tests

This directory contains an optional Docker Compose environment for real SQL Server to TiDB integration tests.

The default unit test path does not run these tests. To run the SQL Server to TiDB integration suite:

```bash
make integration-test
```

The suite starts SQL Server and TiDB unless external DSNs are provided, then runs:

- executor full-load flow: prepare `dbo.orders`, export to local CSV, import into TiDB, and run row-count validation.
- CLI/GitOps E2E flow: run real SQL Server catalog discovery, generate schema/data/validation plans, approve payload hashes, execute DDL/export/import/validation through `worker-executor --execute`, run `worker-validate`, and finish with `validate-repo`.

Run the longer CDC production-validation suite explicitly:

```bash
make cdc-soak-test
```

This sets `SQLSERVER2TIDB_RUN_CDC_SOAK=1`, starts the same SQL Server/TiDB environment unless external DSNs are supplied, enables SQL Server CDC on a dedicated test database, performs a full-load baseline, then executes multiple CDC rounds with insert/update/delete mutations. Each round prepares a reviewed CDC range, writes the matching approval hash, applies it through `cdc-orchestrator --apply-approved`, requires at least one applied CDC change, verifies the TiDB target rows, and checks the source-cluster checkpoint.

Useful environment variables:

- `SQLSERVER2TIDB_INTEGRATION_SQLSERVER_PORT`: host SQL Server port, default `14333`
- `SQLSERVER2TIDB_INTEGRATION_TIDB_PORT`: host TiDB port, default `4000`
- `SQLSERVER2TIDB_INTEGRATION_SQLSERVER_PASSWORD`: SQL Server SA password, default `Sqlserver2tidb!2026`
- `SQLSERVER2TIDB_KEEP_INTEGRATION_ENV=1`: keep containers running after the test
- `SQLSERVER2TIDB_RUN_CDC_SOAK=1`: append the CDC soak test to `make integration-test`
- `SQLSERVER2TIDB_CDC_SOAK_ITERATIONS`: CDC mutation/apply rounds for the soak test, default `3`

You can also bypass Docker Compose and provide DSNs directly. In this mode `scripts/run-integration-tests.sh` does not require Docker:

```bash
SQLSERVER2TIDB_INTEGRATION_SOURCE_DSN='sqlserver://sa:password@127.0.0.1:1433?database=tempdb&encrypt=disable&TrustServerCertificate=true' \
SQLSERVER2TIDB_INTEGRATION_TARGET_DSN='root@tcp(127.0.0.1:4000)/' \
make integration-test
```

For external CDC soak runs, the source DSN user must be able to create or use the `s2t_cdc_e2e` database, enable database/table CDC, and query CDC functions. The target DSN user must be able to recreate the `s2t_cdc_e2e` database.

The repository also includes a manual GitHub Actions workflow, `.github/workflows/cdc-soak.yml`. Configure `SQLSERVER2TIDB_INTEGRATION_SOURCE_DSN` and `SQLSERVER2TIDB_INTEGRATION_TARGET_DSN` as repository or environment secrets, then trigger the `cdc-soak` workflow with the desired iteration count.
