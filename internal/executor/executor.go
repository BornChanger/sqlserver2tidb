package executor

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BornChanger/sqlserver2tidb/internal/buildinfo"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/microsoft/go-mssqldb"
)

const defaultSourceConnectionStringEnv = "SQLSERVER2TIDB_SOURCE_CONNECTION_STRING"
const defaultTargetConnectionStringEnv = "SQLSERVER2TIDB_TARGET_CONNECTION_STRING"

const (
	importEngineSQLInsert      = "sql-insert"
	importEngineTiDBImportInto = "tidb-import-into"
	importEngineImportInto     = "import-into"
)

var csvHTTPClient = &http.Client{Timeout: 10 * time.Minute}

func normalizeImportEngine(engine string) (string, error) {
	engine = strings.ToLower(strings.TrimSpace(engine))
	switch engine {
	case "":
		return importEngineSQLInsert, nil
	case importEngineSQLInsert:
		return importEngineSQLInsert, nil
	case importEngineTiDBImportInto, importEngineImportInto:
		return importEngineTiDBImportInto, nil
	default:
		return "", fmt.Errorf("executor import: import engine %s is not supported; supported engines: %s, %s", engine, importEngineSQLInsert, importEngineTiDBImportInto)
	}
}

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "apply-ddl":
		return runApplyDDL(args[1:], stdout, stderr)
	case "export":
		return runExport(args[1:], stdout, stderr)
	case "import":
		return runImport(args[1:], stdout, stderr)
	case "validate-count":
		return runValidateCount(args[1:], stdout, stderr)
	case "validate-query":
		return runValidateQuery(args[1:], stdout, stderr)
	case "cdc":
		return runCDC(args[1:], stdout, stderr)
	case "version", "-v", "--version":
		fmt.Fprint(stdout, buildinfo.Format("sqlserver2tidb-executor"))
		return 0
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown executor command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runApplyDDL(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("apply-ddl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	ddlFile := fs.String("ddl-file", "", "DDL SQL file to apply")
	targetConnectionStringEnv := fs.String("target-connection-string-env", defaultTargetConnectionStringEnv, "environment variable containing the TiDB/MySQL connection string")
	execute := fs.Bool("execute", false, "apply the DDL")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := requireFields("executor apply-ddl",
		field{"source cluster id", *sourceClusterID},
		field{"project id", *projectID},
		field{"ddl file", *ddlFile},
	); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *execute {
		if err := executeTiDBDDL(context.Background(), applyDDLExecuteSpec{
			Root:                      *root,
			DDLFile:                   *ddlFile,
			TargetConnectionStringEnv: *targetConnectionStringEnv,
		}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "executor apply-ddl completed: %s\n", *ddlFile)
		return 0
	}

	fmt.Fprintln(stdout, "executor apply-ddl dry run")
	fmt.Fprintf(stdout, "root: %s\n", *root)
	fmt.Fprintf(stdout, "source cluster: %s\n", *sourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", *projectID)
	fmt.Fprintf(stdout, "ddl file: %s\n", *ddlFile)
	fmt.Fprintln(stdout, "No TiDB connection will be opened.")
	return 0
}

type applyDDLExecuteSpec struct {
	Root                      string
	DDLFile                   string
	TargetConnectionStringEnv string
}

func executeTiDBDDL(ctx context.Context, spec applyDDLExecuteSpec) error {
	ddlPath := spec.DDLFile
	if !filepath.IsAbs(ddlPath) {
		ddlPath = filepath.Join(spec.Root, filepath.FromSlash(ddlPath))
	}
	data, err := os.ReadFile(ddlPath)
	if err != nil {
		return fmt.Errorf("executor apply-ddl: read DDL file: %w", err)
	}
	if containsTODO(string(data)) {
		return fmt.Errorf("executor apply-ddl: DDL file still contains TODO")
	}
	statements := splitSQLStatements(string(data))
	if len(statements) == 0 {
		return fmt.Errorf("executor apply-ddl: DDL file contains no SQL statements")
	}

	envName := strings.TrimSpace(spec.TargetConnectionStringEnv)
	if envName == "" {
		envName = defaultTargetConnectionStringEnv
	}
	connectionString := strings.TrimSpace(os.Getenv(envName))
	if connectionString == "" {
		return fmt.Errorf("executor apply-ddl: target connection string env %s is not set", envName)
	}

	db, err := sql.Open("mysql", connectionString)
	if err != nil {
		return fmt.Errorf("executor apply-ddl: open TiDB connection: %w", err)
	}
	defer db.Close()

	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("executor apply-ddl: execute DDL: %w", err)
		}
	}
	return nil
}

func splitSQLStatements(script string) []string {
	var statements []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	inBacktick := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(script); i++ {
		ch := script[i]

		if inLineComment {
			current.WriteByte(ch)
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			current.WriteByte(ch)
			if ch == '*' && i+1 < len(script) && script[i+1] == '/' {
				current.WriteByte(script[i+1])
				i++
				inBlockComment = false
			}
			continue
		}
		if inSingleQuote {
			current.WriteByte(ch)
			if ch == '\'' {
				if i+1 < len(script) && script[i+1] == '\'' {
					current.WriteByte(script[i+1])
					i++
					continue
				}
				if !isEscapedByBackslash(script, i) {
					inSingleQuote = false
				}
			}
			continue
		}
		if inDoubleQuote {
			current.WriteByte(ch)
			if ch == '"' {
				if i+1 < len(script) && script[i+1] == '"' {
					current.WriteByte(script[i+1])
					i++
					continue
				}
				if !isEscapedByBackslash(script, i) {
					inDoubleQuote = false
				}
			}
			continue
		}
		if inBacktick {
			current.WriteByte(ch)
			if ch == '`' {
				if i+1 < len(script) && script[i+1] == '`' {
					current.WriteByte(script[i+1])
					i++
					continue
				}
				inBacktick = false
			}
			continue
		}

		switch {
		case ch == '-' && i+1 < len(script) && script[i+1] == '-':
			current.WriteByte(ch)
			current.WriteByte(script[i+1])
			i++
			inLineComment = true
		case ch == '#':
			current.WriteByte(ch)
			inLineComment = true
		case ch == '/' && i+1 < len(script) && script[i+1] == '*':
			current.WriteByte(ch)
			current.WriteByte(script[i+1])
			i++
			inBlockComment = true
		case ch == '\'':
			current.WriteByte(ch)
			inSingleQuote = true
		case ch == '"':
			current.WriteByte(ch)
			inDoubleQuote = true
		case ch == '`':
			current.WriteByte(ch)
			inBacktick = true
		case ch == ';':
			statement := strings.TrimSpace(current.String())
			if statement != "" {
				statements = append(statements, statement)
			}
			current.Reset()
		default:
			current.WriteByte(ch)
		}
	}

	statement := strings.TrimSpace(current.String())
	if statement != "" {
		statements = append(statements, statement)
	}
	return statements
}

