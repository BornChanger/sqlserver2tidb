package gitops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitRepoCreatesGlobalStructure(t *testing.T) {
	root := t.TempDir()

	if err := InitRepo(root); err != nil {
		t.Fatalf("InitRepo() error = %v", err)
	}

	assertFile(t, root, "global/policies/approval-policy.yaml")
	assertFile(t, root, "global/policies/execution-policy.yaml")
	assertFile(t, root, "global/policies/file-schema-policy.yaml")
	assertFile(t, root, "global/templates/project.yaml")
	assertFile(t, root, "global/templates/migration-plan.yaml")
	assertFile(t, root, "global/templates/cutover-runbook.md")
	assertFile(t, root, "global/schemas/cluster.schema.json")
	assertFile(t, root, "global/schemas/project.schema.json")
	assertFile(t, root, "global/schemas/migration-plan.schema.json")
	assertDir(t, root, "clusters")
}

func TestCreateClusterCreatesUpstreamSQLServerClusterDirectory(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))

	err := CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team", "sre-team"},
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	assertFile(t, root, "clusters/prod-sqlserver-a/cluster.yaml")
	assertFile(t, root, "clusters/prod-sqlserver-a/source-profile.yaml")
	assertDir(t, root, "clusters/prod-sqlserver-a/inventory/source-ddl")
	assertFile(t, root, "clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml")
	assertFile(t, root, "clusters/prod-sqlserver-a/state/worker-lease.yaml")
	assertDir(t, root, "clusters/prod-sqlserver-a/projects")

	clusterYAML := readFile(t, root, "clusters/prod-sqlserver-a/cluster.yaml")
	assertContains(t, clusterYAML, "cluster_id: prod-sqlserver-a")
	assertContains(t, clusterYAML, "listener: sqlserver-a.internal")
	assertContains(t, clusterYAML, "secret_ref: vault://migration/prod-sqlserver-a/readonly")
}

func TestCreateProjectCreatesProjectUnderSourceCluster(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))

	err := CreateProject(root, ProjectSpec{
		SourceClusterID: "prod-sqlserver-a",
		ProjectID:       "sales-db-to-tidb-prod-a",
		DisplayName:     "sales DB to TiDB prod A",
		SourceDatabase:  "sales",
		SourceSchemas:   []string{"dbo"},
		TargetName:      "tidb-prod-a",
		TargetDatabase:  "app",
		TargetSecretRef: "vault://migration/tidb-prod-a/migrate-user",
		Mode:            "short-downtime",
		Owners:          []string{"dba-team", "app-team"},
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}

	base := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a"
	assertFile(t, root, base+"/project.yaml")
	assertDir(t, root, base+"/schema/tidb-ddl")
	assertFile(t, root, base+"/schema/conversion-report.md")
	assertFile(t, root, base+"/schema/schema-diff.json")
	assertFile(t, root, base+"/plan/migration-plan.yaml")
	assertFile(t, root, base+"/plan/export-plan.yaml")
	assertFile(t, root, base+"/plan/import-plan.yaml")
	assertFile(t, root, base+"/plan/cdc-plan.yaml")
	assertFile(t, root, base+"/plan/validation-plan.yaml")
	assertFile(t, root, base+"/plan/cutover-runbook.md")
	assertFile(t, root, base+"/state/migration-state.yaml")
	assertFile(t, root, base+"/state/export-chunks.yaml")
	assertFile(t, root, base+"/state/import-jobs.yaml")
	assertFile(t, root, base+"/state/validation-status.yaml")
	assertFile(t, root, base+"/approvals/validation-approval.yaml")
	assertFile(t, root, base+"/approvals/cutover-approval.yaml")

	projectYAML := readFile(t, root, base+"/project.yaml")
	assertContains(t, projectYAML, "project_id: sales-db-to-tidb-prod-a")
	assertContains(t, projectYAML, "source_cluster_id: prod-sqlserver-a")
	assertContains(t, projectYAML, "database: sales")
	assertContains(t, projectYAML, "name: tidb-prod-a")
}

func TestCreateProjectRequiresExistingSourceCluster(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))

	err := CreateProject(root, ProjectSpec{
		SourceClusterID: "missing-cluster",
		ProjectID:       "sales-db-to-tidb-prod-a",
		DisplayName:     "sales DB to TiDB prod A",
		SourceDatabase:  "sales",
		SourceSchemas:   []string{"dbo"},
		TargetName:      "tidb-prod-a",
		TargetDatabase:  "app",
		TargetSecretRef: "vault://migration/tidb-prod-a/migrate-user",
		Mode:            "short-downtime",
		Owners:          []string{"dba-team"},
	})
	if err == nil {
		t.Fatal("CreateProject() expected error for missing source cluster")
	}
	if !strings.Contains(err.Error(), "source cluster") {
		t.Fatalf("CreateProject() error = %v, want source cluster message", err)
	}
}

