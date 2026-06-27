package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ValidationPlanResult struct {
	SourceClusterID string
	ProjectID       string
	Checks          int
	PlanFile        string
}

type ValidationPlanSpec struct {
	IncludeChecksum    bool
	IncludeSampledHash bool
	SampleModulo       int
}

type validationCheckPlan struct {
	ID           string
	Type         string
	SourceObject string
	TargetObject string
	SourceSQL    string
	TargetSQL    string
}

func GenerateValidationPlan(root, sourceClusterID, projectID string) (ValidationPlanResult, error) {
	return GenerateValidationPlanWithSpec(root, sourceClusterID, projectID, ValidationPlanSpec{})
}

func GenerateValidationPlanWithSpec(root, sourceClusterID, projectID string, spec ValidationPlanSpec) (ValidationPlanResult, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return ValidationPlanResult{}, err
	}
	if spec.SampleModulo <= 0 {
		spec.SampleModulo = 100
	}

	clusterDir := filepath.Join(root, "clusters", sourceClusterID)
	projectDir := filepath.Join(clusterDir, "projects", projectID)
	project, err := readProjectMetadata(filepath.Join(projectDir, "project.yaml"))
	if err != nil {
		return ValidationPlanResult{}, err
	}
	if strings.TrimSpace(project.SourceDatabase) == "" || len(project.SourceSchemas) == 0 || strings.TrimSpace(project.TargetDatabase) == "" {
		return ValidationPlanResult{}, fmt.Errorf("project %q is missing source database, source schemas, or target database", projectID)
	}

	inventory, err := readSQLServerInventory(filepath.Join(clusterDir, "inventory", "inventory.json"))
	if err != nil {
		return ValidationPlanResult{}, err
	}

	sourceSchemas := lowerSet(project.SourceSchemas)
	prefixSchema := len(project.SourceSchemas) > 1
	var checks []validationCheckPlan
	for _, database := range inventory.Databases {
		if !strings.EqualFold(database.Name, project.SourceDatabase) {
			continue
		}
		for _, schema := range database.Schemas {
			if !sourceSchemas[strings.ToLower(schema.Name)] {
				continue
			}
			for _, table := range schema.Tables {
				targetTable := table.Name
				if prefixSchema {
					targetTable = schema.Name + "_" + table.Name
				}
				sourceObject := joinObject(database.Name, schema.Name, table.Name)
				targetObject := joinObject(project.TargetDatabase, targetTable)
				checks = append(checks, validationCheckPlan{
					ID:           safeSQLFileName(joinObject(schema.Name, table.Name)) + ".row-count",
					Type:         "row_count",
					SourceObject: sourceObject,
					TargetObject: targetObject,
				})
				numericColumns := exactNumericValidationColumns(table.Columns)
				if spec.IncludeChecksum && len(numericColumns) > 0 {
					checks = append(checks, validationCheckPlan{
						ID:        safeSQLFileName(joinObject(schema.Name, table.Name)) + ".checksum",
						Type:      "checksum",
						SourceSQL: buildSQLServerValidationAggregateSQL(sourceObject, numericColumns, ""),
						TargetSQL: buildTiDBValidationAggregateSQL(targetObject, numericColumns, ""),
					})
				}
				sampleColumn := integerSampleColumn(table.Columns)
				if spec.IncludeSampledHash && len(numericColumns) > 0 && sampleColumn != "" {
					checks = append(checks, validationCheckPlan{
						ID:        safeSQLFileName(joinObject(schema.Name, table.Name)) + ".sampled-hash",
						Type:      "sampled_hash",
						SourceSQL: buildSQLServerValidationAggregateSQL(sourceObject, numericColumns, buildSQLServerSamplePredicate(sampleColumn, spec.SampleModulo)),
						TargetSQL: buildTiDBValidationAggregateSQL(targetObject, numericColumns, buildTiDBSamplePredicate(sampleColumn, spec.SampleModulo)),
					})
				}
			}
		}
	}

	projectRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID))
	planRel := filepath.ToSlash(filepath.Join(projectRel, "plan", "validation-plan.yaml"))
	planPath := filepath.Join(root, filepath.FromSlash(planRel))
	if err := os.WriteFile(planPath, []byte(renderValidationPlanYAML(sourceClusterID, projectID, checks)), 0o644); err != nil {
		return ValidationPlanResult{}, fmt.Errorf("write validation plan: %w", err)
	}

	return ValidationPlanResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Checks:          len(checks),
		PlanFile:        planRel,
	}, nil
}

