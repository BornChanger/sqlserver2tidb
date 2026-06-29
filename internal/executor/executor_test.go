package executor

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestRunExportDryRunAcceptsGzipCompression(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"export",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--chunk-id", "dbo.orders.000001",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--output-uri", "file:///tmp/dbo.orders.000001.csv.gz",
		"--compression", "gzip",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("export code = %d, stderr = %s", code, stderr.String())
	}
	assertOutputContains(t, stdout.String(), "compression: gzip")
}

func TestRunExportDryRunRejectsUnsupportedCompression(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"export",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--chunk-id", "dbo.orders.000001",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--output-uri", "file:///tmp/dbo.orders.000001.csv.zst",
		"--compression", "zstd",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("export dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor export: compression zstd is not supported")
}

func TestRunExportDryRunAcceptsS3OutputURI(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"export",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--chunk-id", "dbo.orders.000001",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--output-uri", "s3://migration/prod/full/dbo.orders.000001.csv",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("export dry-run code = %d, stderr = %s", code, stderr.String())
	}
	assertOutputContains(t, stdout.String(), "output uri: s3://migration/prod/full/dbo.orders.000001.csv")
}

func TestRunExportDryRunAcceptsGCSAndAzureBlobOutputURIs(t *testing.T) {
	for _, outputURI := range []string{
		"gs://migration/prod/full/dbo.orders.000001.csv",
		"azblob://migration/prod/full/dbo.orders.000001.csv",
	} {
		t.Run(outputURI, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := Run([]string{
				"export",
				"--root", ".",
				"--source-cluster-id", "prod-sqlserver-a",
				"--project-id", "sales-db-to-tidb-prod-a",
				"--chunk-id", "dbo.orders.000001",
				"--source-object", "sales.dbo.orders",
				"--target-object", "app.orders",
				"--output-uri", outputURI,
			}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("export dry-run code = %d, stderr = %s", code, stderr.String())
			}
			assertOutputContains(t, stdout.String(), "output uri: "+outputURI)
		})
	}
}

func TestRunExportDryRunRejectsMalformedAzureBlobOutputURI(t *testing.T) {
	for _, tc := range []struct {
		name      string
		outputURI string
		want      string
	}{
		{
			name:      "missing container",
			outputURI: "azblob:///prod/full/dbo.orders.000001.csv",
			want:      "executor export: azblob output URI container is required",
		},
		{
			name:      "missing blob path",
			outputURI: "azblob://migration",
			want:      "executor export: azblob output URI blob path is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := Run([]string{
				"export",
				"--root", ".",
				"--source-cluster-id", "prod-sqlserver-a",
				"--project-id", "sales-db-to-tidb-prod-a",
				"--chunk-id", "dbo.orders.000001",
				"--source-object", "sales.dbo.orders",
				"--target-object", "app.orders",
				"--output-uri", tc.outputURI,
			}, &stdout, &stderr)
			if code == 0 {
				t.Fatalf("export dry-run code = 0, want non-zero; stdout = %s", stdout.String())
			}
			assertOutputContains(t, stderr.String(), tc.want)
		})
	}
}

func TestRunExportDryRunRejectsUnsupportedOutputURI(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"export",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--chunk-id", "dbo.orders.000001",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--output-uri", "ftp://migration/prod/full/dbo.orders.000001.csv",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("export dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor export: only file://, http://, https://, s3://, gs://, and azblob:// output URIs are supported for CSV export")
}

func TestRunExportDryRunRejectsTODOExportPredicate(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"export",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--chunk-id", "dbo.orders.000001",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--output-uri", "file:///tmp/dbo.orders.000001.csv",
		"--predicate", "TODO: choose stable split predicate",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("export dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor export: predicate still contains TODO")
}

func TestRunExportDryRunRejectsInvalidSourceObject(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"export",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--chunk-id", "dbo.orders.000001",
		"--source-object", "orders",
		"--target-object", "app.orders",
		"--output-uri", "file:///tmp/dbo.orders.000001.csv",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("export dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor export: source object must be schema.table or database.schema.table")
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

func TestRunImportDryRunAcceptsGzipCompression(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "file:///tmp/dbo.orders.000001.csv.gz",
		"--depends-on-export-chunk", "dbo.orders.000001",
		"--compression", "gzip",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("import code = %d, stderr = %s", code, stderr.String())
	}
	assertOutputContains(t, stdout.String(), "compression: gzip")
}

func TestRunImportDryRunAcceptsSQLInsertS3SourceURI(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "s3://migration/prod/full/dbo.orders.000001.csv",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("import dry-run code = %d, stderr = %s", code, stderr.String())
	}
	assertOutputContains(t, stdout.String(), "source uri: s3://migration/prod/full/dbo.orders.000001.csv")
}

func TestRunImportDryRunAcceptsSQLInsertGCSAndAzureBlobSourceURIs(t *testing.T) {
	for _, sourceURI := range []string{
		"gs://migration/prod/full/dbo.orders.000001.csv",
		"azblob://migration/prod/full/dbo.orders.000001.csv",
	} {
		t.Run(sourceURI, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := Run([]string{
				"import",
				"--root", ".",
				"--source-cluster-id", "prod-sqlserver-a",
				"--project-id", "sales-db-to-tidb-prod-a",
				"--job-id", "import-dbo.orders.000001",
				"--target-object", "app.orders",
				"--source-uri", sourceURI,
			}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("import dry-run code = %d, stderr = %s", code, stderr.String())
			}
			assertOutputContains(t, stdout.String(), "source uri: "+sourceURI)
		})
	}
}

func TestRunImportDryRunRejectsMalformedAzureBlobSourceURI(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sourceURI string
		want      string
	}{
		{
			name:      "missing container",
			sourceURI: "azblob:///prod/full/dbo.orders.000001.csv",
			want:      "executor import: azblob source URI container is required",
		},
		{
			name:      "missing blob path",
			sourceURI: "azblob://migration",
			want:      "executor import: azblob source URI blob path is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := Run([]string{
				"import",
				"--root", ".",
				"--source-cluster-id", "prod-sqlserver-a",
				"--project-id", "sales-db-to-tidb-prod-a",
				"--job-id", "import-dbo.orders.000001",
				"--target-object", "app.orders",
				"--source-uri", tc.sourceURI,
			}, &stdout, &stderr)
			if code == 0 {
				t.Fatalf("import dry-run code = 0, want non-zero; stdout = %s", stdout.String())
			}
			assertOutputContains(t, stderr.String(), tc.want)
		})
	}
}

func TestRunImportDryRunRejectsTiDBImportIntoGzipCompression(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--engine", "tidb-import-into",
		"--target-object", "app.orders",
		"--source-uri", "file:///tmp/dbo.orders.000001.csv.gz",
		"--depends-on-export-chunk", "dbo.orders.000001",
		"--compression", "gzip",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("import dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor import: compression gzip is only supported with sql-insert import")
}

func TestRunImportDryRunRejectsUnsupportedSQLInsertSourceURI(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "ftp://migration/prod/full/dbo.orders.000001.csv",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("import dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor import: only file://, http://, https://, s3://, gs://, and azblob:// source URIs are supported for sql-insert import")
}

