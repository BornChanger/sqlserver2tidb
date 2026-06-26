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

type validationRowCountCheckPlan struct {
	ID           string
	SourceObject string
	TargetObject string
}

func GenerateValidationPlan(root, sourceClusterID, projectID string) (ValidationPlanResult, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return ValidationPlanResult{}, err
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
	var checks []validationRowCountCheckPlan
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
				checks = append(checks, validationRowCountCheckPlan{
					ID:           safeSQLFileName(joinObject(schema.Name, table.Name)) + ".row-count",
					SourceObject: sourceObject,
					TargetObject: joinObject(project.TargetDatabase, targetTable),
				})
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

func renderValidationPlanYAML(sourceClusterID, projectID string, checks []validationRowCountCheckPlan) string {
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
		b.WriteString("    type: row_count\n")
		fmt.Fprintf(&b, "    source_object: %s\n", check.SourceObject)
		fmt.Fprintf(&b, "    target_object: %s\n", check.TargetObject)
	}
	return b.String()
}