func TestValidateRepoAcceptsInitializedRepository(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if !report.Valid {
		t.Fatalf("ValidateRepo() valid = false, errors = %v", report.Errors)
	}
	if len(report.Errors) != 0 {
		t.Fatalf("ValidateRepo() errors = %v, want none", report.Errors)
	}
}

func TestValidateRepoReportsMissingRequiredGlobalFile(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, os.Remove(filepath.Join(root, "global", "schemas", "project.schema.json")))

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "missing required file: global/schemas/project.schema.json")
}

func TestValidateRepoChecksClusterAndProjectDirectories(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))
	must(t, CreateProject(root, ProjectSpec{
		SourceClusterID: "prod-sqlserver-a",
		ProjectID:       "sales-db-to-tidb-prod-a",
		DisplayName:     "sales DB to TiDB prod A",
		SourceDatabase:  "sales",
		SourceSchemas:   []string{"dbo"},
		TargetName:      "tidb-prod-a",
		TargetDatabase:  "app",
		TargetSecretRef: "vault://migration/tidb-prod-a/migrate-user",
		Mode:            "short-downtime",
		Owners:          []string{"dba-team"},
	}))

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if !report.Valid {
		t.Fatalf("ValidateRepo() valid = false, errors = %v", report.Errors)
	}

	must(t, os.Remove(filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "plan", "migration-plan.yaml")))
	report, err = ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "missing required file: clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/migration-plan.yaml")
}

func TestBuildSQLServerDiscoveryDryRunPlanForCluster(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))
	inventoryBefore := readFile(t, root, "clusters/prod-sqlserver-a/inventory/inventory.json")

	plan, err := BuildSQLServerDiscoveryDryRunPlan(root, "prod-sqlserver-a")
	if err != nil {
		t.Fatalf("BuildSQLServerDiscoveryDryRunPlan() error = %v", err)
	}

	if plan.SourceClusterID != "prod-sqlserver-a" {
		t.Fatalf("SourceClusterID = %q, want prod-sqlserver-a", plan.SourceClusterID)
	}
	if plan.Mode != "dry-run" {
		t.Fatalf("Mode = %q, want dry-run", plan.Mode)
	}
	if plan.WritesFiles {
		t.Fatal("WritesFiles = true, want false")
	}
	assertStringSliceContains(t, plan.TargetFiles, "clusters/prod-sqlserver-a/inventory/inventory.json")
	assertStringSliceContains(t, plan.TargetFiles, "clusters/prod-sqlserver-a/inventory/compatibility-report.md")
	assertStringSliceContains(t, plan.TargetFiles, "clusters/prod-sqlserver-a/inventory/schema-issues.yaml")
	assertStringSliceContains(t, plan.CatalogQueries, "tables: sys.tables + sys.schemas + sys.partitions")
	assertStringSliceContains(t, plan.CatalogQueries, "columns: sys.columns + sys.types")
	assertStringSliceContains(t, plan.CatalogQueries, "cdc: cdc.change_tables + sys.databases")

	inventoryAfter := readFile(t, root, "clusters/prod-sqlserver-a/inventory/inventory.json")
	if inventoryAfter != inventoryBefore {
		t.Fatalf("dry-run changed inventory.json\nbefore:\n%s\nafter:\n%s", inventoryBefore, inventoryAfter)
	}
}

func TestBuildSQLServerDiscoveryDryRunPlanRequiresExistingCluster(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))

	_, err := BuildSQLServerDiscoveryDryRunPlan(root, "missing-cluster")
	if err == nil {
		t.Fatal("BuildSQLServerDiscoveryDryRunPlan() expected error for missing cluster")
	}
	assertContains(t, err.Error(), `source cluster "missing-cluster" does not exist`)
}

