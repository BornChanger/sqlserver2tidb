package executor

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const nonEmptyTargetTestDriverName = "sqlserver2tidb_non_empty_target_test"

var registerNonEmptyTargetTestDriverOnce sync.Once

type nonEmptyTargetTestDriver struct{}

func (driver nonEmptyTargetTestDriver) Open(name string) (driver.Conn, error) {
	return nonEmptyTargetTestConn{}, nil
}

type nonEmptyTargetTestConn struct{}

func (conn nonEmptyTargetTestConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}

func (conn nonEmptyTargetTestConn) Close() error {
	return nil
}

func (conn nonEmptyTargetTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected begin")
}

func (conn nonEmptyTargetTestConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return &singleCountTestRows{count: 1}, nil
}

type singleCountTestRows struct {
	count int64
	read  bool
}

func (rows *singleCountTestRows) Columns() []string {
	return []string{"COUNT(*)"}
}

func (rows *singleCountTestRows) Close() error {
	return nil
}

func (rows *singleCountTestRows) Next(dest []driver.Value) error {
	if rows.read {
		return io.EOF
	}
	rows.read = true
	dest[0] = rows.count
	return nil
}

func registerNonEmptyTargetTestDriver() {
	registerNonEmptyTargetTestDriverOnce.Do(func() {
		sql.Register(nonEmptyTargetTestDriverName, nonEmptyTargetTestDriver{})
	})
}

type recordingCDCExecutor struct {
	called bool
	query  string
	args   []any
}

func (exec *recordingCDCExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	exec.called = true
	exec.query = query
	exec.args = append([]any(nil), args...)
	return nil, nil
}

type recordingCDCSource struct {
	query   string
	fromLSN []byte
	toLSN   []byte
	rows    exportRows
}

func (source *recordingCDCSource) QueryCDCChanges(_ context.Context, query string, fromLSN, toLSN []byte) (exportRows, error) {
	source.query = query
	source.fromLSN = append([]byte(nil), fromLSN...)
	source.toLSN = append([]byte(nil), toLSN...)
	return source.rows, nil
}

type recordingMultiCDCExecutor struct {
	calls []recordedCDCCall
}

type recordedCDCCall struct {
	query string
	args  []any
}

func (exec *recordingMultiCDCExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	exec.calls = append(exec.calls, recordedCDCCall{
		query: query,
		args:  append([]any(nil), args...),
	})
	return nil, nil
}

func TestRunVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("version code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "sqlserver2tidb-executor version dev")
	assertOutputContains(t, output, "commit: unknown")
}

func TestRunExportDryRunCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"export",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--chunk-id", "dbo.orders.000001",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--output-uri", "https://object-store.example/migration/prod/full/dbo.orders.000001.csv",
		"--predicate", "id >= 1 AND id < 1000",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("export code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor export dry run")
	assertOutputContains(t, output, "source cluster: prod-sqlserver-a")
	assertOutputContains(t, output, "project: sales-db-to-tidb-prod-a")
	assertOutputContains(t, output, "chunk id: dbo.orders.000001")
	assertOutputContains(t, output, "source object: sales.dbo.orders")
	assertOutputContains(t, output, "target object: app.orders")
	assertOutputContains(t, output, "output uri: https://object-store.example/migration/prod/full/dbo.orders.000001.csv")
	assertOutputContains(t, output, "predicate: id >= 1 AND id < 1000")
	assertOutputContains(t, output, "No SQL Server connection will be opened.")
	assertOutputContains(t, output, "No CSV output write will be attempted.")
}

func TestRunImportDryRunCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "https://object-store.example/migration/prod/full/dbo.orders.000001.csv",
		"--depends-on-export-chunk", "dbo.orders.000001",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("import code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor import dry run")
	assertOutputContains(t, output, "job id: import-dbo.orders.000001")
	assertOutputContains(t, output, "target object: app.orders")
	assertOutputContains(t, output, "source uri: https://object-store.example/migration/prod/full/dbo.orders.000001.csv")
	assertOutputContains(t, output, "depends on export chunk: dbo.orders.000001")
	assertOutputContains(t, output, "No TiDB connection will be opened.")
}

func TestRunApplyDDLDryRunCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"apply-ddl",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--ddl-file", "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/tidb-ddl/dbo.orders.sql",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("apply-ddl code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor apply-ddl dry run")
	assertOutputContains(t, output, "source cluster: prod-sqlserver-a")
	assertOutputContains(t, output, "project: sales-db-to-tidb-prod-a")
	assertOutputContains(t, output, "ddl file: clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/tidb-ddl/dbo.orders.sql")
	assertOutputContains(t, output, "No TiDB connection will be opened.")
}

