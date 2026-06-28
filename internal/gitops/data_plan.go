package gitops

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const importIntoNullBitmapField = "@sqlserver2tidb_null_bitmap"

type DataMovementPlanSpec struct {
	ObjectURIPrefix string
	ChunkSizeRows   int64
	ExportFormat    string
	ImportEngine    string
}

type DataMovementPlanResult struct {
	SourceClusterID string
	ProjectID       string
	Tables          int
	ExportChunks    int
	ImportJobs      int
	ExportPlanFile  string
	ImportPlanFile  string
}

type dataExportTablePlan struct {
	SourceObject string
	TargetObject string
	RowCount     int64
	Chunks       []dataExportChunkPlan
}

type dataExportChunkPlan struct {
	ID            string
	EstimatedRows int64
	Predicate     string
	OutputURI     string
}

type dataImportJobPlan struct {
	ID                   string
	TargetObject         string
	SourceURI            string
	DependsOnExportChunk string
	Fields               []string
}

func GenerateDataMovementPlans(root, sourceClusterID, projectID string, spec DataMovementPlanSpec) (DataMovementPlanResult, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return DataMovementPlanResult{}, err
	}
	spec = normalizeDataMovementPlanSpec(spec)
	if strings.TrimSpace(spec.ObjectURIPrefix) == "" {
		return DataMovementPlanResult{}, fmt.Errorf("object URI prefix is required")
	}
	if spec.ChunkSizeRows <= 0 {
		return DataMovementPlanResult{}, fmt.Errorf("chunk size rows must be positive")
	}
	if spec.ExportFormat != "csv" {
		return DataMovementPlanResult{}, fmt.Errorf("export format %s is not supported by sqlserver2tidb-executor; supported format: csv", spec.ExportFormat)
	}
	if err := validateSupportedImportEngine(spec.ImportEngine); err != nil {
		return DataMovementPlanResult{}, err
	}
	if err := validateExecutableObjectURIPrefix(spec.ObjectURIPrefix, spec.ImportEngine); err != nil {
		return DataMovementPlanResult{}, err
	}

	clusterDir := filepath.Join(root, "clusters", sourceClusterID)
	projectDir := filepath.Join(clusterDir, "projects", projectID)
	project, err := readProjectMetadata(filepath.Join(projectDir, "project.yaml"))
	if err != nil {
		return DataMovementPlanResult{}, err
	}
	if strings.TrimSpace(project.SourceDatabase) == "" || len(project.SourceSchemas) == 0 || strings.TrimSpace(project.TargetDatabase) == "" {
		return DataMovementPlanResult{}, fmt.Errorf("project %q is missing source database, source schemas, or target database", projectID)
	}

	inventory, err := readSQLServerInventory(filepath.Join(clusterDir, "inventory", "inventory.json"))
	if err != nil {
		return DataMovementPlanResult{}, err
	}

	var tables []dataExportTablePlan
	var jobs []dataImportJobPlan
	sourceSchemas := lowerSet(project.SourceSchemas)
	prefixSchema := len(project.SourceSchemas) > 1
	for _, database := range inventory.Databases {
		if !strings.EqualFold(database.Name, project.SourceDatabase) {
			continue
		}
		for _, schema := range database.Schemas {
			if !sourceSchemas[strings.ToLower(schema.Name)] {
				continue
			}
			for _, table := range schema.Tables {
				tablePlan := buildDataExportTablePlan(project, database.Name, schema.Name, table, prefixSchema, spec)
				for _, chunk := range tablePlan.Chunks {
					fields := []string(nil)
					if spec.ImportEngine == importEngineTiDBImportInto {
						fields = buildTiDBImportIntoPlanFields(table.Columns)
					}
					jobs = append(jobs, dataImportJobPlan{
						ID:                   "import-" + chunk.ID,
						TargetObject:         tablePlan.TargetObject,
						SourceURI:            chunk.OutputURI,
						DependsOnExportChunk: chunk.ID,
						Fields:               fields,
					})
				}
				tables = append(tables, tablePlan)
			}
		}
	}

	projectRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID))
	exportPlanRel := filepath.ToSlash(filepath.Join(projectRel, "plan", "export-plan.yaml"))
	importPlanRel := filepath.ToSlash(filepath.Join(projectRel, "plan", "import-plan.yaml"))
	exportPath := filepath.Join(root, filepath.FromSlash(exportPlanRel))
	importPath := filepath.Join(root, filepath.FromSlash(importPlanRel))

	if err := os.WriteFile(exportPath, []byte(renderExportPlanYAML(sourceClusterID, projectID, spec, tables)), 0o644); err != nil {
		return DataMovementPlanResult{}, fmt.Errorf("write export plan: %w", err)
	}
	if err := os.WriteFile(importPath, []byte(renderImportPlanYAML(sourceClusterID, projectID, spec, jobs)), 0o644); err != nil {
		return DataMovementPlanResult{}, fmt.Errorf("write import plan: %w", err)
	}

	result := DataMovementPlanResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Tables:          len(tables),
		ImportJobs:      len(jobs),
		ExportPlanFile:  exportPlanRel,
		ImportPlanFile:  importPlanRel,
	}
	for _, table := range tables {
		result.ExportChunks += len(table.Chunks)
	}
	return result, nil
}

