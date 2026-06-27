package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CDCPlanSpec struct {
	Mode                   string
	RetentionHoursRequired int
	ApplyBatchSize         int
}

type CDCPlanResult struct {
	SourceClusterID string
	ProjectID       string
	Mode            string
	Tables          int
	PlanFile        string
}

type cdcTrackedTablePlan struct {
	SourceObject   string
	TargetObject   string
	Columns        []string
	KeyColumns     []string
	FromLSN        string
	ToLSN          string
	ApplyBatchSize int
}

func GenerateCDCPlan(root, sourceClusterID, projectID string, spec CDCPlanSpec) (CDCPlanResult, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return CDCPlanResult{}, err
	}
	spec = normalizeCDCPlanSpec(spec)

	clusterDir := filepath.Join(root, "clusters", sourceClusterID)
	projectDir := filepath.Join(clusterDir, "projects", projectID)
	project, err := readProjectMetadata(filepath.Join(projectDir, "project.yaml"))
	if err != nil {
		return CDCPlanResult{}, err
	}
	if strings.TrimSpace(project.SourceDatabase) == "" || len(project.SourceSchemas) == 0 || strings.TrimSpace(project.TargetDatabase) == "" {
		return CDCPlanResult{}, fmt.Errorf("project %q is missing source database, source schemas, or target database", projectID)
	}

	inventory, err := readSQLServerInventory(filepath.Join(clusterDir, "inventory", "inventory.json"))
	if err != nil {
		return CDCPlanResult{}, err
	}

	sourceSchemas := lowerSet(project.SourceSchemas)
	prefixSchema := len(project.SourceSchemas) > 1
	var tables []cdcTrackedTablePlan
	for _, database := range inventory.Databases {
		if !strings.EqualFold(database.Name, project.SourceDatabase) {
			continue
		}
		for _, schema := range database.Schemas {
			if !sourceSchemas[strings.ToLower(schema.Name)] {
				continue
			}
			for _, table := range schema.Tables {
				tables = append(tables, buildCDCTrackedTablePlan(project, database.Name, schema.Name, table, prefixSchema, spec))
			}
		}
	}

	projectRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID))
	planRel := filepath.ToSlash(filepath.Join(projectRel, "plan", "cdc-plan.yaml"))
	planPath := filepath.Join(root, filepath.FromSlash(planRel))
	if err := os.WriteFile(planPath, []byte(renderCDCPlanYAML(sourceClusterID, project, spec, tables)), 0o644); err != nil {
		return CDCPlanResult{}, fmt.Errorf("write cdc plan: %w", err)
	}

	return CDCPlanResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Mode:            spec.Mode,
		Tables:          len(tables),
		PlanFile:        planRel,
	}, nil
}

func normalizeCDCPlanSpec(spec CDCPlanSpec) CDCPlanSpec {
	spec.Mode = strings.ToLower(strings.TrimSpace(spec.Mode))
	if spec.Mode == "" {
		spec.Mode = "sqlserver-cdc"
	}
	if spec.RetentionHoursRequired <= 0 {
		spec.RetentionHoursRequired = 168
	}
	if spec.ApplyBatchSize <= 0 {
		spec.ApplyBatchSize = 1000
	}
	return spec
}

func buildCDCTrackedTablePlan(project projectMetadata, databaseName, schemaName string, table SQLServerTable, prefixSchema bool, spec CDCPlanSpec) cdcTrackedTablePlan {
	targetTable := table.Name
	if prefixSchema {
		targetTable = schemaName + "_" + table.Name
	}
	return cdcTrackedTablePlan{
		SourceObject:   joinObject(databaseName, schemaName, table.Name),
		TargetObject:   joinObject(project.TargetDatabase, targetTable),
		Columns:        chooseCDCCapturedColumns(table),
		KeyColumns:     chooseCDCKeyColumns(table),
		ApplyBatchSize: spec.ApplyBatchSize,
	}
}

func chooseCDCCapturedColumns(table SQLServerTable) []string {
	columns := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		name := strings.TrimSpace(column.Name)
		if name == "" || column.Computed {
			continue
		}
		columns = append(columns, name)
	}
	return columns
}

func chooseCDCKeyColumns(table SQLServerTable) []string {
	for _, index := range table.Indexes {
		if index.PrimaryKey && !index.Filtered && len(index.Columns) > 0 {
			return append([]string(nil), index.Columns...)
		}
	}
	for _, index := range table.Indexes {
		if index.Unique && !index.Filtered && len(index.Columns) > 0 {
			return append([]string(nil), index.Columns...)
		}
	}
	return nil
}

func renderCDCPlanYAML(sourceClusterID string, project projectMetadata, spec CDCPlanSpec, tables []cdcTrackedTablePlan) string {
	var b strings.Builder
	b.WriteString("status: draft\n")
	fmt.Fprintf(&b, "project_id: %s\n", project.ProjectID)
	fmt.Fprintf(&b, "source_cluster_id: %s\n", sourceClusterID)
	fmt.Fprintf(&b, "mode: %s\n", spec.Mode)
	fmt.Fprintf(&b, "retention_hours_required: %d\n", spec.RetentionHoursRequired)
	fmt.Fprintf(&b, "source_database: %s\n", project.SourceDatabase)
	b.WriteString("source_schemas:\n")
	for _, schema := range project.SourceSchemas {
		fmt.Fprintf(&b, "  - %s\n", schema)
	}
	fmt.Fprintf(&b, "target_database: %s\n", project.TargetDatabase)
	b.WriteString("checkpoint_scope: source-cluster\n")
	b.WriteString("checkpoint_file: ../../../state/cdc-checkpoint.yaml\n")
	if len(tables) == 0 {
		b.WriteString("tracked_tables: []\n")
		return b.String()
	}
	b.WriteString("tracked_tables:\n")
	for _, table := range tables {
		fmt.Fprintf(&b, "  - source_object: %s\n", table.SourceObject)
		fmt.Fprintf(&b, "    target_object: %s\n", table.TargetObject)
		if len(table.Columns) == 0 {
			b.WriteString("    columns: []\n")
		} else {
			b.WriteString("    columns:\n")
			for _, column := range table.Columns {
				fmt.Fprintf(&b, "      - %s\n", column)
			}
		}
		if len(table.KeyColumns) == 0 {
			b.WriteString("    key_columns: []\n")
		} else {
			b.WriteString("    key_columns:\n")
			for _, column := range table.KeyColumns {
				fmt.Fprintf(&b, "      - %s\n", column)
			}
		}
		fmt.Fprintf(&b, "    from_lsn: %q\n", table.FromLSN)
		fmt.Fprintf(&b, "    to_lsn: %q\n", table.ToLSN)
		fmt.Fprintf(&b, "    apply_batch_size: %d\n", table.ApplyBatchSize)
		b.WriteString("    status: draft\n")
	}
	return b.String()
}