func TestRunImportDryRunRejectsUnsupportedTiDBImportIntoSourceURI(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "https://object-store.example/migration/prod/full/dbo.orders.000001.csv",
		"--engine", "tidb-import-into",
		"--fields", "id,name,@sqlserver2tidb_null_bitmap",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("import dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor import: IMPORT INTO source URI scheme https is not supported")
}

func TestRunImportDryRunRejectsInvalidTargetObject(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.sales.orders",
		"--source-uri", "file:///tmp/dbo.orders.000001.csv",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("import dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor import: target object must be table or database.table")
}

func TestRunApplyDDLDryRunCommand(t *testing.T) {
	root := t.TempDir()
	ddlFile := filepath.Join(root, "ddl.sql")
	if err := os.WriteFile(ddlFile, []byte("CREATE TABLE IF NOT EXISTS `app`.`orders` (`id` INT);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"apply-ddl",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--ddl-file", ddlFile,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("apply-ddl code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor apply-ddl dry run")
	assertOutputContains(t, output, "source cluster: prod-sqlserver-a")
	assertOutputContains(t, output, "project: sales-db-to-tidb-prod-a")
	assertOutputContains(t, output, "ddl file: "+ddlFile)
	assertOutputContains(t, output, "No TiDB connection will be opened.")
}

func TestRunApplyDDLDryRunRejectsTODODDL(t *testing.T) {
	root := t.TempDir()
	ddlFile := filepath.Join(root, "ddl.sql")
	if err := os.WriteFile(ddlFile, []byte("CREATE TABLE t (c TEXT /* TODO: review */);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"apply-ddl",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--ddl-file", ddlFile,
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("apply-ddl dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor apply-ddl: DDL file still contains TODO")
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

func TestRunImportExecuteRejectsUnsupportedSQLInsertSourceURI(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "ftp://migration/prod/full/dbo.orders.000001.csv",
		"--target-connection-string-env", "MISSING_TIDB_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("import execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor import: only file://, http://, https://, s3://, gs://, and azblob:// source URIs are supported for sql-insert import")
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

func TestRunImportDryRunSupportsTiDBLightningPlan(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "tidb-lightning",
		"--engine", "tidb-lightning",
		"--source-uri", "s3://migration-bucket/sqlserver2tidb/full",
		"--import-plan", "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("import dry-run code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor import dry run")
	assertOutputContains(t, output, "engine: tidb-lightning")
	assertOutputContains(t, output, "source uri: s3://migration-bucket/sqlserver2tidb/full")
	assertOutputContains(t, output, "import plan: clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml")
	assertOutputContains(t, output, "No TiDB Lightning process will be started.")
}

func TestBuildTiDBLightningConfigUsesCSVNullMarker(t *testing.T) {
	config, err := buildTiDBLightningConfig(tiDBLightningConfigSpec{
		DataSourceURI:          "s3://migration-bucket/sqlserver2tidb/full",
		TargetConnectionString: "migrate:secret@tcp(tidb.example.com:4000)/app",
		PDAddr:                 "pd.example.com:2379",
		SortedKVDir:            "/var/lib/sqlserver2tidb/lightning-sorted-kv",
		LogFile:                "tidb-lightning.log",
	})
	if err != nil {
		t.Fatalf("buildTiDBLightningConfig() error = %v", err)
	}
	assertOutputContains(t, config, `[tikv-importer]`)
	assertOutputContains(t, config, `backend = "local"`)
	assertOutputContains(t, config, `sorted-kv-dir = "/var/lib/sqlserver2tidb/lightning-sorted-kv"`)
	assertOutputContains(t, config, `[mydumper]`)
	assertOutputContains(t, config, `data-source-dir = "s3://migration-bucket/sqlserver2tidb/full"`)
	assertOutputContains(t, config, `[mydumper.csv]`)
	assertOutputContains(t, config, `header = true`)
	assertOutputContains(t, config, `null = '\N'`)
	assertOutputContains(t, config, `[tidb]`)
	assertOutputContains(t, config, `host = "tidb.example.com"`)
	assertOutputContains(t, config, `port = 4000`)
	assertOutputContains(t, config, `user = "migrate"`)
	assertOutputContains(t, config, `password = "secret"`)
	assertOutputContains(t, config, `pd-addr = "pd.example.com:2379"`)
}

func TestAuditTiDBLightningCSVSourceSupportsAbsoluteLocalPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.orders.000001.csv")
	if err := os.WriteFile(path, []byte("id,name\n1,Ada\n2,\\N\n"), 0o644); err != nil {
		t.Fatalf("write CSV source: %v", err)
	}

	audit, err := auditTiDBLightningCSVSource(context.Background(), path, compressionNone)
	if err != nil {
		t.Fatalf("auditTiDBLightningCSVSource() error = %v", err)
	}
	if audit.Rows != 2 {
		t.Fatalf("Rows = %d, want 2", audit.Rows)
	}
	if audit.Bytes <= 0 {
		t.Fatalf("Bytes = %d, want > 0", audit.Bytes)
	}
	if !strings.HasPrefix(audit.SHA256, "sha256:") {
		t.Fatalf("SHA256 = %q, want sha256 digest", audit.SHA256)
	}
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

func TestRunImportRejectsInvalidTiDBImportIntoFields(t *testing.T) {
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
		"--fields", "id,@bad-name",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("import dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor import: fields contains invalid user variable \"@bad-name\"")
}

func TestRunImportRejectsDuplicateTiDBImportIntoFields(t *testing.T) {
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
		"--fields", "id,ID",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("import dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor import: fields contains duplicate column \"ID\"")
}

func TestRunImportDryRunAcceptsS3TiDBImportIntoSourceWithoutFields(t *testing.T) {
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
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("import dry-run code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor import dry run")
	assertOutputContains(t, output, "engine: tidb-import-into")
	assertOutputContains(t, output, "source uri: s3://migration/prod/full/dbo.orders.000001.csv")
}

func TestRunImportDryRunAcceptsGCSTiDBImportIntoSourceWithoutFields(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"import",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--job-id", "import-dbo.orders.000001",
		"--target-object", "app.orders",
		"--source-uri", "gs://migration/prod/full/dbo.orders.000001.csv",
		"--engine", "tidb-import-into",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("import dry-run code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor import dry run")
	assertOutputContains(t, output, "engine: tidb-import-into")
	assertOutputContains(t, output, "source uri: gs://migration/prod/full/dbo.orders.000001.csv")
}

func TestExecuteTiDBImportValidatesBatchSizeBeforeSourceURI(t *testing.T) {
	_, err := executeTiDBImport(context.Background(), importExecuteSpec{
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

func TestRunValidateCountDryRunRejectsTODOPredicate(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"validate-count",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--predicate", "TODO: choose predicate",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("validate-count dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor validate-count: predicate still contains TODO")
}

func TestRunValidateCountDryRunRejectsTODOTargetPredicate(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"validate-count",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--target-predicate", "TODO: choose target predicate",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("validate-count dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor validate-count: target predicate still contains TODO")
}

func TestRunValidateCountDryRunRejectsInvalidSourceObject(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"validate-count",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "orders",
		"--target-object", "app.orders",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("validate-count dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor validate-count: source object must be schema.table or database.schema.table")
}

func TestRunValidateCountDryRunRejectsInvalidTargetObject(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"validate-count",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "dbo.orders",
		"--target-object", "app.sales.orders",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("validate-count dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor validate-count: target object must be table or database.table")
}

func TestRunValidateQueryDryRunRejectsTODOSourceSQL(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"validate-query",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--check-id", "orders-total",
		"--source-sql", "TODO: choose source SQL",
		"--target-sql", "SELECT SUM(total) FROM app.orders",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("validate-query dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "check-id=orders-total")
	assertOutputContains(t, stderr.String(), "source_sql still contains TODO")
}

func TestRunValidateQueryDryRunRejectsTODOTargetSQL(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"validate-query",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--check-id", "orders-total",
		"--source-sql", "SELECT SUM(total) FROM sales.dbo.orders",
		"--target-sql", "TODO: choose target SQL",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("validate-query dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "check-id=orders-total")
	assertOutputContains(t, stderr.String(), "target_sql still contains TODO")
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

func TestReadTiDBImportIntoFieldsFromS3SourceSkipsNullBitmap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", r.Method)
		}
		if r.URL.EscapedPath() != "/migration-bucket/full/orders.csv" {
			t.Fatalf("request path = %q, want path-style bucket/object path", r.URL.EscapedPath())
		}
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Fatalf("Accept-Encoding = %q, want identity", r.Header.Get("Accept-Encoding"))
		}
		_, _ = io.WriteString(w, "id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n")
	}))
	defer server.Close()
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SECRETEXAMPLE")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_S3_FORCE_PATH_STYLE", "true")

	fields, err := readTiDBImportIntoFieldsFromSource(context.Background(), "s3://migration-bucket/full/orders.csv")
	if err != nil {
		t.Fatalf("readTiDBImportIntoFieldsFromSource() error = %v", err)
	}
	want := []string{"id", "name", "@sqlserver2tidb_null_bitmap"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("readTiDBImportIntoFieldsFromSource() = %v, want %v", fields, want)
	}
}

func TestAuditTiDBImportIntoLocalSourceRecordsRowsBytesAndSHA(t *testing.T) {
	data := []byte("id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n2,Lin,00\n")
	path := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write CSV fixture: %v", err)
	}

	audit, ok, err := auditTiDBImportIntoLocalSource("file://" + path)
	if err != nil {
		t.Fatalf("auditTiDBImportIntoLocalSource() error = %v", err)
	}
	if !ok {
		t.Fatal("auditTiDBImportIntoLocalSource() ok = false, want true")
	}
	if audit.Rows != 2 {
		t.Fatalf("audit rows = %d, want 2", audit.Rows)
	}
	if audit.Bytes != int64(len(data)) {
		t.Fatalf("audit bytes = %d, want %d", audit.Bytes, len(data))
	}
	sum := sha256.Sum256(data)
	wantSHA := "sha256:" + hex.EncodeToString(sum[:])
	if audit.SHA256 != wantSHA {
		t.Fatalf("audit sha = %q, want %q", audit.SHA256, wantSHA)
	}
}

func TestAuditTiDBImportIntoS3SourceRecordsRowsBytesAndSHA(t *testing.T) {
	data := []byte("id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n2,Lin,00\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", r.Method)
		}
		if r.Header.Get("X-Amz-Content-Sha256") != "UNSIGNED-PAYLOAD" {
			t.Fatalf("X-Amz-Content-Sha256 = %q, want UNSIGNED-PAYLOAD", r.Header.Get("X-Amz-Content-Sha256"))
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SECRETEXAMPLE")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_S3_FORCE_PATH_STYLE", "true")

	audit, ok, err := auditTiDBImportIntoSource(context.Background(), "s3://migration-bucket/full/orders.csv")
	if err != nil {
		t.Fatalf("auditTiDBImportIntoSource() error = %v", err)
	}
	if !ok {
		t.Fatal("auditTiDBImportIntoSource() ok = false, want true")
	}
	if audit.Rows != 2 {
		t.Fatalf("audit rows = %d, want 2", audit.Rows)
	}
	if audit.Bytes != int64(len(data)) {
		t.Fatalf("audit bytes = %d, want %d", audit.Bytes, len(data))
	}
	sum := sha256.Sum256(data)
	wantSHA := "sha256:" + hex.EncodeToString(sum[:])
	if audit.SHA256 != wantSHA {
		t.Fatalf("audit sha = %q, want %q", audit.SHA256, wantSHA)
	}
}

func TestInspectTiDBImportIntoS3SourceDerivesFieldsAndAuditWithSingleGET(t *testing.T) {
	data := []byte("id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n2,Lin,00\n")
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", r.Method)
		}
		if r.URL.EscapedPath() != "/migration-bucket/full/orders.csv" {
			t.Fatalf("request path = %q, want path-style bucket/object path", r.URL.EscapedPath())
		}
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Fatalf("Accept-Encoding = %q, want identity", r.Header.Get("Accept-Encoding"))
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SECRETEXAMPLE")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_S3_FORCE_PATH_STYLE", "true")

	inspection, err := inspectTiDBImportIntoSource(context.Background(), "s3://migration-bucket/full/orders.csv", nil)
	if err != nil {
		t.Fatalf("inspectTiDBImportIntoSource() error = %v", err)
	}
	wantFields := []string{"id", "name", "@sqlserver2tidb_null_bitmap"}
	if !reflect.DeepEqual(inspection.Fields, wantFields) {
		t.Fatalf("inspection fields = %v, want %v", inspection.Fields, wantFields)
	}
	if !inspection.HasAudit {
		t.Fatal("inspection HasAudit = false, want true")
	}
	if inspection.Audit.Rows != 2 {
		t.Fatalf("audit rows = %d, want 2", inspection.Audit.Rows)
	}
	if inspection.Audit.Bytes != int64(len(data)) {
		t.Fatalf("audit bytes = %d, want %d", inspection.Audit.Bytes, len(data))
	}
	sum := sha256.Sum256(data)
	wantSHA := "sha256:" + hex.EncodeToString(sum[:])
	if inspection.Audit.SHA256 != wantSHA {
		t.Fatalf("audit sha = %q, want %q", inspection.Audit.SHA256, wantSHA)
	}
	if requests.Load() != 1 {
		t.Fatalf("S3 GET requests = %d, want 1", requests.Load())
	}
}

func TestInspectTiDBImportIntoGCSSourceDerivesFieldsAndAuditWithSingleGET(t *testing.T) {
	data := []byte("id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n2,Lin,00\n")
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", r.Method)
		}
		if r.URL.EscapedPath() != "/migration-bucket/full/orders.csv" {
			t.Fatalf("request path = %q, want path-style bucket/object path", r.URL.EscapedPath())
		}
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Fatalf("Accept-Encoding = %q, want identity", r.Header.Get("Accept-Encoding"))
		}
		assertOutputContains(t, r.Header.Get("Authorization"), "GOOG4-HMAC-SHA256 Credential=GOOGACCESS/")
		_, _ = w.Write(data)
	}))
	defer server.Close()
	t.Setenv("GCS_ACCESS_KEY_ID", "GOOGACCESS")
	t.Setenv("GCS_SECRET_ACCESS_KEY", "GOOGSECRET")
	t.Setenv("GCS_ENDPOINT_URL", server.URL)

	inspection, err := inspectTiDBImportIntoSource(context.Background(), "gs://migration-bucket/full/orders.csv", nil)
	if err != nil {
		t.Fatalf("inspectTiDBImportIntoSource() error = %v", err)
	}
	wantFields := []string{"id", "name", "@sqlserver2tidb_null_bitmap"}
	if !reflect.DeepEqual(inspection.Fields, wantFields) {
		t.Fatalf("inspection fields = %v, want %v", inspection.Fields, wantFields)
	}
	if !inspection.HasAudit {
		t.Fatal("inspection HasAudit = false, want true")
	}
	if inspection.Audit.Rows != 2 {
		t.Fatalf("audit rows = %d, want 2", inspection.Audit.Rows)
	}
	if inspection.Audit.Bytes != int64(len(data)) {
		t.Fatalf("audit bytes = %d, want %d", inspection.Audit.Bytes, len(data))
	}
	sum := sha256.Sum256(data)
	wantSHA := "sha256:" + hex.EncodeToString(sum[:])
	if inspection.Audit.SHA256 != wantSHA {
		t.Fatalf("audit sha = %q, want %q", inspection.Audit.SHA256, wantSHA)
	}
	if requests.Load() != 1 {
		t.Fatalf("GCS GET requests = %d, want 1", requests.Load())
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

func TestReadCSVImportGzipFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orders.csv.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create gzip fixture: %v", err)
	}
	gzipWriter := gzip.NewWriter(file)
	if _, err := gzipWriter.Write([]byte("id,name\n1,Ada\n2,Lin\n")); err != nil {
		t.Fatalf("write gzip fixture: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close gzip file: %v", err)
	}

	source, err := openCSVImportFileWithCompression("file://"+path, "gzip")
	if err != nil {
		t.Fatalf("openCSVImportFileWithCompression() error = %v", err)
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

func TestReadCSVImportHTTPSource(t *testing.T) {
	var acceptEncoding string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("HTTP method = %s, want GET", r.Method)
		}
		acceptEncoding = r.Header.Get("Accept-Encoding")
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
	if acceptEncoding != "identity" {
		t.Fatalf("Accept-Encoding = %q, want identity", acceptEncoding)
	}
}