func TestExecuteSQLServerDiscoveryWritesInventoryFromCatalogReader(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))

	reader := fakeCatalogReader{
		snapshot: SQLServerCatalogSnapshot{
			Inventory: SQLServerInventory{
				Databases: []SQLServerDatabase{
					{
						Name: "sales",
						Schemas: []SQLServerSchema{
							{
								Name: "dbo",
								Tables: []SQLServerTable{
									{
										Name:     "orders",
										RowCount: 42,
										Columns: []SQLServerColumn{
											{Name: "id", Type: "int", Identity: true},
											{Name: "payload", Type: "xml"},
										},
										Indexes: []SQLServerIndex{
											{Name: "ix_orders_payload", Filtered: true, IncludedColumns: []string{"payload"}},
										},
										Triggers: []SQLServerDBObject{{Name: "tr_orders_audit"}},
									},
								},
								Routines: []SQLServerRoutine{{Name: "sync_orders", Type: "procedure"}},
							},
						},
					},
				},
			},
			SourceDDLs: map[string]string{
				"sales.dbo.orders": "CREATE TABLE dbo.orders (id int NOT NULL);\n",
			},
		},
	}

	result, err := ExecuteSQLServerDiscovery(context.Background(), root, "prod-sqlserver-a", &reader)
	if err != nil {
		t.Fatalf("ExecuteSQLServerDiscovery() error = %v", err)
	}
	if !reader.called {
		t.Fatal("catalog reader was not called")
	}
	if result.Databases != 1 || result.Tables != 1 || result.Columns != 2 {
		t.Fatalf("discovery counts = %+v, want 1 db, 1 table, 2 columns", result)
	}

	inventoryJSON := readFile(t, root, "clusters/prod-sqlserver-a/inventory/inventory.json")
	assertContains(t, inventoryJSON, `"status": "discovered"`)
	assertContains(t, inventoryJSON, `"name": "orders"`)
	assertContains(t, inventoryJSON, `"type": "xml"`)
	assertContains(t, inventoryJSON, `"filtered": true`)
	assertContains(t, inventoryJSON, `"triggers"`)
	assertContains(t, inventoryJSON, `"routines"`)

	sourceDDL := readFile(t, root, "clusters/prod-sqlserver-a/inventory/source-ddl/sales.dbo.orders.sql")
	assertContains(t, sourceDDL, "CREATE TABLE dbo.orders")
}

func TestExecuteSQLServerDiscoveryRequiresExistingCluster(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	reader := fakeCatalogReader{}

	_, err := ExecuteSQLServerDiscovery(context.Background(), root, "missing-cluster", &reader)
	if err == nil {
		t.Fatal("ExecuteSQLServerDiscovery() expected error for missing cluster")
	}
	if reader.called {
		t.Fatal("catalog reader should not be called when cluster is missing")
	}
	assertContains(t, err.Error(), `source cluster "missing-cluster" does not exist`)
}