func isEscapedByBackslash(value string, index int) bool {
	backslashes := 0
	for i := index - 1; i >= 0 && value[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func containsTODO(value string) bool {
	return strings.Contains(strings.ToUpper(value), "TODO")
}

func runExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	chunkID := fs.String("chunk-id", "", "export chunk id")
	sourceObject := fs.String("source-object", "", "source object")
	targetObject := fs.String("target-object", "", "target object")
	outputURI := fs.String("output-uri", "", "export output URI")
	predicate := fs.String("predicate", "", "source split predicate")
	sourceConnectionStringEnv := fs.String("source-connection-string-env", defaultSourceConnectionStringEnv, "environment variable containing the SQL Server connection string")
	execute := fs.Bool("execute", false, "perform the export")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := requireFields("executor export",
		field{"source cluster id", *sourceClusterID},
		field{"project id", *projectID},
		field{"chunk id", *chunkID},
		field{"source object", *sourceObject},
		field{"target object", *targetObject},
		field{"output uri", *outputURI},
	); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *execute {
		if err := executeSQLServerExport(context.Background(), exportExecuteSpec{
			SourceObject:              *sourceObject,
			OutputURI:                 *outputURI,
			Predicate:                 *predicate,
			SourceConnectionStringEnv: *sourceConnectionStringEnv,
		}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "executor export completed: %s -> %s\n", *sourceObject, *outputURI)
		return 0
	}

	fmt.Fprintln(stdout, "executor export dry run")
	fmt.Fprintf(stdout, "root: %s\n", *root)
	fmt.Fprintf(stdout, "source cluster: %s\n", *sourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", *projectID)
	fmt.Fprintf(stdout, "chunk id: %s\n", *chunkID)
	fmt.Fprintf(stdout, "source object: %s\n", *sourceObject)
	fmt.Fprintf(stdout, "target object: %s\n", *targetObject)
	fmt.Fprintf(stdout, "output uri: %s\n", *outputURI)
	if strings.TrimSpace(*predicate) != "" {
		fmt.Fprintf(stdout, "predicate: %s\n", *predicate)
	}
	fmt.Fprintln(stdout, "No SQL Server connection will be opened.")
	fmt.Fprintln(stdout, "No CSV output write will be attempted.")
	return 0
}

type exportExecuteSpec struct {
	SourceObject              string
	OutputURI                 string
	Predicate                 string
	SourceConnectionStringEnv string
}

func executeSQLServerExport(ctx context.Context, spec exportExecuteSpec) error {
	if strings.Contains(strings.ToUpper(spec.Predicate), "TODO") {
		return fmt.Errorf("executor export: predicate still contains TODO")
	}

	output, err := parseExportOutputURI(spec.OutputURI)
	if err != nil {
		return fmt.Errorf("executor export: %w", err)
	}
	if err := prepareExportOutputURI(output); err != nil {
		return fmt.Errorf("executor export: create output directory: %w", err)
	}

	envName := strings.TrimSpace(spec.SourceConnectionStringEnv)
	if envName == "" {
		envName = defaultSourceConnectionStringEnv
	}
	connectionString := strings.TrimSpace(os.Getenv(envName))
	if connectionString == "" {
		return fmt.Errorf("executor export: source connection string env %s is not set", envName)
	}

	query, err := buildSQLServerExportQuery(spec.SourceObject, spec.Predicate)
	if err != nil {
		return fmt.Errorf("executor export: %w", err)
	}

	db, err := sql.Open("sqlserver", connectionString)
	if err != nil {
		return fmt.Errorf("executor export: open SQL Server connection: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("executor export: query source object %s: %w", spec.SourceObject, err)
	}

	outputWriter, err := openCSVExportOutput(ctx, output)
	if err != nil {
		rows.Close()
		return fmt.Errorf("executor export: %w", err)
	}
	if err := writeCSVExportRows(outputWriter, rows); err != nil {
		outputWriter.Close()
		return fmt.Errorf("executor export: write CSV output: %w", err)
	}
	if err := outputWriter.Close(); err != nil {
		return fmt.Errorf("executor export: close CSV output: %w", err)
	}
	return nil
}

func resolveExportOutputFile(outputURI string) (string, error) {
	output, err := parseExportOutputURI(outputURI)
	if err != nil {
		return "", err
	}
	if output.scheme != "file" {
		return "", fmt.Errorf("output URI is not a file:// URI")
	}
	return output.path, nil
}

type exportOutputURI struct {
	scheme string
	path   string
	uri    string
}

func parseExportOutputURI(outputURI string) (exportOutputURI, error) {
	parsed, err := url.Parse(strings.TrimSpace(outputURI))
	if err != nil {
		return exportOutputURI{}, fmt.Errorf("parse output uri: %w", err)
	}
	switch parsed.Scheme {
	case "file":
		if parsed.Host != "" && parsed.Host != "localhost" {
			return exportOutputURI{}, fmt.Errorf("file output URI host must be empty or localhost")
		}
		if strings.TrimSpace(parsed.Path) == "" {
			return exportOutputURI{}, fmt.Errorf("file output URI path is required")
		}
		return exportOutputURI{
			scheme: parsed.Scheme,
			path:   filepath.Clean(parsed.Path),
			uri:    parsed.String(),
		}, nil
	case "http", "https":
		if strings.TrimSpace(parsed.Host) == "" {
			return exportOutputURI{}, fmt.Errorf("%s output URI host is required", parsed.Scheme)
		}
		return exportOutputURI{
			scheme: parsed.Scheme,
			uri:    parsed.String(),
		}, nil
	default:
		return exportOutputURI{}, fmt.Errorf("only file://, http://, and https:// output URIs are supported for --execute")
	}
}

func prepareExportOutputURI(output exportOutputURI) error {
	if output.scheme != "file" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(output.path), 0o755); err != nil {
		return err
	}
	return nil
}

func openCSVExportOutput(ctx context.Context, output exportOutputURI) (io.WriteCloser, error) {
	switch output.scheme {
	case "file":
		file, err := os.Create(output.path)
		if err != nil {
			return nil, fmt.Errorf("create output file: %w", err)
		}
		return file, nil
	case "http", "https":
		return newHTTPExportWriter(ctx, output.uri)
	default:
		return nil, fmt.Errorf("unsupported output URI scheme %q", output.scheme)
	}
}

type httpExportWriter struct {
	writer *io.PipeWriter
	done   chan error
}

func newHTTPExportWriter(ctx context.Context, outputURI string) (*httpExportWriter, error) {
	reader, writer := io.Pipe()
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, outputURI, reader)
	if err != nil {
		reader.Close()
		writer.Close()
		return nil, fmt.Errorf("create CSV output request: %w", err)
	}
	request.Header.Set("Content-Type", "text/csv")

	done := make(chan error, 1)
	go func() {
		response, err := csvHTTPClient.Do(request)
		if err != nil {
			done <- fmt.Errorf("upload CSV output: %w", err)
			return
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		if response.StatusCode < 200 || response.StatusCode > 299 {
			done <- fmt.Errorf("upload CSV output: unexpected HTTP status %s", response.Status)
			return
		}
		done <- nil
	}()
	return &httpExportWriter{writer: writer, done: done}, nil
}

func (w *httpExportWriter) Write(p []byte) (int, error) {
	return w.writer.Write(p)
}

func (w *httpExportWriter) Close() error {
	closeErr := w.writer.Close()
	uploadErr := <-w.done
	if closeErr != nil {
		return closeErr
	}
	return uploadErr
}

func buildSQLServerExportQuery(sourceObject, predicate string) (string, error) {
	parts := strings.Split(strings.TrimSpace(sourceObject), ".")
	if len(parts) != 2 && len(parts) != 3 {
		return "", fmt.Errorf("source object must be schema.table or database.schema.table")
	}
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", fmt.Errorf("source object contains an empty identifier")
		}
		quoted = append(quoted, quoteSQLServerIdentifier(part))
	}

	query := "SELECT * FROM " + strings.Join(quoted, ".")
	predicate = strings.TrimSpace(predicate)
	if predicate != "" {
		query += " WHERE " + predicate
	}
	return query, nil
}