func TestReadCSVImportHTTPSourceRetriesTransientStatus(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		if r.Method != http.MethodGet {
			t.Fatalf("HTTP method = %s, want GET", r.Method)
		}
		if request == 1 {
			http.Error(w, "temporary unavailable", http.StatusServiceUnavailable)
			return
		}
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
	if requests.Load() != 2 {
		t.Fatalf("HTTP GET requests = %d, want 2", requests.Load())
	}
}

func TestReadCSVImportS3Source(t *testing.T) {
	var method string
	var requestPath string
	var acceptEncoding string
	var authorization string
	var contentSHA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		requestPath = r.URL.EscapedPath()
		acceptEncoding = r.Header.Get("Accept-Encoding")
		authorization = r.Header.Get("Authorization")
		contentSHA = r.Header.Get("X-Amz-Content-Sha256")
		w.Header().Set("Content-Type", "text/csv")
		_, _ = io.WriteString(w, "id,name\n1,Ada\n2,Lin\n")
	}))
	defer server.Close()
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SECRETEXAMPLE")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_S3_FORCE_PATH_STYLE", "true")

	source, err := openCSVImportFile("s3://migration-bucket/full/orders.csv")
	if err != nil {
		t.Fatalf("openCSVImportFile() error = %v", err)
	}
	defer source.Close()

	columns, records, err := readCSVImportRecords(source)
	if err != nil {
		t.Fatalf("readCSVImportRecords() error = %v", err)
	}
	if method != http.MethodGet {
		t.Fatalf("method = %q, want GET", method)
	}
	if requestPath != "/migration-bucket/full/orders.csv" {
		t.Fatalf("request path = %q, want path-style bucket/object path", requestPath)
	}
	if acceptEncoding != "identity" {
		t.Fatalf("Accept-Encoding = %q, want identity", acceptEncoding)
	}
	if contentSHA != "UNSIGNED-PAYLOAD" {
		t.Fatalf("X-Amz-Content-Sha256 = %q, want UNSIGNED-PAYLOAD", contentSHA)
	}
	assertOutputContains(t, authorization, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/")
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

func TestReadCSVImportGCSSource(t *testing.T) {
	var method string
	var requestPath string
	var acceptEncoding string
	var authorization string
	var contentSHA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		requestPath = r.URL.EscapedPath()
		acceptEncoding = r.Header.Get("Accept-Encoding")
		authorization = r.Header.Get("Authorization")
		contentSHA = r.Header.Get("X-Goog-Content-Sha256")
		w.Header().Set("Content-Type", "text/csv")
		_, _ = io.WriteString(w, "id,name\n1,Ada\n2,Lin\n")
	}))
	defer server.Close()
	t.Setenv("GCS_ACCESS_KEY_ID", "GOOGACCESS")
	t.Setenv("GCS_SECRET_ACCESS_KEY", "GOOGSECRET")
	t.Setenv("GCS_ENDPOINT_URL", server.URL)

	source, err := openCSVImportFile("gs://migration-bucket/full/orders.csv")
	if err != nil {
		t.Fatalf("openCSVImportFile() error = %v", err)
	}
	defer source.Close()

	columns, records, err := readCSVImportRecords(source)
	if err != nil {
		t.Fatalf("readCSVImportRecords() error = %v", err)
	}
	if method != http.MethodGet {
		t.Fatalf("method = %q, want GET", method)
	}
	if requestPath != "/migration-bucket/full/orders.csv" {
		t.Fatalf("request path = %q, want path-style bucket/object path", requestPath)
	}
	if acceptEncoding != "identity" {
		t.Fatalf("Accept-Encoding = %q, want identity", acceptEncoding)
	}
	if contentSHA != "UNSIGNED-PAYLOAD" {
		t.Fatalf("X-Goog-Content-Sha256 = %q, want UNSIGNED-PAYLOAD", contentSHA)
	}
	assertOutputContains(t, authorization, "GOOG4-HMAC-SHA256 Credential=GOOGACCESS/")
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

