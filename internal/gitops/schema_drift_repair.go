package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SchemaDriftRepairSpec struct {
	Apply          bool
	WritePRDraft   bool
	DataPlan       DataMovementPlanSpec
	CDCPlan        CDCPlanSpec
	ValidationPlan ValidationPlanSpec
}

type SchemaDriftRepairIssue struct {
	SourceObject      string
	Code              string
	Classification    string
	BaselineColumns   []string
	InventoryColumns  []string
	RecommendedAction string
}

type SchemaDriftRepairResult struct {
	SourceClusterID string
	ProjectID       string
	DriftDetected   bool
	Applied         bool
	Issues          []SchemaDriftRepairIssue
	ReportFile      string
	PRDraftFile     string
	UpdatedFiles    []string
}

func RepairSchemaDrift(root, sourceClusterID, projectID string, spec SchemaDriftRepairSpec) (SchemaDriftRepairResult, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return SchemaDriftRepairResult{}, err
	}
	projectDir := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID)
	project, err := readProjectMetadata(filepath.Join(projectDir, "project.yaml"))
	if err != nil {
		return SchemaDriftRepairResult{}, err
	}
	inventory, err := readSQLServerInventory(filepath.Join(root, "clusters", sourceClusterID, "inventory", "inventory.json"))
	if err != nil {
		return SchemaDriftRepairResult{}, err
	}
	diffPath := filepath.Join(projectDir, "schema", "schema-diff.json")
	diff, err := readSchemaDiffForValidation(diffPath)
	if err != nil {
		return SchemaDriftRepairResult{}, err
	}
	result := SchemaDriftRepairResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
	}
	result.Issues = detectSchemaDriftRepairIssues(project, inventory, diff)
	result.DriftDetected = len(result.Issues) > 0

	if spec.Apply && result.DriftDetected {
		if hasManualSchemaDriftIssue(result.Issues) {
			reportFile, writeErr := writeSchemaDriftRepairReport(root, &result)
			if writeErr != nil {
				return SchemaDriftRepairResult{}, writeErr
			}
			result.ReportFile = reportFile
			return result, fmt.Errorf("schema drift contains manual_required issues; report written to %s", reportFile)
		}
		updated, err := applySchemaDriftRepair(root, sourceClusterID, projectID, spec)
		if err != nil {
			return SchemaDriftRepairResult{}, err
		}
		result.Applied = true
		result.UpdatedFiles = appendUniqueStrings(result.UpdatedFiles, updated...)
	}

	reportFile, err := writeSchemaDriftRepairReport(root, &result)
	if err != nil {
		return SchemaDriftRepairResult{}, err
	}
	result.ReportFile = reportFile
	result.UpdatedFiles = appendUniqueStrings(result.UpdatedFiles, reportFile)
	if spec.WritePRDraft {
		prDraftFile, err := writeSchemaDriftRepairPRDraft(root, result)
		if err != nil {
			return SchemaDriftRepairResult{}, err
		}
		result.PRDraftFile = prDraftFile
	}
	return result, nil
}

func detectSchemaDriftRepairIssues(project projectMetadata, inventory SQLServerInventory, diff schemaDiffDocument) []SchemaDriftRepairIssue {
	if strings.TrimSpace(diff.Status) != "reviewed" {
		return nil
	}
	var issues []SchemaDriftRepairIssue
	seen := map[string]bool{}
	for _, tableDiff := range diff.Tables {
		sourceObject := strings.TrimSpace(tableDiff.SourceObject)
		if sourceObject == "" {
			continue
		}
		seen[strings.ToLower(sourceObject)] = true
		table, ok := findInventoryTableBySourceObject(inventory, sourceObject)
		if !ok {
			issues = append(issues, SchemaDriftRepairIssue{
				SourceObject:      sourceObject,
				Code:              "table_missing",
				Classification:    "manual_required",
				BaselineColumns:   schemaDiffSourceColumns(tableDiff.Columns),
				RecommendedAction: "Review whether to remove the table from migration scope or restore it upstream.",
			})
			continue
		}
		if !schemaDiffColumnsMatchInventory(tableDiff.Columns, table.Columns) {
			issues = append(issues, SchemaDriftRepairIssue{
				SourceObject:      sourceObject,
				Code:              "columns_changed",
				Classification:    "auto_repairable",
				BaselineColumns:   schemaDiffSourceColumns(tableDiff.Columns),
				InventoryColumns:  inventoryColumnsWithTypes(table.Columns),
				RecommendedAction: "Regenerate schema, data, CDC, and validation drafts from current inventory.",
			})
		}
	}
	for _, tableRef := range projectInventoryTableRefs(project, inventory) {
		if seen[strings.ToLower(tableRef.SourceObject)] {
			continue
		}
		issues = append(issues, SchemaDriftRepairIssue{
			SourceObject:      tableRef.SourceObject,
			Code:              "table_added",
			Classification:    "auto_repairable",
			InventoryColumns:  inventoryColumnsWithTypes(tableRef.Table.Columns),
			RecommendedAction: "Regenerate schema, data, CDC, and validation drafts from current inventory.",
		})
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Classification != issues[j].Classification {
			return issues[i].Classification < issues[j].Classification
		}
		if issues[i].SourceObject != issues[j].SourceObject {
			return issues[i].SourceObject < issues[j].SourceObject
		}
		return issues[i].Code < issues[j].Code
	})
	return issues
}