func quoteSQLServerIdentifier(identifier string) string {
	return "[" + strings.ReplaceAll(identifier, "]", "]]") + "]"
}

type exportRows interface {
	Columns() ([]string, error)
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

const csvNullBitmapColumn = "__sqlserver2tidb_null_bitmap"

func writeCSVExportRows(w io.Writer, rows exportRows) error {
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column == csvNullBitmapColumn {
			return fmt.Errorf("source column name %q conflicts with internal CSV null bitmap column", csvNullBitmapColumn)
		}
	}
	writer := csv.NewWriter(w)
	header := append(append([]string(nil), columns...), csvNullBitmapColumn)
	if err := writer.Write(header); err != nil {
		return err
	}

	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		record := make([]string, 0, len(values)+1)
		nullBitmap := make([]byte, len(values))
		for i, value := range values {
			if value == nil {
				record = append(record, "")
				nullBitmap[i] = '1'
				continue
			}
			record = append(record, formatCSVValue(value))
			nullBitmap[i] = '0'
		}
		record = append(record, string(nullBitmap))
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writer.Flush()
	return writer.Error()
}

func formatCSVValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case []byte:
		return string(v)
	case time.Time:
		return v.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(v)
	}
}

func runImport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	jobID := fs.String("job-id", "", "import job id")
	engine := fs.String("engine", importEngineSQLInsert, "import engine: sql-insert or tidb-import-into")
	targetObject := fs.String("target-object", "", "target object")
	sourceURI := fs.String("source-uri", "", "import source URI")
	dependsOnExportChunk := fs.String("depends-on-export-chunk", "", "upstream export chunk id")
	targetConnectionStringEnv := fs.String("target-connection-string-env", defaultTargetConnectionStringEnv, "environment variable containing the TiDB/MySQL connection string")
	importBatchSize := fs.Int("import-batch-size", 1000, "rows to commit per import transaction")
	execute := fs.Bool("execute", false, "perform the import")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := requireFields("executor import",
		field{"source cluster id", *sourceClusterID},
		field{"project id", *projectID},
		field{"job id", *jobID},
		field{"target object", *targetObject},
		field{"source uri", *sourceURI},
	); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	normalizedEngine, err := normalizeImportEngine(*engine)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *execute && *importBatchSize <= 0 {
		fmt.Fprintln(stderr, "executor import: import batch size must be positive")
		return 1
	}
	if *execute {
		if err := executeTiDBImport(context.Background(), importExecuteSpec{
			ImportEngine:              normalizedEngine,
			TargetObject:              *targetObject,
			SourceURI:                 *sourceURI,
			TargetConnectionStringEnv: *targetConnectionStringEnv,
			ImportBatchSize:           *importBatchSize,
		}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "executor import completed: %s -> %s\n", *sourceURI, *targetObject)
		return 0
	}

	fmt.Fprintln(stdout, "executor import dry run")
	fmt.Fprintf(stdout, "root: %s\n", *root)
	fmt.Fprintf(stdout, "source cluster: %s\n", *sourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", *projectID)
	fmt.Fprintf(stdout, "job id: %s\n", *jobID)
	fmt.Fprintf(stdout, "engine: %s\n", normalizedEngine)
	fmt.Fprintf(stdout, "target object: %s\n", *targetObject)
	fmt.Fprintf(stdout, "source uri: %s\n", *sourceURI)
	if strings.TrimSpace(*dependsOnExportChunk) != "" {
		fmt.Fprintf(stdout, "depends on export chunk: %s\n", *dependsOnExportChunk)
	}
	fmt.Fprintln(stdout, "No TiDB connection will be opened.")
	return 0
}