func TestRunApplyDDLExecuteRejectsTODODDL(t *testing.T) {
	root := t.TempDir()
	ddlFile := filepath.Join(root, "ddl.sql")
	if err := os.WriteFile(ddlFile, []byte("CREATE TABLE t (c TEXT /* TODO: review */);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"apply-ddl",
		"--execute",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--ddl-file", ddlFile,
		"--target-connection-string-env", "MISSING_TIDB_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("apply-ddl execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor apply-ddl: DDL file still contains TODO")
}

func TestRunApplyDDLExecuteRequiresConnectionStringEnv(t *testing.T) {
	root := t.TempDir()
	ddlFile := filepath.Join(root, "ddl.sql")
	if err := os.WriteFile(ddlFile, []byte("CREATE TABLE IF NOT EXISTS `app`.`orders` (`id` INT);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"apply-ddl",
		"--execute",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--ddl-file", ddlFile,
		"--target-connection-string-env", "MISSING_TIDB_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("apply-ddl execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor apply-ddl: target connection string env MISSING_TIDB_DSN is not set")
}

func TestSplitSQLStatementsIgnoresSemicolonsInLiteralsAndComments(t *testing.T) {
	script := `CREATE TABLE one (note VARCHAR(32) DEFAULT 'a;b');
-- comment with ; semicolon
CREATE TABLE two (note VARCHAR(32) DEFAULT "c;d");
/* block comment ; semicolon */
CREATE TABLE three (note VARCHAR(32) DEFAULT 'it''s; ok');
`
	statements := splitSQLStatements(script)
	if len(statements) != 3 {
		t.Fatalf("splitSQLStatements() returned %d statements, want 3: %#v", len(statements), statements)
	}
	assertOutputContains(t, statements[0], "'a;b'")
	assertOutputContains(t, statements[1], "-- comment with ; semicolon")
	assertOutputContains(t, statements[1], `"c;d"`)
	assertOutputContains(t, statements[2], "/* block comment ; semicolon */")
	assertOutputContains(t, statements[2], "'it''s; ok'")
}

func TestRunImportExecuteRejectsNonFileSourceURI(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "s3://migration/prod/full/dbo.orders.000001.csv",
		"--target-connection-string-env", "MISSING_TIDB_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("import execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor import: only file://, http://, and https:// source URIs are supported for --execute")
}

func TestRunImportExecuteRequiresConnectionStringEnv(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "file:///tmp/dbo.orders.000001.csv",
		"--target-connection-string-env", "MISSING_TIDB_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("import execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor import: target connection string env MISSING_TIDB_DSN is not set")
}

func TestRunImportExecuteRequiresPositiveImportBatchSize(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "file:///tmp/dbo.orders.000001.csv",
		"--target-connection-string-env", "MISSING_TIDB_DSN",
		"--import-batch-size", "0",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("import execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor import: import batch size must be positive")
}

func TestRunImportDryRunIncludesImportEngine(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "file:///tmp/dbo.orders.000001.csv",
		"--engine", "tidb-import-into",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("import dry-run code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor import dry run")
	assertOutputContains(t, output, "engine: tidb-import-into")
}

func TestRunImportDryRunIncludesFields(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "s3://migration/prod/full/dbo.orders.000001.csv",
		"--engine", "tidb-import-into",
		"--fields", "id,name,@sqlserver2tidb_null_bitmap",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("import dry-run code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor import dry run")
	assertOutputContains(t, output, "fields: id,name,@sqlserver2tidb_null_bitmap")
}

func TestRunImportRejectsFieldsForSQLInsert(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "file:///tmp/dbo.orders.000001.csv",
		"--fields", "id,name",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("import dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor import: fields is only supported with tidb-import-into")
}

func TestExecuteTiDBImportValidatesBatchSizeBeforeSourceURI(t *testing.T) {
	err := executeTiDBImport(context.Background(), importExecuteSpec{
		TargetObject:              "app.orders",
		SourceURI:                 "s3://migration/prod/full/dbo.orders.000001.csv",
		TargetConnectionStringEnv: "MISSING_TIDB_DSN",
		ImportBatchSize:           0,
	})
	if err == nil {
		t.Fatal("executeTiDBImport() error = nil, want import batch size error")
	}
	assertOutputContains(t, err.Error(), "executor import: import batch size must be positive")
}

func TestRunImportExecuteRequireEmptyTargetRejectsNonEmptyTargetBeforeOpeningSource(t *testing.T) {
	registerNonEmptyTargetTestDriver()
	oldOpenMySQLDB := openMySQLDB
	openMySQLDB = func(_ string) (*sql.DB, error) {
		return sql.Open(nonEmptyTargetTestDriverName, "non-empty-target")
	}
	defer func() {
		openMySQLDB = oldOpenMySQLDB
	}()
	t.Setenv("TEST_TIDB_DSN", "non-empty-target")
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "file:///definitely/missing.csv",
		"--target-connection-string-env", "TEST_TIDB_DSN",
		"--require-empty-target",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("import execute code = 0, want non-empty target preflight error")
	}
	assertOutputContains(t, stderr.String(), "preflight target table is not empty: app.orders has 1 rows")
	if strings.Contains(stderr.String(), "open CSV source") {
		t.Fatalf("import execute stderr = %q, want target preflight before source open", stderr.String())
	}
}

func TestRunImportExecuteDoesNotRequireEmptyTargetByDefault(t *testing.T) {
	registerNonEmptyTargetTestDriver()
	oldOpenMySQLDB := openMySQLDB
	openMySQLDB = func(_ string) (*sql.DB, error) {
		return sql.Open(nonEmptyTargetTestDriverName, "non-empty-target")
	}
	defer func() {
		openMySQLDB = oldOpenMySQLDB
	}()
	t.Setenv("TEST_TIDB_DSN", "non-empty-target")
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000002",
		"--target-object", "app.orders",
		"--source-uri", "file:///definitely/missing.csv",
		"--target-connection-string-env", "TEST_TIDB_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("import execute code = 0, want missing source error")
	}
	assertOutputContains(t, stderr.String(), "open CSV source")
	if strings.Contains(stderr.String(), "preflight target table is not empty") {
		t.Fatalf("import execute stderr = %q, should not require empty target by default", stderr.String())
	}
}

func TestRunValidateCountDryRunCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"validate-count",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--predicate", "id >= 1",
		"--target-predicate", "id >= 1",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("validate-count code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor validate-count dry run")
	assertOutputContains(t, output, "source object: sales.dbo.orders")
	assertOutputContains(t, output, "target object: app.orders")
	assertOutputContains(t, output, "predicate: id >= 1")
	assertOutputContains(t, output, "target predicate: id >= 1")
	assertOutputContains(t, output, "No SQL Server connection will be opened.")
	assertOutputContains(t, output, "No TiDB connection will be opened.")
}

func TestRunValidateQueryDryRunCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"validate-query",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--check-id", "orders-total",
		"--source-sql", "SELECT SUM(total) FROM sales.dbo.orders",
		"--target-sql", "SELECT SUM(total) FROM app.orders",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("validate-query code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor validate-query dry run")
	assertOutputContains(t, output, "check id: orders-total")
	assertOutputContains(t, output, "source sql: SELECT SUM(total) FROM sales.dbo.orders")
	assertOutputContains(t, output, "target sql: SELECT SUM(total) FROM app.orders")
	assertOutputContains(t, output, "No SQL Server connection will be opened.")
	assertOutputContains(t, output, "No TiDB connection will be opened.")
}

func TestRenderValidateQueryMatchedIncludesCheckID(t *testing.T) {
	output := renderValidateQueryMatched("orders-total", validateQueryResult{
		SourceValue: "42",
		TargetValue: "42",
	})
	assertOutputContains(t, output, "executor validate-query matched")
	assertOutputContains(t, output, "check-id=orders-total")
	assertOutputContains(t, output, "source=42")
	assertOutputContains(t, output, "target=42")
}