func normalizeDataMovementPlanSpec(spec DataMovementPlanSpec) DataMovementPlanSpec {
	spec.ObjectURIPrefix = strings.TrimSpace(spec.ObjectURIPrefix)
	spec.ExportFormat = strings.ToLower(strings.TrimSpace(spec.ExportFormat))
	if spec.ExportFormat == "" {
		spec.ExportFormat = "csv"
	}
	spec.ImportEngine = normalizeImportEngine(spec.ImportEngine)
	return spec
}

func validateExecutableObjectURIPrefix(prefix, importEngine string) error {
	parsed, err := url.Parse(prefix)
	if err != nil {
		return fmt.Errorf("parse object URI prefix: %w", err)
	}
	switch normalizeImportEngine(importEngine) {
	case importEngineTiDBImportInto:
		switch parsed.Scheme {
		case "file":
			return validateLocalFileObjectURIPrefix(parsed)
		case "s3", "gs":
			return validateObjectStorageObjectURIPrefix(parsed)
		case "":
			return fmt.Errorf("object URI prefix must use file://, s3://, or gs:// for tidb-import-into")
		default:
			return fmt.Errorf("object URI prefix scheme %s is not supported by tidb-import-into; supported schemes: file, s3, gs", parsed.Scheme)
		}
	default:
		switch parsed.Scheme {
		case "file":
			return validateLocalFileObjectURIPrefix(parsed)
		case "http", "https":
			if strings.TrimSpace(parsed.Host) == "" {
				return fmt.Errorf("%s object URI prefix host is required", parsed.Scheme)
			}
			return nil
		case "":
			return fmt.Errorf("object URI prefix must use file://, http://, or https://")
		default:
			return fmt.Errorf("object URI prefix scheme %s is not supported by sqlserver2tidb-executor; supported schemes: file, http, https", parsed.Scheme)
		}
	}
}

func validateLocalFileObjectURIPrefix(parsed *url.URL) error {
	if parsed.Host != "" && parsed.Host != "localhost" {
		return fmt.Errorf("file object URI prefix host must be empty or localhost")
	}
	if strings.TrimSpace(parsed.Path) == "" {
		return fmt.Errorf("file object URI prefix path is required")
	}
	if !filepath.IsAbs(filepath.Clean(parsed.Path)) {
		return fmt.Errorf("file object URI prefix path must be absolute")
	}
	return nil
}

func validateObjectStorageObjectURIPrefix(parsed *url.URL) error {
	if strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("%s object URI prefix bucket is required", parsed.Scheme)
	}
	return nil
}

