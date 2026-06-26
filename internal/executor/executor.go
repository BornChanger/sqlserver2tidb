package executor

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/microsoft/go-mssqldb"
)

const defaultSourceConnectionStringEnv = "SQLSERVER2TIDB_SOURCE_CONNECTION_STRING"
const defaultTargetConnectionStringEnv = "SQLSERVER2TIDB_TARGET_CONNECTION_STRING"

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
	case "cdc":
		return runCDC(args[1:], stdout, stderr)
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
	parts := strings.Split(script, ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		statement := strings.TrimSpace(part)
		if statement == "" {
			continue
		}
		statements = append(statements, statement)
	}
	return statements
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
	fmt.Fprintln(stdout, "No object storage write will be attempted.")
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

	outputPath, err := resolveExportOutputFile(spec.OutputURI)
	if err != nil {
		return fmt.Errorf("executor export: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
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

	file, err := os.Create(outputPath)
	if err != nil {
		rows.Close()
		return fmt.Errorf("executor export: create output file: %w", err)
	}
	if err := writeCSVExportRows(file, rows); err != nil {
		file.Close()
		return fmt.Errorf("executor export: write CSV output: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("executor export: close output file: %w", err)
	}
	return nil
}

func resolveExportOutputFile(outputURI string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(outputURI))
	if err != nil {
		return "", fmt.Errorf("parse output uri: %w", err)
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("only file:// output URIs are supported for --execute")
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		return "", fmt.Errorf("file output URI host must be empty or localhost")
	}
	if strings.TrimSpace(parsed.Path) == "" {
		return "", fmt.Errorf("file output URI path is required")
	}
	return filepath.Clean(parsed.Path), nil
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

func writeCSVExportRows(w io.Writer, rows exportRows) error {
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	writer := csv.NewWriter(w)
	if err := writer.Write(columns); err != nil {
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
		record := make([]string, len(values))
		for i, value := range values {
			record[i] = formatCSVValue(value)
		}
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
	if *execute && *importBatchSize <= 0 {
		fmt.Fprintln(stderr, "executor import: import batch size must be positive")
		return 1
	}
	if *execute {
		if err := executeTiDBImport(context.Background(), importExecuteSpec{
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

type importExecuteSpec struct {
	TargetObject              string
	SourceURI                 string
	TargetConnectionStringEnv string
	ImportBatchSize           int
}

func executeTiDBImport(ctx context.Context, spec importExecuteSpec) error {
	sourcePath, err := resolveImportSourceFile(spec.SourceURI)
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

	file, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("executor import: open CSV source: %w", err)
	}
	defer file.Close()

	reader, err := newCSVImportReader(file)
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

func openCSVImportFile(sourceURI string) (*os.File, error) {
	path, err := resolveImportSourceFile(sourceURI)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open CSV source: %w", err)
	}
	return file, nil
}

func resolveImportSourceFile(sourceURI string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(sourceURI))
	if err != nil {
		return "", fmt.Errorf("parse source uri: %w", err)
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("only file:// source URIs are supported for --execute")
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		return "", fmt.Errorf("file source URI host must be empty or localhost")
	}
	if strings.TrimSpace(parsed.Path) == "" {
		return "", fmt.Errorf("file source URI path is required")
	}
	return filepath.Clean(parsed.Path), nil
}

type csvImportReader struct {
	reader  *csv.Reader
	columns []string
}

func newCSVImportReader(r io.Reader) (*csvImportReader, error) {
	reader := csv.NewReader(r)
	columns, err := reader.Read()
	if err != nil {
		return nil, err
	}
	for _, column := range columns {
		if strings.TrimSpace(column) == "" {
			return nil, fmt.Errorf("CSV header contains an empty column name")
		}
	}
	return &csvImportReader{reader: reader, columns: columns}, nil
}

func (reader *csvImportReader) Columns() []string {
	return append([]string(nil), reader.columns...)
}

func (reader *csvImportReader) ReadRecord() ([]string, error) {
	return reader.reader.Read()
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
	parts := strings.Split(strings.TrimSpace(targetObject), ".")
	if len(parts) != 1 && len(parts) != 2 {
		return "", fmt.Errorf("target object must be table or database.table")
	}
	quotedObject := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", fmt.Errorf("target object contains an empty identifier")
		}
		quotedObject = append(quotedObject, quoteTiDBIdentifier(part))
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
		strings.Join(quotedObject, "."),
		strings.Join(quotedColumns, ", "),
		strings.Join(placeholders, ", "),
	), nil
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
			record, err := reader.ReadRecord()
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
			args := make([]any, len(record))
			for j, value := range record {
				args[j] = value
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
  sqlserver2tidb-executor apply-ddl --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --ddl-file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/tidb-ddl/dbo.orders.sql
  sqlserver2tidb-executor apply-ddl --execute --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --ddl-file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/tidb-ddl/dbo.orders.sql
  sqlserver2tidb-executor export --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --chunk-id dbo.orders.000001 --source-object sales.dbo.orders --target-object app.orders --output-uri s3://bucket/path.parquet
  sqlserver2tidb-executor export --execute --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --chunk-id dbo.orders.000001 --source-object sales.dbo.orders --target-object app.orders --output-uri file:///tmp/path.csv --predicate "id >= 1 AND id < 1000"
  sqlserver2tidb-executor import --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --job-id import-dbo.orders.000001 --target-object app.orders --source-uri s3://bucket/path.parquet
  sqlserver2tidb-executor import --execute --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --job-id import-dbo.orders.000001 --target-object app.orders --source-uri file:///tmp/path.csv --import-batch-size 1000
  sqlserver2tidb-executor validate-count --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --source-object sales.dbo.orders --target-object app.orders --predicate "id >= 1" --target-predicate "id >= 1"
  sqlserver2tidb-executor validate-count --execute --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --source-object sales.dbo.orders --target-object app.orders
  sqlserver2tidb-executor cdc --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --source-object sales.dbo.orders --target-object app.orders --apply-batch-size 1000
`)
}