func TestRunValidateQueryExecuteFailureIncludesCheckID(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"validate-query",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--check-id", "orders-total",
		"--source-sql", "TODO: choose source SQL",
		"--target-sql", "SELECT SUM(total) FROM app.orders",
		"--source-connection-string-env", "MISSING_SQLSERVER_DSN",
		"--target-connection-string-env", "MISSING_TIDB_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("validate-query execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "check-id=orders-total")
	assertOutputContains(t, stderr.String(), "source_sql still contains TODO")
}

func TestRunValidateCountExecuteRejectsTODOPredicate(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"validate-count",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--predicate", "TODO: choose predicate",
		"--source-connection-string-env", "MISSING_SQLSERVER_DSN",
		"--target-connection-string-env", "MISSING_TIDB_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("validate-count execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor validate-count: predicate still contains TODO")
}

func TestRunValidateCountExecuteRejectsTODOTargetPredicate(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"validate-count",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--target-predicate", "TODO: choose target predicate",
		"--source-connection-string-env", "MISSING_SQLSERVER_DSN",
		"--target-connection-string-env", "MISSING_TIDB_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("validate-count execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor validate-count: target predicate still contains TODO")
}

func TestRunValidateCountExecuteRequiresSourceConnectionStringEnv(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"validate-count",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--source-connection-string-env", "MISSING_SQLSERVER_DSN",
		"--target-connection-string-env", "MISSING_TIDB_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("validate-count execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor validate-count: source connection string env MISSING_SQLSERVER_DSN is not set")
}

func TestRunValidateCountExecuteRequiresTargetConnectionStringEnv(t *testing.T) {
	t.Setenv("SQLSERVER2TIDB_TEST_SOURCE_DSN", "sqlserver://readonly:secret@localhost?database=sales")
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"validate-count",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--source-connection-string-env", "SQLSERVER2TIDB_TEST_SOURCE_DSN",
		"--target-connection-string-env", "MISSING_TIDB_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("validate-count execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor validate-count: target connection string env MISSING_TIDB_DSN is not set")
}

func TestBuildCountQueriesQuoteObjects(t *testing.T) {
	sourceQuery, err := buildSQLServerCountQuery("sales.dbo.orders", "id >= 1")
	if err != nil {
		t.Fatalf("buildSQLServerCountQuery() error = %v", err)
	}
	wantSource := "SELECT COUNT(*) FROM [sales].[dbo].[orders] WHERE id >= 1"
	if sourceQuery != wantSource {
		t.Fatalf("buildSQLServerCountQuery() = %q, want %q", sourceQuery, wantSource)
	}

	targetQuery, err := buildTiDBCountQuery("app.orders", "id >= 1")
	if err != nil {
		t.Fatalf("buildTiDBCountQuery() error = %v", err)
	}
	wantTarget := "SELECT COUNT(*) FROM `app`.`orders` WHERE id >= 1"
	if targetQuery != wantTarget {
		t.Fatalf("buildTiDBCountQuery() = %q, want %q", targetQuery, wantTarget)
	}
}

func TestBuildTiDBInsertStatementQuotesObjectAndColumns(t *testing.T) {
	stmt, err := buildTiDBInsertStatement("app.orders", []string{"id", "order`name"})
	if err != nil {
		t.Fatalf("buildTiDBInsertStatement() error = %v", err)
	}

	want := "INSERT INTO `app`.`orders` (`id`, `order``name`) VALUES (?, ?)"
	if stmt != want {
		t.Fatalf("buildTiDBInsertStatement() = %q, want %q", stmt, want)
	}
}

func TestBuildTiDBImportIntoStatementQuotesObjectAndUsesCSVHeaderSkip(t *testing.T) {
	stmt, err := buildTiDBImportIntoStatement("app.orders", "file:///tmp/dbo.orders.000001.csv")
	if err != nil {
		t.Fatalf("buildTiDBImportIntoStatement() error = %v", err)
	}

	want := "IMPORT INTO `app`.`orders` FROM '/tmp/dbo.orders.000001.csv' FORMAT 'csv' WITH skip_rows=1"
	if stmt != want {
		t.Fatalf("buildTiDBImportIntoStatement() = %q, want %q", stmt, want)
	}
}

func TestBuildTiDBImportIntoStatementSkipsInternalNullBitmapField(t *testing.T) {
	stmt, err := buildTiDBImportIntoStatementWithFields("app.orders", "file:///tmp/dbo.orders.000001.csv", []string{"id", "name", "@sqlserver2tidb_null_bitmap"})
	if err != nil {
		t.Fatalf("buildTiDBImportIntoStatementWithFields() error = %v", err)
	}

	want := "IMPORT INTO `app`.`orders` (`id`, `name`, @sqlserver2tidb_null_bitmap) FROM '/tmp/dbo.orders.000001.csv' FORMAT 'csv' WITH skip_rows=1"
	if stmt != want {
		t.Fatalf("buildTiDBImportIntoStatementWithFields() = %q, want %q", stmt, want)
	}
}

func TestBuildTiDBImportIntoPreflightQueryQuotesObject(t *testing.T) {
	query, err := buildTiDBImportIntoPreflightQuery("app.orders")
	if err != nil {
		t.Fatalf("buildTiDBImportIntoPreflightQuery() error = %v", err)
	}

	want := "SELECT COUNT(*) FROM `app`.`orders`"
	if query != want {
		t.Fatalf("buildTiDBImportIntoPreflightQuery() = %q, want %q", query, want)
	}
}

func TestBuildTiDBImportIntoStatementRejectsHTTPSource(t *testing.T) {
	_, err := buildTiDBImportIntoStatement("app.orders", "https://object-store.example/dbo.orders.000001.csv")
	if err == nil {
		t.Fatal("buildTiDBImportIntoStatement() error = nil, want unsupported source error")
	}
	assertOutputContains(t, err.Error(), "IMPORT INTO source URI scheme https is not supported")
}

func TestBuildTiDBImportIntoStatementRejectsObjectStorageWithoutBucket(t *testing.T) {
	_, err := buildTiDBImportIntoStatement("app.orders", "s3:///dbo.orders.000001.csv")
	if err == nil {
		t.Fatal("buildTiDBImportIntoStatement() error = nil, want missing bucket error")
	}
	assertOutputContains(t, err.Error(), "s3 IMPORT INTO source URI bucket is required")
}

