//go:build integration

package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/BornChanger/sqlserver2tidb/internal/executor"
	"github.com/BornChanger/sqlserver2tidb/internal/gitops"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/microsoft/go-mssqldb"
)

const (
	cliIntegrationSourceDSNEnv       = "SQLSERVER2TIDB_INTEGRATION_SOURCE_DSN"
	cliIntegrationTargetDSNEnv       = "SQLSERVER2TIDB_INTEGRATION_TARGET_DSN"
	cliIntegrationExecutorHelperEnv  = "SQLSERVER2TIDB_CLI_INTEGRATION_EXECUTOR_HELPER"
	cliIntegrationSourceConnEnv      = "SQLSERVER2TIDB_SOURCE_CONNECTION_STRING"
	cliIntegrationTargetConnEnv      = "SQLSERVER2TIDB_TARGET_CONNECTION_STRING"
	cliIntegrationSourceClusterID    = "integration-sqlserver"
	cliIntegrationProjectID          = "orders-to-tidb"
	cliIntegrationTargetDatabaseName = "s2t_e2e"
	cliIntegrationCDCDatabaseName    = "s2t_cdc_e2e"
	cliIntegrationRunCDCSoakEnv      = "SQLSERVER2TIDB_RUN_CDC_SOAK"
	cliIntegrationCDCSoakIterations  = "SQLSERVER2TIDB_CDC_SOAK_ITERATIONS"
)