func renderValidationPlanYAML(sourceClusterID, projectID string, checks []validationCheckPlan) string {
	var b strings.Builder
	b.WriteString("status: draft\n")
	fmt.Fprintf(&b, "project_id: %s\n", projectID)
	fmt.Fprintf(&b, "source_cluster_id: %s\n", sourceClusterID)
	if len(checks) == 0 {
		b.WriteString("checks: []\n")
		return b.String()
	}
	b.WriteString("checks:\n")
	for _, check := range checks {
		fmt.Fprintf(&b, "  - id: %s\n", check.ID)
		fmt.Fprintf(&b, "    type: %s\n", check.Type)
		switch check.Type {
		case "row_count":
			fmt.Fprintf(&b, "    source_object: %s\n", check.SourceObject)
			fmt.Fprintf(&b, "    target_object: %s\n", check.TargetObject)
		case "checksum", "sampled_hash", "business_sql":
			fmt.Fprintf(&b, "    source_sql: %s\n", quoteYAML(check.SourceSQL))
			fmt.Fprintf(&b, "    target_sql: %s\n", quoteYAML(check.TargetSQL))
		}
	}
	return b.String()
}

func exactNumericValidationColumns(columns []inventoryColumn) []string {
	var names []string
	for _, column := range columns {
		if column.Computed {
			continue
		}
		if isExactNumericSQLServerType(column.Type) {
			names = append(names, column.Name)
		}
	}
	return names
}

func integerSampleColumn(columns []inventoryColumn) string {
	for _, column := range columns {
		if column.Computed {
			continue
		}
		if isIntegerSQLServerType(column.Type) {
			return column.Name
		}
	}
	return ""
}

func isExactNumericSQLServerType(value string) bool {
	base := sqlServerTypeBase(value)
	switch base {
	case "bigint", "int", "smallint", "tinyint", "decimal", "numeric", "money", "smallmoney":
		return true
	default:
		return false
	}
}

func isIntegerSQLServerType(value string) bool {
	base := sqlServerTypeBase(value)
	switch base {
	case "bigint", "int", "smallint", "tinyint":
		return true
	default:
		return false
	}
}

func sqlServerTypeBase(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if idx := strings.IndexAny(value, "( "); idx >= 0 {
		value = value[:idx]
	}
	return value
}

func buildSQLServerValidationAggregateSQL(object string, columns []string, predicate string) string {
	terms := make([]string, 0, len(columns))
	for _, column := range columns {
		terms = append(terms, fmt.Sprintf("COALESCE(CAST(%s AS DECIMAL(38, 6)), 0)", quoteSQLServerIdentifier(column)))
	}
	query := fmt.Sprintf("SELECT COALESCE(SUM(%s), 0) FROM %s", strings.Join(terms, " + "), quoteSQLServerObject(object))
	if strings.TrimSpace(predicate) != "" {
		query += " WHERE " + predicate
	}
	return query
}

func buildTiDBValidationAggregateSQL(object string, columns []string, predicate string) string {
	terms := make([]string, 0, len(columns))
	for _, column := range columns {
		terms = append(terms, fmt.Sprintf("COALESCE(CAST(%s AS DECIMAL(38, 6)), 0)", quoteTiDBIdentifier(column)))
	}
	query := fmt.Sprintf("SELECT COALESCE(SUM(%s), 0) FROM %s", strings.Join(terms, " + "), quoteTiDBObject(object))
	if strings.TrimSpace(predicate) != "" {
		query += " WHERE " + predicate
	}
	return query
}

func buildSQLServerSamplePredicate(column string, modulo int) string {
	return fmt.Sprintf("CAST(%s AS BIGINT) %% %d = 0", quoteSQLServerIdentifier(column), modulo)
}

func buildTiDBSamplePredicate(column string, modulo int) string {
	return fmt.Sprintf("CAST(%s AS SIGNED) %% %d = 0", quoteTiDBIdentifier(column), modulo)
}

func quoteSQLServerObject(object string) string {
	parts := strings.Split(object, ".")
	for i, part := range parts {
		parts[i] = quoteSQLServerIdentifier(part)
	}
	return strings.Join(parts, ".")
}

func quoteTiDBObject(object string) string {
	parts := strings.Split(object, ".")
	for i, part := range parts {
		parts[i] = quoteTiDBIdentifier(part)
	}
	return strings.Join(parts, ".")
}

func quoteSQLServerIdentifier(identifier string) string {
	return "[" + strings.ReplaceAll(identifier, "]", "]]") + "]"
}

func quoteTiDBIdentifier(identifier string) string {
	return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
}
