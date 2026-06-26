package executor

import (
	"bytes"
	"errors"
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

	want := "id,name,active\n1,Ada,true\n2,,false\n"
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