func TestMain(m *testing.M) {
	if os.Getenv(cliIntegrationExecutorHelperEnv) == "1" {
		args := os.Args[1:]
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		os.Exit(executor.Run(args, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func TestSQLServerToTiDBGitOpsE2EFlow(t *testing.T) {
	sourceDSN, targetDSN := cliIntegrationDSNs(t)
	t.Setenv(cliIntegrationSourceConnEnv, sourceDSN)
	t.Setenv(cliIntegrationTargetConnEnv, targetDSN)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	prepareCLIIntegrationSource(t, ctx, sourceDSN)
	prepareCLIIntegrationTarget(t, ctx, targetDSN)
	sourceDatabase := cliIntegrationCurrentDatabase(t, ctx, sourceDSN)

	root := t.TempDir()
	objectStoreDir := filepath.Join(root, "object-store", "full")
	executorBinary := writeCLIIntegrationExecutorHelper(t)

	runCLIIntegrationCommand(t, []string{"init-repo", "--root", root})
	runCLIIntegrationCommand(t, []string{
		"create-cluster",
		"--root", root,
		"--cluster-id", cliIntegrationSourceClusterID,
		"--display-name", "integration SQL Server",
		"--listener", "127.0.0.1",
		"--port", "1433",
		"--secret-ref", "env://" + cliIntegrationSourceConnEnv,
		"--owner", "integration",
	})
	runCLIIntegrationCommand(t, []string{
		"create-project",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--project-id", cliIntegrationProjectID,
		"--display-name", "orders to TiDB integration",
		"--source-database", sourceDatabase,
		"--source-schema", "dbo",
		"--target-name", "integration-tidb",
		"--target-database", cliIntegrationTargetDatabaseName,
		"--target-secret-ref", "env://" + cliIntegrationTargetConnEnv,
		"--owner", "integration",
	})
	runCLIIntegrationCommand(t, []string{
		"discover-sqlserver",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--connection-string-env", cliIntegrationSourceConnEnv,
	})
	runCLIIntegrationCommand(t, []string{"analyze-compatibility", "--root", root, "--source-cluster-id", cliIntegrationSourceClusterID})
	runCLIIntegrationCommand(t, []string{"generate-schema-draft", "--root", root, "--source-cluster-id", cliIntegrationSourceClusterID, "--project-id", cliIntegrationProjectID})
	runCLIIntegrationCommand(t, []string{
		"generate-data-plans",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--project-id", cliIntegrationProjectID,
		"--object-uri-prefix", (&url.URL{Scheme: "file", Path: objectStoreDir}).String(),
	})
	runCLIIntegrationCommand(t, []string{"generate-validation-plan", "--root", root, "--source-cluster-id", cliIntegrationSourceClusterID, "--project-id", cliIntegrationProjectID})

	setCLIIntegrationSchemaDiffStatus(t, root, "reviewed")
	setCLIIntegrationPlanStatus(t, root, "export", "reviewed")
	setCLIIntegrationPlanStatus(t, root, "import", "reviewed")
	setCLIIntegrationPlanStatus(t, root, "validation", "reviewed")
	writeCLIIntegrationApproval(t, root, "ddl", mustCLIIntegrationPayloadHash(t, root, "ddl"))
	writeCLIIntegrationApproval(t, root, "export", mustCLIIntegrationPayloadHash(t, root, "export"))
	writeCLIIntegrationApproval(t, root, "import", mustCLIIntegrationPayloadHash(t, root, "import"))
	writeCLIIntegrationApproval(t, root, "validation", mustCLIIntegrationPayloadHash(t, root, "validation"))

	runCLIIntegrationCommand(t, []string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--project-id", cliIntegrationProjectID,
		"--stage", "ddl",
		"--executor-binary", executorBinary,
		"--target-connection-string-env", cliIntegrationTargetConnEnv,
		"--execute",
	})
	runCLIIntegrationCommand(t, []string{"worker-export", "--root", root, "--source-cluster-id", cliIntegrationSourceClusterID, "--project-id", cliIntegrationProjectID})
	runCLIIntegrationCommand(t, []string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--project-id", cliIntegrationProjectID,
		"--stage", "export",
		"--executor-binary", executorBinary,
		"--source-connection-string-env", cliIntegrationSourceConnEnv,
		"--execute",
	})
	runCLIIntegrationCommand(t, []string{"worker-import", "--root", root, "--source-cluster-id", cliIntegrationSourceClusterID, "--project-id", cliIntegrationProjectID})
	runCLIIntegrationCommand(t, []string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--project-id", cliIntegrationProjectID,
		"--stage", "import",
		"--executor-binary", executorBinary,
		"--target-connection-string-env", cliIntegrationTargetConnEnv,
		"--import-batch-size", "2",
		"--execute",
	})
	runCLIIntegrationCommand(t, []string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--project-id", cliIntegrationProjectID,
		"--stage", "validation",
		"--executor-binary", executorBinary,
		"--source-connection-string-env", cliIntegrationSourceConnEnv,
		"--target-connection-string-env", cliIntegrationTargetConnEnv,
		"--execute",
	})
	runCLIIntegrationCommand(t, []string{"worker-validate", "--root", root, "--source-cluster-id", cliIntegrationSourceClusterID, "--project-id", cliIntegrationProjectID})
	runCLIIntegrationCommand(t, []string{"validate-repo", "--root", root})

	assertCLIIntegrationFileContains(t, root, "clusters/"+cliIntegrationSourceClusterID+"/projects/"+cliIntegrationProjectID+"/evidence/executor-export-run.json", `"status": "succeeded"`)
	assertCLIIntegrationFileContains(t, root, "clusters/"+cliIntegrationSourceClusterID+"/projects/"+cliIntegrationProjectID+"/evidence/executor-import-run.json", `"status": "succeeded"`)
	assertCLIIntegrationFileContains(t, root, "clusters/"+cliIntegrationSourceClusterID+"/projects/"+cliIntegrationProjectID+"/evidence/executor-validation-run.json", `"status": "succeeded"`)
	assertCLIIntegrationTargetRows(t, ctx, targetDSN, 3)
}