func validateImportPlanJobSourceURIs(engine string, jobs []dataImportJobState) error {
	engine = normalizeImportEngine(engine)
	for _, job := range jobs {
		if err := validateImportPlanJobSourceURI(engine, job); err != nil {
			return err
		}
	}
	return nil
}

func validateImportPlanJobFields(engine string, jobs []dataImportJobState) error {
	engine = normalizeImportEngine(engine)
	for _, job := range jobs {
		if engine != importEngineTiDBImportInto {
			if len(job.Fields) > 0 {
				return fmt.Errorf("import job %s fields are only supported with tidb-import-into", job.ID)
			}
			continue
		}
		if err := validateTiDBImportIntoPlanFields(job); err != nil {
			return err
		}
	}
	return nil
}

func validateTiDBImportIntoPlanFields(job dataImportJobState) error {
	seenColumns := make(map[string]struct{}, len(job.Fields))
	seenVariables := make(map[string]struct{}, len(job.Fields))
	for _, raw := range job.Fields {
		field := strings.TrimSpace(raw)
		if field == "" {
			return fmt.Errorf("import job %s fields contains an empty item", job.ID)
		}
		if strings.HasPrefix(field, "@") {
			if !isValidTiDBImportIntoUserVariableField(field) {
				return fmt.Errorf("import job %s fields contains invalid user variable %q", job.ID, field)
			}
			normalized := strings.ToLower(field)
			if _, ok := seenVariables[normalized]; ok {
				return fmt.Errorf("import job %s fields contains duplicate user variable %q", job.ID, field)
			}
			seenVariables[normalized] = struct{}{}
			continue
		}
		normalized := strings.ToLower(field)
		if _, ok := seenColumns[normalized]; ok {
			return fmt.Errorf("import job %s fields contains duplicate column %q", job.ID, field)
		}
		seenColumns[normalized] = struct{}{}
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

func validateImportPlanJobSourceURI(engine string, job dataImportJobState) error {
	sourceURI := strings.TrimSpace(job.SourceURI)
	parsed, err := url.Parse(sourceURI)
	if err != nil {
		return fmt.Errorf("import job %s source_uri parse: %w", job.ID, err)
	}
	switch engine {
	case importEngineTiDBImportInto:
		switch parsed.Scheme {
		case "":
			if filepath.IsAbs(filepath.Clean(sourceURI)) {
				return nil
			}
			return fmt.Errorf("import job %s source_uri local path must be absolute for tidb-import-into", job.ID)
		case "file":
			if err := validateLocalFileImportSourceURI(parsed); err != nil {
				return fmt.Errorf("import job %s source_uri: %w", job.ID, err)
			}
			return nil
		case "s3", "gs":
			if err := validateObjectStorageImportSourceURI(parsed); err != nil {
				return fmt.Errorf("import job %s source_uri: %w", job.ID, err)
			}
			return nil
		default:
			return fmt.Errorf("import job %s source_uri scheme %s is not supported by tidb-import-into; supported schemes: file, s3, gs, or absolute local path", job.ID, parsed.Scheme)
		}
	default:
		switch parsed.Scheme {
		case "file":
			if err := validateLocalFileImportSourceURI(parsed); err != nil {
				return fmt.Errorf("import job %s source_uri: %w", job.ID, err)
			}
			return nil
		case "http", "https":
			if strings.TrimSpace(parsed.Host) == "" {
				return fmt.Errorf("import job %s source_uri: %s source URI host is required", job.ID, parsed.Scheme)
			}
			return nil
		case "":
			return fmt.Errorf("import job %s source_uri must use file://, http://, or https:// for sql-insert", job.ID)
		default:
			return fmt.Errorf("import job %s source_uri scheme %s is not supported by sql-insert; supported schemes: file, http, https", job.ID, parsed.Scheme)
		}
	}
}

func validateLocalFileImportSourceURI(parsed *url.URL) error {
	if parsed.Host != "" && parsed.Host != "localhost" {
		return fmt.Errorf("file source URI host must be empty or localhost")
	}
	if strings.TrimSpace(parsed.Path) == "" {
		return fmt.Errorf("file source URI path is required")
	}
	if !filepath.IsAbs(filepath.Clean(parsed.Path)) {
		return fmt.Errorf("file source URI path must be absolute")
	}
	return nil
}

func validateObjectStorageImportSourceURI(parsed *url.URL) error {
	if strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("%s source URI bucket is required", parsed.Scheme)
	}
	if strings.Trim(strings.TrimSpace(parsed.Path), "/") == "" {
		return fmt.Errorf("%s source URI object path is required", parsed.Scheme)
	}
	return nil
}

func validateExportPlanChunkOutputURIs(chunks []dataExportChunkState) error {
	for _, chunk := range chunks {
		if err := validateExportPlanChunkOutputURI(chunk); err != nil {
			return err
		}
	}
	return nil
}

func validateExportPlanChunkOutputURI(chunk dataExportChunkState) error {
	outputURI := strings.TrimSpace(chunk.OutputURI)
	parsed, err := url.Parse(outputURI)
	if err != nil {
		return fmt.Errorf("export chunk %s output_uri parse: %w", chunk.ID, err)
	}
	switch parsed.Scheme {
	case "file":
		if err := validateLocalFileExportOutputURI(parsed); err != nil {
			return fmt.Errorf("export chunk %s output_uri: %w", chunk.ID, err)
		}
		return nil
	case "http", "https":
		if strings.TrimSpace(parsed.Host) == "" {
			return fmt.Errorf("export chunk %s output_uri: %s output URI host is required", chunk.ID, parsed.Scheme)
		}
		return nil
	case "":
		return fmt.Errorf("export chunk %s output_uri must use file://, http://, or https://", chunk.ID)
	default:
		return fmt.Errorf("export chunk %s output_uri scheme %s is not supported by sqlserver2tidb-executor; supported schemes: file, http, https", chunk.ID, parsed.Scheme)
	}
}

func validateLocalFileExportOutputURI(parsed *url.URL) error {
	if parsed.Host != "" && parsed.Host != "localhost" {
		return fmt.Errorf("file output URI host must be empty or localhost")
	}
	if strings.TrimSpace(parsed.Path) == "" {
		return fmt.Errorf("file output URI path is required")
	}
	if !filepath.IsAbs(filepath.Clean(parsed.Path)) {
		return fmt.Errorf("file output URI path must be absolute")
	}
	return nil
}

func buildDataExportTablePlan(project projectMetadata, databaseName, schemaName string, table SQLServerTable, prefixSchema bool, spec DataMovementPlanSpec) dataExportTablePlan {
	targetTable := table.Name
	if prefixSchema {
		targetTable = schemaName + "_" + table.Name
	}
	sourceObject := joinObject(databaseName, schemaName, table.Name)
	targetObject := joinObject(project.TargetDatabase, targetTable)

	tablePlan := dataExportTablePlan{
		SourceObject: sourceObject,
		TargetObject: targetObject,
		RowCount:     table.RowCount,
	}
	chunkCount := chunkCountForRows(table.RowCount, spec.ChunkSizeRows)
	chunkPrefix := safeSQLFileName(joinObject(schemaName, table.Name))
	outputPrefix := strings.TrimRight(spec.ObjectURIPrefix, "/")
	for i := int64(1); i <= chunkCount; i++ {
		chunkID := fmt.Sprintf("%s.%06d", chunkPrefix, i)
		predicate := "1 = 1"
		if chunkCount > 1 {
			predicate = fmt.Sprintf("TODO: choose stable split predicate for %s chunk %d", sourceObject, i)
		}
		tablePlan.Chunks = append(tablePlan.Chunks, dataExportChunkPlan{
			ID:            chunkID,
			EstimatedRows: estimatedRowsForChunk(table.RowCount, spec.ChunkSizeRows, i),
			Predicate:     predicate,
			OutputURI:     fmt.Sprintf("%s/%s.%s", outputPrefix, chunkID, spec.ExportFormat),
		})
	}
	return tablePlan
}

func buildTiDBImportIntoPlanFields(columns []inventoryColumn) []string {
	fields := make([]string, 0, len(columns)+1)
	for _, column := range columns {
		name := strings.TrimSpace(column.Name)
		if name != "" {
			fields = append(fields, name)
		}
	}
	fields = append(fields, importIntoNullBitmapField)
	return fields
}

func chunkCountForRows(rowCount, chunkSizeRows int64) int64 {
	if rowCount <= 0 {
		return 1
	}
	return (rowCount + chunkSizeRows - 1) / chunkSizeRows
}

func estimatedRowsForChunk(rowCount, chunkSizeRows, chunkNumber int64) int64 {
	if rowCount <= 0 {
		return 0
	}
	remaining := rowCount - ((chunkNumber - 1) * chunkSizeRows)
	if remaining < chunkSizeRows {
		return remaining
	}
	return chunkSizeRows
}

func renderExportPlanYAML(sourceClusterID, projectID string, spec DataMovementPlanSpec, tables []dataExportTablePlan) string {
	var b strings.Builder
	b.WriteString("status: draft\n")
	fmt.Fprintf(&b, "project_id: %s\n", projectID)
	fmt.Fprintf(&b, "source_cluster_id: %s\n", sourceClusterID)
	fmt.Fprintf(&b, "format: %s\n", spec.ExportFormat)
	fmt.Fprintf(&b, "object_uri_prefix: %s\n", spec.ObjectURIPrefix)
	fmt.Fprintf(&b, "chunk_size_rows: %d\n", spec.ChunkSizeRows)
	if len(tables) == 0 {
		b.WriteString("tables: []\n")
		return b.String()
	}
	b.WriteString("tables:\n")
	for _, table := range tables {
		fmt.Fprintf(&b, "  - source_object: %s\n", table.SourceObject)
		fmt.Fprintf(&b, "    target_object: %s\n", table.TargetObject)
		fmt.Fprintf(&b, "    row_count_estimate: %d\n", table.RowCount)
		b.WriteString("    chunks:\n")
		for _, chunk := range table.Chunks {
			fmt.Fprintf(&b, "      - id: %s\n", chunk.ID)
			fmt.Fprintf(&b, "        estimated_rows: %d\n", chunk.EstimatedRows)
			fmt.Fprintf(&b, "        predicate: %s\n", quoteYAML(chunk.Predicate))
			fmt.Fprintf(&b, "        output_uri: %s\n", chunk.OutputURI)
		}
	}
	return b.String()
}

func renderImportPlanYAML(sourceClusterID, projectID string, spec DataMovementPlanSpec, jobs []dataImportJobPlan) string {
	var b strings.Builder
	b.WriteString("status: draft\n")
	fmt.Fprintf(&b, "project_id: %s\n", projectID)
	fmt.Fprintf(&b, "source_cluster_id: %s\n", sourceClusterID)
	fmt.Fprintf(&b, "engine: %s\n", spec.ImportEngine)
	b.WriteString("mode: append\n")
	if len(jobs) == 0 {
		b.WriteString("jobs: []\n")
		return b.String()
	}
	b.WriteString("jobs:\n")
	for _, job := range jobs {
		fmt.Fprintf(&b, "  - id: %s\n", job.ID)
		fmt.Fprintf(&b, "    target_object: %s\n", job.TargetObject)
		fmt.Fprintf(&b, "    source_uri: %s\n", job.SourceURI)
		fmt.Fprintf(&b, "    depends_on_export_chunk: %s\n", job.DependsOnExportChunk)
		if len(job.Fields) > 0 {
			b.WriteString("    fields:\n")
			for _, field := range job.Fields {
				fmt.Fprintf(&b, "      - %s\n", quoteYAML(field))
			}
		}
	}
	return b.String()
}
