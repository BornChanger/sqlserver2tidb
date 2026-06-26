package gitops

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type SchemaDraftResult struct {
	SourceClusterID   string
	ProjectID         string
	Tables            int
	Columns           int
	ManualReviewItems int
	DDLFiles          []string
}

type projectMetadata struct {
	ProjectID       string
	SourceClusterID string
	SourceDatabase  string
	SourceSchemas   []string
	TargetDatabase  string
	Mode            string
	Owners          []string
}

type clusterMetadata struct {
	ClusterID              string
	DisplayName            string
	Listener               string
	Port                   int
	SecretRef              string
	CDCMode                string
	RetentionHoursRequired int
	Owners                 []string
}

type sourceProfileMetadata struct {
	ClusterID string
	Listener  string
	Port      int
	SecretRef string
}

type schemaDiffDocument struct {
	Status          string            `json:"status"`
	SourceClusterID string            `json:"source_cluster_id"`
	ProjectID       string            `json:"project_id"`
	SourceDatabase  string            `json:"source_database"`
	TargetDatabase  string            `json:"target_database"`
	Summary         schemaDiffSummary `json:"summary"`
	Tables          []schemaTableDiff `json:"tables"`
	GeneratedAt     string            `json:"generated_at"`
}

type schemaDiffSummary struct {
	Tables            int `json:"tables"`
	Columns           int `json:"columns"`
	ManualReviewItems int `json:"manual_review_items"`
}

type schemaTableDiff struct {
	SourceObject string             `json:"source_object"`
	TargetObject string             `json:"target_object"`
	DDLFile      string             `json:"ddl_file"`
	Columns      []schemaColumnDiff `json:"columns"`
	ManualReview bool               `json:"manual_review"`
}

type schemaColumnDiff struct {
	SourceColumn string `json:"source_column"`
	SourceType   string `json:"source_type"`
	TargetColumn string `json:"target_column"`
	TargetType   string `json:"target_type"`
	ManualReview bool   `json:"manual_review"`
	Code         string `json:"code,omitempty"`
	Note         string `json:"note,omitempty"`
}

type typeMapping struct {
	TargetType   string
	ManualReview bool
	Code         string
	Note         string
	DDLComment   string
}