func TestSQLServerToTiDBCDCSoakFlow(t *testing.T) {
	if strings.TrimSpace(os.Getenv(cliIntegrationRunCDCSoakEnv)) != "1" {
		t.Skipf("set %s=1 to run the CDC soak integration test", cliIntegrationRunCDCSoakEnv)
	}
	sourceAdminDSN, targetDSN := cliIntegrationDSNs(t)
	sourceDSN := cliIntegrationDSNWithDatabase(t, sourceAdminDSN, cliIntegrationCDCDatabaseName)
	t.Setenv(cliIntegrationSourceConnEnv, sourceDSN)
	t.Setenv(cliIntegrationTargetConnEnv, targetDSN)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	prepareCLIIntegrationCDCSource(t, ctx, sourceAdminDSN, sourceDSN)
	prepareCLIIntegrationTargetDatabase(t, ctx, targetDSN, cliIntegrationCDCDatabaseName)
	sourceDatabase := cliIntegrationCurrentDatabase(t, ctx, sourceDSN)

	root := t.TempDir()
	objectStoreDir := filepath.Join(root, "object-store", "cdc-full")
	executorBinary := writeCLIIntegrationExecutorHelper(t)

	runCLIIntegrationCommand(t, []string{"init-repo", "--root", root})
	runCLIIntegrationCommand(t, []string{
		"create-cluster",
		"--root", root,
		"--cluster-id", cliIntegrationSourceClusterID,
		"--display-name", "integration SQL Server CDC",
		"--listener", "127.0.0.1",
		"--port", "1433",
		"--secret-ref", "env://" + cliIntegrationSourceConnEnv,
		"--owner", "integration",
	})
	runCLIIntegrationCommand(t, []string{
		"create-project",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--project-id", cliIntegrationProjectID,
		"--display-name", "orders CDC soak to TiDB integration",
		"--source-database", sourceDatabase,
		"--source-schema", "dbo",
		"--target-name", "integration-tidb",
		"--target-database", cliIntegrationCDCDatabaseName,
		"--target-secret-ref", "env://" + cliIntegrationTargetConnEnv,
		"--owner", "integration",
	})
	runCLIIntegrationCommand(t, []string{
		"discover-sqlserver",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--connection-string-env", cliIntegrationSourceConnEnv,
	})
	runCLIIntegrationCommand(t, []string{"analyze-compatibility", "--root", root, "--source-cluster-id", cliIntegrationSourceClusterID})
	runCLIIntegrationCommand(t, []string{"generate-schema-draft", "--root", root, "--source-cluster-id", cliIntegrationSourceClusterID, "--project-id", cliIntegrationProjectID})
	runCLIIntegrationCommand(t, []string{
		"generate-data-plans",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--project-id", cliIntegrationProjectID,
		"--object-uri-prefix", (&url.URL{Scheme: "file", Path: objectStoreDir}).String(),
	})
	runCLIIntegrationCommand(t, []string{"generate-validation-plan", "--root", root, "--source-cluster-id", cliIntegrationSourceClusterID, "--project-id", cliIntegrationProjectID})
	runCLIIntegrationCommand(t, []string{"generate-cdc-plan", "--root", root, "--source-cluster-id", cliIntegrationSourceClusterID, "--project-id", cliIntegrationProjectID})

	setCLIIntegrationSchemaDiffStatus(t, root, "reviewed")
	setCLIIntegrationPlanStatus(t, root, "export", "reviewed")
	setCLIIntegrationPlanStatus(t, root, "import", "reviewed")
	setCLIIntegrationPlanStatus(t, root, "validation", "reviewed")
	writeCLIIntegrationApproval(t, root, "ddl", mustCLIIntegrationPayloadHash(t, root, "ddl"))
	writeCLIIntegrationApproval(t, root, "export", mustCLIIntegrationPayloadHash(t, root, "export"))
	writeCLIIntegrationApproval(t, root, "import", mustCLIIntegrationPayloadHash(t, root, "import"))
	writeCLIIntegrationApproval(t, root, "validation", mustCLIIntegrationPayloadHash(t, root, "validation"))

	runCLIIntegrationCommand(t, []string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--project-id", cliIntegrationProjectID,
		"--stage", "ddl",
		"--executor-binary", executorBinary,
		"--target-connection-string-env", cliIntegrationTargetConnEnv,
		"--execute",
	})
	runCLIIntegrationCommand(t, []string{"worker-export", "--root", root, "--source-cluster-id", cliIntegrationSourceClusterID, "--project-id", cliIntegrationProjectID})
	runCLIIntegrationCommand(t, []string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--project-id", cliIntegrationProjectID,
		"--stage", "export",
		"--executor-binary", executorBinary,
		"--source-connection-string-env", cliIntegrationSourceConnEnv,
		"--execute",
	})
	runCLIIntegrationCommand(t, []string{"worker-import", "--root", root, "--source-cluster-id", cliIntegrationSourceClusterID, "--project-id", cliIntegrationProjectID})
	runCLIIntegrationCommand(t, []string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--project-id", cliIntegrationProjectID,
		"--stage", "import",
		"--executor-binary", executorBinary,
		"--target-connection-string-env", cliIntegrationTargetConnEnv,
		"--import-batch-size", "2",
		"--execute",
	})

	sourceObject := cliIntegrationCDCDatabaseName + ".dbo.orders"
	targetObject := cliIntegrationCDCDatabaseName + ".orders"
	lastLSN := queryCLIIntegrationCDCMaxLSN(t, ctx, sourceDSN)
	writeCLIIntegrationCDCCheckpoint(t, root, sourceObject, targetObject, lastLSN)
	expectedRows := map[int]*string{
		1: stringPtr("alice"),
		2: stringPtr("bob"),
		3: nil,
	}

	iterations := cliIntegrationCDCSoakIterationCount(t)
	for iteration := 1; iteration <= iterations; iteration++ {
		applyCLIIntegrationCDCSourceMutation(t, ctx, sourceDSN, iteration, expectedRows)
		nextLSN := waitCLIIntegrationCDCChanges(t, ctx, sourceDSN, lastLSN, 1)
		runCLIIntegrationCommand(t, []string{
			"cdc-orchestrator",
			"--root", root,
			"--source-cluster-id", cliIntegrationSourceClusterID,
			"--project-id", cliIntegrationProjectID,
			"--executor-binary", executorBinary,
			"--source-connection-string-env", cliIntegrationSourceConnEnv,
			"--max-lsn", nextLSN,
			"--pr-draft",
			"--max-iterations", "1",
		})
		setCLIIntegrationPlanStatus(t, root, "cdc", "reviewed")
		writeCLIIntegrationApproval(t, root, "cdc", mustCLIIntegrationPayloadHash(t, root, "cdc"))
		runCLIIntegrationCommand(t, []string{
			"cdc-orchestrator",
			"--root", root,
			"--source-cluster-id", cliIntegrationSourceClusterID,
			"--project-id", cliIntegrationProjectID,
			"--executor-binary", executorBinary,
			"--source-connection-string-env", cliIntegrationSourceConnEnv,
			"--target-connection-string-env", cliIntegrationTargetConnEnv,
			"--max-lsn", nextLSN,
			"--apply-approved",
			"--min-applied-changes", "1",
			"--max-iterations", "1",
			"--command-timeout", "30s",
		})
		lastLSN = nextLSN
		assertCLIIntegrationTargetMatches(t, ctx, targetDSN, cliIntegrationCDCDatabaseName, expectedRows)
		assertCLIIntegrationFileContains(t, root, "clusters/"+cliIntegrationSourceClusterID+"/state/cdc-checkpoint.yaml", "to_lsn: "+lastLSN)
	}

	runCLIIntegrationCommand(t, []string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", cliIntegrationSourceClusterID,
		"--project-id", cliIntegrationProjectID,
		"--stage", "validation",
		"--executor-binary", executorBinary,
		"--source-connection-string-env", cliIntegrationSourceConnEnv,
		"--target-connection-string-env", cliIntegrationTargetConnEnv,
		"--execute",
	})
	runCLIIntegrationCommand(t, []string{"worker-validate", "--root", root, "--source-cluster-id", cliIntegrationSourceClusterID, "--project-id", cliIntegrationProjectID})
	runCLIIntegrationCommand(t, []string{"validate-repo", "--root", root})

	assertCLIIntegrationFileContains(t, root, "clusters/"+cliIntegrationSourceClusterID+"/projects/"+cliIntegrationProjectID+"/evidence/executor-cdc-run.json", `"status": "succeeded"`)
	assertCLIIntegrationFileContains(t, root, "clusters/"+cliIntegrationSourceClusterID+"/projects/"+cliIntegrationProjectID+"/evidence/executor-cdc-run.json", `"cdc_applied_changes"`)
}

