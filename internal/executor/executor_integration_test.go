//go:build integration

package executor

import (
	"bytes"
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	integrationSourceDSNEnv = "SQLSERVER2TIDB_INTEGRATION_SOURCE_DSN"
	integrationTargetDSNEnv = "SQLSERVER2TIDB_INTEGRATION_TARGET_DSN"
)

func TestIntegrationDependenciesAreReady(t *testing.T) {
	sourceDSN, targetDSN := integrationDSNs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pingIntegrationDB(t, ctx, "sqlserver", sourceDSN)
	pingIntegrationDB(t, ctx, "mysql", targetDSN)
}

func TestSQLServerToTiDBFullLoadExecutorFlow(t *testing.T) {
	sourceDSN, targetDSN := integrationDSNs(t)
	t.Setenv(defaultSourceConnectionStringEnv, sourceDSN)
	t.Setenv(defaultTargetConnectionStringEnv, targetDSN)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	prepareIntegrationSource(t, ctx, sourceDSN)
	prepareIntegrationTarget(t, ctx, targetDSN)

	csvPath := filepath.Join(t.TempDir(), "orders.csv")
	csvURI := (&url.URL{Scheme: "file", Path: csvPath}).String()
	runExecutorIntegrationCommand(t, []string{
		"export",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "integration-sqlserver",
		"--project-id", "orders-to-tidb",
		"--chunk-id", "dbo.orders.000001",
		"--source-object", "dbo.orders",
		"--target-object", "s2t_it.orders",
		"--output-uri", csvURI,
	})
	if info, err := os.Stat(csvPath); err != nil || info.Size() == 0 {
		t.Fatalf("export CSV %s stat error = %v, info = %+v", csvPath, err, info)
	}

	runExecutorIntegrationCommand(t, []string{
		"import",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "integration-sqlserver",
		"--project-id", "orders-to-tidb",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "s2t_it.orders",
		"--source-uri", csvURI,
		"--depends-on-export-chunk", "dbo.orders.000001",
		"--import-batch-size", "2",
	})
	runExecutorIntegrationCommand(t, []string{
		"validate-count",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "integration-sqlserver",
		"--project-id", "orders-to-tidb",
		"--source-object", "dbo.orders",
		"--target-object", "s2t_it.orders",
	})
}

func integrationDSNs(t *testing.T) (string, string) {
	t.Helper()
	sourceDSN := strings.TrimSpace(os.Getenv(integrationSourceDSNEnv))
	targetDSN := strings.TrimSpace(os.Getenv(integrationTargetDSNEnv))
	if sourceDSN == "" || targetDSN == "" {
		t.Skipf("set %s and %s to run integration tests", integrationSourceDSNEnv, integrationTargetDSNEnv)
	}
	return sourceDSN, targetDSN
}

func pingIntegrationDB(t *testing.T, ctx context.Context, driverName, dsn string) {
	t.Helper()
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		t.Fatalf("open %s integration DB: %v", driverName, err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping %s integration DB: %v", driverName, err)
	}
}

func prepareIntegrationSource(t *testing.T, ctx context.Context, sourceDSN string) {
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

func prepareIntegrationTarget(t *testing.T, ctx context.Context, targetDSN string) {
	t.Helper()
	db, err := sql.Open("mysql", targetDSN)
	if err != nil {
		t.Fatalf("open TiDB target: %v", err)
	}
	defer db.Close()
	statements := []string{
		"CREATE DATABASE IF NOT EXISTS s2t_it",
		"DROP TABLE IF EXISTS s2t_it.orders",
		"CREATE TABLE s2t_it.orders (id INT NOT NULL PRIMARY KEY, customer_name VARCHAR(100) NULL)",
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare TiDB target statement %q: %v", statement, err)
		}
	}
}

func runExecutorIntegrationCommand(t *testing.T, args []string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := Run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("Run(%v) code = %d\nstdout:\n%s\nstderr:\n%s", args, code, stdout.String(), stderr.String())
	}
}