func GenerateSchemaDraft(root, sourceClusterID, projectID string) (SchemaDraftResult, error) {
	if !idPattern.MatchString(sourceClusterID) {
		return SchemaDraftResult{}, fmt.Errorf("invalid source cluster id %q", sourceClusterID)
	}
	if !idPattern.MatchString(projectID) {
		return SchemaDraftResult{}, fmt.Errorf("invalid project id %q", projectID)
	}

	clusterDir := filepath.Join(root, "clusters", sourceClusterID)
	if info, err := os.Stat(clusterDir); err != nil || !info.IsDir() {
		return SchemaDraftResult{}, fmt.Errorf("source cluster %q does not exist", sourceClusterID)
	}
	projectDir := filepath.Join(clusterDir, "projects", projectID)
	if info, err := os.Stat(projectDir); err != nil || !info.IsDir() {
		return SchemaDraftResult{}, fmt.Errorf("project %q does not exist under source cluster %q", projectID, sourceClusterID)
	}

	project, err := readProjectMetadata(filepath.Join(projectDir, "project.yaml"))
	if err != nil {
		return SchemaDraftResult{}, err
	}
	if project.ProjectID != projectID {
		return SchemaDraftResult{}, fmt.Errorf("project.yaml project_id %q does not match %q", project.ProjectID, projectID)
	}
	if project.SourceClusterID != sourceClusterID {
		return SchemaDraftResult{}, fmt.Errorf("project.yaml source_cluster_id %q does not match %q", project.SourceClusterID, sourceClusterID)
	}
	if strings.TrimSpace(project.SourceDatabase) == "" || len(project.SourceSchemas) == 0 || strings.TrimSpace(project.TargetDatabase) == "" {
		return SchemaDraftResult{}, fmt.Errorf("project %q is missing source database, source schemas, or target database", projectID)
	}

	inventory, err := readSQLServerInventory(filepath.Join(clusterDir, "inventory", "inventory.json"))
	if err != nil {
		return SchemaDraftResult{}, err
	}

	projectRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID))
	ddlDir := filepath.Join(projectDir, "schema", "tidb-ddl")
	if err := os.MkdirAll(ddlDir, 0o755); err != nil {
		return SchemaDraftResult{}, fmt.Errorf("create TiDB DDL directory: %w", err)
	}
	if err := removeGeneratedSQLFiles(ddlDir); err != nil {
		return SchemaDraftResult{}, err
	}

	result := SchemaDraftResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
	}
	diff := schemaDiffDocument{
		Status:          "draft-generated",
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		SourceDatabase:  project.SourceDatabase,
		TargetDatabase:  project.TargetDatabase,
		GeneratedAt:     nowUTC(),
	}

	sourceSchemas := lowerSet(project.SourceSchemas)
	for _, database := range inventory.Databases {
		if !strings.EqualFold(database.Name, project.SourceDatabase) {
			continue
		}
		for _, schema := range database.Schemas {
			if !sourceSchemas[strings.ToLower(schema.Name)] {
				continue
			}
			for _, table := range schema.Tables {
				tableDiff, ddl := buildTableDraft(project, database.Name, schema.Name, table, len(project.SourceSchemas) > 1)
				diff.Tables = append(diff.Tables, tableDiff)
				result.Tables++
				result.Columns += len(table.Columns)
				for _, column := range tableDiff.Columns {
					if column.ManualReview {
						result.ManualReviewItems++
					}
				}

				fileName := safeSQLFileName(joinObject(schema.Name, table.Name)) + ".sql"
				ddlRel := filepath.ToSlash(filepath.Join(projectRel, "schema", "tidb-ddl", fileName))
				if err := os.WriteFile(filepath.Join(ddlDir, fileName), []byte(ddl), 0o644); err != nil {
					return SchemaDraftResult{}, fmt.Errorf("write TiDB DDL %s: %w", fileName, err)
				}
				result.DDLFiles = append(result.DDLFiles, ddlRel)
				diff.Tables[len(diff.Tables)-1].DDLFile = filepath.ToSlash(filepath.Join("schema", "tidb-ddl", fileName))
			}
		}
	}

	diff.Summary = schemaDiffSummary{
		Tables:            result.Tables,
		Columns:           result.Columns,
		ManualReviewItems: result.ManualReviewItems,
	}
	report := renderSchemaConversionReport(project, sourceClusterID, result, diff.Tables)
	if err := os.WriteFile(filepath.Join(projectDir, "schema", "conversion-report.md"), []byte(report), 0o644); err != nil {
		return SchemaDraftResult{}, fmt.Errorf("write schema conversion report: %w", err)
	}
	diffData, err := json.MarshalIndent(diff, "", "  ")
	if err != nil {
		return SchemaDraftResult{}, fmt.Errorf("marshal schema diff: %w", err)
	}
	diffData = append(diffData, '\n')
	if err := os.WriteFile(filepath.Join(projectDir, "schema", "schema-diff.json"), diffData, 0o644); err != nil {
		return SchemaDraftResult{}, fmt.Errorf("write schema diff: %w", err)
	}

	return result, nil
}

