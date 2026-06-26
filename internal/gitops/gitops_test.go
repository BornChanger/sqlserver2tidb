package gitops

import (
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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
