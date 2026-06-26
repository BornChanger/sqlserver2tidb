package executor

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		"--output-uri", "s3://migration/prod/full/dbo.orders.000001.parquet",
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
	assertOutputContains(t, output, "output uri: s3://migration/prod/full/dbo.orders.000001.parquet")
	assertOutputContains(t, output, "predicate: id >= 1 AND id < 1000")
	assertOutputContains(t, output, "No SQL Server connection will be opened.")
	assertOutputContains(t, output, "No object storage write will be attempted.")
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
		"--source-uri", "s3://migration/prod/full/dbo.orders.000001.parquet",
		"--depends-on-export-chunk", "dbo.orders.000001",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("import code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor import dry run")
	assertOutputContains(t, output, "job id: import-dbo.orders.000001")
	assertOutputContains(t, output, "target object: app.orders")
	assertOutputContains(t, output, "source uri: s3://migration/prod/full/dbo.orders.000001.parquet")
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
	assertOutputContains(t, stderr.String(), "executor import: only file:// source URIs are supported for --execute")
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

func TestRunCDCDryRunCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"cdc",
		"--root", ".",
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--source-object", "sales.dbo.orders",
		"--target-object", "app.orders",
		"--apply-batch-size", "1000",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("cdc code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	assertOutputContains(t, output, "executor cdc dry run")
	assertOutputContains(t, output, "source object: sales.dbo.orders")
	assertOutputContains(t, output, "target object: app.orders")
	assertOutputContains(t, output, "apply batch size: 1000")
	assertOutputContains(t, output, "No CDC reader or TiDB apply worker will be started.")
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
	assertOutputContains(t, stderr.String(), "executor export: only file:// output URIs are supported for --execute")
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