func TestAnalyzeSQLServerCompatibilityWritesFindings(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/inventory/inventory.json", `{
  "status": "discovered",
  "databases": [
    {
      "name": "sales",
      "schemas": [
        {
          "name": "dbo",
          "tables": [
            {
              "name": "orders",
              "columns": [
                {"name": "id", "type": "int", "identity": true},
                {"name": "payload", "type": "xml"},
                {"name": "total", "type": "decimal", "computed": true},
                {"name": "rv", "type": "rowversion"}
              ],
              "indexes": [
                {"name": "ix_orders_active", "filtered": true, "included_columns": ["total"]}
              ],
              "triggers": [
                {"name": "tr_orders_audit"}
              ]
            }
          ],
          "routines": [
            {"name": "sync_orders", "type": "procedure"}
          ]
        }
      ]
    }
  ]
}
`)

	report, err := AnalyzeSQLServerCompatibility(root, "prod-sqlserver-a")
	if err != nil {
		t.Fatalf("AnalyzeSQLServerCompatibility() error = %v", err)
	}

	if report.SourceClusterID != "prod-sqlserver-a" {
		t.Fatalf("SourceClusterID = %q, want prod-sqlserver-a", report.SourceClusterID)
	}
	if report.Summary.TotalFindings != 7 {
		t.Fatalf("TotalFindings = %d, want 7\nfindings: %+v", report.Summary.TotalFindings, report.Findings)
	}
	if report.Summary.Blockers != 4 {
		t.Fatalf("Blockers = %d, want 4\nfindings: %+v", report.Summary.Blockers, report.Findings)
	}
	assertFindingCode(t, report.Findings, "SQLSERVER_TYPE_XML")
	assertFindingCode(t, report.Findings, "SQLSERVER_COMPUTED_COLUMN")
	assertFindingCode(t, report.Findings, "SQLSERVER_TYPE_ROWVERSION")
	assertFindingCode(t, report.Findings, "SQLSERVER_FILTERED_INDEX")
	assertFindingCode(t, report.Findings, "SQLSERVER_INCLUDED_COLUMNS")
	assertFindingCode(t, report.Findings, "SQLSERVER_TRIGGER")
	assertFindingCode(t, report.Findings, "SQLSERVER_ROUTINE")

	issuesYAML := readFile(t, root, "clusters/prod-sqlserver-a/inventory/schema-issues.yaml")
	assertContains(t, issuesYAML, "code: SQLSERVER_TYPE_XML")
	assertContains(t, issuesYAML, "object: sales.dbo.orders.payload")
	assertContains(t, issuesYAML, "severity: blocker")

	compatibilityReport := readFile(t, root, "clusters/prod-sqlserver-a/inventory/compatibility-report.md")
	assertContains(t, compatibilityReport, "# Compatibility Report")
	assertContains(t, compatibilityReport, "SQLSERVER_TRIGGER")
	assertContains(t, compatibilityReport, "sales.dbo.orders.tr_orders_audit")
}

func TestAnalyzeSQLServerCompatibilityAllowsPendingInventory(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))

	report, err := AnalyzeSQLServerCompatibility(root, "prod-sqlserver-a")
	if err != nil {
		t.Fatalf("AnalyzeSQLServerCompatibility() error = %v", err)
	}
	if report.Summary.TotalFindings != 0 {
		t.Fatalf("TotalFindings = %d, want 0", report.Summary.TotalFindings)
	}
	assertContains(t, readFile(t, root, "clusters/prod-sqlserver-a/inventory/schema-issues.yaml"), "findings: []")
	assertContains(t, readFile(t, root, "clusters/prod-sqlserver-a/inventory/compatibility-report.md"), "No compatibility findings.")
}

func TestAnalyzeSQLServerCompatibilityRequiresExistingCluster(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))

	_, err := AnalyzeSQLServerCompatibility(root, "missing-cluster")
	if err == nil {
		t.Fatal("AnalyzeSQLServerCompatibility() expected error for missing cluster")
	}
	assertContains(t, err.Error(), `source cluster "missing-cluster" does not exist`)
}

func TestGenerateSchemaDraftWritesProjectScopedDDLAndReports(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))
	must(t, CreateProject(root, ProjectSpec{
		SourceClusterID: "prod-sqlserver-a",
		ProjectID:       "sales-db-to-tidb-prod-a",
		DisplayName:     "sales DB to TiDB prod A",
		SourceDatabase:  "sales",
		SourceSchemas:   []string{"dbo"},
		TargetName:      "tidb-prod-a",
		TargetDatabase:  "app",
		TargetSecretRef: "vault://migration/tidb-prod-a/migrate-user",
		Mode:            "short-downtime",
		Owners:          []string{"dba-team"},
	}))
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/inventory/inventory.json", `{
  "status": "discovered",
  "databases": [
    {
      "name": "sales",
      "schemas": [
        {
          "name": "dbo",
          "tables": [
            {
              "name": "orders",
              "row_count": 42,
              "columns": [
                {"name": "id", "type": "int", "identity": true},
                {"name": "customer_name", "type": "nvarchar"},
                {"name": "computed_total", "type": "decimal", "computed": true},
                {"name": "payload", "type": "xml"},
                {"name": "rv", "type": "rowversion"}
              ]
            }
          ]
        },
        {
          "name": "audit",
          "tables": [
            {
              "name": "events",
              "columns": [
                {"name": "id", "type": "bigint"}
              ]
            }
          ]
        }
      ]
    },
    {
      "name": "hr",
      "schemas": [
        {
          "name": "dbo",
          "tables": [
            {
              "name": "employees",
              "columns": [
                {"name": "id", "type": "int"}
              ]
            }
          ]
        }
      ]
    }
  ]
}
`)

	result, err := GenerateSchemaDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err != nil {
		t.Fatalf("GenerateSchemaDraft() error = %v", err)
	}
	if result.Tables != 1 || result.Columns != 5 || result.ManualReviewItems != 3 {
		t.Fatalf("GenerateSchemaDraft() result = %+v, want 1 table, 5 columns, 3 manual review items", result)
	}

	base := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a"
	ddl := readFile(t, root, base+"/schema/tidb-ddl/dbo.orders.sql")
	assertContains(t, ddl, "CREATE TABLE IF NOT EXISTS `app`.`orders`")
	assertContains(t, ddl, "`id` INT")
	assertContains(t, ddl, "`customer_name` VARCHAR(255)")
	assertContains(t, ddl, "`computed_total` DECIMAL /* TODO: computed column expression requires manual rewrite */")
	assertContains(t, ddl, "`payload` TEXT /* TODO: SQL Server xml requires manual mapping */")
	assertContains(t, ddl, "`rv` VARBINARY(8) /* TODO: SQL Server rowversion requires application-managed replacement */")
	if _, err := os.Stat(filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "schema", "tidb-ddl", "audit.events.sql")); err == nil {
		t.Fatal("GenerateSchemaDraft() wrote table outside project source schema")
	}

	report := readFile(t, root, base+"/schema/conversion-report.md")
	assertContains(t, report, "# Schema Conversion Report")
	assertContains(t, report, "sales.dbo.orders")
	assertContains(t, report, "Manual review items: 3")
	assertContains(t, report, "SQLSERVER_COMPUTED_COLUMN")
	assertContains(t, report, "SQLSERVER_TYPE_XML")
	assertContains(t, report, "SQLSERVER_TYPE_ROWVERSION")

	diff := readFile(t, root, base+"/schema/schema-diff.json")
	assertContains(t, diff, `"project_id": "sales-db-to-tidb-prod-a"`)
	assertContains(t, diff, `"source_object": "sales.dbo.orders"`)
	assertContains(t, diff, `"manual_review": true`)
}

func TestGenerateSchemaDraftRequiresExistingProject(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))

	_, err := GenerateSchemaDraft(root, "prod-sqlserver-a", "missing-project")
	if err == nil {
		t.Fatal("GenerateSchemaDraft() expected error for missing project")
	}
	assertContains(t, err.Error(), `project "missing-project" does not exist under source cluster "prod-sqlserver-a"`)
}

func TestGeneratePRDraftWritesSchemaProjectPRBody(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))
	must(t, CreateProject(root, ProjectSpec{
		SourceClusterID: "prod-sqlserver-a",
		ProjectID:       "sales-db-to-tidb-prod-a",
		DisplayName:     "sales DB to TiDB prod A",
		SourceDatabase:  "sales",
		SourceSchemas:   []string{"dbo"},
		TargetName:      "tidb-prod-a",
		TargetDatabase:  "app",
		TargetSecretRef: "vault://migration/tidb-prod-a/migrate-user",
		Mode:            "short-downtime",
		Owners:          []string{"dba-team"},
	}))

	result, err := GeneratePRDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "schema")
	if err != nil {
		t.Fatalf("GeneratePRDraft() error = %v", err)
	}
	if result.Title != "[schema] sales-db-to-tidb-prod-a" {
		t.Fatalf("Title = %q, want schema project title", result.Title)
	}
	if result.BranchName != "agent/sales-db-to-tidb-prod-a/schema" {
		t.Fatalf("BranchName = %q, want project schema branch", result.BranchName)
	}
	assertStringSliceContains(t, result.Files, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/tidb-ddl/")
	assertStringSliceContains(t, result.Files, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/ddl-approval.yaml")

	body := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/schema-pr.md")
	assertContains(t, body, "# PR Draft: [schema] sales-db-to-tidb-prod-a")
	assertContains(t, body, "Source cluster: `prod-sqlserver-a`")
	assertContains(t, body, "Project: `sales-db-to-tidb-prod-a`")
	assertContains(t, body, "Branch: `agent/sales-db-to-tidb-prod-a/schema`")
	assertContains(t, body, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/schema-diff.json")
	assertContains(t, body, "gh pr create --base main --head agent/sales-db-to-tidb-prod-a/schema")
	assertContains(t, body, "Confirm no plaintext secrets are included.")
}

func TestGeneratePRDraftWritesDiscoveryClusterPRBody(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))

	result, err := GeneratePRDraft(root, "prod-sqlserver-a", "", "discovery")
	if err != nil {
		t.Fatalf("GeneratePRDraft() error = %v", err)
	}
	if result.Title != "[discovery] prod-sqlserver-a" {
		t.Fatalf("Title = %q, want discovery cluster title", result.Title)
	}
	if result.BranchName != "agent/prod-sqlserver-a/discovery" {
		t.Fatalf("BranchName = %q, want cluster discovery branch", result.BranchName)
	}
	assertStringSliceContains(t, result.Files, "clusters/prod-sqlserver-a/inventory/inventory.json")

	body := readFile(t, root, "clusters/prod-sqlserver-a/prs/discovery-pr.md")
	assertContains(t, body, "# PR Draft: [discovery] prod-sqlserver-a")
	assertContains(t, body, "Project: cluster-level")
	assertContains(t, body, "clusters/prod-sqlserver-a/inventory/compatibility-report.md")
	assertContains(t, body, "gh pr create --base main --head agent/prod-sqlserver-a/discovery")
}

func TestGeneratePRDraftRejectsProjectStageWithoutProject(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))

	_, err := GeneratePRDraft(root, "prod-sqlserver-a", "", "schema")
	if err == nil {
		t.Fatal("GeneratePRDraft() expected error for missing project")
	}
	assertContains(t, err.Error(), "project id is required for schema PR draft")
}

func TestPrepareGitHubPRCreateBuildsCommandFromGeneratedDraft(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))
	must(t, CreateProject(root, ProjectSpec{
		SourceClusterID: "prod-sqlserver-a",
		ProjectID:       "sales-db-to-tidb-prod-a",
		DisplayName:     "sales DB to TiDB prod A",
		SourceDatabase:  "sales",
		SourceSchemas:   []string{"dbo"},
		TargetName:      "tidb-prod-a",
		TargetDatabase:  "app",
		TargetSecretRef: "vault://migration/tidb-prod-a/migrate-user",
		Mode:            "short-downtime",
		Owners:          []string{"dba-team"},
	}))
	_, err := GeneratePRDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "schema")
	if err != nil {
		t.Fatalf("GeneratePRDraft() error = %v", err)
	}

	spec, err := PrepareGitHubPRCreate(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "schema")
	if err != nil {
		t.Fatalf("PrepareGitHubPRCreate() error = %v", err)
	}
	if spec.Title != "[schema] sales-db-to-tidb-prod-a" {
		t.Fatalf("Title = %q, want schema title", spec.Title)
	}
	if spec.BodyFile != "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/schema-pr.md" {
		t.Fatalf("BodyFile = %q, want project schema body file", spec.BodyFile)
	}
	wantArgs := []string{
		"pr", "create",
		"--base", "main",
		"--head", "agent/sales-db-to-tidb-prod-a/schema",
		"--title", "[schema] sales-db-to-tidb-prod-a",
		"--body-file", "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/schema-pr.md",
	}
	if strings.Join(spec.Args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("Args = %#v, want %#v", spec.Args, wantArgs)
	}
	assertContains(t, spec.ShellCommand, "gh pr create --base main --head agent/sales-db-to-tidb-prod-a/schema")
	assertContains(t, spec.ShellCommand, "--title '[schema] sales-db-to-tidb-prod-a'")
}

func TestPrepareGitHubPRCreateRequiresGeneratedDraft(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))
	must(t, CreateProject(root, ProjectSpec{
		SourceClusterID: "prod-sqlserver-a",
		ProjectID:       "sales-db-to-tidb-prod-a",
		DisplayName:     "sales DB to TiDB prod A",
		SourceDatabase:  "sales",
		SourceSchemas:   []string{"dbo"},
		TargetName:      "tidb-prod-a",
		TargetDatabase:  "app",
		TargetSecretRef: "vault://migration/tidb-prod-a/migrate-user",
		Mode:            "short-downtime",
		Owners:          []string{"dba-team"},
	}))

	_, err := PrepareGitHubPRCreate(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "schema")
	if err == nil {
		t.Fatal("PrepareGitHubPRCreate() expected missing draft error")
	}
	assertContains(t, err.Error(), "PR draft clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/schema-pr.md does not exist")
	assertContains(t, err.Error(), "run generate-pr-draft first")
}

func TestGenerateDataMovementPlansWritesExportAndImportPlans(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{
  "status": "discovered",
  "databases": [
    {
      "name": "sales",
      "schemas": [
        {
          "name": "dbo",
          "tables": [
            {
              "name": "orders",
              "row_count": 2500000,
              "columns": [
                {"name": "id", "type": "int"},
                {"name": "customer_name", "type": "nvarchar"}
              ]
            }
          ]
        },
        {
          "name": "audit",
          "tables": [
            {
              "name": "events",
              "row_count": 100,
              "columns": [
                {"name": "id", "type": "int"}
              ]
            }
          ]
        }
      ]
    }
  ]
}
`)

	result, err := GenerateDataMovementPlans(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", DataMovementPlanSpec{
		ObjectURIPrefix: "s3://migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
		ChunkSizeRows:   1000000,
		ExportFormat:    "parquet",
		ImportEngine:    "import-into",
	})
	if err != nil {
		t.Fatalf("GenerateDataMovementPlans() error = %v", err)
	}
	if result.Tables != 1 || result.ExportChunks != 3 || result.ImportJobs != 3 {
		t.Fatalf("GenerateDataMovementPlans() result = %+v, want 1 table, 3 chunks, 3 import jobs", result)
	}

	exportPlan := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml")
	assertContains(t, exportPlan, "status: draft")
	assertContains(t, exportPlan, "format: parquet")
	assertContains(t, exportPlan, "chunk_size_rows: 1000000")
	assertContains(t, exportPlan, "source_object: sales.dbo.orders")
	assertContains(t, exportPlan, "target_object: app.orders")
	assertContains(t, exportPlan, "id: dbo.orders.000001")
	assertContains(t, exportPlan, "id: dbo.orders.000003")
	assertContains(t, exportPlan, "output_uri: s3://migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000003.parquet")
	if strings.Contains(exportPlan, "sales.audit.events") {
		t.Fatalf("export plan included table outside project schema:\n%s", exportPlan)
	}

	importPlan := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml")
	assertContains(t, importPlan, "status: draft")
	assertContains(t, importPlan, "engine: import-into")
	assertContains(t, importPlan, "target_object: app.orders")
	assertContains(t, importPlan, "source_uri: s3://migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000001.parquet")
	assertContains(t, importPlan, "depends_on_export_chunk: dbo.orders.000003")
}

