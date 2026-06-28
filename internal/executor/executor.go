package executor

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
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

const (
	compressionNone = "none"
	compressionGzip = "gzip"
)

var csvHTTPClient = &http.Client{Timeout: 10 * time.Minute}

var openMySQLDB = func(connectionString string) (*sql.DB, error) {
	return sql.Open("mysql", connectionString)
}

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

func normalizeCompression(compression string) (string, error) {
	compression = strings.ToLower(strings.TrimSpace(compression))
	switch compression {
	case "":
		return compressionNone, nil
	case compressionNone, compressionGzip:
		return compression, nil
	default:
		return "", fmt.Errorf("compression %s is not supported; supported compression: %s, %s", compression, compressionNone, compressionGzip)
	}
}

func validateCompressionForImportEngine(compression, engine string) error {
	if compression != compressionNone && engine == importEngineTiDBImportInto {
		return fmt.Errorf("compression %s is only supported with %s import", compression, importEngineSQLInsert)
	}
	return nil
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
	case "cdc-lsn":
		return runCDCLSN(args[1:], stdout, stderr)
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

func runCDCLSN(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cdc-lsn", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	sourceObject := fs.String("source-object", "", "optional source object used to derive the CDC capture instance")
	sourceConnectionStringEnv := fs.String("source-connection-string-env", defaultSourceConnectionStringEnv, "environment variable containing the SQL Server CDC connection string")
	execute := fs.Bool("execute", false, "query SQL Server CDC min/max LSNs")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := requireFields("executor cdc-lsn",
		field{"source cluster id", *sourceClusterID},
		field{"project id", *projectID},
	); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	captureInstance := ""
	if strings.TrimSpace(*sourceObject) != "" {
		var err error
		captureInstance, err = sqlServerCDCCaptureInstance(*sourceObject)
		if err != nil {
			fmt.Fprintf(stderr, "executor cdc-lsn: %v\n", err)
			return 1
		}
	}

	if *execute {
		bounds, err := queryCDCLSNBoundsFunc(context.Background(), cdcLSNQuerySpec{
			SourceObject:              *sourceObject,
			SourceConnectionStringEnv: *sourceConnectionStringEnv,
		})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		maxLSN, err := formatSQLServerCDCLSN(bounds.MaxLSN, "max LSN")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		minLSN := ""
		if len(bounds.MinLSN) > 0 {
			minLSN, err = formatSQLServerCDCLSN(bounds.MinLSN, "min LSN")
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
		fmt.Fprintln(stdout, "executor cdc-lsn completed")
		if strings.TrimSpace(*sourceObject) != "" {
			fmt.Fprintf(stdout, "source object: %s\n", strings.TrimSpace(*sourceObject))
		}
		if strings.TrimSpace(bounds.CaptureInstance) != "" {
			fmt.Fprintf(stdout, "capture instance: %s\n", bounds.CaptureInstance)
		}
		if minLSN != "" {
			fmt.Fprintf(stdout, "min_lsn: %s\n", minLSN)
		}
		fmt.Fprintf(stdout, "max_lsn: %s\n", maxLSN)
		return 0
	}

	fmt.Fprintln(stdout, "executor cdc-lsn dry run")
	fmt.Fprintf(stdout, "root: %s\n", *root)
	fmt.Fprintf(stdout, "source cluster: %s\n", *sourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", *projectID)
	if strings.TrimSpace(*sourceObject) != "" {
		fmt.Fprintf(stdout, "source object: %s\n", strings.TrimSpace(*sourceObject))
		fmt.Fprintf(stdout, "capture instance: %s\n", captureInstance)
	}
	fmt.Fprintln(stdout, "No SQL Server CDC LSN query will be executed.")
	return 0
}

type cdcLSNQuerySpec struct {
	SourceObject              string
	SourceConnectionStringEnv string
}

type cdcLSNBounds struct {
	CaptureInstance string
	MinLSN          []byte
	MaxLSN          []byte
}

var queryCDCLSNBoundsFunc = querySQLServerCDCLSNBounds

func querySQLServerCDCLSNBounds(ctx context.Context, spec cdcLSNQuerySpec) (cdcLSNBounds, error) {
	envName := strings.TrimSpace(spec.SourceConnectionStringEnv)
	if envName == "" {
		envName = defaultSourceConnectionStringEnv
	}
	connectionString := strings.TrimSpace(os.Getenv(envName))
	if connectionString == "" {
		return cdcLSNBounds{}, fmt.Errorf("executor cdc-lsn: source connection string env %s is not set", envName)
	}

	db, err := sql.Open("sqlserver", connectionString)
	if err != nil {
		return cdcLSNBounds{}, fmt.Errorf("executor cdc-lsn: open SQL Server connection: %w", err)
	}
	defer db.Close()

	maxQuery, err := buildSQLServerCDCMaxLSNQuery(spec.SourceObject)
	if err != nil {
		return cdcLSNBounds{}, fmt.Errorf("executor cdc-lsn: %w", err)
	}
	var maxLSN []byte
	if err := db.QueryRowContext(ctx, maxQuery).Scan(&maxLSN); err != nil {
		return cdcLSNBounds{}, fmt.Errorf("executor cdc-lsn: query max LSN: %w", err)
	}

	bounds := cdcLSNBounds{MaxLSN: maxLSN}
	if strings.TrimSpace(spec.SourceObject) != "" {
		minQuery, captureInstance, err := buildSQLServerCDCMinLSNQuery(spec.SourceObject)
		if err != nil {
			return cdcLSNBounds{}, fmt.Errorf("executor cdc-lsn: %w", err)
		}
		bounds.CaptureInstance = captureInstance
		if err := db.QueryRowContext(ctx, minQuery, sql.Named("capture_instance", captureInstance)).Scan(&bounds.MinLSN); err != nil {
			return cdcLSNBounds{}, fmt.Errorf("executor cdc-lsn: query min LSN: %w", err)
		}
	}
	return bounds, nil
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
	if _, err := readReviewedDDLStatements(*root, *ddlFile); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
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
	statements, err := readReviewedDDLStatements(spec.Root, spec.DDLFile)
	if err != nil {
		return err
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

func readReviewedDDLStatements(root, ddlFile string) ([]string, error) {
	ddlPath := ddlFile
	if !filepath.IsAbs(ddlPath) {
		ddlPath = filepath.Join(root, filepath.FromSlash(ddlPath))
	}
	data, err := os.ReadFile(ddlPath)
	if err != nil {
		return nil, fmt.Errorf("executor apply-ddl: read DDL file: %w", err)
	}
	if containsTODO(string(data)) {
		return nil, fmt.Errorf("executor apply-ddl: DDL file still contains TODO")
	}
	statements := splitSQLStatements(string(data))
	if len(statements) == 0 {
		return nil, fmt.Errorf("executor apply-ddl: DDL file contains no SQL statements")
	}
	return statements, nil
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
	compression := fs.String("compression", compressionNone, "export compression: none or gzip")
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
	if containsTODO(*predicate) {
		fmt.Fprintln(stderr, "executor export: predicate still contains TODO")
		return 1
	}
	normalizedCompression, err := normalizeCompression(*compression)
	if err != nil {
		fmt.Fprintf(stderr, "executor export: %v\n", err)
		return 1
	}
	if _, err := parseExportOutputURI(*outputURI); err != nil {
		fmt.Fprintf(stderr, "executor export: %v\n", err)
		return 1
	}
	if _, err := buildSQLServerExportQuery(*sourceObject, *predicate); err != nil {
		fmt.Fprintf(stderr, "executor export: %v\n", err)
		return 1
	}
	if *execute {
		result, err := executeSQLServerExport(context.Background(), exportExecuteSpec{
			SourceObject:              *sourceObject,
			OutputURI:                 *outputURI,
			Predicate:                 *predicate,
			Compression:               normalizedCompression,
			SourceConnectionStringEnv: *sourceConnectionStringEnv,
		})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "executor export completed: %s -> %s\n", *sourceObject, *outputURI)
		fmt.Fprintf(stdout, "exported rows: %d\n", result.ExportedRows)
		fmt.Fprintf(stdout, "output bytes: %d\n", result.OutputBytes)
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
	if normalizedCompression != compressionNone {
		fmt.Fprintf(stdout, "compression: %s\n", normalizedCompression)
	}
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
	Compression               string
	SourceConnectionStringEnv string
}

type exportExecuteResult struct {
	ExportedRows int64
	OutputBytes  int64
}

func executeSQLServerExport(ctx context.Context, spec exportExecuteSpec) (exportExecuteResult, error) {
	compression, err := normalizeCompression(spec.Compression)
	if err != nil {
		return exportExecuteResult{}, fmt.Errorf("executor export: %w", err)
	}
	if strings.Contains(strings.ToUpper(spec.Predicate), "TODO") {
		return exportExecuteResult{}, fmt.Errorf("executor export: predicate still contains TODO")
	}

	output, err := parseExportOutputURI(spec.OutputURI)
	if err != nil {
		return exportExecuteResult{}, fmt.Errorf("executor export: %w", err)
	}
	if err := prepareExportOutputURI(output); err != nil {
		return exportExecuteResult{}, fmt.Errorf("executor export: create output directory: %w", err)
	}

	envName := strings.TrimSpace(spec.SourceConnectionStringEnv)
	if envName == "" {
		envName = defaultSourceConnectionStringEnv
	}
	connectionString := strings.TrimSpace(os.Getenv(envName))
	if connectionString == "" {
		return exportExecuteResult{}, fmt.Errorf("executor export: source connection string env %s is not set", envName)
	}

	query, err := buildSQLServerExportQuery(spec.SourceObject, spec.Predicate)
	if err != nil {
		return exportExecuteResult{}, fmt.Errorf("executor export: %w", err)
	}

	db, err := sql.Open("sqlserver", connectionString)
	if err != nil {
		return exportExecuteResult{}, fmt.Errorf("executor export: open SQL Server connection: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return exportExecuteResult{}, fmt.Errorf("executor export: query source object %s: %w", spec.SourceObject, err)
	}

	outputWriter, err := openCSVExportOutput(ctx, output, compression)
	if err != nil {
		rows.Close()
		return exportExecuteResult{}, fmt.Errorf("executor export: %w", err)
	}
	exportedRows, err := writeCSVExportRows(outputWriter, rows)
	if err != nil {
		outputWriter.Close()
		return exportExecuteResult{}, fmt.Errorf("executor export: write CSV output: %w", err)
	}
	if err := outputWriter.Close(); err != nil {
		return exportExecuteResult{}, fmt.Errorf("executor export: close CSV output: %w", err)
	}
	return exportExecuteResult{
		ExportedRows: exportedRows,
		OutputBytes:  outputWriter.BytesWritten(),
	}, nil
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
		return exportOutputURI{}, fmt.Errorf("only file://, http://, and https:// output URIs are supported for CSV export")
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

type csvExportOutput struct {
	io.WriteCloser
	counter *countingWriteCloser
}

func (output *csvExportOutput) BytesWritten() int64 {
	if output == nil || output.counter == nil {
		return 0
	}
	return output.counter.BytesWritten()
}

type countingWriteCloser struct {
	writer io.WriteCloser
	bytes  int64
}

func (w *countingWriteCloser) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *countingWriteCloser) Close() error {
	return w.writer.Close()
}

func (w *countingWriteCloser) BytesWritten() int64 {
	if w == nil {
		return 0
	}
	return w.bytes
}

func openCSVExportOutput(ctx context.Context, output exportOutputURI, compression string) (*csvExportOutput, error) {
	var base io.WriteCloser
	switch output.scheme {
	case "file":
		file, err := os.Create(output.path)
		if err != nil {
			return nil, fmt.Errorf("create output file: %w", err)
		}
		base = file
	case "http", "https":
		writer, err := newHTTPExportWriter(ctx, output.uri, compression)
		if err != nil {
			return nil, err
		}
		base = writer
	default:
		return nil, fmt.Errorf("unsupported output URI scheme %q", output.scheme)
	}
	counter := &countingWriteCloser{writer: base}
	writer, err := wrapCSVExportWriter(counter, compression)
	if err != nil {
		return nil, err
	}
	return &csvExportOutput{WriteCloser: writer, counter: counter}, nil
}

type compressedWriteCloser struct {
	writer io.WriteCloser
	base   io.Closer
}

func (w compressedWriteCloser) Write(p []byte) (int, error) {
	return w.writer.Write(p)
}

func (w compressedWriteCloser) Close() error {
	writerErr := w.writer.Close()
	baseErr := w.base.Close()
	return errors.Join(writerErr, baseErr)
}

func wrapCSVExportWriter(base io.WriteCloser, compression string) (io.WriteCloser, error) {
	compression, err := normalizeCompression(compression)
	if err != nil {
		base.Close()
		return nil, err
	}
	switch compression {
	case compressionNone:
		return base, nil
	case compressionGzip:
		return compressedWriteCloser{
			writer: gzip.NewWriter(base),
			base:   base,
		}, nil
	default:
		base.Close()
		return nil, fmt.Errorf("unsupported compression %q", compression)
	}
}

type httpExportWriter struct {
	writer *io.PipeWriter
	done   chan error
}

func newHTTPExportWriter(ctx context.Context, outputURI, compression string) (*httpExportWriter, error) {
	reader, writer := io.Pipe()
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, outputURI, reader)
	if err != nil {
		reader.Close()
		writer.Close()
		return nil, fmt.Errorf("create CSV output request: %w", err)
	}
	request.Header.Set("Content-Type", "text/csv")
	if compression == compressionGzip {
		request.Header.Set("Content-Encoding", "gzip")
	}

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

func writeCSVExportRows(w io.Writer, rows exportRows) (int64, error) {
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	for _, column := range columns {
		if column == csvNullBitmapColumn {
			return 0, fmt.Errorf("source column name %q conflicts with internal CSV null bitmap column", csvNullBitmapColumn)
		}
	}
	writer := csv.NewWriter(w)
	header := append(append([]string(nil), columns...), csvNullBitmapColumn)
	if err := writer.Write(header); err != nil {
		return 0, err
	}

	var exportedRows int64
	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return 0, err
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
			return 0, err
		}
		exportedRows++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return 0, err
	}
	return exportedRows, nil
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
	fieldsRaw := fs.String("fields", "", "comma-separated TiDB IMPORT INTO field list")
	requireEmptyTarget := fs.Bool("require-empty-target", false, "preflight that the target table is empty before sql-insert import")
	compression := fs.String("compression", compressionNone, "import source compression: none or gzip")
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
	normalizedCompression, err := normalizeCompression(*compression)
	if err != nil {
		fmt.Fprintf(stderr, "executor import: %v\n", err)
		return 1
	}
	if err := validateCompressionForImportEngine(normalizedCompression, normalizedEngine); err != nil {
		fmt.Fprintf(stderr, "executor import: %v\n", err)
		return 1
	}
	fields, err := parseImportFields(*fieldsRaw)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if len(fields) > 0 && normalizedEngine != importEngineTiDBImportInto {
		fmt.Fprintln(stderr, "executor import: fields is only supported with tidb-import-into")
		return 1
	}
	if err := validateImportSourceURIForEngine(normalizedEngine, *sourceURI); err != nil {
		fmt.Fprintf(stderr, "executor import: %v\n", err)
		return 1
	}
	if _, err := quoteTiDBObjectName(*targetObject); err != nil {
		fmt.Fprintf(stderr, "executor import: %v\n", err)
		return 1
	}
	if normalizedEngine == importEngineTiDBImportInto {
		if err := requireTiDBImportIntoFieldsForRemoteSource(*sourceURI, fields); err != nil {
			fmt.Fprintf(stderr, "executor import: %v\n", err)
			return 1
		}
	}
	if *execute && *importBatchSize <= 0 {
		fmt.Fprintln(stderr, "executor import: import batch size must be positive")
		return 1
	}
	if *execute {
		result, err := executeTiDBImport(context.Background(), importExecuteSpec{
			ImportEngine:              normalizedEngine,
			TargetObject:              *targetObject,
			SourceURI:                 *sourceURI,
			TargetConnectionStringEnv: *targetConnectionStringEnv,
			ImportBatchSize:           *importBatchSize,
			Fields:                    fields,
			RequireEmptyTarget:        *requireEmptyTarget,
			Compression:               normalizedCompression,
		})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "executor import completed: %s -> %s\n", *sourceURI, *targetObject)
		if normalizedEngine == importEngineSQLInsert {
			fmt.Fprintf(stdout, "imported rows: %d\n", result.ImportedRows)
			fmt.Fprintf(stdout, "input bytes: %d\n", result.InputBytes)
		}
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
	if normalizedCompression != compressionNone {
		fmt.Fprintf(stdout, "compression: %s\n", normalizedCompression)
	}
	if len(fields) > 0 {
		fmt.Fprintf(stdout, "fields: %s\n", strings.Join(fields, ","))
	}
	if *requireEmptyTarget {
		fmt.Fprintln(stdout, "require empty target: true")
	}
	if strings.TrimSpace(*dependsOnExportChunk) != "" {
		fmt.Fprintf(stdout, "depends on export chunk: %s\n", *dependsOnExportChunk)
	}
	fmt.Fprintln(stdout, "No TiDB connection will be opened.")
	return 0
}

func validateImportSourceURIForEngine(engine, sourceURI string) error {
	switch engine {
	case importEngineSQLInsert:
		_, err := parseImportSourceURI(sourceURI)
		return err
	case importEngineTiDBImportInto:
		_, err := normalizeTiDBImportIntoFileLocation(sourceURI)
		return err
	default:
		return fmt.Errorf("unsupported import engine %q", engine)
	}
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
	if err := validateCountInputsNoTODO(*predicate, *targetPredicate); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := buildSQLServerCountQuery(*sourceObject, *predicate); err != nil {
		fmt.Fprintf(stderr, "executor validate-count: %v\n", err)
		return 1
	}
	if _, err := buildTiDBCountQuery(*targetObject, *targetPredicate); err != nil {
		fmt.Fprintf(stderr, "executor validate-count: %v\n", err)
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
	if err := validateQueryInputsNoTODO(*sourceSQL, *targetSQL); err != nil {
		fmt.Fprintf(stderr, "executor validate-query failed: check-id=%s error=%v\n", *checkID, err)
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

func validateCountInputsNoTODO(predicate, targetPredicate string) error {
	if containsTODO(predicate) {
		return fmt.Errorf("executor validate-count: predicate still contains TODO")
	}
	if containsTODO(targetPredicate) {
		return fmt.Errorf("executor validate-count: target predicate still contains TODO")
	}
	return nil
}

func validateQueryInputsNoTODO(sourceSQL, targetSQL string) error {
	if containsTODO(sourceSQL) {
		return fmt.Errorf("executor validate-query: source_sql still contains TODO")
	}
	if containsTODO(targetSQL) {
		return fmt.Errorf("executor validate-query: target_sql still contains TODO")
	}
	return nil
}

func executeValidateCount(ctx context.Context, spec validateCountExecuteSpec) (validateCountResult, error) {
	if err := validateCountInputsNoTODO(spec.Predicate, spec.TargetPredicate); err != nil {
		return validateCountResult{}, err
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
	if err := validateQueryInputsNoTODO(spec.SourceSQL, spec.TargetSQL); err != nil {
		return validateQueryResult{}, err
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
	Fields                    []string
	RequireEmptyTarget        bool
	Compression               string
}

type importExecuteResult struct {
	ImportedRows int64
	InputBytes   int64
}

func executeTiDBImport(ctx context.Context, spec importExecuteSpec) (importExecuteResult, error) {
	engine, err := normalizeImportEngine(spec.ImportEngine)
	if err != nil {
		return importExecuteResult{}, err
	}
	compression, err := normalizeCompression(spec.Compression)
	if err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: %w", err)
	}
	if err := validateCompressionForImportEngine(compression, engine); err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: %w", err)
	}
	if engine == importEngineTiDBImportInto {
		return executeTiDBImportInto(ctx, spec)
	}
	if spec.ImportBatchSize <= 0 {
		return importExecuteResult{}, fmt.Errorf("executor import: import batch size must be positive")
	}

	source, err := parseImportSourceURI(spec.SourceURI)
	if err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: %w", err)
	}

	envName := strings.TrimSpace(spec.TargetConnectionStringEnv)
	if envName == "" {
		envName = defaultTargetConnectionStringEnv
	}
	connectionString := strings.TrimSpace(os.Getenv(envName))
	if connectionString == "" {
		return importExecuteResult{}, fmt.Errorf("executor import: target connection string env %s is not set", envName)
	}

	db, err := openMySQLDB(connectionString)
	if err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: open TiDB connection: %w", err)
	}
	defer db.Close()

	if spec.RequireEmptyTarget {
		if err := ensureTiDBImportTargetEmpty(ctx, db, spec.TargetObject); err != nil {
			return importExecuteResult{}, fmt.Errorf("executor import: %w", err)
		}
	}

	sourceReader, err := openParsedCSVImportSource(ctx, source)
	if err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: %w", err)
	}
	countingSourceReader := &countingReadCloser{reader: sourceReader}
	sourceReader, err = wrapCSVImportReader(countingSourceReader, compression)
	if err != nil {
		countingSourceReader.Close()
		return importExecuteResult{}, fmt.Errorf("executor import: %w", err)
	}
	defer sourceReader.Close()

	reader, err := newCSVImportReader(sourceReader)
	if err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: read CSV source: %w", err)
	}
	insertSQL, err := buildTiDBInsertStatement(spec.TargetObject, reader.Columns())
	if err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: %w", err)
	}

	importedRows, err := insertCSVImportRows(ctx, db, insertSQL, reader, spec.ImportBatchSize)
	if err != nil {
		return importExecuteResult{}, err
	}
	return importExecuteResult{
		ImportedRows: importedRows,
		InputBytes:   countingSourceReader.BytesRead(),
	}, nil
}

func executeTiDBImportInto(ctx context.Context, spec importExecuteSpec) (importExecuteResult, error) {
	compression, err := normalizeCompression(spec.Compression)
	if err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: %w", err)
	}
	if err := validateCompressionForImportEngine(compression, importEngineTiDBImportInto); err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: %w", err)
	}
	fields := spec.Fields
	if err := requireTiDBImportIntoFieldsForRemoteSource(spec.SourceURI, fields); err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: %w", err)
	}
	if len(fields) == 0 {
		var err error
		fields, err = readTiDBImportIntoFieldsFromLocalSource(spec.SourceURI)
		if err != nil {
			return importExecuteResult{}, fmt.Errorf("executor import: %w", err)
		}
	}
	statement, err := buildTiDBImportIntoStatementWithFields(spec.TargetObject, spec.SourceURI, fields)
	if err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: %w", err)
	}

	envName := strings.TrimSpace(spec.TargetConnectionStringEnv)
	if envName == "" {
		envName = defaultTargetConnectionStringEnv
	}
	connectionString := strings.TrimSpace(os.Getenv(envName))
	if connectionString == "" {
		return importExecuteResult{}, fmt.Errorf("executor import: target connection string env %s is not set", envName)
	}

	db, err := openMySQLDB(connectionString)
	if err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: open TiDB connection: %w", err)
	}
	defer db.Close()

	if err := ensureTiDBImportTargetEmpty(ctx, db, spec.TargetObject); err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: %w", err)
	}

	if _, err := db.ExecContext(ctx, statement); err != nil {
		return importExecuteResult{}, fmt.Errorf("executor import: execute TiDB IMPORT INTO: %w", err)
	}
	return importExecuteResult{}, nil
}