func TestBuildTiDBImportIntoStatementRejectsObjectStorageWithoutObjectPath(t *testing.T) {
	_, err := buildTiDBImportIntoStatement("app.orders", "gs://migration-bucket")
	if err == nil {
		t.Fatal("buildTiDBImportIntoStatement() error = nil, want missing object path error")
	}
	assertOutputContains(t, err.Error(), "gs IMPORT INTO source URI object path is required")
}

func TestBuildTiDBImportIntoStatementRejectsRelativeLocalPath(t *testing.T) {
	_, err := buildTiDBImportIntoStatement("app.orders", "relative/dbo.orders.000001.csv")
	if err == nil {
		t.Fatal("buildTiDBImportIntoStatement() error = nil, want relative local path error")
	}
	assertOutputContains(t, err.Error(), "local IMPORT INTO source path must be absolute")
}

func TestReadTiDBImportIntoFieldsFromLocalSourceRejectsRelativePath(t *testing.T) {
	_, err := readTiDBImportIntoFieldsFromLocalSource("relative/dbo.orders.000001.csv")
	if err == nil {
		t.Fatal("readTiDBImportIntoFieldsFromLocalSource() error = nil, want relative local path error")
	}
	assertOutputContains(t, err.Error(), "local IMPORT INTO source path must be absolute")
}

func TestReadTiDBImportIntoFieldsFromLocalSourceSkipsNullBitmap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(path, []byte("id,name,__sqlserver2tidb_null_bitmap\n1,Ada,0\n"), 0o644); err != nil {
		t.Fatalf("write CSV fixture: %v", err)
	}

	fields, err := readTiDBImportIntoFieldsFromLocalSource("file://" + path)
	if err != nil {
		t.Fatalf("readTiDBImportIntoFieldsFromLocalSource() error = %v", err)
	}
	want := []string{"id", "name", "@sqlserver2tidb_null_bitmap"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("readTiDBImportIntoFieldsFromLocalSource() = %v, want %v", fields, want)
	}
}

func TestReadCSVImportFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(path, []byte("id,name\n1,Ada\n2,\n"), 0o644); err != nil {
		t.Fatalf("write CSV fixture: %v", err)
	}

	file, err := openCSVImportFile("file://" + path)
	if err != nil {
		t.Fatalf("openCSVImportFile() error = %v", err)
	}
	defer file.Close()

	columns, records, err := readCSVImportRecords(file)
	if err != nil {
		t.Fatalf("readCSVImportRecords() error = %v", err)
	}
	if strings.Join(columns, ",") != "id,name" {
		t.Fatalf("columns = %v, want [id name]", columns)
	}
	if len(records) != 2 {
		t.Fatalf("records len = %d, want 2", len(records))
	}
	if records[0][0] != "1" || records[0][1] != "Ada" {
		t.Fatalf("records[0] = %v, want [1 Ada]", records[0])
	}
	if records[1][0] != "2" || records[1][1] != "" {
		t.Fatalf("records[1] = %v, want [2 \"\"]", records[1])
	}
}

func TestReadCSVImportHTTPSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("HTTP method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "text/csv")
		_, _ = io.WriteString(w, "id,name\n1,Ada\n2,Lin\n")
	}))
	defer server.Close()

	source, err := openCSVImportFile(server.URL + "/orders.csv")
	if err != nil {
		t.Fatalf("openCSVImportFile() error = %v", err)
	}
	defer source.Close()

	columns, records, err := readCSVImportRecords(source)
	if err != nil {
		t.Fatalf("readCSVImportRecords() error = %v", err)
	}
	if strings.Join(columns, ",") != "id,name" {
		t.Fatalf("columns = %v, want [id name]", columns)
	}
	if len(records) != 2 {
		t.Fatalf("records len = %d, want 2", len(records))
	}
	if records[1][0] != "2" || records[1][1] != "Lin" {
		t.Fatalf("records[1] = %v, want [2 Lin]", records[1])
	}
}

func TestCSVImportReaderStreamsRecords(t *testing.T) {
	reader, err := newCSVImportReader(strings.NewReader("id,name\n1,Ada\n2,Lin\n"))
	if err != nil {
		t.Fatalf("newCSVImportReader() error = %v", err)
	}
	if strings.Join(reader.Columns(), ",") != "id,name" {
		t.Fatalf("columns = %v, want [id name]", reader.Columns())
	}

	first, err := reader.ReadRecord()
	if err != nil {
		t.Fatalf("ReadRecord() first error = %v", err)
	}
	if strings.Join(first, ",") != "1,Ada" {
		t.Fatalf("first record = %v, want [1 Ada]", first)
	}

	second, err := reader.ReadRecord()
	if err != nil {
		t.Fatalf("ReadRecord() second error = %v", err)
	}
	if strings.Join(second, ",") != "2,Lin" {
		t.Fatalf("second record = %v, want [2 Lin]", second)
	}

	_, err = reader.ReadRecord()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("ReadRecord() final error = %v, want io.EOF", err)
	}
}

func TestCSVImportReaderRestoresNullBitmapValues(t *testing.T) {
	reader, err := newCSVImportReader(strings.NewReader("id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n2,,01\n"))
	if err != nil {
		t.Fatalf("newCSVImportReader() error = %v", err)
	}
	if strings.Join(reader.Columns(), ",") != "id,name" {
		t.Fatalf("columns = %v, want [id name]", reader.Columns())
	}

	first, err := reader.ReadValues()
	if err != nil {
		t.Fatalf("ReadValues() first error = %v", err)
	}
	if first[0] != "1" || first[1] != "Ada" {
		t.Fatalf("first values = %#v, want [1 Ada]", first)
	}

	second, err := reader.ReadValues()
	if err != nil {
		t.Fatalf("ReadValues() second error = %v", err)
	}
	if second[0] != "2" || second[1] != nil {
		t.Fatalf("second values = %#v, want [2 nil]", second)
	}
}

func TestCSVImportReaderRejectsDuplicateHeaderColumns(t *testing.T) {
	_, err := newCSVImportReader(strings.NewReader("id,name,ID\n1,Ada,2\n"))
	if err == nil {
		t.Fatal("newCSVImportReader() error = nil, want duplicate column error")
	}
	assertOutputContains(t, err.Error(), `CSV header contains duplicate column name "ID"`)
}

func TestRunCDCDryRunCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--columns", "id,customer_name",
		"--key-columns", "id",
		"--from-lsn", "0x00000027000001f40001",
		"--to-lsn", "0x00000027000001f40002",
		"--apply-batch-size", "1000",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("cdc code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor cdc dry run")
	assertOutputContains(t, output, "source object: sales.dbo.orders")
	assertOutputContains(t, output, "target object: app.orders")
	assertOutputContains(t, output, "columns: id,customer_name")
	assertOutputContains(t, output, "key columns: id")
	assertOutputContains(t, output, "from LSN: 0x00000027000001f40001")
	assertOutputContains(t, output, "to LSN: 0x00000027000001f40002")
	assertOutputContains(t, output, "apply batch size: 1000")
	assertOutputContains(t, output, "No CDC reader or TiDB apply worker will be started.")
}

func TestRunCDCLSNDryRunCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc-lsn",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("cdc-lsn code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor cdc-lsn dry run")
	assertOutputContains(t, output, "source cluster: prod-sqlserver-a")
	assertOutputContains(t, output, "project: sales-db-to-tidb-prod-a")
	assertOutputContains(t, output, "source object: sales.dbo.orders")
	assertOutputContains(t, output, "capture instance: dbo_orders")
	assertOutputContains(t, output, "No SQL Server CDC LSN query will be executed.")
}

func TestRunCDCLSNExecutePrintsMinAndMaxLSN(t *testing.T) {
	oldQuery := queryCDCLSNBoundsFunc
	t.Cleanup(func() {
		queryCDCLSNBoundsFunc = oldQuery
	})
	queryCDCLSNBoundsFunc = func(_ context.Context, spec cdcLSNQuerySpec) (cdcLSNBounds, error) {
		if spec.SourceObject != "sales.dbo.orders" {
			t.Fatalf("source object = %s, want sales.dbo.orders", spec.SourceObject)
		}
		if spec.SourceConnectionStringEnv != "SQLSERVER_CDC_TEST_DSN" {
			t.Fatalf("source env = %s, want SQLSERVER_CDC_TEST_DSN", spec.SourceConnectionStringEnv)
		}
		return cdcLSNBounds{
			CaptureInstance: "dbo_orders",
			MinLSN:          []byte{0x00, 0x00, 0x00, 0x27, 0x00, 0x00, 0x01, 0xf4, 0x00, 0x01},
			MaxLSN:          []byte{0x00, 0x00, 0x00, 0x27, 0x00, 0x00, 0x01, 0xf4, 0x00, 0x03},
		}, nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"cdc-lsn",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--source-connection-string-env", "SQLSERVER_CDC_TEST_DSN",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("cdc-lsn execute code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor cdc-lsn completed")
	assertOutputContains(t, output, "source object: sales.dbo.orders")
	assertOutputContains(t, output, "capture instance: dbo_orders")
	assertOutputContains(t, output, "min_lsn: 0x00000027000001f40001")
	assertOutputContains(t, output, "max_lsn: 0x00000027000001f40003")
}

func TestRunCDCLSNExecuteRequiresSourceConnectionStringEnv(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc-lsn",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-connection-string-env", "MISSING_SQLSERVER_CDC_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("cdc-lsn execute code = 0, want missing source env error")
	}
	assertOutputContains(t, stderr.String(), "executor cdc-lsn: source connection string env MISSING_SQLSERVER_CDC_DSN is not set")
}

func TestRunCDCExecuteRequiresLSNRange(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--columns", "id,customer_name",
		"--key-columns", "id",
		"--apply-batch-size", "1000",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("cdc execute code = 0, want missing LSN error")
	}
	assertOutputContains(t, stderr.String(), "executor cdc: from LSN is required")
}

func TestRunCDCExecuteRejectsReversedLSNRange(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--columns", "id,customer_name",
		"--key-columns", "id",
		"--from-lsn", "0x00000027000001f40003",
		"--to-lsn", "0x00000027000001f40002",
		"--apply-batch-size", "1000",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("cdc execute code = 0, want reversed LSN range error")
	}
	assertOutputContains(t, stderr.String(), "executor cdc: from LSN must be less than or equal to to LSN")
}

func TestRunCDCExecuteRequiresSourceConnectionStringEnv(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--columns", "id,customer_name",
		"--key-columns", "id",
		"--from-lsn", "0x00000027000001f40001",
		"--to-lsn", "0x00000027000001f40002",
		"--apply-batch-size", "1000",
		"--source-connection-string-env", "MISSING_SQLSERVER_CDC_DSN",
		"--target-connection-string-env", "MISSING_TIDB_APPLY_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("cdc execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor cdc: source connection string env MISSING_SQLSERVER_CDC_DSN is not set")
}

func TestRunCDCExecuteRequiresTargetConnectionStringEnv(t *testing.T) {
	t.Setenv("SQLSERVER_CDC_TEST_DSN", "sqlserver://readonly:secret@localhost?database=sales")
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--columns", "id,customer_name",
		"--key-columns", "id",
		"--from-lsn", "0x00000027000001f40001",
		"--to-lsn", "0x00000027000001f40002",
		"--apply-batch-size", "1000",
		"--source-connection-string-env", "SQLSERVER_CDC_TEST_DSN",
		"--target-connection-string-env", "MISSING_TIDB_APPLY_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("cdc execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor cdc: target connection string env MISSING_TIDB_APPLY_DSN is not set")
}

func TestRunCDCRejectsMissingColumns(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--key-columns", "id",
		"--apply-batch-size", "1000",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("cdc code = 0, want missing columns error")
	}
	assertOutputContains(t, stderr.String(), "executor cdc: columns is required")
}

func TestRunCDCExecuteRejectsKeyColumnOutsideColumns(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--columns", "id,customer_name",
		"--key-columns", "tenant_id",
		"--from-lsn", "0x00000027000001f40001",
		"--to-lsn", "0x00000027000001f40002",
		"--apply-batch-size", "1000",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("cdc execute code = 0, want key column outside columns error")
	}
	assertOutputContains(t, stderr.String(), "executor cdc: CDC key column tenant_id is not present in captured columns")
}