func readClusterMetadata(path string) (clusterMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return clusterMetadata{}, fmt.Errorf("read cluster metadata: %w", err)
	}

	var meta clusterMetadata
	section := ""
	listKey := ""
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		if indent == 0 {
			listKey = ""
		}
		if strings.HasPrefix(trimmed, "- ") {
			if listKey == "owners" {
				meta.Owners = append(meta.Owners, trimYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
			}
			continue
		}
		if strings.HasSuffix(trimmed, ":") {
			key := strings.TrimSpace(strings.TrimSuffix(trimmed, ":"))
			if indent == 0 {
				section = key
				listKey = ""
				if key == "owners" {
					listKey = "owners"
				}
				continue
			}
			listKey = ""
			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := trimYAMLScalar(parts[1])
		switch {
		case indent == 0 && key == "cluster_id":
			meta.ClusterID = value
			section = ""
		case indent == 0 && key == "display_name":
			meta.DisplayName = value
			section = ""
		case indent == 0 && key == "owners":
			if value == "[]" {
				meta.Owners = nil
			} else if value != "" {
				meta.Owners = []string{value}
			}
			section = "owners"
			listKey = "owners"
		case section == "source" && key == "listener":
			meta.Listener = value
		case section == "source" && key == "port":
			if value != "" {
				port, err := strconv.Atoi(value)
				if err != nil {
					return clusterMetadata{}, fmt.Errorf("parse cluster port %q: %w", value, err)
				}
				meta.Port = port
			}
		case section == "source" && key == "secret_ref":
			meta.SecretRef = value
		case section == "cdc" && key == "mode":
			meta.CDCMode = value
		case section == "cdc" && key == "retention_hours_required":
			if value != "" {
				retentionHours, err := strconv.Atoi(value)
				if err != nil {
					return clusterMetadata{}, fmt.Errorf("parse cdc retention_hours_required %q: %w", value, err)
				}
				meta.RetentionHoursRequired = retentionHours
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return clusterMetadata{}, fmt.Errorf("scan cluster metadata: %w", err)
	}
	return meta, nil
}

func readSourceProfileMetadata(path string) (sourceProfileMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sourceProfileMetadata{}, fmt.Errorf("read source profile: %w", err)
	}

	var meta sourceProfileMetadata
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		if strings.HasSuffix(trimmed, ":") {
			key := strings.TrimSpace(strings.TrimSuffix(trimmed, ":"))
			if indent == 0 {
				section = key
			}
			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := trimYAMLScalar(parts[1])
		switch {
		case indent == 0 && key == "cluster_id":
			meta.ClusterID = value
			section = ""
		case section == "connection" && key == "listener":
			meta.Listener = value
		case section == "connection" && key == "port":
			if value != "" {
				port, err := strconv.Atoi(value)
				if err != nil {
					return sourceProfileMetadata{}, fmt.Errorf("parse source profile port %q: %w", value, err)
				}
				meta.Port = port
			}
		case section == "connection" && key == "secret_ref":
			meta.SecretRef = value
		}
	}
	if err := scanner.Err(); err != nil {
		return sourceProfileMetadata{}, fmt.Errorf("scan source profile: %w", err)
	}
	return meta, nil
}

func readProjectMetadata(path string) (projectMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return projectMetadata{}, fmt.Errorf("read project metadata: %w", err)
	}

	var meta projectMetadata
	section := ""
	listKey := ""
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		if indent == 0 {
			listKey = ""
		}
		if strings.HasPrefix(trimmed, "- ") {
			if listKey == "source.schemas" {
				meta.SourceSchemas = append(meta.SourceSchemas, trimYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
			}
			if listKey == "owners" {
				meta.Owners = append(meta.Owners, trimYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
			}
			continue
		}
		if strings.HasSuffix(trimmed, ":") {
			key := strings.TrimSpace(strings.TrimSuffix(trimmed, ":"))
			if indent == 0 {
				section = key
				listKey = ""
				if key == "owners" {
					listKey = "owners"
				}
				continue
			}
			if section == "source" && key == "schemas" {
				listKey = "source.schemas"
			} else {
				listKey = ""
			}
			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := trimYAMLScalar(parts[1])
		switch {
		case indent == 0 && key == "project_id":
			meta.ProjectID = value
			section = ""
		case indent == 0 && key == "source_cluster_id":
			meta.SourceClusterID = value
			section = ""
		case indent == 0 && key == "mode":
			meta.Mode = value
			section = ""
		case indent == 0 && key == "owners":
			if value == "[]" {
				meta.Owners = nil
			} else if value != "" {
				meta.Owners = []string{value}
			}
			section = "owners"
			listKey = "owners"
		case section == "source" && key == "database":
			meta.SourceDatabase = value
		case section == "source" && key == "schemas":
			listKey = "source.schemas"
			if value == "[]" {
				meta.SourceSchemas = nil
			}
		case section == "target" && key == "database":
			meta.TargetDatabase = value
		}
	}
	if err := scanner.Err(); err != nil {
		return projectMetadata{}, fmt.Errorf("scan project metadata: %w", err)
	}
	return meta, nil
}

func readSQLServerInventory(path string) (SQLServerInventory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SQLServerInventory{}, fmt.Errorf("read inventory: %w", err)
	}
	var inventory SQLServerInventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		return SQLServerInventory{}, fmt.Errorf("parse inventory: %w", err)
	}
	return inventory, nil
}

func removeGeneratedSQLFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read TiDB DDL directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return fmt.Errorf("remove stale TiDB DDL %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func buildTableDraft(project projectMetadata, databaseName, schemaName string, table SQLServerTable, prefixSchema bool) (schemaTableDiff, string) {
	targetTable := table.Name
	if prefixSchema {
		targetTable = schemaName + "_" + table.Name
	}
	sourceObject := joinObject(databaseName, schemaName, table.Name)
	targetObject := joinObject(project.TargetDatabase, targetTable)

	tableDiff := schemaTableDiff{
		SourceObject: sourceObject,
		TargetObject: targetObject,
	}
	var b strings.Builder
	fmt.Fprintf(&b, "-- Generated by sqlserver2tidb. Review before execution.\n")
	fmt.Fprintf(&b, "-- Source: %s\n", sourceObject)
	fmt.Fprintf(&b, "CREATE TABLE IF NOT EXISTS %s.%s (\n", quoteTiDBIdent(project.TargetDatabase), quoteTiDBIdent(targetTable))
	for i, column := range table.Columns {
		mapping := mapSQLServerTypeToTiDB(column)
		mapping = applyColumnSemantics(column, mapping)
		tableDiff.Columns = append(tableDiff.Columns, schemaColumnDiff{
			SourceColumn: column.Name,
			SourceType:   column.Type,
			TargetColumn: column.Name,
			TargetType:   mapping.TargetType,
			ManualReview: mapping.ManualReview,
			Code:         mapping.Code,
			Note:         mapping.Note,
		})
		if mapping.ManualReview {
			tableDiff.ManualReview = true
		}

		fmt.Fprintf(&b, "  %s %s", quoteTiDBIdent(column.Name), mapping.TargetType)
		if mapping.DDLComment != "" {
			fmt.Fprintf(&b, " /* TODO: %s */", mapping.DDLComment)
		}
		if i < len(table.Columns)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(");\n")
	return tableDiff, b.String()
}

func applyColumnSemantics(column SQLServerColumn, mapping typeMapping) typeMapping {
	if !column.Computed {
		return mapping
	}
	computedNote := "Computed column expression must be reviewed before TiDB DDL generation."
	computedComment := "computed column expression requires manual rewrite"
	if mapping.ManualReview {
		mapping.Note = joinSentences(mapping.Note, computedNote)
		mapping.DDLComment = joinSentences(mapping.DDLComment, computedComment)
		return mapping
	}
	mapping.ManualReview = true
	mapping.Code = "SQLSERVER_COMPUTED_COLUMN"
	mapping.Note = computedNote
	mapping.DDLComment = computedComment
	return mapping
}

func mapSQLServerTypeToTiDB(column SQLServerColumn) typeMapping {
	sourceType := strings.TrimSpace(column.Type)
	baseType := baseSQLServerType(sourceType)
	parameter := sqlTypeParameter(sourceType)

	switch baseType {
	case "bigint":
		return typeMapping{TargetType: "BIGINT"}
	case "int":
		return typeMapping{TargetType: "INT"}
	case "smallint":
		return typeMapping{TargetType: "SMALLINT"}
	case "tinyint":
		return typeMapping{TargetType: "TINYINT"}
	case "bit":
		return typeMapping{TargetType: "TINYINT(1)"}
	case "decimal", "numeric":
		return typeMapping{TargetType: typeWithDefaultParameter("DECIMAL", parameter, "")}
	case "money":
		return typeMapping{TargetType: "DECIMAL(19,4)"}
	case "smallmoney":
		return typeMapping{TargetType: "DECIMAL(10,4)"}
	case "float":
		return typeMapping{TargetType: "DOUBLE"}
	case "real":
		return typeMapping{TargetType: "FLOAT"}
	case "char", "nchar":
		return typeMapping{TargetType: characterType("CHAR", parameter)}
	case "varchar", "nvarchar":
		return typeMapping{TargetType: characterType("VARCHAR", parameter)}
	case "text", "ntext":
		return typeMapping{TargetType: "TEXT"}
	case "date":
		return typeMapping{TargetType: "DATE"}
	case "datetime", "datetime2", "smalldatetime":
		return typeMapping{TargetType: "DATETIME"}
	case "datetimeoffset":
		return manualTypeMapping("DATETIME", "SQLSERVER_TYPE_DATETIMEOFFSET", "SQL Server datetimeoffset loses timezone offset in TiDB DATETIME.", "SQL Server datetimeoffset requires timezone semantics review")
	case "time":
		return typeMapping{TargetType: "TIME"}
	case "binary":
		return typeMapping{TargetType: binaryType("BINARY", parameter)}
	case "varbinary":
		return typeMapping{TargetType: binaryType("VARBINARY", parameter)}
	case "image":
		return typeMapping{TargetType: "BLOB"}
	case "uniqueidentifier":
		return typeMapping{TargetType: "CHAR(36)"}
	case "xml":
		return manualTypeMapping("TEXT", "SQLSERVER_TYPE_XML", "SQL Server xml column requires an explicit TiDB representation.", "SQL Server xml requires manual mapping")
	case "rowversion", "timestamp":
		return manualTypeMapping("VARBINARY(8)", "SQLSERVER_TYPE_ROWVERSION", "SQL Server rowversion/timestamp has no direct TiDB equivalent.", "SQL Server rowversion requires application-managed replacement")
	case "sql_variant":
		return manualTypeMapping("TEXT", "SQLSERVER_TYPE_SQL_VARIANT", "SQL Server sql_variant requires explicit target typing.", "SQL Server sql_variant requires explicit target typing")
	case "hierarchyid":
		return manualTypeMapping("TEXT", "SQLSERVER_TYPE_HIERARCHYID", "SQL Server hierarchyid requires application-specific encoding.", "SQL Server hierarchyid requires application-specific encoding")
	case "geometry", "geography":
		return manualTypeMapping("BLOB", "SQLSERVER_TYPE_SPATIAL", "SQL Server spatial data requires TiDB-compatible encoding or external spatial handling.", "SQL Server spatial data requires target encoding review")
	default:
		return manualTypeMapping("TEXT", "SQLSERVER_TYPE_UNMAPPED", fmt.Sprintf("SQL Server type %q has no built-in mapping yet.", sourceType), fmt.Sprintf("SQL Server type %s requires manual mapping", sourceType))
	}
}

func manualTypeMapping(targetType, code, note, ddlComment string) typeMapping {
	return typeMapping{
		TargetType:   targetType,
		ManualReview: true,
		Code:         code,
		Note:         note,
		DDLComment:   ddlComment,
	}
}

func baseSQLServerType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if index := strings.Index(value, "("); index >= 0 {
		value = value[:index]
	}
	if fields := strings.Fields(value); len(fields) > 0 {
		value = fields[0]
	}
	return strings.TrimSpace(value)
}