func cliIntegrationDSNs(t *testing.T) (string, string) {
	t.Helper()
	sourceDSN := strings.TrimSpace(os.Getenv(cliIntegrationSourceDSNEnv))
	targetDSN := strings.TrimSpace(os.Getenv(cliIntegrationTargetDSNEnv))
	if sourceDSN == "" || targetDSN == "" {
		t.Skipf("set %s and %s to run CLI integration tests", cliIntegrationSourceDSNEnv, cliIntegrationTargetDSNEnv)
	}
	return sourceDSN, targetDSN
}

func prepareCLIIntegrationSource(t *testing.T, ctx context.Context, sourceDSN string) {
	t.Helper()
	db, err := sql.Open("sqlserver", sourceDSN)
	if err != nil {
		t.Fatalf("open SQL Server source: %v", err)
	}
	defer db.Close()
	statements := []string{
		"IF OBJECT_ID('dbo.orders', 'U') IS NOT NULL DROP TABLE dbo.orders",
		"CREATE TABLE dbo.orders (id INT NOT NULL PRIMARY KEY, customer_name NVARCHAR(100) NULL)",
		"INSERT INTO dbo.orders (id, customer_name) VALUES (1, N'alice'), (2, N'bob'), (3, NULL)",
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare SQL Server source statement %q: %v", statement, err)
		}
	}
}