func TestReadCSVImportAzureBlobSource(t *testing.T) {
	var method string
	var requestPath string
	var acceptEncoding string
	var authorization string
	var version string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		requestPath = r.URL.EscapedPath()
		acceptEncoding = r.Header.Get("Accept-Encoding")
		authorization = r.Header.Get("Authorization")
		version = r.Header.Get("X-Ms-Version")
		w.Header().Set("Content-Type", "text/csv")
		_, _ = io.WriteString(w, "id,name\n1,Ada\n2,Lin\n")
	}))
	defer server.Close()
	t.Setenv("AZURE_STORAGE_ACCOUNT", "devstoreaccount1")
	t.Setenv("AZURE_STORAGE_KEY", "c2VjcmV0")
	t.Setenv("AZURE_BLOB_ENDPOINT_URL", server.URL)

	source, err := openCSVImportFile("azblob://migration/full/orders.csv")
	if err != nil {
		t.Fatalf("openCSVImportFile() error = %v", err)
	}
	defer source.Close()

	columns, records, err := readCSVImportRecords(source)
	if err != nil {
		t.Fatalf("readCSVImportRecords() error = %v", err)
	}
	if method != http.MethodGet {
		t.Fatalf("method = %q, want GET", method)
	}
	if requestPath != "/migration/full/orders.csv" {
		t.Fatalf("request path = %q, want container/blob path", requestPath)
	}
	if acceptEncoding != "identity" {
		t.Fatalf("Accept-Encoding = %q, want identity", acceptEncoding)
	}
	if version == "" {
		t.Fatal("X-Ms-Version is empty")
	}
	assertOutputContains(t, authorization, "SharedKey devstoreaccount1:")
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