func openCSVImportFile(sourceURI string) (io.ReadCloser, error) {
	return openCSVImportFileWithCompression(sourceURI, compressionNone)
}

func openCSVImportFileWithCompression(sourceURI, compression string) (io.ReadCloser, error) {
	source, err := parseImportSourceURI(sourceURI)
	if err != nil {
		return nil, err
	}
	reader, err := openParsedCSVImportSource(context.Background(), source)
	if err != nil {
		return nil, err
	}
	wrapped, err := wrapCSVImportReader(reader, compression)
	if err != nil {
		reader.Close()
		return nil, err
	}
	return wrapped, nil
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
		return importSourceURI{}, fmt.Errorf("only file://, http://, and https:// source URIs are supported for sql-insert import")
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

type compressedReadCloser struct {
	reader io.Reader
	closer io.Closer
	base   io.Closer
}

type countingReadCloser struct {
	reader io.ReadCloser
	bytes  int64
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytes += int64(n)
	return n, err
}

func (r *countingReadCloser) Close() error {
	return r.reader.Close()
}

func (r *countingReadCloser) BytesRead() int64 {
	if r == nil {
		return 0
	}
	return r.bytes
}

func (r compressedReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r compressedReadCloser) Close() error {
	closeErr := r.closer.Close()
	baseErr := r.base.Close()
	return errors.Join(closeErr, baseErr)
}

func wrapCSVImportReader(base io.ReadCloser, compression string) (io.ReadCloser, error) {
	compression, err := normalizeCompression(compression)
	if err != nil {
		return nil, err
	}
	switch compression {
	case compressionNone:
		return base, nil
	case compressionGzip:
		gzipReader, err := gzip.NewReader(base)
		if err != nil {
			return nil, fmt.Errorf("open gzip CSV source: %w", err)
		}
		return compressedReadCloser{
			reader: gzipReader,
			closer: gzipReader,
			base:   base,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported compression %q", compression)
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

func ensureTiDBImportTargetEmpty(ctx context.Context, db *sql.DB, targetObject string) error {
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

func parseImportFields(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	fields := make([]string, 0, len(parts))
	for _, part := range parts {
		field := strings.TrimSpace(part)
		if field == "" {
			return nil, fmt.Errorf("executor import: fields contains an empty item")
		}
		fields = append(fields, field)
	}
	if err := validateTiDBImportIntoFields(fields, "executor import: fields"); err != nil {
		return nil, err
	}
	return fields, nil
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
	if err := validateTiDBImportIntoFields(fields, "IMPORT INTO field list"); err != nil {
		return "", err
	}
	quoted := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if strings.HasPrefix(field, "@") {
			quoted = append(quoted, field)
			continue
		}
		quoted = append(quoted, quoteTiDBIdentifier(field))
	}
	return "(" + strings.Join(quoted, ", ") + ")", nil
}

func validateTiDBImportIntoFields(fields []string, label string) error {
	seenColumns := make(map[string]struct{}, len(fields))
	seenVariables := make(map[string]struct{}, len(fields))
	for _, raw := range fields {
		field := strings.TrimSpace(raw)
		if field == "" {
			return fmt.Errorf("%s contains an empty item", label)
		}
		if strings.HasPrefix(field, "@") {
			if !isValidTiDBImportIntoUserVariableField(field) {
				return fmt.Errorf("%s contains invalid user variable %q", label, field)
			}
			normalized := strings.ToLower(field)
			if _, ok := seenVariables[normalized]; ok {
				return fmt.Errorf("%s contains duplicate user variable %q", label, field)
			}
			seenVariables[normalized] = struct{}{}
			continue
		}
		normalized := strings.ToLower(field)
		if _, ok := seenColumns[normalized]; ok {
			return fmt.Errorf("%s contains duplicate column %q", label, field)
		}
		seenColumns[normalized] = struct{}{}
	}
	return nil
}

func requireTiDBImportIntoFieldsForRemoteSource(sourceURI string, fields []string) error {
	parsed, err := url.Parse(strings.TrimSpace(sourceURI))
	if err != nil {
		return fmt.Errorf("parse IMPORT INTO source URI: %w", err)
	}
	switch parsed.Scheme {
	case "s3", "gs":
		if len(fields) == 0 {
			return fmt.Errorf("fields are required for %s tidb-import-into source URI because remote header inspection is not implemented", parsed.Scheme)
		}
	}
	return nil
}

func isValidTiDBImportIntoUserVariableField(field string) bool {
	if len(field) <= 1 || field[0] != '@' {
		return false
	}
	for i := 1; i < len(field); i++ {
		ch := field[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '.' || ch == '$' {
			continue
		}
		return false
	}
	return true
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
		path, err = cleanAbsoluteTiDBImportIntoLocalPath(sourceURI)
		if err != nil {
			return nil, err
		}
	case "file":
		if parsed.Host != "" && parsed.Host != "localhost" {
			return nil, fmt.Errorf("file source URI host must be empty or localhost")
		}
		if strings.TrimSpace(parsed.Path) == "" {
			return nil, fmt.Errorf("file source URI path is required")
		}
		path, err = cleanAbsoluteTiDBImportIntoLocalPath(parsed.Path)
		if err != nil {
			return nil, err
		}
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
		return cleanAbsoluteTiDBImportIntoLocalPath(sourceURI)
	case "file":
		if parsed.Host != "" && parsed.Host != "localhost" {
			return "", fmt.Errorf("file source URI host must be empty or localhost")
		}
		if strings.TrimSpace(parsed.Path) == "" {
			return "", fmt.Errorf("file source URI path is required")
		}
		return cleanAbsoluteTiDBImportIntoLocalPath(parsed.Path)
	case "s3", "gs":
		if err := validateTiDBImportIntoObjectStorageLocation(parsed); err != nil {
			return "", err
		}
		return parsed.String(), nil
	default:
		return "", fmt.Errorf("IMPORT INTO source URI scheme %s is not supported; supported schemes: file, s3, gs, or local path", parsed.Scheme)
	}
}

func cleanAbsoluteTiDBImportIntoLocalPath(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("local IMPORT INTO source path must be absolute")
	}
	return path, nil
}

func validateTiDBImportIntoObjectStorageLocation(parsed *url.URL) error {
	if strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("%s IMPORT INTO source URI bucket is required", parsed.Scheme)
	}
	if strings.Trim(strings.TrimSpace(parsed.Path), "/") == "" {
		return fmt.Errorf("%s IMPORT INTO source URI object path is required", parsed.Scheme)
	}
	return nil
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

func insertCSVImportRows(ctx context.Context, db *sql.DB, insertSQL string, reader *csvImportReader, batchSize int) (int64, error) {
	if batchSize <= 0 {
		return 0, fmt.Errorf("executor import: import batch size must be positive")
	}

	var rowNumber int64
	for {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return 0, fmt.Errorf("executor import: begin transaction: %w", err)
		}
		stmt, err := tx.PrepareContext(ctx, insertSQL)
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("executor import: prepare insert: %w", err)
		}

		rowsInBatch := 0
		for rowsInBatch < batchSize {
			args, err := reader.ReadValues()
			if errors.Is(err, io.EOF) {
				stmt.Close()
				if rowsInBatch == 0 {
					tx.Rollback()
					return rowNumber, nil
				}
				if err := tx.Commit(); err != nil {
					return 0, fmt.Errorf("executor import: commit transaction: %w", err)
				}
				return rowNumber, nil
			}
			if err != nil {
				stmt.Close()
				tx.Rollback()
				return 0, fmt.Errorf("executor import: read CSV row %d: %w", rowNumber+1, err)
			}
			if _, err := stmt.ExecContext(ctx, args...); err != nil {
				stmt.Close()
				tx.Rollback()
				return 0, fmt.Errorf("executor import: insert row %d: %w", rowNumber+1, err)
			}
			rowNumber++
			rowsInBatch++
		}

		if err := stmt.Close(); err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("executor import: close insert statement: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("executor import: commit transaction: %w", err)
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
	columnsRaw := fs.String("columns", "", "comma-separated SQL Server CDC captured columns")
	keyColumnsRaw := fs.String("key-columns", "", "comma-separated target key columns for CDC upsert/delete apply")
	fromLSNRaw := fs.String("from-lsn", "", "inclusive SQL Server CDC from LSN as a 10-byte hex value")
	toLSNRaw := fs.String("to-lsn", "", "inclusive SQL Server CDC to LSN as a 10-byte hex value")
	applyBatchSize := fs.Int("apply-batch-size", 0, "apply batch size")
	sourceConnectionStringEnv := fs.String("source-connection-string-env", defaultSourceConnectionStringEnv, "environment variable containing the SQL Server CDC connection string")
	targetConnectionStringEnv := fs.String("target-connection-string-env", defaultTargetConnectionStringEnv, "environment variable containing the TiDB/MySQL connection string")
	execute := fs.Bool("execute", false, "perform CDC apply for the provided LSN range")
	if err := fs.Parse(args); err != nil {
		return 2
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
	keyColumns, err := parseCDCKeyColumns(*keyColumnsRaw)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	columns, err := parseCDCColumns(*columnsRaw)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *applyBatchSize <= 0 {
		fmt.Fprintln(stderr, "executor cdc: apply batch size must be positive")
		return 1
	}
	if err := validateCDCKeyColumnsInCapturedColumns(columns, keyColumns); err != nil {
		fmt.Fprintf(stderr, "executor cdc: %v\n", err)
		return 1
	}
	fromLSN, err := parseSQLServerCDCLSN(*fromLSNRaw, "from LSN")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	toLSN, err := parseSQLServerCDCLSN(*toLSNRaw, "to LSN")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := validateSQLServerCDCLSNRange(fromLSN, toLSN); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := buildSQLServerCDCChangesQuery(*sourceObject, columns); err != nil {
		fmt.Fprintf(stderr, "executor cdc: %v\n", err)
		return 1
	}
	if _, err := buildTiDBCDCUpsertStatement(*targetObject, columns, keyColumns); err != nil {
		fmt.Fprintf(stderr, "executor cdc: %v\n", err)
		return 1
	}
	if *execute {
		result, err := executeCDCApplyFunc(context.Background(), cdcExecuteSpec{
			SourceObject:              *sourceObject,
			TargetObject:              *targetObject,
			SourceConnectionStringEnv: *sourceConnectionStringEnv,
			TargetConnectionStringEnv: *targetConnectionStringEnv,
			Columns:                   columns,
			KeyColumns:                keyColumns,
			FromLSN:                   fromLSN,
			ToLSN:                     toLSN,
		})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "executor cdc completed: %s -> %s\n", *sourceObject, *targetObject)
		fmt.Fprintf(stdout, "applied changes: %d\n", result.AppliedChanges)
		return 0
	}

	fmt.Fprintln(stdout, "executor cdc dry run")
	fmt.Fprintf(stdout, "root: %s\n", *root)
	fmt.Fprintf(stdout, "source cluster: %s\n", *sourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", *projectID)
	fmt.Fprintf(stdout, "source object: %s\n", *sourceObject)
	fmt.Fprintf(stdout, "target object: %s\n", *targetObject)
	fmt.Fprintf(stdout, "columns: %s\n", strings.Join(columns, ","))
	fmt.Fprintf(stdout, "key columns: %s\n", strings.Join(keyColumns, ","))
	if strings.TrimSpace(*fromLSNRaw) != "" {
		fmt.Fprintf(stdout, "from LSN: %s\n", strings.TrimSpace(*fromLSNRaw))
	}
	if strings.TrimSpace(*toLSNRaw) != "" {
		fmt.Fprintf(stdout, "to LSN: %s\n", strings.TrimSpace(*toLSNRaw))
	}
	fmt.Fprintf(stdout, "apply batch size: %s\n", strconv.Itoa(*applyBatchSize))
	fmt.Fprintln(stdout, "No CDC reader or TiDB apply worker will be started.")
	return 0
}

func validateCDCKeyColumnsInCapturedColumns(columns, keyColumns []string) error {
	columnSet := make(map[string]string, len(columns))
	for _, column := range columns {
		column = strings.TrimSpace(column)
		columnSet[strings.ToLower(column)] = column
	}
	_, err := normalizeCDCKeyColumnSet(keyColumns, columnSet)
	return err
}

type cdcExecuteSpec struct {
	SourceObject              string
	TargetObject              string
	SourceConnectionStringEnv string
	TargetConnectionStringEnv string
	Columns                   []string
	KeyColumns                []string
	FromLSN                   []byte
	ToLSN                     []byte
}

type cdcApplyResult struct {
	AppliedChanges int
}

var executeCDCApplyFunc = executeCDCApply

func executeCDCApply(ctx context.Context, spec cdcExecuteSpec) (cdcApplyResult, error) {
	_ = ctx
	if len(spec.Columns) == 0 {
		return cdcApplyResult{}, fmt.Errorf("executor cdc: columns is required")
	}
	if len(spec.KeyColumns) == 0 {
		return cdcApplyResult{}, fmt.Errorf("executor cdc: key columns is required")
	}
	columnSet := make(map[string]string, len(spec.Columns))
	for _, column := range spec.Columns {
		column = strings.TrimSpace(column)
		if column == "" {
			return cdcApplyResult{}, fmt.Errorf("executor cdc: columns contains an empty item")
		}
		normalized := strings.ToLower(column)
		if _, ok := columnSet[normalized]; ok {
			return cdcApplyResult{}, fmt.Errorf("executor cdc: columns contains duplicate column %s", column)
		}
		columnSet[normalized] = column
	}
	if _, err := normalizeCDCKeyColumnSet(spec.KeyColumns, columnSet); err != nil {
		return cdcApplyResult{}, fmt.Errorf("executor cdc: %w", err)
	}
	if len(spec.FromLSN) == 0 {
		return cdcApplyResult{}, fmt.Errorf("executor cdc: from LSN is required")
	}
	if len(spec.ToLSN) == 0 {
		return cdcApplyResult{}, fmt.Errorf("executor cdc: to LSN is required")
	}
	if err := validateSQLServerCDCLSNRange(spec.FromLSN, spec.ToLSN); err != nil {
		return cdcApplyResult{}, err
	}
	sourceEnvName := strings.TrimSpace(spec.SourceConnectionStringEnv)
	if sourceEnvName == "" {
		sourceEnvName = defaultSourceConnectionStringEnv
	}
	sourceConnectionString := strings.TrimSpace(os.Getenv(sourceEnvName))
	if sourceConnectionString == "" {
		return cdcApplyResult{}, fmt.Errorf("executor cdc: source connection string env %s is not set", sourceEnvName)
	}

	targetEnvName := strings.TrimSpace(spec.TargetConnectionStringEnv)
	if targetEnvName == "" {
		targetEnvName = defaultTargetConnectionStringEnv
	}
	targetConnectionString := strings.TrimSpace(os.Getenv(targetEnvName))
	if targetConnectionString == "" {
		return cdcApplyResult{}, fmt.Errorf("executor cdc: target connection string env %s is not set", targetEnvName)
	}

	sourceDB, err := sql.Open("sqlserver", sourceConnectionString)
	if err != nil {
		return cdcApplyResult{}, fmt.Errorf("executor cdc: open SQL Server connection: %w", err)
	}
	defer sourceDB.Close()

	targetDB, err := sql.Open("mysql", targetConnectionString)
	if err != nil {
		return cdcApplyResult{}, fmt.Errorf("executor cdc: open TiDB connection: %w", err)
	}
	defer targetDB.Close()

	applied, err := applySQLServerCDCChanges(ctx, sqlServerCDCQuerier{db: sourceDB}, targetDB, cdcApplySpec{
		SourceObject: spec.SourceObject,
		TargetObject: spec.TargetObject,
		Columns:      spec.Columns,
		KeyColumns:   spec.KeyColumns,
		FromLSN:      spec.FromLSN,
		ToLSN:        spec.ToLSN,
	})
	if err != nil {
		return cdcApplyResult{}, fmt.Errorf("executor cdc: %w", err)
	}
	return cdcApplyResult{AppliedChanges: applied}, nil
}

type cdcApplySpec struct {
	SourceObject string
	TargetObject string
	Columns      []string
	KeyColumns   []string
	FromLSN      []byte
	ToLSN        []byte
}

type cdcChangeQuerier interface {
	QueryCDCChanges(ctx context.Context, query string, fromLSN, toLSN []byte) (exportRows, error)
}

type sqlServerCDCQuerier struct {
	db *sql.DB
}

func (querier sqlServerCDCQuerier) QueryCDCChanges(ctx context.Context, query string, fromLSN, toLSN []byte) (exportRows, error) {
	return querier.db.QueryContext(ctx, query, sql.Named("from_lsn", fromLSN), sql.Named("to_lsn", toLSN))
}

func applySQLServerCDCChanges(ctx context.Context, source cdcChangeQuerier, target cdcStatementExecutor, spec cdcApplySpec) (int, error) {
	query, err := buildSQLServerCDCChangesQuery(spec.SourceObject, spec.Columns)
	if err != nil {
		return 0, err
	}
	rows, err := source.QueryCDCChanges(ctx, query, spec.FromLSN, spec.ToLSN)
	if err != nil {
		return 0, fmt.Errorf("query SQL Server CDC changes: %w", err)
	}
	changes, err := readSQLServerCDCChangeRows(rows, spec.Columns)
	if err != nil {
		return 0, fmt.Errorf("read SQL Server CDC changes: %w", err)
	}
	applied := 0
	for i, change := range changes {
		if err := applyTiDBCDCChange(ctx, target, spec.TargetObject, spec.KeyColumns, change); err != nil {
			return applied, fmt.Errorf("apply CDC change %d: %w", i+1, err)
		}
		if change.Operation != cdcApplySkip {
			applied++
		}
	}
	return applied, nil
}

func buildTiDBCDCUpsertStatement(targetObject string, columns, keyColumns []string) (string, error) {
	quotedTargetObject, err := quoteTiDBObjectName(targetObject)
	if err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return "", fmt.Errorf("CDC captured columns must contain at least one column")
	}
	if len(keyColumns) == 0 {
		return "", fmt.Errorf("CDC key columns is required")
	}

	quotedColumns := make([]string, 0, len(columns))
	placeholders := make([]string, 0, len(columns))
	columnSet := make(map[string]string, len(columns))
	for _, column := range columns {
		column = strings.TrimSpace(column)
		if column == "" {
			return "", fmt.Errorf("CDC captured columns contains an empty column")
		}
		normalized := strings.ToLower(column)
		if _, ok := columnSet[normalized]; ok {
			return "", fmt.Errorf("CDC captured columns contains duplicate column %s", column)
		}
		columnSet[normalized] = column
		quotedColumns = append(quotedColumns, quoteTiDBIdentifier(column))
		placeholders = append(placeholders, "?")
	}

	keySet, err := normalizeCDCKeyColumnSet(keyColumns, columnSet)
	if err != nil {
		return "", err
	}

	assignments := make([]string, 0, len(columns))
	for _, column := range columns {
		if _, ok := keySet[strings.ToLower(column)]; ok {
			continue
		}
		quotedColumn := quoteTiDBIdentifier(column)
		assignments = append(assignments, fmt.Sprintf("%s = VALUES(%s)", quotedColumn, quotedColumn))
	}
	if len(assignments) == 0 {
		keyColumn := strings.TrimSpace(keyColumns[0])
		quotedKeyColumn := quoteTiDBIdentifier(keyColumn)
		assignments = append(assignments, fmt.Sprintf("%s = VALUES(%s)", quotedKeyColumn, quotedKeyColumn))
	}

	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON DUPLICATE KEY UPDATE %s",
		quotedTargetObject,
		strings.Join(quotedColumns, ", "),
		strings.Join(placeholders, ", "),
		strings.Join(assignments, ", "),
	), nil
}

func buildTiDBCDCDeleteStatement(targetObject string, keyColumns []string) (string, error) {
	quotedTargetObject, err := quoteTiDBObjectName(targetObject)
	if err != nil {
		return "", err
	}
	if len(keyColumns) == 0 {
		return "", fmt.Errorf("CDC key columns is required")
	}
	seenColumns := map[string]struct{}{}
	predicates := make([]string, 0, len(keyColumns))
	for _, column := range keyColumns {
		column = strings.TrimSpace(column)
		if column == "" {
			return "", fmt.Errorf("CDC key columns contains an empty column")
		}
		normalized := strings.ToLower(column)
		if _, ok := seenColumns[normalized]; ok {
			return "", fmt.Errorf("CDC key columns contains duplicate column %s", column)
		}
		seenColumns[normalized] = struct{}{}
		predicates = append(predicates, fmt.Sprintf("%s = ?", quoteTiDBIdentifier(column)))
	}
	return fmt.Sprintf("DELETE FROM %s WHERE %s", quotedTargetObject, strings.Join(predicates, " AND ")), nil
}

func normalizeCDCKeyColumnSet(keyColumns []string, columnSet map[string]string) (map[string]struct{}, error) {
	keySet := make(map[string]struct{}, len(keyColumns))
	for _, column := range keyColumns {
		column = strings.TrimSpace(column)
		if column == "" {
			return nil, fmt.Errorf("CDC key columns contains an empty column")
		}
		normalized := strings.ToLower(column)
		if _, ok := keySet[normalized]; ok {
			return nil, fmt.Errorf("CDC key columns contains duplicate column %s", column)
		}
		if _, ok := columnSet[normalized]; !ok {
			return nil, fmt.Errorf("CDC key column %s is not present in captured columns", column)
		}
		keySet[normalized] = struct{}{}
	}
	return keySet, nil
}

type cdcApplyOperation string

const (
	cdcApplyDelete cdcApplyOperation = "delete"
	cdcApplyUpsert cdcApplyOperation = "upsert"
	cdcApplySkip   cdcApplyOperation = "skip"
)

type cdcChangeRow struct {
	Operation cdcApplyOperation
	Columns   []string
	Values    []any
}

type cdcStatementExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func normalizeSQLServerCDCOperation(operation int) (cdcApplyOperation, error) {
	switch operation {
	case 1:
		return cdcApplyDelete, nil
	case 2, 4:
		return cdcApplyUpsert, nil
	case 3:
		return cdcApplySkip, nil
	default:
		return "", fmt.Errorf("unsupported SQL Server CDC operation %d", operation)
	}
}

func applyTiDBCDCChange(ctx context.Context, exec cdcStatementExecutor, targetObject string, keyColumns []string, change cdcChangeRow) error {
	if len(change.Columns) != len(change.Values) {
		return fmt.Errorf("CDC change column count %d does not match value count %d", len(change.Columns), len(change.Values))
	}
	switch change.Operation {
	case cdcApplySkip:
		return nil
	case cdcApplyUpsert:
		statement, err := buildTiDBCDCUpsertStatement(targetObject, change.Columns, keyColumns)
		if err != nil {
			return err
		}
		if _, err := exec.ExecContext(ctx, statement, change.Values...); err != nil {
			return fmt.Errorf("execute CDC upsert: %w", err)
		}
		return nil
	case cdcApplyDelete:
		statement, err := buildTiDBCDCDeleteStatement(targetObject, keyColumns)
		if err != nil {
			return err
		}
		args, err := cdcKeyValues(change.Columns, change.Values, keyColumns)
		if err != nil {
			return err
		}
		if _, err := exec.ExecContext(ctx, statement, args...); err != nil {
			return fmt.Errorf("execute CDC delete: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported CDC apply operation %q", change.Operation)
	}
}

func cdcKeyValues(columns []string, values []any, keyColumns []string) ([]any, error) {
	if len(columns) != len(values) {
		return nil, fmt.Errorf("CDC change column count %d does not match value count %d", len(columns), len(values))
	}
	valueByColumn := make(map[string]any, len(columns))
	for i, column := range columns {
		column = strings.TrimSpace(column)
		if column == "" {
			return nil, fmt.Errorf("CDC captured columns contains an empty column")
		}
		valueByColumn[strings.ToLower(column)] = values[i]
	}
	args := make([]any, 0, len(keyColumns))
	for _, column := range keyColumns {
		column = strings.TrimSpace(column)
		value, ok := valueByColumn[strings.ToLower(column)]
		if !ok {
			return nil, fmt.Errorf("CDC key column %s is not present in captured columns", column)
		}
		args = append(args, value)
	}
	return args, nil
}

func buildSQLServerCDCChangesQuery(sourceObject string, columns []string) (string, error) {
	parts, err := sqlServerCDCObjectParts(sourceObject)
	if err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return "", fmt.Errorf("CDC captured columns must contain at least one column")
	}

	functionParts := make([]string, 0, 3)
	if len(parts) == 3 {
		functionParts = append(functionParts, quoteSQLServerIdentifier(strings.TrimSpace(parts[0])))
	}
	schemaName := strings.TrimSpace(parts[len(parts)-2])
	tableName := strings.TrimSpace(parts[len(parts)-1])
	captureInstance := schemaName + "_" + tableName
	functionParts = append(functionParts,
		quoteSQLServerIdentifier("cdc"),
		quoteSQLServerIdentifier("fn_cdc_get_all_changes_"+captureInstance),
	)

	selectColumns := []string{
		quoteSQLServerIdentifier("__$operation"),
		quoteSQLServerIdentifier("__$start_lsn"),
		quoteSQLServerIdentifier("__$seqval"),
	}
	seenColumns := map[string]struct{}{}
	for _, column := range columns {
		column = strings.TrimSpace(column)
		if column == "" {
			return "", fmt.Errorf("CDC captured columns contains an empty column")
		}
		normalized := strings.ToLower(column)
		if _, ok := seenColumns[normalized]; ok {
			return "", fmt.Errorf("CDC captured columns contains duplicate column %s", column)
		}
		seenColumns[normalized] = struct{}{}
		selectColumns = append(selectColumns, quoteSQLServerIdentifier(column))
	}

	return fmt.Sprintf(
		"SELECT %s FROM %s(@from_lsn, @to_lsn, 'all update old') ORDER BY %s, %s",
		strings.Join(selectColumns, ", "),
		strings.Join(functionParts, "."),
		quoteSQLServerIdentifier("__$start_lsn"),
		quoteSQLServerIdentifier("__$seqval"),
	), nil
}

func buildSQLServerCDCMaxLSNQuery(sourceObject string) (string, error) {
	if strings.TrimSpace(sourceObject) == "" {
		return "SELECT sys.fn_cdc_get_max_lsn()", nil
	}
	parts, err := sqlServerCDCObjectParts(sourceObject)
	if err != nil {
		return "", err
	}
	if len(parts) == 3 {
		return fmt.Sprintf("SELECT %s.%s.%s()",
			quoteSQLServerIdentifier(strings.TrimSpace(parts[0])),
			quoteSQLServerIdentifier("sys"),
			quoteSQLServerIdentifier("fn_cdc_get_max_lsn"),
		), nil
	}
	return "SELECT sys.fn_cdc_get_max_lsn()", nil
}

func buildSQLServerCDCMinLSNQuery(sourceObject string) (string, string, error) {
	parts, err := sqlServerCDCObjectParts(sourceObject)
	if err != nil {
		return "", "", err
	}
	captureInstance, err := sqlServerCDCCaptureInstance(sourceObject)
	if err != nil {
		return "", "", err
	}
	if len(parts) == 3 {
		return fmt.Sprintf("SELECT %s.%s.%s(@capture_instance)",
			quoteSQLServerIdentifier(strings.TrimSpace(parts[0])),
			quoteSQLServerIdentifier("sys"),
			quoteSQLServerIdentifier("fn_cdc_get_min_lsn"),
		), captureInstance, nil
	}
	return "SELECT sys.fn_cdc_get_min_lsn(@capture_instance)", captureInstance, nil
}

func sqlServerCDCCaptureInstance(sourceObject string) (string, error) {
	parts, err := sqlServerCDCObjectParts(sourceObject)
	if err != nil {
		return "", err
	}
	schemaName := strings.TrimSpace(parts[len(parts)-2])
	tableName := strings.TrimSpace(parts[len(parts)-1])
	return schemaName + "_" + tableName, nil
}

func sqlServerCDCObjectParts(sourceObject string) ([]string, error) {
	parts := strings.Split(strings.TrimSpace(sourceObject), ".")
	if len(parts) != 2 && len(parts) != 3 {
		return nil, fmt.Errorf("source object must be schema.table or database.schema.table")
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return nil, fmt.Errorf("source object contains an empty identifier")
		}
	}
	return parts, nil
}

func readSQLServerCDCChangeRows(rows exportRows, capturedColumns []string) ([]cdcChangeRow, error) {
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	expectedColumns := append([]string{"__$operation", "__$start_lsn", "__$seqval"}, capturedColumns...)
	if len(columns) != len(expectedColumns) {
		return nil, fmt.Errorf("CDC query returned %d columns, want %d", len(columns), len(expectedColumns))
	}
	for i, expected := range expectedColumns {
		if !strings.EqualFold(columns[i], expected) {
			return nil, fmt.Errorf("CDC query column %d = %q, want %q", i+1, columns[i], expected)
		}
	}

	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	var changes []cdcChangeRow
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		operationCode, err := cdcOperationCode(values[0])
		if err != nil {
			return nil, err
		}
		operation, err := normalizeSQLServerCDCOperation(operationCode)
		if err != nil {
			return nil, err
		}
		changeValues := append([]any(nil), values[3:]...)
		changes = append(changes, cdcChangeRow{
			Operation: operation,
			Columns:   append([]string(nil), capturedColumns...),
			Values:    changeValues,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return changes, nil
}

func cdcOperationCode(value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case []byte:
		code, err := strconv.Atoi(string(v))
		if err != nil {
			return 0, fmt.Errorf("parse SQL Server CDC operation %q: %w", string(v), err)
		}
		return code, nil
	case string:
		code, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("parse SQL Server CDC operation %q: %w", v, err)
		}
		return code, nil
	default:
		return 0, fmt.Errorf("unsupported SQL Server CDC operation type %T", value)
	}
}

func parseCDCKeyColumns(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("executor cdc: key columns is required")
	}
	parts := strings.Split(raw, ",")
	columns := make([]string, 0, len(parts))
	seenColumns := map[string]struct{}{}
	for _, part := range parts {
		column := strings.TrimSpace(part)
		if column == "" {
			return nil, fmt.Errorf("executor cdc: key columns contains an empty item")
		}
		normalized := strings.ToLower(column)
		if _, ok := seenColumns[normalized]; ok {
			return nil, fmt.Errorf("executor cdc: key columns contains duplicate column %s", column)
		}
		seenColumns[normalized] = struct{}{}
		columns = append(columns, column)
	}
	return columns, nil
}