func runValidateCount(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate-count", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	sourceObject := fs.String("source-object", "", "source object")
	targetObject := fs.String("target-object", "", "target object")
	predicate := fs.String("predicate", "", "source validation predicate")
	targetPredicate := fs.String("target-predicate", "", "target validation predicate")
	sourceConnectionStringEnv := fs.String("source-connection-string-env", defaultSourceConnectionStringEnv, "environment variable containing the SQL Server connection string")
	targetConnectionStringEnv := fs.String("target-connection-string-env", defaultTargetConnectionStringEnv, "environment variable containing the TiDB/MySQL connection string")
	execute := fs.Bool("execute", false, "perform the row count validation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := requireFields("executor validate-count",
		field{"source cluster id", *sourceClusterID},
		field{"project id", *projectID},
		field{"source object", *sourceObject},
		field{"target object", *targetObject},
	); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *execute {
		result, err := executeValidateCount(context.Background(), validateCountExecuteSpec{
			SourceObject:              *sourceObject,
			TargetObject:              *targetObject,
			Predicate:                 *predicate,
			TargetPredicate:           *targetPredicate,
			SourceConnectionStringEnv: *sourceConnectionStringEnv,
			TargetConnectionStringEnv: *targetConnectionStringEnv,
		})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "executor validate-count matched: source=%d target=%d\n", result.SourceRows, result.TargetRows)
		return 0
	}

	fmt.Fprintln(stdout, "executor validate-count dry run")
	fmt.Fprintf(stdout, "root: %s\n", *root)
	fmt.Fprintf(stdout, "source cluster: %s\n", *sourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", *projectID)
	fmt.Fprintf(stdout, "source object: %s\n", *sourceObject)
	fmt.Fprintf(stdout, "target object: %s\n", *targetObject)
	if strings.TrimSpace(*predicate) != "" {
		fmt.Fprintf(stdout, "predicate: %s\n", *predicate)
	}
	if strings.TrimSpace(*targetPredicate) != "" {
		fmt.Fprintf(stdout, "target predicate: %s\n", *targetPredicate)
	}
	fmt.Fprintln(stdout, "No SQL Server connection will be opened.")
	fmt.Fprintln(stdout, "No TiDB connection will be opened.")
	return 0
}