func prepareCLIIntegrationTarget(t *testing.T, ctx context.Context, targetDSN string) {
	t.Helper()
	prepareCLIIntegrationTargetDatabase(t, ctx, targetDSN, cliIntegrationTargetDatabaseName)
}

func prepareCLIIntegrationTargetDatabase(t *testing.T, ctx context.Context, targetDSN, databaseName string) {
	t.Helper()
	db, err := sql.Open("mysql", targetDSN)
	if err != nil {
		t.Fatalf("open TiDB target: %v", err)
	}
	defer db.Close()
	statements := []string{
		"DROP DATABASE IF EXISTS " + quoteMySQLIdentifierForIntegration(databaseName),
		"CREATE DATABASE " + quoteMySQLIdentifierForIntegration(databaseName),
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare TiDB target statement %q: %v", statement, err)
		}
	}
}

func prepareCLIIntegrationCDCSource(t *testing.T, ctx context.Context, adminDSN, sourceDSN string) {
	t.Helper()
	adminDB, err := sql.Open("sqlserver", adminDSN)
	if err != nil {
		t.Fatalf("open SQL Server admin source: %v", err)
	}
	defer adminDB.Close()
	if _, err := adminDB.ExecContext(ctx, "IF DB_ID(N'"+cliIntegrationCDCDatabaseName+"') IS NULL CREATE DATABASE "+quoteSQLServerIdentifierForIntegration(cliIntegrationCDCDatabaseName)); err != nil {
		t.Fatalf("create SQL Server CDC database: %v", err)
	}

	db, err := sql.Open("sqlserver", sourceDSN)
	if err != nil {
		t.Fatalf("open SQL Server CDC source: %v", err)
	}
	defer db.Close()
	statements := []string{
		"IF EXISTS (SELECT 1 FROM sys.databases WHERE name = DB_NAME() AND is_cdc_enabled = 0) EXEC sys.sp_cdc_enable_db",
		"IF OBJECT_ID('dbo.orders', 'U') IS NOT NULL BEGIN IF EXISTS (SELECT 1 FROM sys.tables WHERE object_id = OBJECT_ID('dbo.orders') AND is_tracked_by_cdc = 1) EXEC sys.sp_cdc_disable_table @source_schema=N'dbo', @source_name=N'orders', @capture_instance=N'dbo_orders'; DROP TABLE dbo.orders; END",
		"CREATE TABLE dbo.orders (id INT NOT NULL PRIMARY KEY, customer_name NVARCHAR(100) NULL)",
		"INSERT INTO dbo.orders (id, customer_name) VALUES (1, N'alice'), (2, N'bob'), (3, NULL)",
		"EXEC sys.sp_cdc_enable_table @source_schema=N'dbo', @source_name=N'orders', @role_name=NULL, @supports_net_changes=0",
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare SQL Server CDC source statement %q: %v", statement, err)
		}
	}
}