func TestReadCSVImportS3SourceRetriesTransientStatus(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", r.Method)
		}
		if r.URL.EscapedPath() != "/migration-bucket/full/orders.csv" {
			t.Fatalf("request path = %q, want path-style bucket/object path", r.URL.EscapedPath())
		}
		if request == 1 {
			http.Error(w, "slow down", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "id,name\n1,Ada\n2,Lin\n")
	}))
	defer server.Close()
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SECRETEXAMPLE")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_S3_FORCE_PATH_STYLE", "true")

	source, err := openCSVImportFile("s3://migration-bucket/full/orders.csv")
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
	if requests.Load() != 2 {
		t.Fatalf("S3 GET requests = %d, want 2", requests.Load())
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

func TestRunCDCEnableDryRunCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc-enable",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--capture-instance", "dbo_orders_custom",
		"--role-name", "cdc_reader",
		"--supports-net-changes",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("cdc-enable code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor cdc-enable dry run")
	assertOutputContains(t, output, "source cluster: prod-sqlserver-a")
	assertOutputContains(t, output, "project: sales-db-to-tidb-prod-a")
	assertOutputContains(t, output, "source object: sales.dbo.orders")
	assertOutputContains(t, output, "capture instance: dbo_orders_custom")
	assertOutputContains(t, output, "role name: cdc_reader")
	assertOutputContains(t, output, "supports net changes: true")
	assertOutputContains(t, output, "preflight checks: SQL Server Agent, CDC capture job, CDC cleanup job, retention, and permissions")
	assertOutputContains(t, output, "No SQL Server CDC enablement will be executed.")
}

func TestRunCDCEnableDryRunDerivesCaptureInstance(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc-enable",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("cdc-enable code = %d, stderr = %s", code, stderr.String())
	}
	assertOutputContains(t, stdout.String(), "capture instance: dbo_orders")
	assertOutputContains(t, stdout.String(), "supports net changes: false")
}