func runValidateQuery(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate-query", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	checkID := fs.String("check-id", "", "validation check id")
	sourceSQL := fs.String("source-sql", "", "SQL query to run on SQL Server; must return one row and one column")
	targetSQL := fs.String("target-sql", "", "SQL query to run on TiDB/MySQL; must return one row and one column")
	sourceConnectionStringEnv := fs.String("source-connection-string-env", defaultSourceConnectionStringEnv, "environment variable containing the SQL Server connection string")
	targetConnectionStringEnv := fs.String("target-connection-string-env", defaultTargetConnectionStringEnv, "environment variable containing the TiDB/MySQL connection string")
	execute := fs.Bool("execute", false, "perform the scalar query validation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := requireFields("executor validate-query",
		field{"source cluster id", *sourceClusterID},
		field{"project id", *projectID},
		field{"check id", *checkID},
		field{"source sql", *sourceSQL},
		field{"target sql", *targetSQL},
	); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *execute {
		result, err := executeValidateQuery(context.Background(), validateQueryExecuteSpec{
			SourceSQL:                 *sourceSQL,
			TargetSQL:                 *targetSQL,
			SourceConnectionStringEnv: *sourceConnectionStringEnv,
			TargetConnectionStringEnv: *targetConnectionStringEnv,
		})
		if err != nil {
			fmt.Fprintf(stderr, "executor validate-query failed: check-id=%s error=%v\n", *checkID, err)
			return 1
		}
		fmt.Fprint(stdout, renderValidateQueryMatched(*checkID, result))
		return 0
	}

	fmt.Fprintln(stdout, "executor validate-query dry run")
	fmt.Fprintf(stdout, "root: %s\n", *root)
	fmt.Fprintf(stdout, "source cluster: %s\n", *sourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", *projectID)
	fmt.Fprintf(stdout, "check id: %s\n", *checkID)
	fmt.Fprintf(stdout, "source sql: %s\n", *sourceSQL)
	fmt.Fprintf(stdout, "target sql: %s\n", *targetSQL)
	fmt.Fprintln(stdout, "No SQL Server connection will be opened.")
	fmt.Fprintln(stdout, "No TiDB connection will be opened.")
	return 0
}

func renderValidateQueryMatched(checkID string, result validateQueryResult) string {
	return fmt.Sprintf("executor validate-query matched: check-id=%s source=%s target=%s\n", checkID, result.SourceValue, result.TargetValue)
}

type validateCountExecuteSpec struct {
	SourceObject              string
	TargetObject              string
	Predicate                 string
	TargetPredicate           string
	SourceConnectionStringEnv string
	TargetConnectionStringEnv string
}

type validateCountResult struct {
	SourceRows int64
	TargetRows int64
}

type validateQueryExecuteSpec struct {
	SourceSQL                 string
	TargetSQL                 string
	SourceConnectionStringEnv string
	TargetConnectionStringEnv string
}

type validateQueryResult struct {
	SourceValue string
	TargetValue string
}

func executeValidateCount(ctx context.Context, spec validateCountExecuteSpec) (validateCountResult, error) {
	if strings.Contains(strings.ToUpper(spec.Predicate), "TODO") {
		return validateCountResult{}, fmt.Errorf("executor validate-count: predicate still contains TODO")
	}
	if strings.Contains(strings.ToUpper(spec.TargetPredicate), "TODO") {
		return validateCountResult{}, fmt.Errorf("executor validate-count: target predicate still contains TODO")
	}

	sourceEnvName := strings.TrimSpace(spec.SourceConnectionStringEnv)
	if sourceEnvName == "" {
		sourceEnvName = defaultSourceConnectionStringEnv
	}
	sourceConnectionString := strings.TrimSpace(os.Getenv(sourceEnvName))
	if sourceConnectionString == "" {
		return validateCountResult{}, fmt.Errorf("executor validate-count: source connection string env %s is not set", sourceEnvName)
	}

	targetEnvName := strings.TrimSpace(spec.TargetConnectionStringEnv)
	if targetEnvName == "" {
		targetEnvName = defaultTargetConnectionStringEnv
	}
	targetConnectionString := strings.TrimSpace(os.Getenv(targetEnvName))
	if targetConnectionString == "" {
		return validateCountResult{}, fmt.Errorf("executor validate-count: target connection string env %s is not set", targetEnvName)
	}

	sourceQuery, err := buildSQLServerCountQuery(spec.SourceObject, spec.Predicate)
	if err != nil {
		return validateCountResult{}, fmt.Errorf("executor validate-count: %w", err)
	}
	targetQuery, err := buildTiDBCountQuery(spec.TargetObject, spec.TargetPredicate)
	if err != nil {
		return validateCountResult{}, fmt.Errorf("executor validate-count: %w", err)
	}

	sourceDB, err := sql.Open("sqlserver", sourceConnectionString)
	if err != nil {
		return validateCountResult{}, fmt.Errorf("executor validate-count: open SQL Server connection: %w", err)
	}
	defer sourceDB.Close()

	targetDB, err := sql.Open("mysql", targetConnectionString)
	if err != nil {
		return validateCountResult{}, fmt.Errorf("executor validate-count: open TiDB connection: %w", err)
	}
	defer targetDB.Close()

	var sourceRows int64
	if err := sourceDB.QueryRowContext(ctx, sourceQuery).Scan(&sourceRows); err != nil {
		return validateCountResult{}, fmt.Errorf("executor validate-count: query source count: %w", err)
	}
	var targetRows int64
	if err := targetDB.QueryRowContext(ctx, targetQuery).Scan(&targetRows); err != nil {
		return validateCountResult{}, fmt.Errorf("executor validate-count: query target count: %w", err)
	}
	if sourceRows != targetRows {
		return validateCountResult{}, fmt.Errorf("executor validate-count: row count mismatch: source=%d target=%d", sourceRows, targetRows)
	}
	return validateCountResult{SourceRows: sourceRows, TargetRows: targetRows}, nil
}

func executeValidateQuery(ctx context.Context, spec validateQueryExecuteSpec) (validateQueryResult, error) {
	if strings.Contains(strings.ToUpper(spec.SourceSQL), "TODO") {
		return validateQueryResult{}, fmt.Errorf("executor validate-query: source_sql still contains TODO")
	}
	if strings.Contains(strings.ToUpper(spec.TargetSQL), "TODO") {
		return validateQueryResult{}, fmt.Errorf("executor validate-query: target_sql still contains TODO")
	}

	sourceEnvName := strings.TrimSpace(spec.SourceConnectionStringEnv)
	if sourceEnvName == "" {
		sourceEnvName = defaultSourceConnectionStringEnv
	}
	sourceConnectionString := strings.TrimSpace(os.Getenv(sourceEnvName))
	if sourceConnectionString == "" {
		return validateQueryResult{}, fmt.Errorf("executor validate-query: source connection string env %s is not set", sourceEnvName)
	}

	targetEnvName := strings.TrimSpace(spec.TargetConnectionStringEnv)
	if targetEnvName == "" {
		targetEnvName = defaultTargetConnectionStringEnv
	}
	targetConnectionString := strings.TrimSpace(os.Getenv(targetEnvName))
	if targetConnectionString == "" {
		return validateQueryResult{}, fmt.Errorf("executor validate-query: target connection string env %s is not set", targetEnvName)
	}

	sourceDB, err := sql.Open("sqlserver", sourceConnectionString)
	if err != nil {
		return validateQueryResult{}, fmt.Errorf("executor validate-query: open SQL Server connection: %w", err)
	}
	defer sourceDB.Close()

	targetDB, err := sql.Open("mysql", targetConnectionString)
	if err != nil {
		return validateQueryResult{}, fmt.Errorf("executor validate-query: open TiDB connection: %w", err)
	}
	defer targetDB.Close()

	sourceValue, err := querySingleScalar(ctx, sourceDB, spec.SourceSQL)
	if err != nil {
		return validateQueryResult{}, fmt.Errorf("executor validate-query: query source SQL: %w", err)
	}
	targetValue, err := querySingleScalar(ctx, targetDB, spec.TargetSQL)
	if err != nil {
		return validateQueryResult{}, fmt.Errorf("executor validate-query: query target SQL: %w", err)
	}
	if sourceValue != targetValue {
		return validateQueryResult{}, fmt.Errorf("executor validate-query mismatch: source=%s target=%s", sourceValue, targetValue)
	}
	return validateQueryResult{SourceValue: sourceValue, TargetValue: targetValue}, nil
}

func querySingleScalar(ctx context.Context, db *sql.DB, query string) (string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	if len(columns) != 1 {
		return "", fmt.Errorf("query returned %d columns, want 1", len(columns))
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("query returned no rows")
	}
	var raw any
	if err := rows.Scan(&raw); err != nil {
		return "", err
	}
	if rows.Next() {
		return "", fmt.Errorf("query returned more than one row")
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return normalizeSQLScalar(raw), nil
}

func normalizeSQLScalar(value any) string {
	switch v := value.(type) {
	case nil:
		return "<NULL>"
	case []byte:
		return string(v)
	case time.Time:
		return v.Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(v)
	}
}

type importExecuteSpec struct {
	ImportEngine              string
	TargetObject              string
	SourceURI                 string
	TargetConnectionStringEnv string
	ImportBatchSize           int
}

func executeTiDBImport(ctx context.Context, spec importExecuteSpec) error {
	engine, err := normalizeImportEngine(spec.ImportEngine)
	if err != nil {
		return err
	}
	if engine == importEngineTiDBImportInto {
		return executeTiDBImportInto(ctx, spec)
	}
	if spec.ImportBatchSize <= 0 {
		return fmt.Errorf("executor import: import batch size must be positive")
	}

	source, err := parseImportSourceURI(spec.SourceURI)
	if err != nil {
		return fmt.Errorf("executor import: %w", err)
	}

	envName := strings.TrimSpace(spec.TargetConnectionStringEnv)
	if envName == "" {
		envName = defaultTargetConnectionStringEnv
	}
	connectionString := strings.TrimSpace(os.Getenv(envName))
	if connectionString == "" {
		return fmt.Errorf("executor import: target connection string env %s is not set", envName)
	}

	sourceReader, err := openParsedCSVImportSource(ctx, source)
	if err != nil {
		return fmt.Errorf("executor import: %w", err)
	}
	defer sourceReader.Close()

	reader, err := newCSVImportReader(sourceReader)
	if err != nil {
		return fmt.Errorf("executor import: read CSV source: %w", err)
	}
	insertSQL, err := buildTiDBInsertStatement(spec.TargetObject, reader.Columns())
	if err != nil {
		return fmt.Errorf("executor import: %w", err)
	}

	db, err := sql.Open("mysql", connectionString)
	if err != nil {
		return fmt.Errorf("executor import: open TiDB connection: %w", err)
	}
	defer db.Close()

	return insertCSVImportRows(ctx, db, insertSQL, reader, spec.ImportBatchSize)
}

func executeTiDBImportInto(ctx context.Context, spec importExecuteSpec) error {
	fields, err := readTiDBImportIntoFieldsFromLocalSource(spec.SourceURI)
	if err != nil {
		return fmt.Errorf("executor import: %w", err)
	}
	statement, err := buildTiDBImportIntoStatementWithFields(spec.TargetObject, spec.SourceURI, fields)
	if err != nil {
		return fmt.Errorf("executor import: %w", err)
	}

	envName := strings.TrimSpace(spec.TargetConnectionStringEnv)
	if envName == "" {
		envName = defaultTargetConnectionStringEnv
	}
	connectionString := strings.TrimSpace(os.Getenv(envName))
	if connectionString == "" {
		return fmt.Errorf("executor import: target connection string env %s is not set", envName)
	}

	db, err := sql.Open("mysql", connectionString)
	if err != nil {
		return fmt.Errorf("executor import: open TiDB connection: %w", err)
	}
	defer db.Close()

	if err := ensureTiDBImportIntoTargetEmpty(ctx, db, spec.TargetObject); err != nil {
		return fmt.Errorf("executor import: %w", err)
	}

	if _, err := db.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("executor import: execute TiDB IMPORT INTO: %w", err)
	}
	return nil
}

func openCSVImportFile(sourceURI string) (io.ReadCloser, error) {
	source, err := parseImportSourceURI(sourceURI)
	if err != nil {
		return nil, err
	}
	return openParsedCSVImportSource(context.Background(), source)
}

type importSourceURI struct {
	scheme string
	path   string
	uri    string
}

func parseImportSourceURI(sourceURI string) (importSourceURI, error) {
	parsed, err := url.Parse(strings.TrimSpace(sourceURI))
	if err != nil {
		return importSourceURI{}, fmt.Errorf("parse source uri: %w", err)
	}
	switch parsed.Scheme {
	case "file":
		if parsed.Host != "" && parsed.Host != "localhost" {
			return importSourceURI{}, fmt.Errorf("file source URI host must be empty or localhost")
		}
		if strings.TrimSpace(parsed.Path) == "" {
			return importSourceURI{}, fmt.Errorf("file source URI path is required")
		}
		return importSourceURI{
			scheme: parsed.Scheme,
			path:   filepath.Clean(parsed.Path),
			uri:    parsed.String(),
		}, nil
	case "http", "https":
		if strings.TrimSpace(parsed.Host) == "" {
			return importSourceURI{}, fmt.Errorf("%s source URI host is required", parsed.Scheme)
		}
		return importSourceURI{
			scheme: parsed.Scheme,
			uri:    parsed.String(),
		}, nil
	default:
		return importSourceURI{}, fmt.Errorf("only file://, http://, and https:// source URIs are supported for --execute")
	}
}

func resolveImportSourceFile(sourceURI string) (string, error) {
	source, err := parseImportSourceURI(sourceURI)
	if err != nil {
		return "", err
	}
	if source.scheme != "file" {
		return "", fmt.Errorf("source URI is not a file:// URI")
	}
	return source.path, nil
}

func openParsedCSVImportSource(ctx context.Context, source importSourceURI) (io.ReadCloser, error) {
	switch source.scheme {
	case "file":
		file, err := os.Open(source.path)
		if err != nil {
			return nil, fmt.Errorf("open CSV source: %w", err)
		}
		return file, nil
	case "http", "https":
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.uri, nil)
		if err != nil {
			return nil, fmt.Errorf("create CSV source request: %w", err)
		}
		response, err := csvHTTPClient.Do(request)
		if err != nil {
			return nil, fmt.Errorf("download CSV source: %w", err)
		}
		if response.StatusCode < 200 || response.StatusCode > 299 {
			response.Body.Close()
			return nil, fmt.Errorf("download CSV source: unexpected HTTP status %s", response.Status)
		}
		return response.Body, nil
	default:
		return nil, fmt.Errorf("unsupported source URI scheme %q", source.scheme)
	}
}