func cliIntegrationCurrentDatabase(t *testing.T, ctx context.Context, sourceDSN string) string {
	t.Helper()
	db, err := sql.Open("sqlserver", sourceDSN)
	if err != nil {
		t.Fatalf("open SQL Server source: %v", err)
	}
	defer db.Close()
	var database string
	if err := db.QueryRowContext(ctx, "SELECT DB_NAME()").Scan(&database); err != nil {
		t.Fatalf("query current SQL Server database: %v", err)
	}
	return database
}

func writeCLIIntegrationExecutorHelper(t *testing.T) string {
	t.Helper()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	path := filepath.Join(t.TempDir(), "sqlserver2tidb-executor-helper")
	content := fmt.Sprintf("#!/usr/bin/env bash\nset -euo pipefail\n%s=1 exec %q -- \"$@\"\n", cliIntegrationExecutorHelperEnv, testBinary)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executor helper: %v", err)
	}
	return path
}

func cliIntegrationDSNWithDatabase(t *testing.T, rawDSN, databaseName string) string {
	t.Helper()
	parsed, err := url.Parse(rawDSN)
	if err == nil && parsed.Scheme == "sqlserver" {
		query := parsed.Query()
		query.Set("database", databaseName)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	if strings.Contains(strings.ToLower(rawDSN), "database=") {
		parts := strings.Split(rawDSN, ";")
		for i, part := range parts {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(part)), "database=") {
				parts[i] = "database=" + databaseName
			}
		}
		return strings.Join(parts, ";")
	}
	separator := ";"
	if strings.HasSuffix(rawDSN, ";") {
		separator = ""
	}
	return rawDSN + separator + "database=" + databaseName
}

func cliIntegrationCDCSoakIterationCount(t *testing.T) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(cliIntegrationCDCSoakIterations))
	if raw == "" {
		return 3
	}
	var value int
	if _, err := fmt.Sscanf(raw, "%d", &value); err != nil || value <= 0 {
		t.Fatalf("%s must be a positive integer, got %q", cliIntegrationCDCSoakIterations, raw)
	}
	return value
}

func mustCLIIntegrationPayloadHash(t *testing.T, root, stage string) string {
	t.Helper()
	hash, err := gitops.ComputePayloadHashForStage(root, cliIntegrationSourceClusterID, cliIntegrationProjectID, stage)
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(%s) error = %v", stage, err)
	}
	return hash
}

func setCLIIntegrationSchemaDiffStatus(t *testing.T, root, status string) {
	t.Helper()
	rel := filepath.Join("clusters", cliIntegrationSourceClusterID, "projects", cliIntegrationProjectID, "schema", "schema-diff.json")
	data := readCLIIntegrationFile(t, root, rel)
	updated := strings.Replace(data, `"status": "draft-generated"`, `"status": "`+status+`"`, 1)
	if updated == data {
		t.Fatalf("schema diff %s does not contain draft-generated status", rel)
	}
	writeCLIIntegrationFile(t, root, rel, updated)
}

func setCLIIntegrationPlanStatus(t *testing.T, root, stage, status string) {
	t.Helper()
	rel := filepath.Join("clusters", cliIntegrationSourceClusterID, "projects", cliIntegrationProjectID, "plan", stage+"-plan.yaml")
	data := readCLIIntegrationFile(t, root, rel)
	updated := strings.Replace(data, "status: draft", "status: "+status, 1)
	if updated == data {
		t.Fatalf("plan %s does not contain draft status", rel)
	}
	writeCLIIntegrationFile(t, root, rel, updated)
}

func writeCLIIntegrationApproval(t *testing.T, root, stage, payloadHash string) {
	t.Helper()
	rel := filepath.Join("clusters", cliIntegrationSourceClusterID, "projects", cliIntegrationProjectID, "approvals", stage+"-approval.yaml")
	body := fmt.Sprintf(`approval_id: %s-integration
project_id: %s
source_cluster_id: %s
action: %s
payload_hash: %s
required_reviewers:
  - integration
approved_by:
  - integration
status: approved
approved_at: "2026-06-29T00:00:00Z"
`, stage, cliIntegrationProjectID, cliIntegrationSourceClusterID, stage, payloadHash)
	writeCLIIntegrationFile(t, root, rel, body)
}