func TestRunCDCExecutePrintsAppliedChangeCount(t *testing.T) {
	oldExecute := executeCDCApplyFunc
	t.Cleanup(func() {
		executeCDCApplyFunc = oldExecute
	})
	executeCDCApplyFunc = func(_ context.Context, spec cdcExecuteSpec) (cdcApplyResult, error) {
		if spec.SourceObject != "sales.dbo.orders" || spec.TargetObject != "app.orders" {
			t.Fatalf("cdc spec objects = %s -> %s", spec.SourceObject, spec.TargetObject)
		}
		if !reflect.DeepEqual(spec.FromLSN, []byte{0x00, 0x00, 0x00, 0x27, 0x00, 0x00, 0x01, 0xf4, 0x00, 0x01}) {
			t.Fatalf("from LSN = %#v", spec.FromLSN)
		}
		return cdcApplyResult{AppliedChanges: 2}, nil
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--columns", "id,customer_name",
		"--key-columns", "id",
		"--from-lsn", "0x00000027000001f40001",
		"--to-lsn", "0x00000027000001f40002",
		"--apply-batch-size", "1000",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("cdc execute code = %d, stderr = %s", code, stderr.String())
	}
	assertOutputContains(t, stdout.String(), "executor cdc completed: sales.dbo.orders -> app.orders")
	assertOutputContains(t, stdout.String(), "applied changes: 2")
}

func TestBuildTiDBCDCUpsertStatementQuotesTargetAndSkipsKeyUpdates(t *testing.T) {
	stmt, err := buildTiDBCDCUpsertStatement("app.orders", []string{"id", "customer`name", "total"}, []string{"id"})
	if err != nil {
		t.Fatalf("buildTiDBCDCUpsertStatement() error = %v", err)
	}

	want := "INSERT INTO `app`.`orders` (`id`, `customer``name`, `total`) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE `customer``name` = VALUES(`customer``name`), `total` = VALUES(`total`)"
	if stmt != want {
		t.Fatalf("buildTiDBCDCUpsertStatement() = %q, want %q", stmt, want)
	}
}

func TestBuildTiDBCDCDeleteStatementUsesKeyColumns(t *testing.T) {
	stmt, err := buildTiDBCDCDeleteStatement("app.orders", []string{"tenant_id", "id"})
	if err != nil {
		t.Fatalf("buildTiDBCDCDeleteStatement() error = %v", err)
	}

	want := "DELETE FROM `app`.`orders` WHERE `tenant_id` = ? AND `id` = ?"
	if stmt != want {
		t.Fatalf("buildTiDBCDCDeleteStatement() = %q, want %q", stmt, want)
	}
}

func TestBuildTiDBCDCUpsertStatementRequiresKeyColumnsInCapturedColumns(t *testing.T) {
	_, err := buildTiDBCDCUpsertStatement("app.orders", []string{"id", "customer_name"}, []string{"missing_id"})
	if err == nil {
		t.Fatal("buildTiDBCDCUpsertStatement() error = nil, want missing key column error")
	}
	assertOutputContains(t, err.Error(), "CDC key column missing_id is not present in captured columns")
}

func TestParseSQLServerCDCLSN(t *testing.T) {
	got, err := parseSQLServerCDCLSN("0x00000027000001f40001", "from LSN")
	if err != nil {
		t.Fatalf("parseSQLServerCDCLSN() error = %v", err)
	}
	want := []byte{0x00, 0x00, 0x00, 0x27, 0x00, 0x00, 0x01, 0xf4, 0x00, 0x01}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LSN bytes = %#v, want %#v", got, want)
	}

	_, err = parseSQLServerCDCLSN("0x1234", "from LSN")
	if err == nil {
		t.Fatal("parseSQLServerCDCLSN() error = nil, want length error")
	}
	assertOutputContains(t, err.Error(), "executor cdc: from LSN must be a 10-byte hex value")
}

func TestFormatSQLServerCDCLSN(t *testing.T) {
	got, err := formatSQLServerCDCLSN([]byte{0x00, 0x00, 0x00, 0x27, 0x00, 0x00, 0x01, 0xf4, 0x00, 0x03}, "max LSN")
	if err != nil {
		t.Fatalf("formatSQLServerCDCLSN() error = %v", err)
	}
	if got != "0x00000027000001f40003" {
		t.Fatalf("formatSQLServerCDCLSN() = %q, want 10-byte hex", got)
	}

	_, err = formatSQLServerCDCLSN([]byte{0x12}, "max LSN")
	if err == nil {
		t.Fatal("formatSQLServerCDCLSN() error = nil, want length error")
	}
	assertOutputContains(t, err.Error(), "executor cdc-lsn: max LSN must be a 10-byte value")
}

func TestBuildSQLServerCDCLSNQueries(t *testing.T) {
	maxQuery, err := buildSQLServerCDCMaxLSNQuery("sales.dbo.orders")
	if err != nil {
		t.Fatalf("buildSQLServerCDCMaxLSNQuery() error = %v", err)
	}
	if maxQuery != "SELECT [sales].[sys].[fn_cdc_get_max_lsn]()" {
		t.Fatalf("max query = %q", maxQuery)
	}

	minQuery, captureInstance, err := buildSQLServerCDCMinLSNQuery("sales.dbo.orders")
	if err != nil {
		t.Fatalf("buildSQLServerCDCMinLSNQuery() error = %v", err)
	}
	if minQuery != "SELECT [sales].[sys].[fn_cdc_get_min_lsn](@capture_instance)" {
		t.Fatalf("min query = %q", minQuery)
	}
	if captureInstance != "dbo_orders" {
		t.Fatalf("capture instance = %q, want dbo_orders", captureInstance)
	}
}