func sqlTypeParameter(value string) string {
	start := strings.Index(value, "(")
	end := strings.LastIndex(value, ")")
	if start < 0 || end <= start {
		return ""
	}
	return strings.TrimSpace(value[start+1 : end])
}

func typeWithDefaultParameter(typeName, parameter, defaultParameter string) string {
	if parameter == "" {
		if defaultParameter == "" {
			return typeName
		}
		return typeName + "(" + defaultParameter + ")"
	}
	return typeName + "(" + parameter + ")"
}

func characterType(typeName, parameter string) string {
	if strings.EqualFold(parameter, "max") {
		return "TEXT"
	}
	return typeWithDefaultParameter(typeName, parameter, "255")
}

func binaryType(typeName, parameter string) string {
	if parameter == "" || strings.EqualFold(parameter, "max") {
		return "BLOB"
	}
	return typeName + "(" + parameter + ")"
}

func renderSchemaConversionReport(project projectMetadata, sourceClusterID string, result SchemaDraftResult, tables []schemaTableDiff) string {
	var b strings.Builder
	b.WriteString("# Schema Conversion Report\n\n")
	fmt.Fprintf(&b, "- Source cluster: `%s`\n", sourceClusterID)
	fmt.Fprintf(&b, "- Project: `%s`\n", project.ProjectID)
	fmt.Fprintf(&b, "- Source database: `%s`\n", project.SourceDatabase)
	fmt.Fprintf(&b, "- Source schemas: `%s`\n", strings.Join(project.SourceSchemas, "`, `"))
	fmt.Fprintf(&b, "- Target database: `%s`\n", project.TargetDatabase)
	fmt.Fprintf(&b, "- Tables: %d\n", result.Tables)
	fmt.Fprintf(&b, "- Columns: %d\n", result.Columns)
	fmt.Fprintf(&b, "- Manual review items: %d\n\n", result.ManualReviewItems)

	if len(tables) == 0 {
		b.WriteString("No project-scoped tables were found in the source inventory.\n")
		return b.String()
	}

	b.WriteString("## Tables\n\n")
	b.WriteString("| Source | Target | DDL file | Columns | Manual review |\n")
	b.WriteString("| --- | --- | --- | ---: | --- |\n")
	for _, table := range tables {
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %d | %t |\n",
			table.SourceObject,
			table.TargetObject,
			table.DDLFile,
			len(table.Columns),
			table.ManualReview,
		)
	}

	if result.ManualReviewItems == 0 {
		b.WriteString("\nNo manual review items were generated by the rule-based mapper.\n")
		return b.String()
	}

	b.WriteString("\n## Manual Review Items\n\n")
	b.WriteString("| Object | Source type | Draft type | Code | Note |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, table := range tables {
		for _, column := range table.Columns {
			if !column.ManualReview {
				continue
			}
			fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | `%s` | %s |\n",
				joinObject(table.SourceObject, column.SourceColumn),
				column.SourceType,
				column.TargetType,
				column.Code,
				escapeMarkdownTable(column.Note),
			)
		}
	}
	return b.String()
}

func lowerSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[strings.ToLower(value)] = true
		}
	}
	return out
}

func quoteTiDBIdent(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func trimYAMLScalar(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) && len(value) >= 2 {
		value = value[1 : len(value)-1]
	}
	return value
}

func joinSentences(left, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	return left + " " + right
}