func parseCDCColumns(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("executor cdc: columns is required")
	}
	parts := strings.Split(raw, ",")
	columns := make([]string, 0, len(parts))
	seenColumns := map[string]struct{}{}
	for _, part := range parts {
		column := strings.TrimSpace(part)
		if column == "" {
			return nil, fmt.Errorf("executor cdc: columns contains an empty item")
		}
		normalized := strings.ToLower(column)
		if _, ok := seenColumns[normalized]; ok {
			return nil, fmt.Errorf("executor cdc: columns contains duplicate column %s", column)
		}
		seenColumns[normalized] = struct{}{}
		columns = append(columns, column)
	}
	return columns, nil
}

func parseSQLServerCDCLSN(raw, label string) ([]byte, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, fmt.Errorf("executor cdc: %s is required", label)
	}
	value = strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 10 {
		return nil, fmt.Errorf("executor cdc: %s must be a 10-byte hex value", label)
	}
	return decoded, nil
}

func validateSQLServerCDCLSNRange(fromLSN, toLSN []byte) error {
	if bytes.Compare(fromLSN, toLSN) > 0 {
		return fmt.Errorf("executor cdc: from LSN must be less than or equal to to LSN")
	}
	return nil
}

func formatSQLServerCDCLSN(value []byte, label string) (string, error) {
	if len(value) != 10 {
		return "", fmt.Errorf("executor cdc-lsn: %s must be a 10-byte value", label)
	}
	return "0x" + hex.EncodeToString(value), nil
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
  sqlserver2tidb-executor cdc-lsn --execute --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --source-object sales.dbo.orders
  sqlserver2tidb-executor cdc --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --source-object sales.dbo.orders --target-object app.orders --columns id,customer_name --key-columns id --from-lsn 0x00000027000001f40001 --to-lsn 0x00000027000001f40002 --apply-batch-size 1000
`)
}