func TestGenerateDataMovementPlansRequiresObjectURIPrefix(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"discovered","databases":[]}`)

	_, err := GenerateDataMovementPlans(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", DataMovementPlanSpec{
		ChunkSizeRows: 1000000,
		ExportFormat:  "parquet",
		ImportEngine:  "import-into",
	})
	if err == nil {
		t.Fatal("GenerateDataMovementPlans() expected missing object URI prefix error")
	}
	assertContains(t, err.Error(), "object URI prefix is required")
}

func TestRunExportWorkerRequiresApprovedExportApproval(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))
	stateBefore := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml")
	evidenceBefore := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/precheck.json")

	_, err := RunExportWorker(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err == nil {
		t.Fatal("RunExportWorker() expected approval error")
	}
	assertContains(t, err.Error(), "export approval is not approved")
	stateAfter := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml")
	evidenceAfter := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/precheck.json")
	if stateAfter != stateBefore {
		t.Fatalf("export worker changed state before approval\nbefore:\n%s\nafter:\n%s", stateBefore, stateAfter)
	}
	if evidenceAfter != evidenceBefore {
		t.Fatalf("export worker changed evidence before approval\nbefore:\n%s\nafter:\n%s", evidenceBefore, evidenceAfter)
	}
}

func TestRunExportWorkerWritesPlannedStateWhenApprovedHashMatches(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)

	result, err := RunExportWorker(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err != nil {
		t.Fatalf("RunExportWorker() error = %v", err)
	}
	if result.Stage != "export" || result.Status != "planned" || result.Items != 3 {
		t.Fatalf("RunExportWorker() result = %+v, want export planned with 3 items", result)
	}
	if result.PayloadHash != hash {
		t.Fatalf("PayloadHash = %q, want %q", result.PayloadHash, hash)
	}

	state := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml")
	assertContains(t, state, "phase: export")
	assertContains(t, state, "status: planned")
	assertContains(t, state, "payload_hash: "+hash)
	assertContains(t, state, "id: dbo.orders.000001")
	assertContains(t, state, "id: dbo.orders.000003")
	assertContains(t, state, "output_uri: s3://migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000003.parquet")

	evidence := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/precheck.json")
	assertContains(t, evidence, `"stage": "export"`)
	assertContains(t, evidence, `"status": "planned"`)
	assertContains(t, evidence, `"items": 3`)
	assertContains(t, evidence, `"payload_hash": "`+hash+`"`)
}

func TestRunImportWorkerWritesPlannedStateWhenApprovedHashMatches(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	must(t, GenerateDataPlansOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "import")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(import) error = %v", err)
	}
	writeStageApproval(t, root, "import", hash)

	result, err := RunImportWorker(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err != nil {
		t.Fatalf("RunImportWorker() error = %v", err)
	}
	if result.Stage != "import" || result.Status != "planned" || result.Items != 3 {
		t.Fatalf("RunImportWorker() result = %+v, want import planned with 3 items", result)
	}
	if result.PayloadHash != hash {
		t.Fatalf("PayloadHash = %q, want %q", result.PayloadHash, hash)
	}

	state := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/import-jobs.yaml")
	assertContains(t, state, "phase: import")
	assertContains(t, state, "status: planned")
	assertContains(t, state, "payload_hash: "+hash)
	assertContains(t, state, "id: import-dbo.orders.000001")
	assertContains(t, state, "depends_on_export_chunk: dbo.orders.000003")

	evidence := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/import-summary.json")
	assertContains(t, evidence, `"stage": "import"`)
	assertContains(t, evidence, `"status": "planned"`)
	assertContains(t, evidence, `"items": 3`)
	assertContains(t, evidence, `"payload_hash": "`+hash+`"`)
}

func TestRunValidationWorkerRequiresApprovedValidationApproval(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{
  "status": "discovered",
  "databases": [
    {
      "name": "sales",
      "schemas": [
        {
          "name": "dbo",
          "tables": [
            {
              "name": "orders",
              "columns": [
                {"name": "id", "type": "int"}
              ]
            }
          ]
        }
      ]
    }
  ]
}
`)
	must(t, GenerateSchemaDraftOnly(root))
	stateBefore := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/validation-status.yaml")
	evidenceBefore := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/validation-report.md")

	_, err := RunValidationWorker(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err == nil {
		t.Fatal("RunValidationWorker() expected approval error")
	}
	assertContains(t, err.Error(), "validation approval is not approved")
	stateAfter := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/validation-status.yaml")
	evidenceAfter := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/validation-report.md")
	if stateAfter != stateBefore {
		t.Fatalf("validation worker changed state before approval\nbefore:\n%s\nafter:\n%s", stateBefore, stateAfter)
	}
	if evidenceAfter != evidenceBefore {
		t.Fatalf("validation worker changed evidence before approval\nbefore:\n%s\nafter:\n%s", evidenceBefore, evidenceAfter)
	}
}