type projectInventoryTableRef struct {
	SourceObject string
	Table        SQLServerTable
}

func projectInventoryTableRefs(project projectMetadata, inventory SQLServerInventory) []projectInventoryTableRef {
	sourceSchemas := lowerSet(project.SourceSchemas)
	var refs []projectInventoryTableRef
	for _, database := range inventory.Databases {
		if !strings.EqualFold(database.Name, project.SourceDatabase) {
			continue
		}
		for _, schema := range database.Schemas {
			if !sourceSchemas[strings.ToLower(schema.Name)] {
				continue
			}
			for _, table := range schema.Tables {
				refs = append(refs, projectInventoryTableRef{
					SourceObject: joinObject(database.Name, schema.Name, table.Name),
					Table:        table,
				})
			}
		}
	}
	return refs
}

func hasManualSchemaDriftIssue(issues []SchemaDriftRepairIssue) bool {
	for _, issue := range issues {
		if issue.Classification == "manual_required" {
			return true
		}
	}
	return false
}

func applySchemaDriftRepair(root, sourceClusterID, projectID string, spec SchemaDriftRepairSpec) ([]string, error) {
	if strings.TrimSpace(spec.DataPlan.ObjectURIPrefix) == "" {
		return nil, fmt.Errorf("object URI prefix is required for schema drift auto repair data plan regeneration")
	}
	var updated []string
	if _, err := GenerateSchemaDraft(root, sourceClusterID, projectID); err != nil {
		return nil, err
	}
	projectRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID))
	updated = appendUniqueStrings(updated,
		filepath.ToSlash(filepath.Join(projectRel, "schema", "tidb-ddl/")),
		filepath.ToSlash(filepath.Join(projectRel, "schema", "conversion-report.md")),
		filepath.ToSlash(filepath.Join(projectRel, "schema", "schema-diff.json")),
	)
	if _, err := GenerateDataMovementPlans(root, sourceClusterID, projectID, spec.DataPlan); err != nil {
		return nil, err
	}
	updated = appendUniqueStrings(updated,
		filepath.ToSlash(filepath.Join(projectRel, "plan", "export-plan.yaml")),
		filepath.ToSlash(filepath.Join(projectRel, "plan", "import-plan.yaml")),
	)
	if _, err := GenerateCDCPlan(root, sourceClusterID, projectID, spec.CDCPlan); err != nil {
		return nil, err
	}
	updated = appendUniqueStrings(updated, filepath.ToSlash(filepath.Join(projectRel, "plan", "cdc-plan.yaml")))
	if _, err := GenerateValidationPlanWithSpec(root, sourceClusterID, projectID, spec.ValidationPlan); err != nil {
		return nil, err
	}
	updated = appendUniqueStrings(updated, filepath.ToSlash(filepath.Join(projectRel, "plan", "validation-plan.yaml")))
	return updated, nil
}

func writeSchemaDriftRepairReport(root string, result *SchemaDriftRepairResult) (string, error) {
	rel := filepath.ToSlash(filepath.Join("clusters", result.SourceClusterID, "projects", result.ProjectID, "evidence", "schema-drift-report.md"))
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create schema drift report directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(renderSchemaDriftRepairReport(*result)), 0o644); err != nil {
		return "", fmt.Errorf("write schema drift report: %w", err)
	}
	return rel, nil
}