func TestRunCDCEnableExecutePrintsStatus(t *testing.T) {
	oldExecute := executeCDCEnableFunc
	t.Cleanup(func() {
		executeCDCEnableFunc = oldExecute
	})
	executeCDCEnableFunc = func(_ context.Context, spec cdcEnableSpec) (cdcEnableResult, error) {
		if spec.SourceObject != "sales.dbo.orders" {
			t.Fatalf("source object = %s, want sales.dbo.orders", spec.SourceObject)
		}
		if spec.CaptureInstance != "dbo_orders" {
			t.Fatalf("capture instance = %s, want dbo_orders", spec.CaptureInstance)
		}
		if spec.SourceConnectionStringEnv != "SQLSERVER_CDC_ADMIN_DSN" {
			t.Fatalf("source env = %s, want SQLSERVER_CDC_ADMIN_DSN", spec.SourceConnectionStringEnv)
		}
		if spec.RoleName != "" {
			t.Fatalf("role name = %q, want empty", spec.RoleName)
		}
		if spec.SupportsNetChanges {
			t.Fatalf("supports net changes = true, want false")
		}
		return cdcEnableResult{
			DatabaseCDCAlreadyEnabled: false,
			TableCDCAlreadyEnabled:    true,
			SQLServerAgentRunning:     true,
			CaptureJobPresent:         true,
			CleanupJobPresent:         true,
			RetentionMinutes:          10080,
			PermissionCheckPassed:     true,
		}, nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"cdc-enable",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--source-connection-string-env", "SQLSERVER_CDC_ADMIN_DSN",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("cdc-enable execute code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor cdc-enable completed: sales.dbo.orders")
	assertOutputContains(t, output, "capture instance: dbo_orders")
	assertOutputContains(t, output, "database cdc already enabled: false")
	assertOutputContains(t, output, "table cdc already enabled: true")
	assertOutputContains(t, output, "sql server agent running: true")
	assertOutputContains(t, output, "cdc capture job present: true")
	assertOutputContains(t, output, "cdc cleanup job present: true")
	assertOutputContains(t, output, "cdc cleanup retention minutes: 10080")
	assertOutputContains(t, output, "cdc admin permission check passed: true")
}

func TestBuildSQLServerCDCEnablePreflightQueries(t *testing.T) {
	queries, err := buildSQLServerCDCEnablePreflightQueries("sales.dbo.orders")
	if err != nil {
		t.Fatalf("buildSQLServerCDCEnablePreflightQueries() error = %v", err)
	}
	assertOutputContains(t, queries.SQLServerAgentStatus, "FROM sys.dm_server_services")
	assertOutputContains(t, queries.SQLServerAgentStatus, "SQL Server Agent")
	assertOutputContains(t, queries.CDCCaptureJobStatus, "FROM msdb.dbo.sysjobs")
	if queries.CaptureJobName != "cdc.sales_capture" {
		t.Fatalf("capture job name = %q, want cdc.sales_capture", queries.CaptureJobName)
	}
	if queries.CleanupJobName != "cdc.sales_cleanup" {
		t.Fatalf("cleanup job name = %q, want cdc.sales_cleanup", queries.CleanupJobName)
	}
	assertOutputContains(t, queries.CDCCleanupJobStatus, "FROM msdb.dbo.sysjobs")
	assertOutputContains(t, queries.CDCCleanupRetention, "FROM msdb.dbo.cdc_jobs")
	assertOutputContains(t, queries.CDCCleanupRetention, "job_type = N'cleanup'")
	assertOutputContains(t, queries.PermissionCheck, "IS_SRVROLEMEMBER('sysadmin')")
	assertOutputContains(t, queries.PermissionCheck, "IS_MEMBER('db_owner')")
}

func TestRunCDCDryRunRequiresLSNRange(t *testing.T) {
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
		"--apply-batch-size", "1000",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("cdc dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor cdc: from LSN is required")
}

func TestRunCDCDryRunRejectsReversedLSNRange(t *testing.T) {
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
		"--from-lsn", "0x00000027000001f40003",
		"--to-lsn", "0x00000027000001f40002",
		"--apply-batch-size", "1000",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("cdc dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor cdc: from LSN must be less than or equal to to LSN")
}

func TestRunCDCDryRunRejectsKeyColumnOutsideColumns(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc",
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
		t.Fatalf("cdc dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor cdc: CDC key column tenant_id is not present in captured columns")
}

func TestRunCDCDryRunRejectsInvalidSourceObject(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "orders",
		"--target-object", "app.orders",
		"--columns", "id,customer_name",
		"--key-columns", "id",
		"--from-lsn", "0x00000027000001f40001",
		"--to-lsn", "0x00000027000001f40002",
		"--apply-batch-size", "1000",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("cdc dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor cdc: source object must be schema.table or database.schema.table")
}

func TestRunCDCDryRunRejectsInvalidTargetObject(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.sales.orders",
		"--columns", "id,customer_name",
		"--key-columns", "id",
		"--from-lsn", "0x00000027000001f40001",
		"--to-lsn", "0x00000027000001f40002",
		"--apply-batch-size", "1000",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("cdc dry-run code = 0, want non-zero; stdout = %s", stdout.String())
	}
	assertOutputContains(t, stderr.String(), "executor cdc: target object must be table or database.table")
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
		if spec.ApplyBatchSize != 1000 {
			t.Fatalf("apply batch size = %d, want 1000", spec.ApplyBatchSize)
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

func TestBuildSQLServerCDCEnableStatements(t *testing.T) {
	enableDB, err := buildSQLServerCDCEnableDBStatement("sales.dbo.orders")
	if err != nil {
		t.Fatalf("buildSQLServerCDCEnableDBStatement() error = %v", err)
	}
	if enableDB != "EXEC [sales].[sys].[sp_cdc_enable_db]" {
		t.Fatalf("enable DB statement = %q", enableDB)
	}

	enableTable, err := buildSQLServerCDCEnableTableStatement("sales.dbo.orders")
	if err != nil {
		t.Fatalf("buildSQLServerCDCEnableTableStatement() error = %v", err)
	}
	wantTable := "EXEC [sales].[sys].[sp_cdc_enable_table] @source_schema = @source_schema, @source_name = @source_name, @role_name = @role_name, @capture_instance = @capture_instance, @supports_net_changes = @supports_net_changes"
	if enableTable != wantTable {
		t.Fatalf("enable table statement = %q, want %q", enableTable, wantTable)
	}

	currentDBEnable, err := buildSQLServerCDCEnableDBStatement("dbo.orders")
	if err != nil {
		t.Fatalf("buildSQLServerCDCEnableDBStatement(current db) error = %v", err)
	}
	if currentDBEnable != "EXEC sys.sp_cdc_enable_db" {
		t.Fatalf("current DB enable statement = %q", currentDBEnable)
	}
}

func TestBuildSQLServerCDCTableStatusQuery(t *testing.T) {
	query, err := buildSQLServerCDCTableStatusQuery("sales.dbo.orders")
	if err != nil {
		t.Fatalf("buildSQLServerCDCTableStatusQuery() error = %v", err)
	}
	want := "SELECT COUNT_BIG(1) FROM [sales].[cdc].[change_tables] AS ct JOIN [sales].[sys].[tables] AS t ON t.object_id = ct.source_object_id JOIN [sales].[sys].[schemas] AS s ON s.schema_id = t.schema_id WHERE s.name = @source_schema AND t.name = @source_name AND ct.capture_instance = @capture_instance"
	if query != want {
		t.Fatalf("table status query = %q, want %q", query, want)
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
		SourceObject:   "sales.dbo.orders",
		TargetObject:   "app.orders",
		Columns:        []string{"id", "customer_name"},
		KeyColumns:     []string{"id"},
		FromLSN:        fromLSN,
		ToLSN:          toLSN,
		ApplyBatchSize: 1000,
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

func TestApplySQLServerCDCChangesFlushesBatchesWhileStreaming(t *testing.T) {
	source := &recordingCDCSource{
		rows: &fakeExportRows{
			columns: []string{"__$operation", "__$start_lsn", "__$seqval", "id", "customer_name"},
			values: [][]any{
				{int64(2), []byte{0x01}, []byte{0x01}, int64(1), "Ada"},
				{int64(4), []byte{0x02}, []byte{0x01}, int64(2), "Lin"},
				{int64(2), []byte{0x03}, []byte{0x01}, int64(3), "Grace"},
			},
			scanErrAt: 3,
			scanErr:   errors.New("scan failed after first batch"),
		},
	}
	target := &recordingMultiCDCExecutor{}

	applied, err := applySQLServerCDCChanges(context.Background(), source, target, cdcApplySpec{
		SourceObject:   "sales.dbo.orders",
		TargetObject:   "app.orders",
		Columns:        []string{"id", "customer_name"},
		KeyColumns:     []string{"id"},
		FromLSN:        []byte{0x00, 0x00, 0x00, 0x27, 0x00, 0x00, 0x01, 0xf4, 0x00, 0x01},
		ToLSN:          []byte{0x00, 0x00, 0x00, 0x27, 0x00, 0x00, 0x01, 0xf4, 0x00, 0x02},
		ApplyBatchSize: 2,
	})
	if err == nil {
		t.Fatal("applySQLServerCDCChanges() error = nil, want scan error")
	}
	assertOutputContains(t, err.Error(), "scan failed after first batch")
	if applied != 2 {
		t.Fatalf("applied = %d, want first flushed batch count 2", applied)
	}
	if len(target.calls) != 2 {
		t.Fatalf("target calls = %d, want first batch flushed before later scan error", len(target.calls))
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

func TestRunExportExecuteRejectsUnsupportedOutputURI(t *testing.T) {
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
		"--output-uri", "ftp://migration/prod/full/dbo.orders.000001.csv",
		"--source-connection-string-env", "MISSING_SQLSERVER_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("export execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor export: only file://, http://, https://, s3://, gs://, and azblob:// output URIs are supported for CSV export")
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

func TestRunExportExecutePreflightsS3CredentialsBeforeConnectionStringEnv(t *testing.T) {
	var stdout, stderr bytes.Buffer
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")

	code := Run([]string{
		"export",
		"--execute",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--chunk-id", "dbo.orders.000001",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--output-uri", "s3://migration-bucket/full/dbo.orders.000001.csv",
		"--source-connection-string-env", "MISSING_SQLSERVER_DSN",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("export execute code = 0, want non-zero")
	}
	assertOutputContains(t, stderr.String(), "executor export: prepare output URI: AWS_ACCESS_KEY_ID is required for s3 export output")
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

	exportedRows, err := writeCSVExportRows(&output, rows)
	if err != nil {
		t.Fatalf("writeCSVExportRows() error = %v", err)
	}
	if exportedRows != 2 {
		t.Fatalf("exported rows = %d, want 2", exportedRows)
	}

	want := "id,name,active,__sqlserver2tidb_null_bitmap\n1,Ada,true,000\n2,,false,010\n"
	if output.String() != want {
		t.Fatalf("CSV output = %q, want %q", output.String(), want)
	}
	if !rows.closed {
		t.Fatalf("rows closed = false, want true")
	}
}

func TestWriteCSVExportRowsWithBackslashNNullEncoding(t *testing.T) {
	var output bytes.Buffer
	rows := &fakeExportRows{
		columns: []string{"id", "name", "active"},
		values: [][]any{
			{int64(1), "Ada", true},
			{int64(2), nil, false},
		},
	}

	exportedRows, err := writeCSVExportRowsWithNullEncoding(&output, rows, nullEncodingBackslashN)
	if err != nil {
		t.Fatalf("writeCSVExportRowsWithNullEncoding() error = %v", err)
	}
	if exportedRows != 2 {
		t.Fatalf("exported rows = %d, want 2", exportedRows)
	}

	want := "id,name,active\n1,Ada,true\n2,\\N,false\n"
	if output.String() != want {
		t.Fatalf("CSV output = %q, want %q", output.String(), want)
	}
	if !rows.closed {
		t.Fatalf("rows closed = false, want true")
	}
}

func TestCSVExportOutputRecordsSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbo.orders.000001.csv")
	output, err := openCSVExportOutput(context.Background(), exportOutputURI{
		scheme: "file",
		path:   path,
	}, compressionNone)
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

	if _, err := writeCSVExportRows(output, rows); err != nil {
		t.Fatalf("writeCSVExportRows() error = %v", err)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("output.Close() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	sum := sha256.Sum256(data)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if output.SHA256() != want {
		t.Fatalf("output SHA256 = %q, want %q", output.SHA256(), want)
	}
}

func TestCSVExportOutputPublishesLocalFileOnlyAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbo.orders.000001.csv")
	output, err := openCSVExportOutput(context.Background(), exportOutputURI{
		scheme: "file",
		path:   path,
	}, compressionNone)
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

	if _, err := writeCSVExportRows(output, rows); err != nil {
		t.Fatalf("writeCSVExportRows() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target file stat before close error = %v, want not exist", err)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("output.Close() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read target file after close: %v", err)
	}
	want := "id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n2,,01\n"
	if string(data) != want {
		t.Fatalf("target file = %q, want %q", string(data), want)
	}
}

func TestCSVExportOutputAbortRemovesLocalTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dbo.orders.000001.csv")
	output, err := openCSVExportOutput(context.Background(), exportOutputURI{
		scheme: "file",
		path:   path,
	}, compressionNone)
	if err != nil {
		t.Fatalf("openCSVExportOutput() error = %v", err)
	}
	if _, err := output.Write([]byte("partial csv")); err != nil {
		t.Fatalf("output.Write() error = %v", err)
	}
	if err := output.Abort(); err != nil {
		t.Fatalf("output.Abort() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target file stat after abort error = %v, want not exist", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".dbo.orders.000001.csv.*.tmp"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temp files after abort = %v, want none", matches)
	}
}

func TestOpenCSVExportOutputInvalidCompressionDoesNotPublishLocalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dbo.orders.000001.csv")
	_, err := openCSVExportOutput(context.Background(), exportOutputURI{
		scheme: "file",
		path:   path,
	}, "brotli")
	if err == nil {
		t.Fatal("openCSVExportOutput() error = nil, want unsupported compression error")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target file stat after open error = %v, want not exist", statErr)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".dbo.orders.000001.csv.*.tmp"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temp files after open error = %v, want none", matches)
	}
}

func TestWriteCSVExportRowsHTTPOutput(t *testing.T) {
	var method string
	var contentType string
	var body bytes.Buffer
	requestStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestStarted <- struct{}{}
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
	}, compressionNone)
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

	exportedRows, err := writeCSVExportRows(output, rows)
	if err != nil {
		t.Fatalf("writeCSVExportRows() error = %v", err)
	}
	if exportedRows != 2 {
		t.Fatalf("exported rows = %d, want 2", exportedRows)
	}
	startedBeforeClose := false
	select {
	case <-requestStarted:
		startedBeforeClose = true
	case <-time.After(100 * time.Millisecond):
	}
	if err := output.Close(); err != nil {
		t.Fatalf("output.Close() error = %v", err)
	}
	if !startedBeforeClose {
		select {
		case <-requestStarted:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for HTTP upload")
		}
	}
	if startedBeforeClose {
		t.Fatal("HTTP upload started before output.Close(), want close-time upload")
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

func TestWriteCSVExportRowsHTTPOutputRetriesTransientStatus(t *testing.T) {
	var requests atomic.Int64
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		if r.Method != http.MethodPut {
			t.Fatalf("HTTP method = %s, want PUT", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		bodies = append(bodies, string(body))
		if request == 1 {
			http.Error(w, "temporary unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	output, err := openCSVExportOutput(context.Background(), exportOutputURI{
		scheme: "http",
		uri:    server.URL + "/dbo.orders.000001.csv",
	}, compressionNone)
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

	exportedRows, err := writeCSVExportRows(output, rows)
	if err != nil {
		t.Fatalf("writeCSVExportRows() error = %v", err)
	}
	if exportedRows != 2 {
		t.Fatalf("exported rows = %d, want 2", exportedRows)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("output.Close() error = %v", err)
	}

	wantBody := "id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n2,,01\n"
	if requests.Load() != 2 {
		t.Fatalf("HTTP PUT requests = %d, want 2", requests.Load())
	}
	if !reflect.DeepEqual(bodies, []string{wantBody, wantBody}) {
		t.Fatalf("request bodies = %q, want two complete CSV payloads", bodies)
	}
}

func TestWriteCSVExportRowsS3Output(t *testing.T) {
	var method string
	var requestPath string
	var authorization string
	var contentSHA string
	var securityToken string
	var body bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		requestPath = r.URL.EscapedPath()
		authorization = r.Header.Get("Authorization")
		contentSHA = r.Header.Get("X-Amz-Content-Sha256")
		securityToken = r.Header.Get("X-Amz-Security-Token")
		if _, err := io.Copy(&body, r.Body); err != nil {
			t.Fatalf("read request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SECRETEXAMPLE")
	t.Setenv("AWS_SESSION_TOKEN", "SESSIONEXAMPLE")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_S3_FORCE_PATH_STYLE", "true")

	output, err := openCSVExportOutput(context.Background(), exportOutputURI{
		scheme: "s3",
		uri:    "s3://migration-bucket/full/dbo.orders.000001.csv",
	}, compressionNone)
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

	exportedRows, err := writeCSVExportRows(output, rows)
	if err != nil {
		t.Fatalf("writeCSVExportRows() error = %v", err)
	}
	if exportedRows != 2 {
		t.Fatalf("exported rows = %d, want 2", exportedRows)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("output.Close() error = %v", err)
	}

	if method != http.MethodPut {
		t.Fatalf("method = %q, want PUT", method)
	}
	if requestPath != "/migration-bucket/full/dbo.orders.000001.csv" {
		t.Fatalf("request path = %q, want path-style bucket/object path", requestPath)
	}
	wantBody := "id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n2,,01\n"
	if body.String() != wantBody {
		t.Fatalf("request body = %q, want %q", body.String(), wantBody)
	}
	sum := sha256.Sum256([]byte(wantBody))
	if contentSHA != hex.EncodeToString(sum[:]) {
		t.Fatalf("X-Amz-Content-Sha256 = %q, want payload hash", contentSHA)
	}
	if securityToken != "SESSIONEXAMPLE" {
		t.Fatalf("X-Amz-Security-Token = %q, want session token", securityToken)
	}
	assertOutputContains(t, authorization, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/")
	assertOutputContains(t, authorization, "SignedHeaders=")
	assertOutputContains(t, authorization, "Signature=")
}

func TestWriteCSVExportRowsGCSOutput(t *testing.T) {
	var method string
	var requestPath string
	var authorization string
	var contentSHA string
	var body bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		requestPath = r.URL.EscapedPath()
		authorization = r.Header.Get("Authorization")
		contentSHA = r.Header.Get("X-Goog-Content-Sha256")
		if _, err := io.Copy(&body, r.Body); err != nil {
			t.Fatalf("read request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("GCS_ACCESS_KEY_ID", "GOOGACCESS")
	t.Setenv("GCS_SECRET_ACCESS_KEY", "GOOGSECRET")
	t.Setenv("GCS_ENDPOINT_URL", server.URL)

	output, err := openCSVExportOutput(context.Background(), exportOutputURI{
		scheme: "gs",
		uri:    "gs://migration-bucket/full/dbo.orders.000001.csv",
	}, compressionNone)
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

	exportedRows, err := writeCSVExportRows(output, rows)
	if err != nil {
		t.Fatalf("writeCSVExportRows() error = %v", err)
	}
	if exportedRows != 2 {
		t.Fatalf("exported rows = %d, want 2", exportedRows)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("output.Close() error = %v", err)
	}

	if method != http.MethodPut {
		t.Fatalf("method = %q, want PUT", method)
	}
	if requestPath != "/migration-bucket/full/dbo.orders.000001.csv" {
		t.Fatalf("request path = %q, want path-style bucket/object path", requestPath)
	}
	wantBody := "id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n2,,01\n"
	if body.String() != wantBody {
		t.Fatalf("request body = %q, want %q", body.String(), wantBody)
	}
	sum := sha256.Sum256([]byte(wantBody))
	if contentSHA != hex.EncodeToString(sum[:]) {
		t.Fatalf("X-Goog-Content-Sha256 = %q, want payload hash", contentSHA)
	}
	assertOutputContains(t, authorization, "GOOG4-HMAC-SHA256 Credential=GOOGACCESS/")
	assertOutputContains(t, authorization, "SignedHeaders=")
	assertOutputContains(t, authorization, "Signature=")
}

func TestWriteCSVExportRowsAzureBlobOutput(t *testing.T) {
	var method string
	var requestPath string
	var authorization string
	var blobType string
	var version string
	var body bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		requestPath = r.URL.EscapedPath()
		authorization = r.Header.Get("Authorization")
		blobType = r.Header.Get("X-Ms-Blob-Type")
		version = r.Header.Get("X-Ms-Version")
		if _, err := io.Copy(&body, r.Body); err != nil {
			t.Fatalf("read request body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	t.Setenv("AZURE_STORAGE_ACCOUNT", "devstoreaccount1")
	t.Setenv("AZURE_STORAGE_KEY", "c2VjcmV0")
	t.Setenv("AZURE_BLOB_ENDPOINT_URL", server.URL)

	output, err := openCSVExportOutput(context.Background(), exportOutputURI{
		scheme: "azblob",
		uri:    "azblob://migration/full/dbo.orders.000001.csv",
	}, compressionNone)
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

	exportedRows, err := writeCSVExportRows(output, rows)
	if err != nil {
		t.Fatalf("writeCSVExportRows() error = %v", err)
	}
	if exportedRows != 2 {
		t.Fatalf("exported rows = %d, want 2", exportedRows)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("output.Close() error = %v", err)
	}

	if method != http.MethodPut {
		t.Fatalf("method = %q, want PUT", method)
	}
	if requestPath != "/migration/full/dbo.orders.000001.csv" {
		t.Fatalf("request path = %q, want container/blob path", requestPath)
	}
	if blobType != "BlockBlob" {
		t.Fatalf("X-Ms-Blob-Type = %q, want BlockBlob", blobType)
	}
	if version == "" {
		t.Fatal("X-Ms-Version is empty")
	}
	wantBody := "id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n2,,01\n"
	if body.String() != wantBody {
		t.Fatalf("request body = %q, want %q", body.String(), wantBody)
	}
	assertOutputContains(t, authorization, "SharedKey devstoreaccount1:")
}

func TestWriteCSVExportRowsS3OutputRetriesTransientStatus(t *testing.T) {
	var requests atomic.Int64
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		if r.Method != http.MethodPut {
			t.Fatalf("method = %q, want PUT", r.Method)
		}
		if r.URL.EscapedPath() != "/migration-bucket/full/dbo.orders.000001.csv" {
			t.Fatalf("request path = %q, want path-style bucket/object path", r.URL.EscapedPath())
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		bodies = append(bodies, string(body))
		if request == 1 {
			http.Error(w, "slow down", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SECRETEXAMPLE")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_S3_FORCE_PATH_STYLE", "true")

	output, err := openCSVExportOutput(context.Background(), exportOutputURI{
		scheme: "s3",
		uri:    "s3://migration-bucket/full/dbo.orders.000001.csv",
	}, compressionNone)
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

	exportedRows, err := writeCSVExportRows(output, rows)
	if err != nil {
		t.Fatalf("writeCSVExportRows() error = %v", err)
	}
	if exportedRows != 2 {
		t.Fatalf("exported rows = %d, want 2", exportedRows)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("output.Close() error = %v", err)
	}

	wantBody := "id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n2,,01\n"
	if requests.Load() != 2 {
		t.Fatalf("S3 PUT requests = %d, want 2", requests.Load())
	}
	if !reflect.DeepEqual(bodies, []string{wantBody, wantBody}) {
		t.Fatalf("request bodies = %q, want two complete CSV payloads", bodies)
	}
}

func TestWriteCSVExportRowsHTTPGzipOutput(t *testing.T) {
	var contentEncoding string
	var body bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentEncoding = r.Header.Get("Content-Encoding")
		if _, err := io.Copy(&body, r.Body); err != nil {
			t.Fatalf("read request body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	output, err := openCSVExportOutput(context.Background(), exportOutputURI{
		scheme: "http",
		uri:    server.URL + "/dbo.orders.000001.csv.gz",
	}, compressionGzip)
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

	exportedRows, err := writeCSVExportRows(output, rows)
	if err != nil {
		t.Fatalf("writeCSVExportRows() error = %v", err)
	}
	if exportedRows != 2 {
		t.Fatalf("exported rows = %d, want 2", exportedRows)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("output.Close() error = %v", err)
	}

	if contentEncoding != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", contentEncoding)
	}
	gzipReader, err := gzip.NewReader(bytes.NewReader(body.Bytes()))
	if err != nil {
		t.Fatalf("open gzip HTTP body: %v", err)
	}
	decompressed, err := io.ReadAll(gzipReader)
	if err != nil {
		t.Fatalf("read gzip HTTP body: %v", err)
	}
	if err := gzipReader.Close(); err != nil {
		t.Fatalf("close gzip reader: %v", err)
	}
	want := "id,name,__sqlserver2tidb_null_bitmap\n1,Ada,00\n2,,01\n"
	if string(decompressed) != want {
		t.Fatalf("decompressed HTTP body = %q, want %q", string(decompressed), want)
	}
}

func TestCSVExportOutputAbortDoesNotStartHTTPUpload(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestStarted <- struct{}{}
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	output, err := openCSVExportOutput(context.Background(), exportOutputURI{
		scheme: "http",
		uri:    server.URL + "/dbo.orders.000001.csv",
	}, compressionNone)
	if err != nil {
		t.Fatalf("openCSVExportOutput() error = %v", err)
	}

	if _, err := output.Write([]byte("partial csv")); err != nil {
		t.Fatalf("output.Write() error = %v", err)
	}
	if err := output.Abort(); err != nil {
		t.Fatalf("output.Abort() error = %v", err)
	}

	select {
	case <-requestStarted:
		t.Fatal("HTTP upload started after abort, want no request")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestObjectExportWriterNilWriteReturnsError(t *testing.T) {
	var writer *objectExportWriter

	_, err := writer.Write([]byte("csv"))
	if err == nil {
		t.Fatal("Write() error = nil, want closed writer error")
	}
	if !strings.Contains(err.Error(), "object export writer is closed") {
		t.Fatalf("Write() error = %v, want closed object writer error", err)
	}
}

type fakeExportRows struct {
	columns   []string
	values    [][]any
	idx       int
	closed    bool
	scanErrAt int
	scanErr   error
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
	if rows.scanErrAt > 0 && rows.idx+1 == rows.scanErrAt {
		return rows.scanErr
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