func TestRunValidationWorkerWritesPassedEvidenceWhenApprovedHashMatches(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{
  "status": "discovered",
  "databases": [
    {
      "name": "sales",
      "schemas": [
        {
          "name": "dbo",
          "tables": [
            {
              "name": "orders",
              "columns": [
                {"name": "id", "type": "int"},
                {"name": "customer_name", "type": "nvarchar"}
              ]
            }
          ]
        }
      ]
    }
  ]
}
`)
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "validation")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage() error = %v", err)
	}
	writeValidationApproval(t, root, hash)

	result, err := RunValidationWorker(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err != nil {
		t.Fatalf("RunValidationWorker() error = %v", err)
	}
	if !result.Passed {
		t.Fatalf("RunValidationWorker() Passed = false, checks = %+v", result.Checks)
	}
	if result.PayloadHash != hash {
		t.Fatalf("PayloadHash = %q, want %q", result.PayloadHash, hash)
	}

	state := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/validation-status.yaml")
	assertContains(t, state, "status: passed")
	assertContains(t, state, "payload_hash: "+hash)
	assertContains(t, state, "name: schema_diff_parseable")

	report := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/validation-report.md")
	assertContains(t, report, "# Validation Report")
	assertContains(t, report, "- Status: `passed`")
	assertContains(t, report, "- Payload hash: `"+hash+"`")
	assertContains(t, report, "schema_diff_parseable")
}

func TestRunValidationWorkerWritesFailedEvidenceForManualReviewItems(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{
  "status": "discovered",
  "databases": [
    {
      "name": "sales",
      "schemas": [
        {
          "name": "dbo",
          "tables": [
            {
              "name": "orders",
              "columns": [
                {"name": "payload", "type": "xml"}
              ]
            }
          ]
        }
      ]
    }
  ]
}
`)
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "validation")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage() error = %v", err)
	}
	writeValidationApproval(t, root, hash)

	result, err := RunValidationWorker(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err != nil {
		t.Fatalf("RunValidationWorker() error = %v", err)
	}
	if result.Passed {
		t.Fatalf("RunValidationWorker() Passed = true, want false")
	}
	state := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/validation-status.yaml")
	assertContains(t, state, "status: failed")
	assertContains(t, state, "name: schema_manual_review_cleared")
	assertContains(t, state, "manual review items remain")
}