func assertCLIIntegrationTargetRows(t *testing.T, ctx context.Context, targetDSN string, want int) {
	t.Helper()
	db, err := sql.Open("mysql", targetDSN)
	if err != nil {
		t.Fatalf("open TiDB target: %v", err)
	}
	defer db.Close()
	var got int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+cliIntegrationTargetDatabaseName+".orders").Scan(&got); err != nil {
		t.Fatalf("query TiDB row count: %v", err)
	}
	if got != want {
		t.Fatalf("target row count = %d, want %d", got, want)
	}
}

func writeCLIIntegrationCDCCheckpoint(t *testing.T, root, sourceObject, targetObject, lsn string) {
	t.Helper()
	body := fmt.Sprintf(`source_cluster_id: %s
phase: cdc
status: running
project_id: %s
mode: sqlserver-cdc
checkpoint_scope: source-cluster
checkpoints:
  - source_object: %s
    target_object: %s
    from_lsn: %s
    to_lsn: %s
    applied_changes: 0
    completed_at: "2026-06-29T00:00:00Z"
updated_at: "2026-06-29T00:00:00Z"
`, cliIntegrationSourceClusterID, cliIntegrationProjectID, sourceObject, targetObject, lsn, lsn)
	writeCLIIntegrationFile(t, root, "clusters/"+cliIntegrationSourceClusterID+"/state/cdc-checkpoint.yaml", body)
}

func applyCLIIntegrationCDCSourceMutation(t *testing.T, ctx context.Context, sourceDSN string, iteration int, expectedRows map[int]*string) {
	t.Helper()
	db, err := sql.Open("sqlserver", sourceDSN)
	if err != nil {
		t.Fatalf("open SQL Server CDC source: %v", err)
	}
	defer db.Close()
	newID := 100 + iteration
	newName := fmt.Sprintf("new-%d", iteration)
	updatedName := fmt.Sprintf("alice-%d", iteration)
	statements := []string{
		fmt.Sprintf("INSERT INTO dbo.orders (id, customer_name) VALUES (%d, N'%s')", newID, newName),
		fmt.Sprintf("UPDATE dbo.orders SET customer_name = N'%s' WHERE id = 1", updatedName),
	}
	if iteration == 1 {
		statements = append(statements, "DELETE FROM dbo.orders WHERE id = 2")
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("apply CDC source mutation statement %q: %v", statement, err)
		}
	}
	expectedRows[newID] = stringPtr(newName)
	expectedRows[1] = stringPtr(updatedName)
	if iteration == 1 {
		delete(expectedRows, 2)
	}
}