func TestApplySQLServerCDCChangesQueriesRangeAndAppliesRows(t *testing.T) {
	source := &recordingCDCSource{
		rows: &fakeExportRows{
			columns: []string{"__$operation", "__$start_lsn", "__$seqval", "id", "customer_name"},
			values: [][]any{
				{int64(2), []byte{0x01}, []byte{0x01}, int64(1), "Ada"},
				{int64(1), []byte{0x02}, []byte{0x01}, int64(2), "Lin"},
			},
		},
	}
	target := &recordingMultiCDCExecutor{}
	fromLSN := []byte{0x00, 0x00, 0x00, 0x27, 0x00, 0x00, 0x01, 0xf4, 0x00, 0x01}
	toLSN := []byte{0x00, 0x00, 0x00, 0x27, 0x00, 0x00, 0x01, 0xf4, 0x00, 0x02}

	applied, err := applySQLServerCDCChanges(context.Background(), source, target, cdcApplySpec{
		SourceObject: "sales.dbo.orders",
		TargetObject: "app.orders",
		Columns:      []string{"id", "customer_name"},
		KeyColumns:   []string{"id"},
		FromLSN:      fromLSN,
		ToLSN:        toLSN,
	})
	if err != nil {
		t.Fatalf("applySQLServerCDCChanges() error = %v", err)
	}
	if applied != 2 {
		t.Fatalf("applied = %d, want 2", applied)
	}
	wantQuery := "SELECT [__$operation], [__$start_lsn], [__$seqval], [id], [customer_name] FROM [sales].[cdc].[fn_cdc_get_all_changes_dbo_orders](@from_lsn, @to_lsn, 'all update old') ORDER BY [__$start_lsn], [__$seqval]"
	if source.query != wantQuery {
		t.Fatalf("query = %q, want %q", source.query, wantQuery)
	}
	if !reflect.DeepEqual(source.fromLSN, fromLSN) || !reflect.DeepEqual(source.toLSN, toLSN) {
		t.Fatalf("LSN range = %#v..%#v", source.fromLSN, source.toLSN)
	}
	if len(target.calls) != 2 {
		t.Fatalf("target calls = %d, want 2", len(target.calls))
	}
	if target.calls[0].query != "INSERT INTO `app`.`orders` (`id`, `customer_name`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `customer_name` = VALUES(`customer_name`)" {
		t.Fatalf("first query = %q", target.calls[0].query)
	}
	if target.calls[1].query != "DELETE FROM `app`.`orders` WHERE `id` = ?" {
		t.Fatalf("second query = %q", target.calls[1].query)
	}
}