func renderSchemaDriftRepairReport(result SchemaDriftRepairResult) string {
	var b strings.Builder
	b.WriteString("# Schema Drift Report\n\n")
	fmt.Fprintf(&b, "- Source cluster: `%s`\n", result.SourceClusterID)
	fmt.Fprintf(&b, "- Project: `%s`\n", result.ProjectID)
	fmt.Fprintf(&b, "- Generated at: `%s`\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Drift detected: `%t`\n", result.DriftDetected)
	fmt.Fprintf(&b, "- Auto repair applied: `%t`\n\n", result.Applied)
	if len(result.Issues) == 0 {
		b.WriteString("No reviewed schema drift was detected.\n")
		return b.String()
	}
	b.WriteString("## Issues\n\n")
	b.WriteString("| Source object | Classification | Code | Baseline columns | Current inventory columns | Recommended action |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, issue := range result.Issues {
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | `%s` | `%s` | %s |\n",
			escapeMarkdownTable(issue.SourceObject),
			escapeMarkdownTable(issue.Classification),
			escapeMarkdownTable(issue.Code),
			escapeMarkdownTable(strings.Join(issue.BaselineColumns, ", ")),
			escapeMarkdownTable(strings.Join(issue.InventoryColumns, ", ")),
			escapeMarkdownTable(issue.RecommendedAction),
		)
	}
	if len(result.UpdatedFiles) > 0 {
		b.WriteString("\n## Updated Files\n\n")
		for _, file := range result.UpdatedFiles {
			fmt.Fprintf(&b, "- `%s`\n", file)
		}
	}
	return b.String()
}

func writeSchemaDriftRepairPRDraft(root string, result SchemaDriftRepairResult) (string, error) {
	rel := filepath.ToSlash(filepath.Join("clusters", result.SourceClusterID, "projects", result.ProjectID, "prs", "schema-drift-pr.md"))
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create schema drift PR draft directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(renderSchemaDriftRepairPRDraft(result)), 0o644); err != nil {
		return "", fmt.Errorf("write schema drift PR draft: %w", err)
	}
	return rel, nil
}

func renderSchemaDriftRepairPRDraft(result SchemaDriftRepairResult) string {
	title := fmt.Sprintf("[schema-drift] %s", result.ProjectID)
	branch := fmt.Sprintf("agent/%s/schema-drift", result.ProjectID)
	var b strings.Builder
	fmt.Fprintf(&b, "# PR Draft: %s\n\n", title)
	b.WriteString("## Summary\n\n")
	b.WriteString("- Stage: `schema-drift`\n")
	fmt.Fprintf(&b, "- Source cluster: `%s`\n", result.SourceClusterID)
	fmt.Fprintf(&b, "- Project: `%s`\n", result.ProjectID)
	fmt.Fprintf(&b, "- Branch: `%s`\n", branch)
	b.WriteString("- Generated by: `sqlserver2tidb repair-schema-drift`\n")
	fmt.Fprintf(&b, "- Drift issues: `%d`\n", len(result.Issues))
	fmt.Fprintf(&b, "- Auto repair applied: `%t`\n\n", result.Applied)

	b.WriteString("## Files To Review\n\n")
	files := appendUniqueStrings(nil, result.UpdatedFiles...)
	files = appendUniqueStrings(files, result.ReportFile)
	sort.Strings(files)
	for _, file := range files {
		if strings.TrimSpace(file) == "" {
			continue
		}
		fmt.Fprintf(&b, "- `%s`\n", file)
	}

	b.WriteString("\n## Drift Issues\n\n")
	if len(result.Issues) == 0 {
		b.WriteString("No reviewed schema drift was detected.\n")
	} else {
		for _, issue := range result.Issues {
			fmt.Fprintf(&b, "- `%s` `%s` `%s`: %s\n", issue.SourceObject, issue.Classification, issue.Code, issue.RecommendedAction)
		}
	}

	b.WriteString("\n## Operator Checklist\n\n")
	b.WriteString("- [ ] Confirm drift classification is correct.\n")
	b.WriteString("- [ ] Confirm regenerated schema/data/CDC/validation drafts match the current SQL Server inventory.\n")
	b.WriteString("- [ ] Re-review changed plans before recomputing approval payload hashes.\n")
	b.WriteString("- [ ] Confirm no plaintext secrets are included.\n")

	b.WriteString("\n## Suggested GitHub Command\n\n")
	b.WriteString("```bash\n")
	fmt.Fprintf(&b, "gh pr create --base main --head %s --title %q --body-file %s\n",
		branch,
		title,
		filepath.ToSlash(filepath.Join("clusters", result.SourceClusterID, "projects", result.ProjectID, "prs", "schema-drift-pr.md")),
	)
	b.WriteString("```\n")
	return b.String()
}

func inventoryColumnsWithTypes(columns []SQLServerColumn) []string {
	values := make([]string, 0, len(columns))
	for _, column := range columns {
		name := strings.TrimSpace(column.Name)
		if name == "" {
			continue
		}
		value := name
		if strings.TrimSpace(column.Type) != "" {
			value += ":" + strings.TrimSpace(column.Type)
		}
		values = append(values, value)
	}
	return values
}

func appendUniqueStrings(values []string, more ...string) []string {
	seen := make(map[string]bool, len(values)+len(more))
	out := make([]string, 0, len(values)+len(more))
	for _, value := range append(values, more...) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
