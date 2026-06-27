package gitops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CompatibilityReport struct {
	SourceClusterID string
	InventoryFile   string
	Summary         CompatibilitySummary
	Findings        []CompatibilityFinding
}

type CompatibilitySummary struct {
	TotalFindings int
	Blockers      int
	Warnings      int
	Info          int
}

type CompatibilityFinding struct {
	Severity       string
	Code           string
	Object         string
	Message        string
	Recommendation string
}

type sqlServerInventory struct {
	Status    string              `json:"status"`
	Databases []inventoryDatabase `json:"databases"`
}

type inventoryDatabase struct {
	Name    string            `json:"name"`
	Schemas []inventorySchema `json:"schemas"`
}

type inventorySchema struct {
	Name     string             `json:"name"`
	Tables   []inventoryTable   `json:"tables"`
	Routines []inventoryRoutine `json:"routines"`
}

type inventoryTable struct {
	Name     string              `json:"name"`
	RowCount int64               `json:"row_count,omitempty"`
	Columns  []inventoryColumn   `json:"columns"`
	Indexes  []inventoryIndex    `json:"indexes"`
	Triggers []inventoryDBObject `json:"triggers"`
}

type inventoryColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Identity bool   `json:"identity,omitempty"`
	Computed bool   `json:"computed,omitempty"`
}

type inventoryIndex struct {
	Name            string   `json:"name"`
	Columns         []string `json:"columns,omitempty"`
	Unique          bool     `json:"unique,omitempty"`
	PrimaryKey      bool     `json:"primary_key,omitempty"`
	Filtered        bool     `json:"filtered,omitempty"`
	IncludedColumns []string `json:"included_columns,omitempty"`
}

type inventoryRoutine struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type inventoryDBObject struct {
	Name string `json:"name"`
}

type SQLServerInventory = sqlServerInventory
type SQLServerDatabase = inventoryDatabase
type SQLServerSchema = inventorySchema
type SQLServerTable = inventoryTable
type SQLServerColumn = inventoryColumn
type SQLServerIndex = inventoryIndex
type SQLServerRoutine = inventoryRoutine
type SQLServerDBObject = inventoryDBObject

func AnalyzeSQLServerCompatibility(root, sourceClusterID string) (CompatibilityReport, error) {
	if !idPattern.MatchString(sourceClusterID) {
		return CompatibilityReport{}, fmt.Errorf("invalid source cluster id %q", sourceClusterID)
	}

	clusterDir := filepath.Join(root, "clusters", sourceClusterID)
	if info, err := os.Stat(clusterDir); err != nil || !info.IsDir() {
		return CompatibilityReport{}, fmt.Errorf("source cluster %q does not exist", sourceClusterID)
	}

	inventoryRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "inventory", "inventory.json"))
	inventoryPath := filepath.Join(root, filepath.FromSlash(inventoryRel))
	data, err := os.ReadFile(inventoryPath)
	if err != nil {
		return CompatibilityReport{}, fmt.Errorf("read inventory: %w", err)
	}

	var inventory sqlServerInventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		return CompatibilityReport{}, fmt.Errorf("parse inventory: %w", err)
	}

	report := CompatibilityReport{
		SourceClusterID: sourceClusterID,
		InventoryFile:   inventoryRel,
	}
	report.Findings = analyzeInventory(inventory)
	report.Summary = summarizeFindings(report.Findings)

	inventoryDir := filepath.Join(root, "clusters", sourceClusterID, "inventory")
	if err := os.WriteFile(filepath.Join(inventoryDir, "schema-issues.yaml"), []byte(renderSchemaIssuesYAML(report)), 0o644); err != nil {
		return CompatibilityReport{}, fmt.Errorf("write schema issues: %w", err)
	}
	if err := os.WriteFile(filepath.Join(inventoryDir, "compatibility-report.md"), []byte(renderCompatibilityMarkdown(report)), 0o644); err != nil {
		return CompatibilityReport{}, fmt.Errorf("write compatibility report: %w", err)
	}

	return report, nil
}