type csvImportReader struct {
	reader        *csv.Reader
	columns       []string
	hasNullBitmap bool
}

func newCSVImportReader(r io.Reader) (*csvImportReader, error) {
	reader := csv.NewReader(r)
	columns, err := reader.Read()
	if err != nil {
		return nil, err
	}
	hasNullBitmap := false
	if len(columns) > 0 && columns[len(columns)-1] == csvNullBitmapColumn {
		hasNullBitmap = true
		columns = append([]string(nil), columns[:len(columns)-1]...)
	}
	seenColumns := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		if strings.TrimSpace(column) == "" {
			return nil, fmt.Errorf("CSV header contains an empty column name")
		}
		if column == csvNullBitmapColumn {
			return nil, fmt.Errorf("CSV header contains reserved column name %q outside the final null bitmap position", csvNullBitmapColumn)
		}
		normalizedColumn := strings.ToLower(column)
		if _, ok := seenColumns[normalizedColumn]; ok {
			return nil, fmt.Errorf("CSV header contains duplicate column name %q", column)
		}
		seenColumns[normalizedColumn] = struct{}{}
	}
	return &csvImportReader{reader: reader, columns: columns, hasNullBitmap: hasNullBitmap}, nil
}

func (reader *csvImportReader) Columns() []string {
	return append([]string(nil), reader.columns...)
}

func (reader *csvImportReader) ReadRecord() ([]string, error) {
	record, err := reader.reader.Read()
	if err != nil {
		return nil, err
	}
	if reader.hasNullBitmap {
		return append([]string(nil), record[:len(record)-1]...), nil
	}
	return record, nil
}

func (reader *csvImportReader) ReadValues() ([]any, error) {
	record, err := reader.reader.Read()
	if err != nil {
		return nil, err
	}
	if !reader.hasNullBitmap {
		values := make([]any, len(record))
		for i, value := range record {
			values[i] = value
		}
		return values, nil
	}
	values := record[:len(record)-1]
	nullBitmap := record[len(record)-1]
	if len(nullBitmap) != len(values) {
		return nil, fmt.Errorf("CSV null bitmap length %d does not match column count %d", len(nullBitmap), len(values))
	}
	out := make([]any, len(values))
	for i, value := range values {
		switch nullBitmap[i] {
		case '0':
			out[i] = value
		case '1':
			out[i] = nil
		default:
			return nil, fmt.Errorf("CSV null bitmap contains invalid marker %q at column %d", nullBitmap[i], i+1)
		}
	}
	return out, nil
}

func readCSVImportRecords(r io.Reader) ([]string, [][]string, error) {
	reader, err := newCSVImportReader(r)
	if err != nil {
		return nil, nil, err
	}
	var records [][]string
	for {
		record, err := reader.ReadRecord()
		if errors.Is(err, io.EOF) {
			return reader.Columns(), records, nil
		}
		if err != nil {
			return nil, nil, err
		}
		records = append(records, record)
	}
}