func TestNormalizeSQLServerCDCOperation(t *testing.T) {
	tests := []struct {
		code int
		want cdcApplyOperation
	}{
		{code: 1, want: cdcApplyDelete},
		{code: 2, want: cdcApplyUpsert},
		{code: 3, want: cdcApplySkip},
		{code: 4, want: cdcApplyUpsert},
	}
	for _, tt := range tests {
		got, err := normalizeSQLServerCDCOperation(tt.code)
		if err != nil {
			t.Fatalf("normalizeSQLServerCDCOperation(%d) error = %v", tt.code, err)
		}
		if got != tt.want {
			t.Fatalf("normalizeSQLServerCDCOperation(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestApplyTiDBCDCChangeAppliesUpsert(t *testing.T) {
	exec := &recordingCDCExecutor{}
	err := applyTiDBCDCChange(context.Background(), exec, "app.orders", []string{"id"}, cdcChangeRow{
		Operation: cdcApplyUpsert,
		Columns:   []string{"id", "customer_name"},
		Values:    []any{int64(1), "Ada"},
	})
	if err != nil {
		t.Fatalf("applyTiDBCDCChange() error = %v", err)
	}
	if exec.query != "INSERT INTO `app`.`orders` (`id`, `customer_name`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `customer_name` = VALUES(`customer_name`)" {
		t.Fatalf("query = %q", exec.query)
	}
	if !reflect.DeepEqual(exec.args, []any{int64(1), "Ada"}) {
		t.Fatalf("args = %#v", exec.args)
	}
}

func TestApplyTiDBCDCChangeAppliesDeleteWithKeyValues(t *testing.T) {
	exec := &recordingCDCExecutor{}
	err := applyTiDBCDCChange(context.Background(), exec, "app.orders", []string{"tenant_id", "id"}, cdcChangeRow{
		Operation: cdcApplyDelete,
		Columns:   []string{"id", "tenant_id", "customer_name"},
		Values:    []any{int64(7), int64(42), "Ada"},
	})
	if err != nil {
		t.Fatalf("applyTiDBCDCChange() error = %v", err)
	}
	if exec.query != "DELETE FROM `app`.`orders` WHERE `tenant_id` = ? AND `id` = ?" {
		t.Fatalf("query = %q", exec.query)
	}
	if !reflect.DeepEqual(exec.args, []any{int64(42), int64(7)}) {
		t.Fatalf("args = %#v", exec.args)
	}
}

func TestApplyTiDBCDCChangeSkipsBeforeUpdateRows(t *testing.T) {
	exec := &recordingCDCExecutor{}
	err := applyTiDBCDCChange(context.Background(), exec, "app.orders", []string{"id"}, cdcChangeRow{
		Operation: cdcApplySkip,
		Columns:   []string{"id", "customer_name"},
		Values:    []any{int64(1), "Ada"},
	})
	if err != nil {
		t.Fatalf("applyTiDBCDCChange() error = %v", err)
	}
	if exec.called {
		t.Fatalf("ExecContext was called for skip operation")
	}
}

func TestBuildSQLServerCDCChangesQueryQuotesCaptureFunctionAndColumns(t *testing.T) {
	query, err := buildSQLServerCDCChangesQuery("sales.dbo.orders", []string{"id", "customer]name"})
	if err != nil {
		t.Fatalf("buildSQLServerCDCChangesQuery() error = %v", err)
	}

	want := "SELECT [__$operation], [__$start_lsn], [__$seqval], [id], [customer]]name] FROM [sales].[cdc].[fn_cdc_get_all_changes_dbo_orders](@from_lsn, @to_lsn, 'all update old') ORDER BY [__$start_lsn], [__$seqval]"
	if query != want {
		t.Fatalf("buildSQLServerCDCChangesQuery() = %q, want %q", query, want)
	}
}

func TestBuildSQLServerCDCChangesQueryRejectsInvalidSourceObject(t *testing.T) {
	_, err := buildSQLServerCDCChangesQuery("orders", []string{"id"})
	if err == nil {
		t.Fatal("buildSQLServerCDCChangesQuery() error = nil, want invalid object error")
	}
	assertOutputContains(t, err.Error(), "source object must be schema.table or database.schema.table")
}

func TestReadSQLServerCDCChangeRows(t *testing.T) {
	rows := &fakeExportRows{
		columns: []string{"__$operation", "__$start_lsn", "__$seqval", "id", "customer_name"},
		values: [][]any{
			{int64(2), []byte{0x01}, []byte{0x01}, int64(1), "Ada"},
			{int64(1), []byte{0x02}, []byte{0x01}, int64(2), "Lin"},
		},
	}

	changes, err := readSQLServerCDCChangeRows(rows, []string{"id", "customer_name"})
	if err != nil {
		t.Fatalf("readSQLServerCDCChangeRows() error = %v", err)
	}
	if !rows.closed {
		t.Fatalf("rows closed = false, want true")
	}
	if len(changes) != 2 {
		t.Fatalf("changes len = %d, want 2", len(changes))
	}
	if changes[0].Operation != cdcApplyUpsert || changes[1].Operation != cdcApplyDelete {
		t.Fatalf("operations = %q, %q", changes[0].Operation, changes[1].Operation)
	}
	if !reflect.DeepEqual(changes[0].Columns, []string{"id", "customer_name"}) {
		t.Fatalf("columns = %#v", changes[0].Columns)
	}
	if !reflect.DeepEqual(changes[0].Values, []any{int64(1), "Ada"}) {
		t.Fatalf("values = %#v", changes[0].Values)
	}
}

func TestReadSQLServerCDCChangeRowsRejectsUnexpectedColumns(t *testing.T) {
	rows := &fakeExportRows{
		columns: []string{"__$operation", "__$start_lsn", "id"},
		values:  [][]any{{int64(2), []byte{0x01}, int64(1)}},
	}

	_, err := readSQLServerCDCChangeRows(rows, []string{"id"})
	if err == nil {
		t.Fatal("readSQLServerCDCChangeRows() error = nil, want column mismatch")
	}
	assertOutputContains(t, err.Error(), "CDC query returned 3 columns, want 4")
}

func TestRunExportRequiresChunkID(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"export",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--output-uri", "s3://migration/prod/full/dbo.orders.000001.parquet",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("export code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor export: chunk id is required")
}

func TestRunExportExecuteRejectsTODOExportPredicate(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"export",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--chunk-id", "dbo.orders.000001",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--output-uri", "file:///tmp/dbo.orders.000001.csv",
		"--predicate", "TODO: choose stable split predicate",
		"--source-connection-string-env", "MISSING_SQLSERVER_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("export execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor export: predicate still contains TODO")
}

func TestRunExportExecuteRejectsNonFileOutputURI(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"export",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--chunk-id", "dbo.orders.000001",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--output-uri", "s3://migration/prod/full/dbo.orders.000001.csv",
		"--source-connection-string-env", "MISSING_SQLSERVER_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("export execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor export: only file://, http://, and https:// output URIs are supported for --execute")
}

func TestRunExportExecuteAcceptsHTTPOutputURIBeforeConnectionStringEnv(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"export",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--chunk-id", "dbo.orders.000001",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--output-uri", "https://object-store.example/dbo.orders.000001.csv",
		"--source-connection-string-env", "MISSING_SQLSERVER_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("export execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor export: source connection string env MISSING_SQLSERVER_DSN is not set")
}

func TestRunExportExecuteRequiresConnectionStringEnv(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"export",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--chunk-id", "dbo.orders.000001",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--output-uri", "file:///tmp/dbo.orders.000001.csv",
		"--source-connection-string-env", "MISSING_SQLSERVER_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("export execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor export: source connection string env MISSING_SQLSERVER_DSN is not set")
}

func TestBuildSQLServerExportQueryQuotesObjectAndPredicate(t *testing.T) {
	query, err := buildSQLServerExportQuery("sales.dbo.orders", "id >= 1 AND id < 1000")
	if err != nil {
		t.Fatalf("buildSQLServerExportQuery() error = %v", err)
	}

	want := "SELECT * FROM [sales].[dbo].[orders] WHERE id >= 1 AND id < 1000"
	if query != want {
		t.Fatalf("buildSQLServerExportQuery() = %q, want %q", query, want)
	}
}

func TestWriteCSVExportRows(t *testing.T) {
	var output bytes.Buffer
	rows := &fakeExportRows{
		columns: []string{"id", "name", "active"},
		values: [][]any{
			{int64(1), "Ada", true},
			{int64(2), nil, false},
		},
	}

	if err := writeCSVExportRows(&output, rows); err != nil {
		t.Fatalf("writeCSVExportRows() error = %v", err)
	}

	want := "id,name,active,__sqlserver2tidb_null_bitmap\n1,Ada,true,000\n2,,false,010\n"
	if output.String() != want {
		t.Fatalf("CSV output = %q, want %q", output.String(), want)
	}
	if !rows.closed {
		t.Fatalf("rows closed = false, want true")
	}
}

func TestWriteCSVExportRowsHTTPOutput(t *testing.T) {
	var method string
	var contentType string
	var body bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		contentType = r.Header.Get("Content-Type")
		if _, err := io.Copy(&body, r.Body); err != nil {
			t.Fatalf("read request body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	output, err := openCSVExportOutput(context.Background(), exportOutputURI{
		scheme: "http",
		uri:    server.URL + "/dbo.orders.000001.csv",
	})
	if err != nil {
		t.Fatalf("openCSVExportOutput() error = %v", err)
	}
	rows := &fakeExportRows{
		columns: []string{"id", "name"},
		values: [][]any{
			{int64(1), "Ada"},
			{int64(2), nil},
		},
	}

	if err := writeCSVExportRows(output, rows); err != nil {
		t.Fatalf("writeCSVExportRows() error = %v", err)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("output.Close() error = %v", err)
	}

	if method != http.MethodPut {
		t.Fatalf("HTTP method = %s, want PUT", method)
	}
	if contentType != "text/csv" {
		t.Fatalf("Content-Type = %q, want text/csv", contentType)
	}
	want := "id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n2,,01\n"
	if body.String() != want {
		t.Fatalf("HTTP body = %q, want %q", body.String(), want)
	}
	if !rows.closed {
		t.Fatalf("rows closed = false, want true")
	}
}

type fakeExportRows struct {
	columns []string
	values  [][]any
	idx     int
	closed  bool
}

func (rows *fakeExportRows) Columns() ([]string, error) {
	return rows.columns, nil
}

func (rows *fakeExportRows) Next() bool {
	return rows.idx < len(rows.values)
}

func (rows *fakeExportRows) Scan(dest ...any) error {
	if rows.idx >= len(rows.values) {
		return errors.New("scan after end")
	}
	for i := range dest {
		ptr, ok := dest[i].(*any)
		if !ok {
			return errors.New("destination is not *any")
		}
		*ptr = rows.values[rows.idx][i]
	}
	rows.idx++
	return nil
}

func (rows *fakeExportRows) Err() error {
	return nil
}

func (rows *fakeExportRows) Close() error {
	rows.closed = true
	return nil
}

func assertOutputContains(t *testing.T, content, want string) {
	t.Helper()
	if !strings.Contains(content, want) {
		t.Fatalf("expected output to contain %q\noutput:\n%s", want, content)
	}
}