func assertFile(t *testing.T, root, rel string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("expected file %s: %v", rel, err)
	}
	if info.IsDir() {
		t.Fatalf("expected file %s, got directory", rel)
	}
}

func assertDir(t *testing.T, root, rel string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("expected directory %s: %v", rel, err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory %s, got file", rel)
	}
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected content to contain %q\ncontent:\n%s", want, got)
	}
}

func assertStringSliceContains(t *testing.T, got []string, want string) {
	t.Helper()
	for _, item := range got {
		if item == want {
			return
		}
	}
	t.Fatalf("expected slice to contain %q\nslice:\n%v", want, got)
}

func assertFindingCode(t *testing.T, findings []CompatibilityFinding, code string) {
	t.Helper()
	for _, finding := range findings {
		if finding.Code == code {
			return
		}
	}
	t.Fatalf("expected finding code %q\nfindings:\n%+v", code, findings)
}

func writeFileForTest(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func createValidationWorkerProject(t *testing.T, root, inventory string) {
	t.Helper()
	must(t, InitRepo(root))
	must(t, CreateCluster(root, ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}))
	must(t, CreateProject(root, ProjectSpec{
		SourceClusterID: "prod-sqlserver-a",
		ProjectID:       "sales-db-to-tidb-prod-a",
		DisplayName:     "sales DB to TiDB prod A",
		SourceDatabase:  "sales",
		SourceSchemas:   []string{"dbo"},
		TargetName:      "tidb-prod-a",
		TargetDatabase:  "app",
		TargetSecretRef: "vault://migration/tidb-prod-a/migrate-user",
		Mode:            "short-downtime",
		Owners:          []string{"dba-team"},
	}))
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/inventory/inventory.json", inventory)
}