func buildTiDBInsertStatement(targetObject string, columns []string) (string, error) {
	quotedTargetObject, err := quoteTiDBObjectName(targetObject)
	if err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return "", fmt.Errorf("CSV header must contain at least one column")
	}
	quotedColumns := make([]string, 0, len(columns))
	placeholders := make([]string, 0, len(columns))
	for _, column := range columns {
		column = strings.TrimSpace(column)
		if column == "" {
			return "", fmt.Errorf("CSV header contains an empty column name")
		}
		quotedColumns = append(quotedColumns, quoteTiDBIdentifier(column))
		placeholders = append(placeholders, "?")
	}

	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		quotedTargetObject,
		strings.Join(quotedColumns, ", "),
		strings.Join(placeholders, ", "),
	), nil
}

func buildTiDBImportIntoStatement(targetObject, sourceURI string) (string, error) {
	return buildTiDBImportIntoStatementWithFields(targetObject, sourceURI, nil)
}

func ensureTiDBImportIntoTargetEmpty(ctx context.Context, db *sql.DB, targetObject string) error {
	query, err := buildTiDBImportIntoPreflightQuery(targetObject)
	if err != nil {
		return err
	}

	var count int64
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return fmt.Errorf("preflight target table row count: %w", err)
	}
	if count != 0 {
		return fmt.Errorf("preflight target table is not empty: %s has %d rows", targetObject, count)
	}
	return nil
}

func buildTiDBImportIntoPreflightQuery(targetObject string) (string, error) {
	return buildTiDBCountQuery(targetObject, "")
}

func buildTiDBImportIntoStatementWithFields(targetObject, sourceURI string, fields []string) (string, error) {
	quotedTargetObject, err := quoteTiDBObjectName(targetObject)
	if err != nil {
		return "", err
	}
	fileLocation, err := normalizeTiDBImportIntoFileLocation(sourceURI)
	if err != nil {
		return "", err
	}
	fieldList, err := buildTiDBImportIntoFieldList(fields)
	if err != nil {
		return "", err
	}
	target := quotedTargetObject
	if fieldList != "" {
		target += " " + fieldList
	}
	return fmt.Sprintf("IMPORT INTO %s FROM %s FORMAT 'csv' WITH skip_rows=1",
		target,
		quoteTiDBStringLiteral(fileLocation),
	), nil
}

func buildTiDBImportIntoFieldList(fields []string) (string, error) {
	if len(fields) == 0 {
		return "", nil
	}
	quoted := make([]string, 0, len(fields))
	seenColumns := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			return "", fmt.Errorf("IMPORT INTO field list contains an empty field")
		}
		if strings.HasPrefix(field, "@") {
			quoted = append(quoted, field)
			continue
		}
		normalizedColumn := strings.ToLower(field)
		if _, ok := seenColumns[normalizedColumn]; ok {
			return "", fmt.Errorf("CSV header contains duplicate column name %q", field)
		}
		seenColumns[normalizedColumn] = struct{}{}
		quoted = append(quoted, quoteTiDBIdentifier(field))
	}
	return "(" + strings.Join(quoted, ", ") + ")", nil
}

func readTiDBImportIntoFieldsFromLocalSource(sourceURI string) ([]string, error) {
	sourceURI = strings.TrimSpace(sourceURI)
	if sourceURI == "" {
		return nil, fmt.Errorf("IMPORT INTO source URI is required")
	}
	parsed, err := url.Parse(sourceURI)
	if err != nil {
		return nil, fmt.Errorf("parse IMPORT INTO source URI: %w", err)
	}
	var path string
	switch parsed.Scheme {
	case "":
		path = sourceURI
	case "file":
		if parsed.Host != "" && parsed.Host != "localhost" {
			return nil, fmt.Errorf("file source URI host must be empty or localhost")
		}
		if strings.TrimSpace(parsed.Path) == "" {
			return nil, fmt.Errorf("file source URI path is required")
		}
		path = filepath.Clean(parsed.Path)
	case "s3", "gs":
		return nil, nil
	default:
		return nil, fmt.Errorf("IMPORT INTO source URI scheme %s is not supported; supported schemes: file, s3, gs, or local path", parsed.Scheme)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read IMPORT INTO CSV header: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	columns, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read IMPORT INTO CSV header: %w", err)
	}
	return buildTiDBImportIntoFieldsFromCSVHeader(columns)
}

func buildTiDBImportIntoFieldsFromCSVHeader(columns []string) ([]string, error) {
	if len(columns) == 0 {
		return nil, fmt.Errorf("CSV header must contain at least one column")
	}
	fields := make([]string, 0, len(columns))
	seenColumns := make(map[string]struct{}, len(columns))
	for i, column := range columns {
		column = strings.TrimSpace(column)
		if column == "" {
			return nil, fmt.Errorf("CSV header contains an empty column name")
		}
		if column == csvNullBitmapColumn {
			if i != len(columns)-1 {
				return nil, fmt.Errorf("CSV header contains reserved column name %q outside the final null bitmap position", csvNullBitmapColumn)
			}
			fields = append(fields, "@sqlserver2tidb_null_bitmap")
			continue
		}
		normalizedColumn := strings.ToLower(column)
		if _, ok := seenColumns[normalizedColumn]; ok {
			return nil, fmt.Errorf("CSV header contains duplicate column name %q", column)
		}
		seenColumns[normalizedColumn] = struct{}{}
		fields = append(fields, column)
	}
	return fields, nil
}

func quoteTiDBObjectName(targetObject string) (string, error) {
	parts := strings.Split(strings.TrimSpace(targetObject), ".")
	if len(parts) != 1 && len(parts) != 2 {
		return "", fmt.Errorf("target object must be table or database.table")
	}
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", fmt.Errorf("target object contains an empty identifier")
		}
		quoted = append(quoted, quoteTiDBIdentifier(part))
	}
	return strings.Join(quoted, "."), nil
}

func normalizeTiDBImportIntoFileLocation(sourceURI string) (string, error) {
	sourceURI = strings.TrimSpace(sourceURI)
	if sourceURI == "" {
		return "", fmt.Errorf("IMPORT INTO source URI is required")
	}
	parsed, err := url.Parse(sourceURI)
	if err != nil {
		return "", fmt.Errorf("parse IMPORT INTO source URI: %w", err)
	}
	switch parsed.Scheme {
	case "":
		return sourceURI, nil
	case "file":
		if parsed.Host != "" && parsed.Host != "localhost" {
			return "", fmt.Errorf("file source URI host must be empty or localhost")
		}
		if strings.TrimSpace(parsed.Path) == "" {
			return "", fmt.Errorf("file source URI path is required")
		}
		return filepath.Clean(parsed.Path), nil
	case "s3", "gs":
		return parsed.String(), nil
	default:
		return "", fmt.Errorf("IMPORT INTO source URI scheme %s is not supported; supported schemes: file, s3, gs, or local path", parsed.Scheme)
	}
}

func quoteTiDBStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func buildSQLServerCountQuery(sourceObject, predicate string) (string, error) {
	parts := strings.Split(strings.TrimSpace(sourceObject), ".")
	if len(parts) != 2 && len(parts) != 3 {
		return "", fmt.Errorf("source object must be schema.table or database.schema.table")
	}
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", fmt.Errorf("source object contains an empty identifier")
		}
		quoted = append(quoted, quoteSQLServerIdentifier(part))
	}

	query := "SELECT COUNT(*) FROM " + strings.Join(quoted, ".")
	predicate = strings.TrimSpace(predicate)
	if predicate != "" {
		query += " WHERE " + predicate
	}
	return query, nil
}

func buildTiDBCountQuery(targetObject, predicate string) (string, error) {
	parts := strings.Split(strings.TrimSpace(targetObject), ".")
	if len(parts) != 1 && len(parts) != 2 {
		return "", fmt.Errorf("target object must be table or database.table")
	}
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", fmt.Errorf("target object contains an empty identifier")
		}
		quoted = append(quoted, quoteTiDBIdentifier(part))
	}
	query := "SELECT COUNT(*) FROM " + strings.Join(quoted, ".")
	predicate = strings.TrimSpace(predicate)
	if predicate != "" {
		query += " WHERE " + predicate
	}
	return query, nil
}

func quoteTiDBIdentifier(identifier string) string {
	return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
}

func insertCSVImportRows(ctx context.Context, db *sql.DB, insertSQL string, reader *csvImportReader, batchSize int) error {
	if batchSize <= 0 {
		return fmt.Errorf("executor import: import batch size must be positive")
	}

	rowNumber := 0
	for {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("executor import: begin transaction: %w", err)
		}
		stmt, err := tx.PrepareContext(ctx, insertSQL)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("executor import: prepare insert: %w", err)
		}

		rowsInBatch := 0
		for rowsInBatch < batchSize {
			args, err := reader.ReadValues()
			if errors.Is(err, io.EOF) {
				stmt.Close()
				if rowsInBatch == 0 {
					tx.Rollback()
					return nil
				}
				if err := tx.Commit(); err != nil {
					return fmt.Errorf("executor import: commit transaction: %w", err)
				}
				return nil
			}
			if err != nil {
				stmt.Close()
				tx.Rollback()
				return fmt.Errorf("executor import: read CSV row %d: %w", rowNumber+1, err)
			}
			if _, err := stmt.ExecContext(ctx, args...); err != nil {
				stmt.Close()
				tx.Rollback()
				return fmt.Errorf("executor import: insert row %d: %w", rowNumber+1, err)
			}
			rowNumber++
			rowsInBatch++
		}

		if err := stmt.Close(); err != nil {
			tx.Rollback()
			return fmt.Errorf("executor import: close insert statement: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("executor import: commit transaction: %w", err)
		}
	}
}

func runCDC(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cdc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	sourceObject := fs.String("source-object", "", "source object")
	targetObject := fs.String("target-object", "", "target object")
	applyBatchSize := fs.Int("apply-batch-size", 0, "apply batch size")
	execute := fs.Bool("execute", false, "perform CDC apply; not implemented in this adapter")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *execute {
		fmt.Fprintln(stderr, "executor cdc: --execute is not implemented yet")
		return 1
	}
	if err := requireFields("executor cdc",
		field{"source cluster id", *sourceClusterID},
		field{"project id", *projectID},
		field{"source object", *sourceObject},
		field{"target object", *targetObject},
	); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *applyBatchSize <= 0 {
		fmt.Fprintln(stderr, "executor cdc: apply batch size must be positive")
		return 1
	}

	fmt.Fprintln(stdout, "executor cdc dry run")
	fmt.Fprintf(stdout, "root: %s\n", *root)
	fmt.Fprintf(stdout, "source cluster: %s\n", *sourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", *projectID)
	fmt.Fprintf(stdout, "source object: %s\n", *sourceObject)
	fmt.Fprintf(stdout, "target object: %s\n", *targetObject)
	fmt.Fprintf(stdout, "apply batch size: %s\n", strconv.Itoa(*applyBatchSize))
	fmt.Fprintln(stdout, "No CDC reader or TiDB apply worker will be started.")
	return 0
}

type field struct {
	name  string
	value string
}

func requireFields(prefix string, fields ...field) error {
	for _, f := range fields {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("%s: %s is required", prefix, f.name)
		}
	}
	return nil
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `sqlserver2tidb-executor executes reviewed migration work items.

Usage:
  sqlserver2tidb-executor version
  sqlserver2tidb-executor apply-ddl --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --ddl-file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/tidb-ddl/dbo.orders.sql
  sqlserver2tidb-executor apply-ddl --execute --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --ddl-file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/tidb-ddl/dbo.orders.sql
  sqlserver2tidb-executor export --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --chunk-id dbo.orders.000001 --source-object sales.dbo.orders --target-object app.orders --output-uri https://object-store.example/path.csv
  sqlserver2tidb-executor export --execute --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --chunk-id dbo.orders.000001 --source-object sales.dbo.orders --target-object app.orders --output-uri file:///tmp/path.csv --predicate "id >= 1 AND id < 1000"
  sqlserver2tidb-executor import --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --job-id import-dbo.orders.000001 --target-object app.orders --source-uri https://object-store.example/path.csv
  sqlserver2tidb-executor import --execute --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --job-id import-dbo.orders.000001 --target-object app.orders --source-uri file:///tmp/path.csv --import-batch-size 1000
  sqlserver2tidb-executor validate-count --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --source-object sales.dbo.orders --target-object app.orders --predicate "id >= 1" --target-predicate "id >= 1"
  sqlserver2tidb-executor validate-count --execute --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --source-object sales.dbo.orders --target-object app.orders
  sqlserver2tidb-executor validate-query --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --check-id orders-total --source-sql "SELECT SUM(total) FROM sales.dbo.orders" --target-sql "SELECT SUM(total) FROM app.orders"
  sqlserver2tidb-executor validate-query --execute --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --check-id orders-total --source-sql "SELECT SUM(total) FROM sales.dbo.orders" --target-sql "SELECT SUM(total) FROM app.orders"
  sqlserver2tidb-executor cdc --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --source-object sales.dbo.orders --target-object app.orders --apply-batch-size 1000
`)
}