func waitCLIIntegrationCDCChanges(t *testing.T, ctx context.Context, sourceDSN, fromLSN string, minChanges int) string {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	fromBytes := mustCLIIntegrationLSNBytes(t, fromLSN)
	for time.Now().Before(deadline) {
		toLSN := queryCLIIntegrationCDCMaxLSN(t, ctx, sourceDSN)
		if compareCLIIntegrationLSN(t, fromLSN, toLSN) < 0 {
			changeCount := queryCLIIntegrationCDCChangeCount(t, ctx, sourceDSN, fromBytes, mustCLIIntegrationLSNBytes(t, toLSN))
			if changeCount >= minChanges {
				return toLSN
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("timed out waiting for at least %d CDC changes after %s", minChanges, fromLSN)
	return ""
}

func queryCLIIntegrationCDCMaxLSN(t *testing.T, ctx context.Context, sourceDSN string) string {
	t.Helper()
	db, err := sql.Open("sqlserver", sourceDSN)
	if err != nil {
		t.Fatalf("open SQL Server CDC source: %v", err)
	}
	defer db.Close()
	var maxLSN []byte
	if err := db.QueryRowContext(ctx, "SELECT sys.fn_cdc_get_max_lsn()").Scan(&maxLSN); err != nil {
		t.Fatalf("query SQL Server CDC max LSN: %v", err)
	}
	if len(maxLSN) != 10 {
		t.Fatalf("SQL Server CDC max LSN length = %d, want 10", len(maxLSN))
	}
	return "0x" + hex.EncodeToString(maxLSN)
}

func queryCLIIntegrationCDCChangeCount(t *testing.T, ctx context.Context, sourceDSN string, fromLSN, toLSN []byte) int {
	t.Helper()
	db, err := sql.Open("sqlserver", sourceDSN)
	if err != nil {
		t.Fatalf("open SQL Server CDC source: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM cdc.fn_cdc_get_all_changes_dbo_orders(@from_lsn, @to_lsn, 'all update old')", sql.Named("from_lsn", fromLSN), sql.Named("to_lsn", toLSN)).Scan(&count); err != nil {
		t.Fatalf("query SQL Server CDC change count: %v", err)
	}
	return count
}

func assertCLIIntegrationTargetMatches(t *testing.T, ctx context.Context, targetDSN, databaseName string, expected map[int]*string) {
	t.Helper()
	db, err := sql.Open("mysql", targetDSN)
	if err != nil {
		t.Fatalf("open TiDB target: %v", err)
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT id, customer_name FROM "+quoteMySQLIdentifierForIntegration(databaseName)+".orders ORDER BY id")
	if err != nil {
		t.Fatalf("query TiDB target rows: %v", err)
	}
	defer rows.Close()
	got := map[int]*string{}
	for rows.Next() {
		var id int
		var name sql.NullString
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("scan TiDB target row: %v", err)
		}
		if name.Valid {
			value := name.String
			got[id] = &value
		} else {
			got[id] = nil
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate TiDB target rows: %v", err)
	}
	if !integrationRowsEqual(got, expected) {
		t.Fatalf("target rows = %s, want %s", formatIntegrationRows(got), formatIntegrationRows(expected))
	}
}

func runCLIIntegrationCommand(t *testing.T, args []string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := Run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("Run(%v) code = %d\nstdout:\n%s\nstderr:\n%s", args, code, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func mustCLIIntegrationLSNBytes(t *testing.T, raw string) []byte {
	t.Helper()
	trimmed := strings.TrimPrefix(strings.TrimSpace(raw), "0x")
	value, err := hex.DecodeString(trimmed)
	if err != nil {
		t.Fatalf("decode LSN %q: %v", raw, err)
	}
	if len(value) != 10 {
		t.Fatalf("LSN %q length = %d, want 10 bytes", raw, len(value))
	}
	return value
}

func compareCLIIntegrationLSN(t *testing.T, left, right string) int {
	t.Helper()
	leftBytes := mustCLIIntegrationLSNBytes(t, left)
	rightBytes := mustCLIIntegrationLSNBytes(t, right)
	return bytes.Compare(leftBytes, rightBytes)
}

func quoteSQLServerIdentifierForIntegration(identifier string) string {
	return "[" + strings.ReplaceAll(identifier, "]", "]]") + "]"
}

func quoteMySQLIdentifierForIntegration(identifier string) string {
	return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
}

func stringPtr(value string) *string {
	return &value
}

func integrationRowsEqual(left, right map[int]*string) bool {
	if len(left) != len(right) {
		return false
	}
	for id, leftValue := range left {
		rightValue, ok := right[id]
		if !ok {
			return false
		}
		switch {
		case leftValue == nil && rightValue == nil:
		case leftValue != nil && rightValue != nil && *leftValue == *rightValue:
		default:
			return false
		}
	}
	return true
}

func formatIntegrationRows(rows map[int]*string) string {
	ids := make([]int, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		value := "NULL"
		if rows[id] != nil {
			value = *rows[id]
		}
		parts = append(parts, fmt.Sprintf("%d=%s", id, value))
	}
	return strings.Join(parts, ",")
}

func readCLIIntegrationFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func writeCLIIntegrationFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func assertCLIIntegrationFileContains(t *testing.T, root, rel, want string) {
	t.Helper()
	if got := readCLIIntegrationFile(t, root, rel); !strings.Contains(got, want) {
		t.Fatalf("%s does not contain %q:\n%s", rel, want, got)
	}
}