func GenerateSchemaDraftOnly(root string) error {
	_, err := GenerateSchemaDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	return err
}

func GenerateDataPlansOnly(root string) error {
	_, err := GenerateDataMovementPlans(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", DataMovementPlanSpec{
		ObjectURIPrefix: "s3://migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
		ChunkSizeRows:   1000000,
		ExportFormat:    "parquet",
		ImportEngine:    "import-into",
	})
	return err
}

func dataWorkerInventory() string {
	return `{
  "status": "discovered",
  "databases": [
    {
      "name": "sales",
      "schemas": [
        {
          "name": "dbo",
          "tables": [
            {
              "name": "orders",
              "row_count": 2500000,
              "columns": [
                {"name": "id", "type": "int"},
                {"name": "customer_name", "type": "nvarchar"}
              ]
            }
          ]
        }
      ]
    }
  ]
}
`
}

func writeValidationApproval(t *testing.T, root, payloadHash string) {
	t.Helper()
	writeStageApproval(t, root, "validation", payloadHash)
}

func writeStageApproval(t *testing.T, root, stage, payloadHash string) {
	t.Helper()
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/"+stage+"-approval.yaml", fmt.Sprintf(`approval_id: %s-test
project_id: sales-db-to-tidb-prod-a
source_cluster_id: prod-sqlserver-a
action: %s
payload_hash: %s
required_reviewers:
  - dba-team
approved_by:
  - dba-team
status: approved
approved_at: "2026-06-26T00:00:00Z"
`, stage, stage, payloadHash))
}

type fakeCatalogReader struct {
	snapshot SQLServerCatalogSnapshot
	called   bool
}

func (reader *fakeCatalogReader) DiscoverSQLServerCatalog(ctx context.Context) (SQLServerCatalogSnapshot, error) {
	reader.called = true
	return reader.snapshot, nil
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