func analyzeInventory(inventory sqlServerInventory) []CompatibilityFinding {
	var findings []CompatibilityFinding
	for _, database := range inventory.Databases {
		for _, schema := range database.Schemas {
			schemaObject := joinObject(database.Name, schema.Name)
			for _, table := range schema.Tables {
				tableObject := joinObject(schemaObject, table.Name)
				for _, column := range table.Columns {
					columnObject := joinObject(tableObject, column.Name)
					columnType := strings.ToLower(strings.TrimSpace(column.Type))
					switch columnType {
					case "xml":
						findings = append(findings, CompatibilityFinding{
							Severity:       "blocker",
							Code:           "SQLSERVER_TYPE_XML",
							Object:         columnObject,
							Message:        "SQL Server xml column requires an explicit TiDB representation.",
							Recommendation: "Convert to JSON, TEXT, or a normalized child table after application review.",
						})
					case "rowversion", "timestamp":
						findings = append(findings, CompatibilityFinding{
							Severity:       "blocker",
							Code:           "SQLSERVER_TYPE_ROWVERSION",
							Object:         columnObject,
							Message:        "SQL Server rowversion/timestamp has no direct TiDB equivalent.",
							Recommendation: "Replace with application-managed versioning or an explicit binary column strategy.",
						})
					}
					if column.Computed {
						findings = append(findings, CompatibilityFinding{
							Severity:       "warning",
							Code:           "SQLSERVER_COMPUTED_COLUMN",
							Object:         columnObject,
							Message:        "Computed column expression must be reviewed before TiDB DDL generation.",
							Recommendation: "Rewrite as a generated column only when the expression is TiDB-compatible; otherwise materialize in application logic.",
						})
					}
				}
				for _, index := range table.Indexes {
					indexObject := joinObject(tableObject, index.Name)
					if index.Filtered {
						findings = append(findings, CompatibilityFinding{
							Severity:       "warning",
							Code:           "SQLSERVER_FILTERED_INDEX",
							Object:         indexObject,
							Message:        "Filtered index semantics require manual review for TiDB.",
							Recommendation: "Replace with a full index, generated-column index, or application query rewrite after workload review.",
						})
					}
					if len(index.IncludedColumns) > 0 {
						findings = append(findings, CompatibilityFinding{
							Severity:       "warning",
							Code:           "SQLSERVER_INCLUDED_COLUMNS",
							Object:         indexObject,
							Message:        "SQL Server included columns do not map directly to TiDB index syntax.",
							Recommendation: "Review whether a composite index should include these columns as key columns.",
						})
					}
				}
				for _, trigger := range table.Triggers {
					findings = append(findings, CompatibilityFinding{
						Severity:       "blocker",
						Code:           "SQLSERVER_TRIGGER",
						Object:         joinObject(tableObject, trigger.Name),
						Message:        "SQL Server trigger must be rewritten or retired before migration.",
						Recommendation: "Move trigger behavior into application code, TiDB-compatible logic, or an external workflow.",
					})
				}
			}
			for _, routine := range schema.Routines {
				findings = append(findings, CompatibilityFinding{
					Severity:       "blocker",
					Code:           "SQLSERVER_ROUTINE",
					Object:         joinObject(schemaObject, routine.Name),
					Message:        fmt.Sprintf("SQL Server %s requires manual rewrite for TiDB.", routineType(routine.Type)),
					Recommendation: "Rewrite T-SQL routine logic in application code or a TiDB-compatible SQL layer.",
				})
			}
		}
	}
	return findings
}

func summarizeFindings(findings []CompatibilityFinding) CompatibilitySummary {
	summary := CompatibilitySummary{TotalFindings: len(findings)}
	for _, finding := range findings {
		switch finding.Severity {
		case "blocker":
			summary.Blockers++
		case "warning":
			summary.Warnings++
		default:
			summary.Info++
		}
	}
	return summary
}

func renderSchemaIssuesYAML(report CompatibilityReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "source_cluster_id: %s\n", report.SourceClusterID)
	fmt.Fprintf(&b, "inventory_file: %s\n", report.InventoryFile)
	b.WriteString("summary:\n")
	fmt.Fprintf(&b, "  total_findings: %d\n", report.Summary.TotalFindings)
	fmt.Fprintf(&b, "  blockers: %d\n", report.Summary.Blockers)
	fmt.Fprintf(&b, "  warnings: %d\n", report.Summary.Warnings)
	fmt.Fprintf(&b, "  info: %d\n", report.Summary.Info)
	if len(report.Findings) == 0 {
		b.WriteString("findings: []\n")
		return b.String()
	}
	b.WriteString("findings:\n")
	for _, finding := range report.Findings {
		fmt.Fprintf(&b, "  - severity: %s\n", finding.Severity)
		fmt.Fprintf(&b, "    code: %s\n", finding.Code)
		fmt.Fprintf(&b, "    object: %s\n", finding.Object)
		fmt.Fprintf(&b, "    message: %s\n", quoteYAML(finding.Message))
		fmt.Fprintf(&b, "    recommendation: %s\n", quoteYAML(finding.Recommendation))
	}
	return b.String()
}

func renderCompatibilityMarkdown(report CompatibilityReport) string {
	var b strings.Builder
	b.WriteString("# Compatibility Report\n\n")
	fmt.Fprintf(&b, "- Source cluster: `%s`\n", report.SourceClusterID)
	fmt.Fprintf(&b, "- Inventory file: `%s`\n", report.InventoryFile)
	fmt.Fprintf(&b, "- Total findings: %d\n", report.Summary.TotalFindings)
	fmt.Fprintf(&b, "- Blockers: %d\n", report.Summary.Blockers)
	fmt.Fprintf(&b, "- Warnings: %d\n", report.Summary.Warnings)
	fmt.Fprintf(&b, "- Info: %d\n\n", report.Summary.Info)
	if len(report.Findings) == 0 {
		b.WriteString("No compatibility findings.\n")
		return b.String()
	}
	b.WriteString("| Severity | Code | Object | Recommendation |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, finding := range report.Findings {
		fmt.Fprintf(&b, "| %s | `%s` | `%s` | %s |\n",
			finding.Severity,
			finding.Code,
			finding.Object,
			escapeMarkdownTable(finding.Recommendation),
		)
	}
	return b.String()
}

func joinObject(parts ...string) string {
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, ".")
}

func routineType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "routine"
	}
	return value
}

func quoteYAML(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func escapeMarkdownTable(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}
