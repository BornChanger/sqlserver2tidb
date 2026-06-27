package gitops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	assertFile(t, root, "global/schemas/export-plan.schema.json")
	assertFile(t, root, "global/schemas/import-plan.schema.json")
	assertFile(t, root, "global/schemas/cdc-plan.schema.json")
	assertFile(t, root, "global/schemas/validation-plan.schema.json")
	assertDir(t, root, "clusters")

	policyYAML := readFile(t, root, "global/policies/file-schema-policy.yaml")
	assertContains(t, policyYAML, "export_plan: global/schemas/export-plan.schema.json")
	assertContains(t, policyYAML, "import_plan: global/schemas/import-plan.schema.json")
	assertContains(t, policyYAML, "cdc_plan: global/schemas/cdc-plan.schema.json")
	assertContains(t, policyYAML, "validation_plan: global/schemas/validation-plan.schema.json")
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

func TestCreateClusterRejectsExistingClusterDirectory(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	spec := ClusterSpec{
		ClusterID:              "prod-sqlserver-a",
		DisplayName:            "prod SQL Server A",
		Listener:               "sqlserver-a.internal",
		Port:                   1433,
		SecretRef:              "vault://migration/prod-sqlserver-a/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}
	must(t, CreateCluster(root, spec))
	before := readFile(t, root, "clusters/prod-sqlserver-a/cluster.yaml")

	spec.DisplayName = "replacement"
	spec.Listener = "replacement.internal"
	err := CreateCluster(root, spec)
	if err == nil {
		t.Fatal("CreateCluster() error = nil, want duplicate cluster error")
	}
	assertContains(t, err.Error(), `source cluster "prod-sqlserver-a" already exists`)
	after := readFile(t, root, "clusters/prod-sqlserver-a/cluster.yaml")
	if after != before {
		t.Fatalf("CreateCluster() overwrote existing cluster.yaml\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestCreateClusterRequiresOwner(t *testing.T) {
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
	})
	if err == nil {
		t.Fatal("CreateCluster() error = nil, want owner requirement")
	}
	assertContains(t, err.Error(), "at least one cluster owner is required")
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

	migrationPlan := readFile(t, root, base+"/plan/migration-plan.yaml")
	assertContains(t, migrationPlan, "format: csv")
	assertContains(t, migrationPlan, "engine: sql-insert")
}

func TestCreateProjectRejectsExistingProjectDirectory(t *testing.T) {
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
	spec := ProjectSpec{
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
	}
	must(t, CreateProject(root, spec))
	before := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/project.yaml")

	spec.DisplayName = "replacement"
	spec.TargetDatabase = "replacement"
	err := CreateProject(root, spec)
	if err == nil {
		t.Fatal("CreateProject() error = nil, want duplicate project error")
	}
	assertContains(t, err.Error(), `migration project "sales-db-to-tidb-prod-a" already exists`)
	after := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/project.yaml")
	if after != before {
		t.Fatalf("CreateProject() overwrote existing project.yaml\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestCreateProjectRequiresOwner(t *testing.T) {
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
	})
	if err == nil {
		t.Fatal("CreateProject() error = nil, want owner requirement")
	}
	assertContains(t, err.Error(), "at least one project owner is required")
}

func TestCreateProjectRejectsUnsupportedMode(t *testing.T) {
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
		Mode:            "blue-green",
		Owners:          []string{"dba-team"},
	})
	if err == nil {
		t.Fatal("CreateProject() error = nil, want unsupported mode error")
	}
	assertContains(t, err.Error(), `unsupported migration mode "blue-green"`)
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

func TestValidateRepoReportsProjectWithoutOwners(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	projectRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/project.yaml"
	projectYAML := readFile(t, root, projectRel)
	projectYAML = strings.Replace(projectYAML, "owners:\n  - dba-team\n", "owners: []\n", 1)
	writeFileForTest(t, root, projectRel, projectYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid project owner metadata")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid project metadata clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/project.yaml: at least one project owner is required")
}

func TestValidateRepoReportsUnsupportedProjectMode(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	projectRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/project.yaml"
	projectYAML := readFile(t, root, projectRel)
	projectYAML = strings.Replace(projectYAML, "mode: short-downtime", "mode: blue-green", 1)
	writeFileForTest(t, root, projectRel, projectYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid project mode metadata")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid project metadata clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/project.yaml: unsupported migration mode "blue-green"`)
}

func TestValidateRepoReportsProjectWithoutSourceSchema(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	projectRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/project.yaml"
	projectYAML := readFile(t, root, projectRel)
	projectYAML = strings.Replace(projectYAML, "  schemas:\n    - dbo\n", "  schemas: []\n", 1)
	writeFileForTest(t, root, projectRel, projectYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid project source schema metadata")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid project metadata clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/project.yaml: at least one source schema is required")
}

func TestValidateRepoReportsClusterWithoutOwners(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	clusterRel := "clusters/prod-sqlserver-a/cluster.yaml"
	clusterYAML := readFile(t, root, clusterRel)
	clusterYAML = strings.Replace(clusterYAML, "owners:\n  - dba-team\n", "owners: []\n", 1)
	writeFileForTest(t, root, clusterRel, clusterYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid cluster owner metadata")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid cluster metadata clusters/prod-sqlserver-a/cluster.yaml: at least one cluster owner is required")
}

func TestValidateRepoReportsInvalidClusterRetention(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	clusterRel := "clusters/prod-sqlserver-a/cluster.yaml"
	clusterYAML := readFile(t, root, clusterRel)
	clusterYAML = strings.Replace(clusterYAML, "retention_hours_required: 168", "retention_hours_required: 0", 1)
	writeFileForTest(t, root, clusterRel, clusterYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid cluster retention metadata")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid cluster metadata clusters/prod-sqlserver-a/cluster.yaml: cdc retention hours must be positive")
}

func TestValidateRepoReportsClusterIDMismatch(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	clusterRel := "clusters/prod-sqlserver-a/cluster.yaml"
	clusterYAML := readFile(t, root, clusterRel)
	clusterYAML = strings.Replace(clusterYAML, "cluster_id: prod-sqlserver-a", "cluster_id: prod-sqlserver-b", 1)
	writeFileForTest(t, root, clusterRel, clusterYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want cluster id mismatch")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid cluster metadata clusters/prod-sqlserver-a/cluster.yaml: cluster_id "prod-sqlserver-b" does not match directory id "prod-sqlserver-a"`)
}

func TestValidateRepoReportsSourceProfileMismatch(t *testing.T) {
	tests := []struct {
		name      string
		oldValue  string
		newValue  string
		wantError string
	}{
		{
			name:      "cluster_id",
			oldValue:  "cluster_id: prod-sqlserver-a",
			newValue:  "cluster_id: prod-sqlserver-b",
			wantError: `invalid source profile clusters/prod-sqlserver-a/source-profile.yaml: cluster_id "prod-sqlserver-b" does not match cluster metadata "prod-sqlserver-a"`,
		},
		{
			name:      "listener",
			oldValue:  "listener: sqlserver-a.internal",
			newValue:  "listener: sqlserver-b.internal",
			wantError: `invalid source profile clusters/prod-sqlserver-a/source-profile.yaml: listener "sqlserver-b.internal" does not match cluster listener "sqlserver-a.internal"`,
		},
		{
			name:      "port",
			oldValue:  "port: 1433",
			newValue:  "port: 1444",
			wantError: `invalid source profile clusters/prod-sqlserver-a/source-profile.yaml: port 1444 does not match cluster port 1433`,
		},
		{
			name:      "secret_ref",
			oldValue:  "secret_ref: vault://migration/prod-sqlserver-a/readonly",
			newValue:  "secret_ref: vault://migration/prod-sqlserver-b/readonly",
			wantError: `invalid source profile clusters/prod-sqlserver-a/source-profile.yaml: secret_ref "vault://migration/prod-sqlserver-b/readonly" does not match cluster secret_ref "vault://migration/prod-sqlserver-a/readonly"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			profileRel := "clusters/prod-sqlserver-a/source-profile.yaml"
			profileYAML := readFile(t, root, profileRel)
			profileYAML = strings.Replace(profileYAML, tt.oldValue, tt.newValue, 1)
			writeFileForTest(t, root, profileRel, profileYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want source profile mismatch")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsClusterStateMetadataMismatch(t *testing.T) {
	tests := []struct {
		name      string
		rel       string
		oldValue  string
		newValue  string
		wantError string
	}{
		{
			name:      "cdc_checkpoint",
			rel:       "clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml",
			oldValue:  "source_cluster_id: prod-sqlserver-a",
			newValue:  "source_cluster_id: prod-sqlserver-b",
			wantError: `invalid cluster state clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml: source_cluster_id "prod-sqlserver-b" does not match cluster metadata "prod-sqlserver-a"`,
		},
		{
			name:      "worker_lease",
			rel:       "clusters/prod-sqlserver-a/state/worker-lease.yaml",
			oldValue:  "source_cluster_id: prod-sqlserver-a",
			newValue:  "source_cluster_id: prod-sqlserver-b",
			wantError: `invalid cluster state clusters/prod-sqlserver-a/state/worker-lease.yaml: source_cluster_id "prod-sqlserver-b" does not match cluster metadata "prod-sqlserver-a"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			stateYAML := readFile(t, root, tt.rel)
			stateYAML = strings.Replace(stateYAML, tt.oldValue, tt.newValue, 1)
			writeFileForTest(t, root, tt.rel, stateYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want cluster state metadata mismatch")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsCDCCheckpointModeMismatch(t *testing.T) {
	tests := []struct {
		name      string
		oldValue  string
		newValue  string
		wantError string
	}{
		{
			name:      "capture_mode",
			oldValue:  "capture_mode: sqlserver-cdc",
			newValue:  "capture_mode: debezium",
			wantError: `invalid cluster state clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml: capture_mode "debezium" does not match cluster cdc mode "sqlserver-cdc"`,
		},
		{
			name:      "mode",
			oldValue:  "capture_mode: sqlserver-cdc",
			newValue:  "mode: debezium",
			wantError: `invalid cluster state clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml: mode "debezium" does not match cluster cdc mode "sqlserver-cdc"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			rel := "clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml"
			stateYAML := readFile(t, root, rel)
			stateYAML = strings.Replace(stateYAML, tt.oldValue, tt.newValue, 1)
			writeFileForTest(t, root, rel, stateYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want CDC checkpoint mode mismatch")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsInvalidCDCCheckpointStatus(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	rel := "clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml"
	stateYAML := readFile(t, root, rel)
	stateYAML = strings.Replace(stateYAML, "status: not_started", "status: replaying", 1)
	writeFileForTest(t, root, rel, stateYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid CDC checkpoint status")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid cluster state clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml: unsupported CDC checkpoint status "replaying"; supported statuses: not_started, planned, running, caught_up, failed`)
}

func TestValidateRepoReportsInvalidCDCCheckpointUpdatedAt(t *testing.T) {
	tests := []struct {
		name      string
		newValue  string
		wantError string
	}{
		{
			name:      "missing",
			newValue:  `updated_at: ""`,
			wantError: "CDC checkpoint updated_at is required",
		},
		{
			name:      "invalid",
			newValue:  `updated_at: "not-a-time"`,
			wantError: "CDC checkpoint updated_at must be RFC3339",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			rel := "clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml"
			stateYAML := readFile(t, root, rel)
			stateYAML = replaceTopLevelLineForTest(t, stateYAML, "updated_at", tt.newValue)
			writeFileForTest(t, root, rel, stateYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want invalid CDC checkpoint updated_at")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), "invalid cluster state "+rel+": "+tt.wantError)
		})
	}
}

func TestValidateRepoReportsInvalidWorkerLeasePhase(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	leaseRel := "clusters/prod-sqlserver-a/state/worker-lease.yaml"
	leaseYAML := readFile(t, root, leaseRel)
	leaseYAML = strings.Replace(leaseYAML, "phase: idle", "phase: running", 1)
	writeFileForTest(t, root, leaseRel, leaseYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid worker lease phase")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid cluster state clusters/prod-sqlserver-a/state/worker-lease.yaml: unsupported worker lease phase "running"; supported phases: idle, export, import, cdc, validation`)
}

func TestValidateRepoReportsIncompleteActiveWorkerLease(t *testing.T) {
	tests := []struct {
		name      string
		oldValue  string
		newValue  string
		wantError string
	}{
		{
			name:      "holder",
			oldValue:  "holder: agent-a",
			newValue:  `holder: ""`,
			wantError: "active worker lease requires holder",
		},
		{
			name:      "lease_id",
			oldValue:  "lease_id: lease-1",
			newValue:  `lease_id: ""`,
			wantError: "active worker lease requires lease_id",
		},
		{
			name:      "expires_at",
			oldValue:  `expires_at: "2026-06-26T00:15:00Z"`,
			newValue:  `expires_at: ""`,
			wantError: "active worker lease requires expires_at",
		},
		{
			name:      "renewed_at",
			oldValue:  `renewed_at: "2026-06-26T00:00:00Z"`,
			newValue:  `renewed_at: ""`,
			wantError: "active worker lease requires renewed_at",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			leaseRel := "clusters/prod-sqlserver-a/state/worker-lease.yaml"
			leaseYAML := `source_cluster_id: prod-sqlserver-a
holder: agent-a
lease_id: lease-1
phase: export
project_id: sales-db-to-tidb-prod-a
expires_at: "2026-06-26T00:15:00Z"
renewed_at: "2026-06-26T00:00:00Z"
`
			leaseYAML = strings.Replace(leaseYAML, tt.oldValue, tt.newValue, 1)
			writeFileForTest(t, root, leaseRel, leaseYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want incomplete active worker lease")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), "invalid cluster state "+leaseRel+": "+tt.wantError)
		})
	}
}

func TestValidateRepoReportsInvalidActiveWorkerLeaseTimes(t *testing.T) {
	tests := []struct {
		name      string
		oldValue  string
		newValue  string
		wantError string
	}{
		{
			name:      "invalid_expires_at",
			oldValue:  `expires_at: "2026-06-26T00:15:00Z"`,
			newValue:  `expires_at: "not-a-time"`,
			wantError: "active worker lease expires_at must be RFC3339",
		},
		{
			name:      "invalid_renewed_at",
			oldValue:  `renewed_at: "2026-06-26T00:00:00Z"`,
			newValue:  `renewed_at: "not-a-time"`,
			wantError: "active worker lease renewed_at must be RFC3339",
		},
		{
			name:      "expires_before_renewed",
			oldValue:  `expires_at: "2026-06-26T00:15:00Z"`,
			newValue:  `expires_at: "2026-06-25T23:59:59Z"`,
			wantError: "active worker lease expires_at must not be before renewed_at",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			leaseRel := "clusters/prod-sqlserver-a/state/worker-lease.yaml"
			leaseYAML := `source_cluster_id: prod-sqlserver-a
holder: agent-a
lease_id: lease-1
phase: export
project_id: sales-db-to-tidb-prod-a
expires_at: "2026-06-26T00:15:00Z"
renewed_at: "2026-06-26T00:00:00Z"
`
			leaseYAML = strings.Replace(leaseYAML, tt.oldValue, tt.newValue, 1)
			writeFileForTest(t, root, leaseRel, leaseYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want invalid active worker lease times")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), "invalid cluster state "+leaseRel+": "+tt.wantError)
		})
	}
}

func TestValidateRepoReportsInvalidInventoryJSON(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	inventoryRel := "clusters/prod-sqlserver-a/inventory/inventory.json"
	writeFileForTest(t, root, inventoryRel, "{")

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid inventory JSON")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid inventory clusters/prod-sqlserver-a/inventory/inventory.json: parse inventory`)
}

func TestValidateRepoReportsProjectIDMismatch(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	projectRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/project.yaml"
	projectYAML := readFile(t, root, projectRel)
	projectYAML = strings.Replace(projectYAML, "project_id: sales-db-to-tidb-prod-a", "project_id: inventory-db-to-tidb-prod-a", 1)
	writeFileForTest(t, root, projectRel, projectYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want project id mismatch")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid project metadata clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/project.yaml: project_id "inventory-db-to-tidb-prod-a" does not match directory id "sales-db-to-tidb-prod-a"`)
}

func TestValidateRepoReportsProjectSourceClusterIDMismatch(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	projectRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/project.yaml"
	projectYAML := readFile(t, root, projectRel)
	projectYAML = strings.Replace(projectYAML, "source_cluster_id: prod-sqlserver-a", "source_cluster_id: prod-sqlserver-b", 1)
	writeFileForTest(t, root, projectRel, projectYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want source cluster id mismatch")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid project metadata clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/project.yaml: source_cluster_id "prod-sqlserver-b" does not match parent cluster id "prod-sqlserver-a"`)
}

func TestValidateRepoReportsMigrationPlanMetadataMismatch(t *testing.T) {
	tests := []struct {
		name      string
		oldValue  string
		newValue  string
		wantError string
	}{
		{
			name:      "plan_version",
			oldValue:  "plan_version: 1",
			newValue:  "plan_version: 0",
			wantError: `invalid migration plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/migration-plan.yaml: plan_version must be greater than or equal to 1`,
		},
		{
			name:      "project_id",
			oldValue:  "project_id: sales-db-to-tidb-prod-a",
			newValue:  "project_id: inventory-db-to-tidb-prod-a",
			wantError: `invalid migration plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/migration-plan.yaml: project_id "inventory-db-to-tidb-prod-a" does not match project metadata "sales-db-to-tidb-prod-a"`,
		},
		{
			name:      "source_cluster_id",
			oldValue:  "source_cluster_id: prod-sqlserver-a",
			newValue:  "source_cluster_id: prod-sqlserver-b",
			wantError: `invalid migration plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/migration-plan.yaml: source_cluster_id "prod-sqlserver-b" does not match project metadata "prod-sqlserver-a"`,
		},
		{
			name:      "mode",
			oldValue:  "mode: short-downtime",
			newValue:  "mode: blue-green",
			wantError: `invalid migration plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/migration-plan.yaml: unsupported migration mode "blue-green"; supported modes: offline, short-downtime, low-downtime`,
		},
		{
			name:      "mode_mismatch",
			oldValue:  "mode: short-downtime",
			newValue:  "mode: offline",
			wantError: `invalid migration plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/migration-plan.yaml: mode "offline" does not match project metadata "short-downtime"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			planRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/migration-plan.yaml"
			planYAML := readFile(t, root, planRel)
			planYAML = strings.Replace(planYAML, tt.oldValue, tt.newValue, 1)
			writeFileForTest(t, root, planRel, planYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want migration plan mismatch")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsCDCPlanMetadataMismatch(t *testing.T) {
	tests := []struct {
		name      string
		oldValue  string
		newValue  string
		wantError string
	}{
		{
			name:      "project_id",
			oldValue:  "project_id: sales-db-to-tidb-prod-a",
			newValue:  "project_id: inventory-db-to-tidb-prod-a",
			wantError: `invalid cdc plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml: project_id "inventory-db-to-tidb-prod-a" does not match project metadata "sales-db-to-tidb-prod-a"`,
		},
		{
			name:      "source_cluster_id",
			oldValue:  "source_cluster_id: prod-sqlserver-a",
			newValue:  "source_cluster_id: prod-sqlserver-b",
			wantError: `invalid cdc plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml: source_cluster_id "prod-sqlserver-b" does not match project metadata "prod-sqlserver-a"`,
		},
		{
			name:      "mode",
			oldValue:  "mode: sqlserver-cdc",
			newValue:  "mode: custom-cdc",
			wantError: `invalid cdc plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml: mode "custom-cdc" does not match cluster cdc mode "sqlserver-cdc"`,
		},
		{
			name:      "checkpoint_file",
			oldValue:  "checkpoint_file: ../../../state/cdc-checkpoint.yaml",
			newValue:  "checkpoint_file: state/cdc-checkpoint.yaml",
			wantError: `invalid cdc plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml: checkpoint_file "state/cdc-checkpoint.yaml" does not match "../../../state/cdc-checkpoint.yaml"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, dataWorkerInventory())
			must(t, GenerateCDCPlanOnly(root))
			planRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml"
			planYAML := readFile(t, root, planRel)
			planYAML = strings.Replace(planYAML, tt.oldValue, tt.newValue, 1)
			writeFileForTest(t, root, planRel, planYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want cdc plan metadata mismatch")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsValidationPlanMetadataMismatch(t *testing.T) {
	tests := []struct {
		name      string
		oldValue  string
		newValue  string
		wantError string
	}{
		{
			name:      "project_id",
			oldValue:  "project_id: sales-db-to-tidb-prod-a",
			newValue:  "project_id: inventory-db-to-tidb-prod-a",
			wantError: `invalid validation plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/validation-plan.yaml: project_id "inventory-db-to-tidb-prod-a" does not match project metadata "sales-db-to-tidb-prod-a"`,
		},
		{
			name:      "source_cluster_id",
			oldValue:  "source_cluster_id: prod-sqlserver-a",
			newValue:  "source_cluster_id: prod-sqlserver-b",
			wantError: `invalid validation plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/validation-plan.yaml: source_cluster_id "prod-sqlserver-b" does not match project metadata "prod-sqlserver-a"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, dataWorkerInventory())
			if _, err := GenerateValidationPlan(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a"); err != nil {
				t.Fatalf("GenerateValidationPlan() error = %v", err)
			}
			planRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/validation-plan.yaml"
			planYAML := readFile(t, root, planRel)
			planYAML = strings.Replace(planYAML, tt.oldValue, tt.newValue, 1)
			writeFileForTest(t, root, planRel, planYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want validation plan metadata mismatch")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsSchemaDiffMetadataMismatch(t *testing.T) {
	tests := []struct {
		name      string
		oldValue  string
		newValue  string
		wantError string
	}{
		{
			name:      "project_id",
			oldValue:  `"project_id": "sales-db-to-tidb-prod-a"`,
			newValue:  `"project_id": "inventory-db-to-tidb-prod-a"`,
			wantError: `invalid schema diff clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/schema-diff.json: project_id "inventory-db-to-tidb-prod-a" does not match project metadata "sales-db-to-tidb-prod-a"`,
		},
		{
			name:      "source_cluster_id",
			oldValue:  `"source_cluster_id": "prod-sqlserver-a"`,
			newValue:  `"source_cluster_id": "prod-sqlserver-b"`,
			wantError: `invalid schema diff clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/schema-diff.json: source_cluster_id "prod-sqlserver-b" does not match project metadata "prod-sqlserver-a"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, dataWorkerInventory())
			must(t, GenerateSchemaDraftOnly(root))
			diffRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/schema-diff.json"
			diffJSON := readFile(t, root, diffRel)
			diffJSON = strings.Replace(diffJSON, tt.oldValue, tt.newValue, 1)
			writeFileForTest(t, root, diffRel, diffJSON)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want schema diff metadata mismatch")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsInvalidSchemaDiffJSON(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	diffRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/schema-diff.json"
	writeFileForTest(t, root, diffRel, "{")

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid schema diff JSON")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid schema diff clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/schema-diff.json: parse schema diff JSON`)
}

func TestValidateRepoReportsEvidenceMetadataMismatch(t *testing.T) {
	tests := []struct {
		name      string
		rel       string
		body      string
		wantError string
	}{
		{
			name: "precheck_project_id",
			rel:  "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/precheck.json",
			body: `{
  "status": "planned",
  "project_id": "inventory-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-a"
}
`,
			wantError: `invalid evidence clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/precheck.json: project_id "inventory-db-to-tidb-prod-a" does not match project metadata "sales-db-to-tidb-prod-a"`,
		},
		{
			name: "cdc_catchup_source_cluster_id",
			rel:  "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/cdc-catchup.json",
			body: `{
  "status": "planned",
  "project_id": "sales-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-b"
}
`,
			wantError: `invalid evidence clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/cdc-catchup.json: source_cluster_id "prod-sqlserver-b" does not match project metadata "prod-sqlserver-a"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			writeFileForTest(t, root, tt.rel, tt.body)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want evidence metadata mismatch")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsInvalidEvidenceJSON(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	evidenceRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/import-summary.json"
	writeFileForTest(t, root, evidenceRel, "{")

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid evidence JSON")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid evidence clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/import-summary.json: parse evidence JSON`)
}

func TestValidateRepoReportsExecutorEvidenceMetadataMismatch(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	evidenceRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json"
	writeFileForTest(t, root, evidenceRel, `{
  "stage": "export",
  "status": "succeeded",
  "project_id": "inventory-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-a",
  "payload_hash": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "commands": [
    {
      "id": "export-1",
      "args": ["sqlserver2tidb-executor", "export"],
      "shell_command": "sqlserver2tidb-executor export",
      "exit_code": 0,
      "started_at": "2026-06-26T00:00:00Z",
      "completed_at": "2026-06-26T00:00:01Z",
      "duration_ms": 1000
    }
  ]
}
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want executor evidence metadata mismatch")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid executor evidence clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json: project_id "inventory-db-to-tidb-prod-a" does not match project metadata "sales-db-to-tidb-prod-a"`)
}

func TestValidateRepoReportsInvalidExecutorEvidenceJSON(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	evidenceRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json"
	writeFileForTest(t, root, evidenceRel, "{")

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid executor evidence JSON")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid executor evidence clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json: parse executor evidence JSON`)
}

func TestValidateRepoReportsDataPlanMetadataMismatch(t *testing.T) {
	tests := []struct {
		name      string
		rel       string
		oldValue  string
		newValue  string
		wantError string
	}{
		{
			name:      "export_project_id",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml",
			oldValue:  "project_id: sales-db-to-tidb-prod-a",
			newValue:  "project_id: inventory-db-to-tidb-prod-a",
			wantError: `invalid export plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml: project_id "inventory-db-to-tidb-prod-a" does not match project metadata "sales-db-to-tidb-prod-a"`,
		},
		{
			name:      "import_source_cluster_id",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml",
			oldValue:  "source_cluster_id: prod-sqlserver-a",
			newValue:  "source_cluster_id: prod-sqlserver-b",
			wantError: `invalid import plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml: source_cluster_id "prod-sqlserver-b" does not match project metadata "prod-sqlserver-a"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, dataWorkerInventory())
			must(t, GenerateDataPlansOnly(root))
			reviewExportPlanPredicates(t, root)
			planYAML := readFile(t, root, tt.rel)
			planYAML = strings.Replace(planYAML, tt.oldValue, tt.newValue, 1)
			writeFileForTest(t, root, tt.rel, planYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want data plan metadata mismatch")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsProjectStateMetadataMismatch(t *testing.T) {
	tests := []struct {
		name      string
		rel       string
		oldValue  string
		newValue  string
		wantError string
	}{
		{
			name:      "migration_state_project_id",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/migration-state.yaml",
			oldValue:  "project_id: sales-db-to-tidb-prod-a",
			newValue:  "project_id: inventory-db-to-tidb-prod-a",
			wantError: `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/migration-state.yaml: project_id "inventory-db-to-tidb-prod-a" does not match project metadata "sales-db-to-tidb-prod-a"`,
		},
		{
			name:      "export_chunks_source_cluster_id",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml",
			oldValue:  "source_cluster_id: prod-sqlserver-a",
			newValue:  "source_cluster_id: prod-sqlserver-b",
			wantError: `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml: source_cluster_id "prod-sqlserver-b" does not match project metadata "prod-sqlserver-a"`,
		},
		{
			name:      "import_jobs_project_id",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/import-jobs.yaml",
			oldValue:  "project_id: sales-db-to-tidb-prod-a",
			newValue:  "project_id: inventory-db-to-tidb-prod-a",
			wantError: `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/import-jobs.yaml: project_id "inventory-db-to-tidb-prod-a" does not match project metadata "sales-db-to-tidb-prod-a"`,
		},
		{
			name:      "validation_status_source_cluster_id",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/validation-status.yaml",
			oldValue:  "source_cluster_id: prod-sqlserver-a",
			newValue:  "source_cluster_id: prod-sqlserver-b",
			wantError: `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/validation-status.yaml: source_cluster_id "prod-sqlserver-b" does not match project metadata "prod-sqlserver-a"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			stateYAML := readFile(t, root, tt.rel)
			stateYAML = strings.Replace(stateYAML, tt.oldValue, tt.newValue, 1)
			writeFileForTest(t, root, tt.rel, stateYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want project state metadata mismatch")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsInvalidMigrationStatePhase(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	rel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/migration-state.yaml"
	stateYAML := readFile(t, root, rel)
	stateYAML = strings.Replace(stateYAML, "phase: planning", "phase: unknown", 1)
	writeFileForTest(t, root, rel, stateYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid migration state phase")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/migration-state.yaml: unsupported migration state phase "unknown"; supported phases: planning, ddl, export, import, cdc, validation, cutover, completed`)
}

func TestValidateRepoReportsInvalidMigrationStateStatus(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	rel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/migration-state.yaml"
	stateYAML := readFile(t, root, rel)
	stateYAML = strings.Replace(stateYAML, "status: not_started", "status: ready", 1)
	writeFileForTest(t, root, rel, stateYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid migration state status")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/migration-state.yaml: unsupported migration state status "ready"; supported statuses: not_started, planned, running, completed, failed`)
}

func TestValidateRepoReportsInvalidMigrationStateUpdatedAt(t *testing.T) {
	tests := []struct {
		name      string
		newValue  string
		wantError string
	}{
		{
			name:      "missing",
			newValue:  `updated_at: ""`,
			wantError: "migration state updated_at is required",
		},
		{
			name:      "invalid",
			newValue:  `updated_at: "not-a-time"`,
			wantError: "migration state updated_at must be RFC3339",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			rel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/migration-state.yaml"
			stateYAML := readFile(t, root, rel)
			stateYAML = replaceTopLevelLineForTest(t, stateYAML, "updated_at", tt.newValue)
			writeFileForTest(t, root, rel, stateYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want invalid migration state updated_at")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), "invalid state file "+rel+": "+tt.wantError)
		})
	}
}

func TestValidateRepoReportsInvalidValidationStatusState(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	rel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/validation-status.yaml"
	stateYAML := readFile(t, root, rel)
	stateYAML = strings.Replace(stateYAML, "status: pending", "status: unknown", 1)
	writeFileForTest(t, root, rel, stateYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid validation status state")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/validation-status.yaml: unsupported validation status "unknown"; supported statuses: pending, passed, failed`)
}

func TestValidateRepoReportsInvalidValidationStatusUpdatedAt(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	rel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/validation-status.yaml"
	stateYAML := readFile(t, root, rel)
	stateYAML = strings.Replace(stateYAML, "checks: []", "updated_at: \"not-a-time\"\nchecks: []", 1)
	writeFileForTest(t, root, rel, stateYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid validation status updated_at")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/validation-status.yaml: validation status updated_at must be RFC3339`)
}

func TestValidateRepoReportsInvalidDataStatePhase(t *testing.T) {
	tests := []struct {
		name      string
		rel       string
		listLine  string
		newPhase  string
		wantError string
	}{
		{
			name:      "export_chunks",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml",
			listLine:  "chunks: []",
			newPhase:  "phase: import",
			wantError: `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml: export state phase "import" does not match expected phase "export"`,
		},
		{
			name:      "import_jobs",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/import-jobs.yaml",
			listLine:  "jobs: []",
			newPhase:  "phase: export",
			wantError: `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/import-jobs.yaml: import state phase "export" does not match expected phase "import"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			stateYAML := readFile(t, root, tt.rel)
			stateYAML = strings.Replace(stateYAML, tt.listLine, tt.newPhase+"\n"+tt.listLine, 1)
			writeFileForTest(t, root, tt.rel, stateYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want invalid data state phase")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsInvalidDataStateUpdatedAt(t *testing.T) {
	tests := []struct {
		name      string
		rel       string
		listLine  string
		wantError string
	}{
		{
			name:      "export_chunks",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml",
			listLine:  "chunks: []",
			wantError: `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml: export state updated_at must be RFC3339`,
		},
		{
			name:      "import_jobs",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/import-jobs.yaml",
			listLine:  "jobs: []",
			wantError: `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/import-jobs.yaml: import state updated_at must be RFC3339`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			stateYAML := readFile(t, root, tt.rel)
			stateYAML = strings.Replace(stateYAML, tt.listLine, "updated_at: \"not-a-time\"\n"+tt.listLine, 1)
			writeFileForTest(t, root, tt.rel, stateYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want invalid data state updated_at")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsInvalidDataStateStatus(t *testing.T) {
	tests := []struct {
		name      string
		rel       string
		listLine  string
		status    string
		wantError string
	}{
		{
			name:      "export_chunks",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml",
			listLine:  "chunks: []",
			status:    "status: completed",
			wantError: `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml: unsupported export state status "completed"; supported statuses: planned`,
		},
		{
			name:      "import_jobs",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/import-jobs.yaml",
			listLine:  "jobs: []",
			status:    "status: completed",
			wantError: `invalid state file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/import-jobs.yaml: unsupported import state status "completed"; supported statuses: planned`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			stateYAML := readFile(t, root, tt.rel)
			stateYAML = strings.Replace(stateYAML, tt.listLine, tt.status+"\n"+tt.listLine, 1)
			writeFileForTest(t, root, tt.rel, stateYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want invalid data state status")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsApprovalMetadataMismatch(t *testing.T) {
	tests := []struct {
		name      string
		rel       string
		oldValue  string
		newValue  string
		wantError string
	}{
		{
			name:      "action",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/export-approval.yaml",
			oldValue:  "action: export",
			newValue:  "action: import",
			wantError: `invalid approval clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/export-approval.yaml: action "import" does not match approval file stage "export"`,
		},
		{
			name:      "project_id",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/cdc-approval.yaml",
			oldValue:  "project_id: sales-db-to-tidb-prod-a",
			newValue:  "project_id: inventory-db-to-tidb-prod-a",
			wantError: `invalid approval clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/cdc-approval.yaml: project_id "inventory-db-to-tidb-prod-a" does not match project metadata "sales-db-to-tidb-prod-a"`,
		},
		{
			name:      "source_cluster_id",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/validation-approval.yaml",
			oldValue:  "source_cluster_id: prod-sqlserver-a",
			newValue:  "source_cluster_id: prod-sqlserver-b",
			wantError: `invalid approval clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/validation-approval.yaml: source_cluster_id "prod-sqlserver-b" does not match project metadata "prod-sqlserver-a"`,
		},
		{
			name:      "status",
			rel:       "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/ddl-approval.yaml",
			oldValue:  "status: pending",
			newValue:  "status: ready",
			wantError: `invalid approval clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/ddl-approval.yaml: unsupported approval status "ready"; supported statuses: pending, approved, rejected`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
			approvalYAML := readFile(t, root, tt.rel)
			approvalYAML = strings.Replace(approvalYAML, tt.oldValue, tt.newValue, 1)
			writeFileForTest(t, root, tt.rel, approvalYAML)

			report, err := ValidateRepo(root)
			if err != nil {
				t.Fatalf("ValidateRepo() error = %v", err)
			}
			if report.Valid {
				t.Fatal("ValidateRepo() valid = true, want approval metadata mismatch")
			}
			assertContains(t, strings.Join(report.Errors, "\n"), tt.wantError)
		})
	}
}

func TestValidateRepoReportsApprovedApprovalWithoutPayloadHash(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	approvalRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/export-approval.yaml"
	approvalYAML := readFile(t, root, approvalRel)
	approvalYAML = strings.Replace(approvalYAML, "status: pending", "status: approved", 1)
	writeFileForTest(t, root, approvalRel, approvalYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want approved approval without payload hash")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid approval clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/export-approval.yaml: approved approval requires payload_hash`)
}

func TestValidateRepoReportsApprovedApprovalWithoutReviewer(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	approvalRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/export-approval.yaml"
	approvalYAML := readFile(t, root, approvalRel)
	approvalYAML = strings.Replace(approvalYAML, `payload_hash: ""`, "payload_hash: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", 1)
	approvalYAML = strings.Replace(approvalYAML, "status: pending", "status: approved", 1)
	writeFileForTest(t, root, approvalRel, approvalYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want approved approval without reviewer")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid approval clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/export-approval.yaml: approved approval requires at least one approved_by reviewer`)
}

func TestValidateRepoReportsInvalidApprovalPayloadHash(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"pending","databases":[]}`)
	approvalRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/export-approval.yaml"
	approvalYAML := readFile(t, root, approvalRel)
	approvalYAML = strings.Replace(approvalYAML, `payload_hash: ""`, "payload_hash: stale", 1)
	writeFileForTest(t, root, approvalRel, approvalYAML)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatal("ValidateRepo() valid = true, want invalid approval payload hash")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), `invalid approval clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/export-approval.yaml: payload_hash "stale" must use sha256:<64 hex chars>`)
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

func TestValidateRepoReportsMissingFileSchemaPolicyMapping(t *testing.T) {
	root := t.TempDir()
	must(t, InitRepo(root))
	writeFileForTest(t, root, "global/policies/file-schema-policy.yaml", `version: 1
schemas:
  cluster: global/schemas/cluster.schema.json
  project: global/schemas/project.schema.json
  migration_plan: global/schemas/migration-plan.schema.json
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "file schema policy missing mapping validation_plan: global/schemas/validation-plan.schema.json")
}

func TestValidateRepoReportsInvalidExportPlanContent(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml", `status: reviewed
tables:
  - source_object: sales.dbo.orders
    target_object: app.orders
    chunks:
      - id: dbo.orders.000001
        estimated_rows: 10
        predicate: id >= 1 and id < 11
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid export plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml: export chunk dbo.orders.000001 output_uri is required")
}

func TestValidateRepoReportsDuplicateExportChunkID(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml", `status: reviewed
tables:
  - source_object: sales.dbo.orders
    target_object: app.orders
    chunks:
      - id: dbo.orders.000001
        estimated_rows: 10
        predicate: id >= 1 and id < 11
        output_uri: file:///tmp/dbo.orders.000001.csv
      - id: dbo.orders.000001
        estimated_rows: 10
        predicate: id >= 11 and id < 21
        output_uri: file:///tmp/dbo.orders.000001-copy.csv
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid export plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml: duplicate export chunk id dbo.orders.000001")
}

func TestValidateRepoReportsTODOExportPredicate(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml", `status: reviewed
tables:
  - source_object: sales.dbo.orders
    target_object: app.orders
    chunks:
      - id: dbo.orders.000001
        estimated_rows: 10
        predicate: "TODO: choose split predicate"
        output_uri: file:///tmp/dbo.orders.000001.csv
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid export plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml: export chunk dbo.orders.000001 predicate still contains TODO")
}

func TestValidateRepoReportsUnsupportedExportFormat(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml", `status: reviewed
format: parquet
tables:
  - source_object: sales.dbo.orders
    target_object: app.orders
    chunks:
      - id: dbo.orders.000001
        estimated_rows: 10
        predicate: id >= 1 and id < 11
        output_uri: file:///tmp/dbo.orders.000001.parquet
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid export plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml: export format parquet is not supported by sqlserver2tidb-executor")
}

func TestValidateRepoReportsInvalidImportPlanContent(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml", `status: reviewed
jobs:
  - id: import-dbo.orders.000001
    target_object: app.orders
    depends_on_export_chunk: dbo.orders.000001
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid import plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml: import job import-dbo.orders.000001 source_uri is required")
}

func TestValidateRepoReportsUnsupportedImportEngine(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml", `status: reviewed
engine: import-into
jobs:
  - id: import-dbo.orders.000001
    target_object: app.orders
    source_uri: file:///tmp/dbo.orders.000001.csv
    depends_on_export_chunk: dbo.orders.000001
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid import plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml: import engine import-into is not supported by sqlserver2tidb-executor")
}

func TestValidateRepoReportsDuplicateImportJobID(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml", `status: reviewed
jobs:
  - id: import-dbo.orders.000001
    target_object: app.orders
    source_uri: file:///tmp/dbo.orders.000001.csv
    depends_on_export_chunk: dbo.orders.000001
  - id: import-dbo.orders.000001
    target_object: app.orders
    source_uri: file:///tmp/dbo.orders.000001-copy.csv
    depends_on_export_chunk: dbo.orders.000001-copy
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid import plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml: duplicate import job id import-dbo.orders.000001")
}

func TestValidateRepoReportsImportJobMissingExportDependency(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml", `status: reviewed
tables:
  - source_object: sales.dbo.orders
    target_object: app.orders
    chunks:
      - id: dbo.orders.000001
        estimated_rows: 10
        predicate: "1 = 1"
        output_uri: file:///tmp/dbo.orders.000001.csv
`)
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml", `status: reviewed
jobs:
  - id: import-dbo.orders.000002
    target_object: app.orders
    source_uri: file:///tmp/dbo.orders.000002.csv
    depends_on_export_chunk: dbo.orders.000002
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid import plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml: import job import-dbo.orders.000002 depends_on_export_chunk dbo.orders.000002 does not exist in export plan")
}

func TestValidateRepoReportsImportJobSourceURIMismatch(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml", `status: reviewed
tables:
  - source_object: sales.dbo.orders
    target_object: app.orders
    chunks:
      - id: dbo.orders.000001
        estimated_rows: 10
        predicate: "1 = 1"
        output_uri: file:///tmp/dbo.orders.000001.csv
`)
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml", `status: reviewed
jobs:
  - id: import-dbo.orders.000001
    target_object: app.orders
    source_uri: file:///tmp/dbo.orders.000001-copy.csv
    depends_on_export_chunk: dbo.orders.000001
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid import plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml: import job import-dbo.orders.000001 source_uri file:///tmp/dbo.orders.000001-copy.csv does not match export chunk dbo.orders.000001 output_uri file:///tmp/dbo.orders.000001.csv")
}

func TestValidateRepoReportsInvalidCDCPlanContent(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml", `status: reviewed
mode: sqlserver-cdc
tracked_tables:
  - source_object: sales.dbo.orders
    target_object: app.orders
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid cdc plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml: cdc tracked table sales.dbo.orders apply_batch_size must be positive")
}

func TestValidateRepoReportsDuplicateCDCTrackedTable(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml", `status: reviewed
mode: sqlserver-cdc
tracked_tables:
  - source_object: sales.dbo.orders
    target_object: app.orders
    apply_batch_size: 1000
  - source_object: sales.dbo.orders
    target_object: app.orders_copy
    apply_batch_size: 1000
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid cdc plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml: duplicate cdc tracked source_object sales.dbo.orders")
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

func TestValidateRepoReportsInvalidValidationPlanRowCountCheck(t *testing.T) {
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
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/validation-plan.yaml", `status: draft
checks:
  - id: orders-row-count
    type: row_count
    source_object: sales.dbo.orders
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid validation plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/validation-plan.yaml: row_count check orders-row-count target_object is required")
}

func TestValidateRepoReportsDuplicateValidationCheckID(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/validation-plan.yaml", `status: reviewed
checks:
  - id: orders-row-count
    type: row_count
    source_object: sales.dbo.orders
    target_object: app.orders
  - id: orders-row-count
    type: row_count
    source_object: sales.dbo.orders_archive
    target_object: app.orders_archive
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid validation plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/validation-plan.yaml: duplicate validation check id orders-row-count")
}

func TestValidateRepoReportsTODOValidationPlanPredicate(t *testing.T) {
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
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/validation-plan.yaml", `status: draft
checks:
  - id: orders-row-count
    type: row_count
    source_object: sales.dbo.orders
    target_object: app.orders
    target_predicate: "TODO: choose target predicate"
`)

	report, err := ValidateRepo(root)
	if err != nil {
		t.Fatalf("ValidateRepo() error = %v", err)
	}
	if report.Valid {
		t.Fatalf("ValidateRepo() valid = true, want false")
	}
	assertContains(t, strings.Join(report.Errors, "\n"), "invalid validation plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/validation-plan.yaml: row_count check orders-row-count target_predicate still contains TODO")
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

func TestGenerateExecutorEvidencePRDraftWritesDDLBody(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeStageApproval(t, root, "ddl", hash)
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json", `{
  "stage": "ddl",
  "status": "succeeded",
  "project_id": "sales-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-a",
  "payload_hash": "`+hash+`",
  "commands": [
    {
      "id": "schema/tidb-ddl/dbo.orders.sql",
      "args": ["sqlserver2tidb-executor", "apply-ddl", "--execute"],
      "shell_command": "sqlserver2tidb-executor apply-ddl --execute",
      "exit_code": 0,
      "output": "applied\n",
      "started_at": "2026-01-02T03:04:05Z",
      "completed_at": "2026-01-02T03:04:06Z",
      "duration_ms": 1000
    }
  ]
}
`)

	draft, err := GenerateExecutorEvidencePRDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("GenerateExecutorEvidencePRDraft() error = %v", err)
	}
	if draft.Title != "[executor-evidence:ddl] sales-db-to-tidb-prod-a" {
		t.Fatalf("Title = %q, want executor evidence title", draft.Title)
	}
	if draft.BranchName != "agent/sales-db-to-tidb-prod-a/executor-ddl-evidence" {
		t.Fatalf("BranchName = %q, want executor evidence branch", draft.BranchName)
	}
	if draft.BodyFile != "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/executor-ddl-evidence-pr.md" {
		t.Fatalf("BodyFile = %q, want executor evidence body", draft.BodyFile)
	}
	assertStringSliceContains(t, draft.Files, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json")
	assertStringSliceContains(t, draft.Files, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/ddl-approval.yaml")
	body := readFile(t, root, draft.BodyFile)
	assertContains(t, body, "Stage: `ddl`")
	assertContains(t, body, "Status: `succeeded`")
	assertContains(t, body, "Payload hash: `"+hash+"`")
	assertContains(t, body, "## Executor Commands")
	assertContains(t, body, "| schema/tidb-ddl/dbo.orders.sql | 0 | 2026-01-02T03:04:05Z | 2026-01-02T03:04:06Z | 1000 |")
	assertContains(t, body, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json")
	assertContains(t, body, "gh pr create --base main --head agent/sales-db-to-tidb-prod-a/executor-ddl-evidence")
}

func TestGenerateExecutorEvidencePRDraftRejectsEmptyCommands(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeStageApproval(t, root, "ddl", hash)
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json", `{
  "stage": "ddl",
  "status": "succeeded",
  "project_id": "sales-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-a",
  "payload_hash": "`+hash+`",
  "commands": []
}
`)

	_, err = GenerateExecutorEvidencePRDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err == nil {
		t.Fatal("GenerateExecutorEvidencePRDraft() expected empty commands error")
	}
	assertContains(t, err.Error(), "executor evidence commands must contain at least one command")
}

func TestGenerateExecutorEvidencePRDraftRejectsSucceededStatusWithFailedCommand(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeStageApproval(t, root, "ddl", hash)
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json", `{
  "stage": "ddl",
  "status": "succeeded",
  "project_id": "sales-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-a",
  "payload_hash": "`+hash+`",
  "commands": [
    {
      "id": "schema/tidb-ddl/dbo.orders.sql",
      "shell_command": "sqlserver2tidb-executor apply-ddl --execute",
      "exit_code": 17
    }
  ]
}
`)

	_, err = GenerateExecutorEvidencePRDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err == nil {
		t.Fatal("GenerateExecutorEvidencePRDraft() expected succeeded status conflict error")
	}
	assertContains(t, err.Error(), "executor evidence status succeeded conflicts with command schema/tidb-ddl/dbo.orders.sql exit_code 17")
}

func TestGenerateExecutorEvidencePRDraftRejectsUnknownExecutorStatus(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeStageApproval(t, root, "ddl", hash)
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json", `{
  "stage": "ddl",
  "status": "success",
  "project_id": "sales-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-a",
  "payload_hash": "`+hash+`",
  "commands": [
    {
      "id": "schema/tidb-ddl/dbo.orders.sql",
      "shell_command": "sqlserver2tidb-executor apply-ddl --execute",
      "exit_code": 0,
      "started_at": "2026-01-02T03:04:05Z",
      "completed_at": "2026-01-02T03:04:06Z",
      "duration_ms": 1000
    }
  ]
}
`)

	_, err = GenerateExecutorEvidencePRDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err == nil {
		t.Fatal("GenerateExecutorEvidencePRDraft() expected unsupported status error")
	}
	assertContains(t, err.Error(), `executor evidence status "success" is unsupported`)
}

func TestGenerateExecutorEvidencePRDraftRejectsFailedStatusWithoutFailedCommand(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeStageApproval(t, root, "ddl", hash)
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json", `{
  "stage": "ddl",
  "status": "failed",
  "project_id": "sales-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-a",
  "payload_hash": "`+hash+`",
  "commands": [
    {
      "id": "schema/tidb-ddl/dbo.orders.sql",
      "args": ["sqlserver2tidb-executor", "apply-ddl", "--execute"],
      "shell_command": "sqlserver2tidb-executor apply-ddl --execute",
      "exit_code": 0,
      "started_at": "2026-01-02T03:04:05Z",
      "completed_at": "2026-01-02T03:04:06Z",
      "duration_ms": 1000
    }
  ]
}
`)

	_, err = GenerateExecutorEvidencePRDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err == nil {
		t.Fatal("GenerateExecutorEvidencePRDraft() expected failed status conflict error")
	}
	assertContains(t, err.Error(), "executor evidence status failed requires at least one non-zero command exit_code")
}

func TestGenerateExecutorEvidencePRDraftRejectsCommandWithoutTiming(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeStageApproval(t, root, "ddl", hash)
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json", `{
  "stage": "ddl",
  "status": "succeeded",
  "project_id": "sales-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-a",
  "payload_hash": "`+hash+`",
  "commands": [
    {
      "id": "schema/tidb-ddl/dbo.orders.sql",
      "args": ["sqlserver2tidb-executor", "apply-ddl", "--execute"],
      "shell_command": "sqlserver2tidb-executor apply-ddl --execute",
      "exit_code": 0
    }
  ]
}
`)

	_, err = GenerateExecutorEvidencePRDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err == nil {
		t.Fatal("GenerateExecutorEvidencePRDraft() expected missing command timing error")
	}
	assertContains(t, err.Error(), "executor evidence command schema/tidb-ddl/dbo.orders.sql started_at is required")
}

func TestGenerateExecutorEvidencePRDraftRejectsCommandWithoutArgs(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeStageApproval(t, root, "ddl", hash)
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json", `{
  "stage": "ddl",
  "status": "succeeded",
  "project_id": "sales-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-a",
  "payload_hash": "`+hash+`",
  "commands": [
    {
      "id": "schema/tidb-ddl/dbo.orders.sql",
      "shell_command": "sqlserver2tidb-executor apply-ddl --execute",
      "exit_code": 0,
      "started_at": "2026-01-02T03:04:05Z",
      "completed_at": "2026-01-02T03:04:06Z",
      "duration_ms": 1000
    }
  ]
}
`)

	_, err = GenerateExecutorEvidencePRDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err == nil {
		t.Fatal("GenerateExecutorEvidencePRDraft() expected missing command args error")
	}
	assertContains(t, err.Error(), "executor evidence command schema/tidb-ddl/dbo.orders.sql args must contain at least one argument")
}

func TestGenerateExecutorEvidencePRDraftRejectsDuplicateCommandIDs(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeStageApproval(t, root, "ddl", hash)
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json", `{
  "stage": "ddl",
  "status": "succeeded",
  "project_id": "sales-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-a",
  "payload_hash": "`+hash+`",
  "commands": [
    {
      "id": "schema/tidb-ddl/dbo.orders.sql",
      "args": ["sqlserver2tidb-executor", "apply-ddl", "--execute"],
      "shell_command": "sqlserver2tidb-executor apply-ddl --execute",
      "exit_code": 0,
      "started_at": "2026-01-02T03:04:05Z",
      "completed_at": "2026-01-02T03:04:06Z",
      "duration_ms": 1000
    },
    {
      "id": "schema/tidb-ddl/dbo.orders.sql",
      "args": ["sqlserver2tidb-executor", "apply-ddl", "--execute"],
      "shell_command": "sqlserver2tidb-executor apply-ddl --execute",
      "exit_code": 0,
      "started_at": "2026-01-02T03:05:05Z",
      "completed_at": "2026-01-02T03:05:06Z",
      "duration_ms": 1000
    }
  ]
}
`)

	_, err = GenerateExecutorEvidencePRDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err == nil {
		t.Fatal("GenerateExecutorEvidencePRDraft() expected duplicate command id error")
	}
	assertContains(t, err.Error(), "executor evidence command id schema/tidb-ddl/dbo.orders.sql is duplicated")
}

func TestPrepareExecutorEvidencePRCreateBuildsGitAndGitHubCommands(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeStageApproval(t, root, "ddl", hash)
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json", `{
  "stage": "ddl",
  "status": "succeeded",
  "project_id": "sales-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-a",
  "payload_hash": "`+hash+`",
  "commands": [
    {
      "id": "schema/tidb-ddl/dbo.orders.sql",
      "args": ["sqlserver2tidb-executor", "apply-ddl", "--execute"],
      "shell_command": "sqlserver2tidb-executor apply-ddl --execute",
      "exit_code": 0,
      "output": "applied\n",
      "started_at": "2026-01-02T03:04:05Z",
      "completed_at": "2026-01-02T03:04:06Z",
      "duration_ms": 1000
    }
  ]
}
`)
	if _, err := GenerateExecutorEvidencePRDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl"); err != nil {
		t.Fatalf("GenerateExecutorEvidencePRDraft() error = %v", err)
	}

	spec, err := PrepareExecutorEvidencePRCreate(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("PrepareExecutorEvidencePRCreate() error = %v", err)
	}
	wantFiles := []string{
		"clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json",
		"clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/executor-ddl-evidence-pr.md",
	}
	if !reflect.DeepEqual(spec.Files, wantFiles) {
		t.Fatalf("Files = %#v, want %#v", spec.Files, wantFiles)
	}
	assertContains(t, spec.ShellCommands[0], "git switch -c agent/sales-db-to-tidb-prod-a/executor-ddl-evidence")
	assertContains(t, spec.ShellCommands[1], "git add clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json")
	assertContains(t, spec.ShellCommands[2], "git commit -m '[executor-evidence:ddl] sales-db-to-tidb-prod-a'")
	assertContains(t, spec.ShellCommands[3], "git push -u origin agent/sales-db-to-tidb-prod-a/executor-ddl-evidence")
	assertContains(t, spec.ShellCommands[4], "gh pr create --base main --head agent/sales-db-to-tidb-prod-a/executor-ddl-evidence")
}

func TestGenerateExecutorEvidencePRDraftRejectsStalePayloadHash(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeStageApproval(t, root, "ddl", hash)
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-ddl-run.json", `{
  "stage": "ddl",
  "status": "succeeded",
  "project_id": "sales-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-a",
  "payload_hash": "stale"
}
`)

	_, err = GenerateExecutorEvidencePRDraft(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err == nil {
		t.Fatal("GenerateExecutorEvidencePRDraft() expected stale payload error")
	}
	assertContains(t, err.Error(), "executor evidence payload hash stale does not match current approved payload hash")
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
		ObjectURIPrefix: "https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
		ChunkSizeRows:   1000000,
		ExportFormat:    "csv",
		ImportEngine:    "sql-insert",
	})
	if err != nil {
		t.Fatalf("GenerateDataMovementPlans() error = %v", err)
	}
	if result.Tables != 1 || result.ExportChunks != 3 || result.ImportJobs != 3 {
		t.Fatalf("GenerateDataMovementPlans() result = %+v, want 1 table, 3 chunks, 3 import jobs", result)
	}

	exportPlan := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml")
	assertContains(t, exportPlan, "status: draft")
	assertContains(t, exportPlan, "format: csv")
	assertContains(t, exportPlan, "chunk_size_rows: 1000000")
	assertContains(t, exportPlan, "source_object: sales.dbo.orders")
	assertContains(t, exportPlan, "target_object: app.orders")
	assertContains(t, exportPlan, "id: dbo.orders.000001")
	assertContains(t, exportPlan, "id: dbo.orders.000003")
	assertContains(t, exportPlan, "output_uri: https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000003.csv")
	if strings.Contains(exportPlan, "sales.audit.events") {
		t.Fatalf("export plan included table outside project schema:\n%s", exportPlan)
	}

	importPlan := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml")
	assertContains(t, importPlan, "status: draft")
	assertContains(t, importPlan, "engine: sql-insert")
	assertContains(t, importPlan, "target_object: app.orders")
	assertContains(t, importPlan, "source_uri: https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000001.csv")
	assertContains(t, importPlan, "depends_on_export_chunk: dbo.orders.000003")
}

func TestGenerateDataMovementPlansUsesTrivialPredicateForSingleChunkTable(t *testing.T) {
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
		ObjectURIPrefix: "https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
		ChunkSizeRows:   1000000,
		ExportFormat:    "csv",
		ImportEngine:    "sql-insert",
	})
	if err != nil {
		t.Fatalf("GenerateDataMovementPlans() error = %v", err)
	}
	if result.ExportChunks != 1 {
		t.Fatalf("ExportChunks = %d, want 1", result.ExportChunks)
	}

	exportPlan := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml")
	assertContains(t, exportPlan, `predicate: "1 = 1"`)
	if strings.Contains(exportPlan, "TODO") {
		t.Fatalf("single chunk export plan should not require a TODO predicate:\n%s", exportPlan)
	}

	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)
	workerResult, err := RunExportWorker(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err != nil {
		t.Fatalf("RunExportWorker() error = %v", err)
	}
	if workerResult.Items != 1 || workerResult.Status != "planned" {
		t.Fatalf("RunExportWorker() result = %+v, want one planned item", workerResult)
	}
}

func TestGenerateDataMovementPlansRequiresObjectURIPrefix(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"discovered","databases":[]}`)

	_, err := GenerateDataMovementPlans(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", DataMovementPlanSpec{
		ChunkSizeRows: 1000000,
		ExportFormat:  "csv",
		ImportEngine:  "sql-insert",
	})
	if err == nil {
		t.Fatal("GenerateDataMovementPlans() expected missing object URI prefix error")
	}
	assertContains(t, err.Error(), "object URI prefix is required")
}

func TestGenerateDataMovementPlansRejectsUnsupportedExportFormat(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"discovered","databases":[]}`)

	_, err := GenerateDataMovementPlans(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", DataMovementPlanSpec{
		ObjectURIPrefix: "https://object-store.example/migration/prod/full",
		ChunkSizeRows:   1000000,
		ExportFormat:    "parquet",
		ImportEngine:    "sql-insert",
	})
	if err == nil {
		t.Fatal("GenerateDataMovementPlans() expected unsupported export format error")
	}
	assertContains(t, err.Error(), "export format parquet is not supported by sqlserver2tidb-executor")
}

func TestGenerateDataMovementPlansRejectsUnsupportedImportEngine(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"discovered","databases":[]}`)

	_, err := GenerateDataMovementPlans(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", DataMovementPlanSpec{
		ObjectURIPrefix: "https://object-store.example/migration/prod/full",
		ChunkSizeRows:   1000000,
		ExportFormat:    "csv",
		ImportEngine:    "import-into",
	})
	if err == nil {
		t.Fatal("GenerateDataMovementPlans() expected unsupported import engine error")
	}
	assertContains(t, err.Error(), "import engine import-into is not supported by sqlserver2tidb-executor")
}

func TestGenerateDataMovementPlansRejectsUnsupportedObjectURIScheme(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, `{"status":"discovered","databases":[]}`)

	_, err := GenerateDataMovementPlans(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", DataMovementPlanSpec{
		ObjectURIPrefix: "s3://migration/prod/full",
		ChunkSizeRows:   1000000,
		ExportFormat:    "csv",
		ImportEngine:    "sql-insert",
	})
	if err == nil {
		t.Fatal("GenerateDataMovementPlans() expected unsupported object URI scheme error")
	}
	assertContains(t, err.Error(), "object URI prefix scheme s3 is not supported by sqlserver2tidb-executor")
}

func TestNormalizeDataMovementPlanSpecDefaultsToExecutableCSVPlan(t *testing.T) {
	spec := normalizeDataMovementPlanSpec(DataMovementPlanSpec{
		ObjectURIPrefix: "https://object-store.example/migration/prod/full",
		ChunkSizeRows:   1000000,
	})

	if spec.ExportFormat != "csv" {
		t.Fatalf("ExportFormat = %q, want csv", spec.ExportFormat)
	}
	if spec.ImportEngine != "sql-insert" {
		t.Fatalf("ImportEngine = %q, want sql-insert", spec.ImportEngine)
	}
}

func TestGenerateCDCPlanWritesProjectCDCPlan(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())

	result, err := GenerateCDCPlan(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", CDCPlanSpec{
		Mode:                   "sqlserver-cdc",
		RetentionHoursRequired: 168,
		ApplyBatchSize:         1000,
	})
	if err != nil {
		t.Fatalf("GenerateCDCPlan() error = %v", err)
	}
	if result.Mode != "sqlserver-cdc" || result.Tables != 1 {
		t.Fatalf("GenerateCDCPlan() result = %+v, want sqlserver-cdc with 1 table", result)
	}

	plan := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml")
	assertContains(t, plan, "status: draft")
	assertContains(t, plan, "project_id: sales-db-to-tidb-prod-a")
	assertContains(t, plan, "source_cluster_id: prod-sqlserver-a")
	assertContains(t, plan, "mode: sqlserver-cdc")
	assertContains(t, plan, "retention_hours_required: 168")
	assertContains(t, plan, "source_database: sales")
	assertContains(t, plan, "source_schemas:")
	assertContains(t, plan, "- dbo")
	assertContains(t, plan, "tracked_tables:")
	assertContains(t, plan, "source_object: sales.dbo.orders")
	assertContains(t, plan, "target_object: app.orders")
	assertContains(t, plan, "apply_batch_size: 1000")
	assertContains(t, plan, "checkpoint_scope: source-cluster")
	assertContains(t, plan, "checkpoint_file: ../../../state/cdc-checkpoint.yaml")
}

func TestGenerateValidationPlanWritesRowCountChecks(t *testing.T) {
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
                {"name": "id", "type": "int"}
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

	result, err := GenerateValidationPlan(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err != nil {
		t.Fatalf("GenerateValidationPlan() error = %v", err)
	}
	if result.Checks != 1 {
		t.Fatalf("GenerateValidationPlan() result = %+v, want 1 check", result)
	}

	plan := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/validation-plan.yaml")
	assertContains(t, plan, "status: draft")
	assertContains(t, plan, "project_id: sales-db-to-tidb-prod-a")
	assertContains(t, plan, "source_cluster_id: prod-sqlserver-a")
	assertContains(t, plan, "checks:")
	assertContains(t, plan, "id: dbo.orders.row-count")
	assertContains(t, plan, "type: row_count")
	assertContains(t, plan, "source_object: sales.dbo.orders")
	assertContains(t, plan, "target_object: app.orders")
	if strings.Contains(plan, "sales.audit.events") {
		t.Fatalf("validation plan included table outside project schema:\n%s", plan)
	}
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
	reviewExportPlanPredicates(t, root)
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
	assertContains(t, state, "output_uri: https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000003.csv")

	evidence := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/precheck.json")
	assertContains(t, evidence, `"stage": "export"`)
	assertContains(t, evidence, `"status": "planned"`)
	assertContains(t, evidence, `"items": 3`)
	assertContains(t, evidence, `"payload_hash": "`+hash+`"`)
}

func TestRunExportWorkerRejectsTODOExportPredicate(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)

	_, err = RunExportWorker(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err == nil {
		t.Fatal("RunExportWorker() expected TODO predicate error")
	}
	assertContains(t, err.Error(), "export chunk dbo.orders.000001 predicate still contains TODO")
}

func TestPrepareWorkerExecutorBuildsExportCommandsWhenApprovedHashMatches(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))
	reviewExportPlanPredicates(t, root)
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)

	spec, err := PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export", WorkerExecutorPrepareSpec{})
	if err != nil {
		t.Fatalf("PrepareWorkerExecutor() error = %v", err)
	}
	if spec.Stage != "export" || spec.PayloadHash != hash {
		t.Fatalf("executor spec = %+v, want export with hash %s", spec, hash)
	}
	if len(spec.Commands) != 3 {
		t.Fatalf("Commands = %d, want 3", len(spec.Commands))
	}
	first := spec.Commands[0]
	if first.ID != "dbo.orders.000001" {
		t.Fatalf("first command ID = %q, want first export chunk", first.ID)
	}
	assertContains(t, first.ShellCommand, "sqlserver2tidb-executor export")
	assertContains(t, first.ShellCommand, "--root .")
	assertContains(t, first.ShellCommand, "--source-cluster-id prod-sqlserver-a")
	assertContains(t, first.ShellCommand, "--project-id sales-db-to-tidb-prod-a")
	assertContains(t, first.ShellCommand, "--chunk-id dbo.orders.000001")
	assertContains(t, first.ShellCommand, "--source-object sales.dbo.orders")
	assertContains(t, first.ShellCommand, "--target-object app.orders")
	assertContains(t, first.ShellCommand, "--output-uri https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000001.csv")
}

func TestPrepareWorkerExecutorRejectsUnsupportedExportFormat(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))
	exportPlanRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml"
	exportPlan := readFile(t, root, exportPlanRel)
	exportPlan = strings.Replace(exportPlan, "format: csv", "format: parquet", 1)
	writeFileForTest(t, root, exportPlanRel, exportPlan)
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)

	_, err = PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export", WorkerExecutorPrepareSpec{})
	if err == nil {
		t.Fatal("PrepareWorkerExecutor() expected unsupported export format error")
	}
	assertContains(t, err.Error(), "export format parquet is not supported by sqlserver2tidb-executor")
}

func TestPrepareWorkerExecutorRejectsTODOExportPredicate(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)

	_, err = PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export", WorkerExecutorPrepareSpec{})
	if err == nil {
		t.Fatal("PrepareWorkerExecutor() expected TODO predicate error")
	}
	assertContains(t, err.Error(), "export chunk dbo.orders.000001 predicate still contains TODO")
}

func TestPrepareWorkerExecutorAddsExportConnectionStringEnv(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))
	reviewExportPlanPredicates(t, root)
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)

	spec, err := PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export", WorkerExecutorPrepareSpec{
		SourceConnectionStringEnv: "SQLSERVER_READONLY_DSN",
	})
	if err != nil {
		t.Fatalf("PrepareWorkerExecutor() error = %v", err)
	}
	if len(spec.Commands) == 0 {
		t.Fatal("Commands is empty")
	}
	assertContains(t, spec.Commands[0].ShellCommand, "--source-connection-string-env SQLSERVER_READONLY_DSN")
}

func TestPrepareWorkerExecutorRequiresApprovedStage(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))

	_, err := PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export", WorkerExecutorPrepareSpec{})
	if err == nil {
		t.Fatal("PrepareWorkerExecutor() expected approval error")
	}
	assertContains(t, err.Error(), "export approval is not approved")
}

func TestPrepareWorkerExecutorBuildsImportCommandsWhenApprovedHashMatches(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	must(t, GenerateDataPlansOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "import")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(import) error = %v", err)
	}
	writeStageApproval(t, root, "import", hash)

	spec, err := PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "import", WorkerExecutorPrepareSpec{})
	if err != nil {
		t.Fatalf("PrepareWorkerExecutor() error = %v", err)
	}
	if spec.Stage != "import" || spec.PayloadHash != hash || len(spec.Commands) != 3 {
		t.Fatalf("executor spec = %+v, want import with 3 commands and hash %s", spec, hash)
	}
	first := spec.Commands[0]
	if first.ID != "import-dbo.orders.000001" {
		t.Fatalf("first command ID = %q, want first import job", first.ID)
	}
	assertContains(t, first.ShellCommand, "sqlserver2tidb-executor import")
	assertContains(t, first.ShellCommand, "--job-id import-dbo.orders.000001")
	assertContains(t, first.ShellCommand, "--target-object app.orders")
	assertContains(t, first.ShellCommand, "--source-uri https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000001.csv")
	assertContains(t, first.ShellCommand, "--depends-on-export-chunk dbo.orders.000001")
}

func TestPrepareWorkerExecutorRejectsUnsupportedImportEngine(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	must(t, GenerateDataPlansOnly(root))
	importPlanRel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml"
	importPlan := readFile(t, root, importPlanRel)
	importPlan = strings.Replace(importPlan, "engine: sql-insert", "engine: import-into", 1)
	writeFileForTest(t, root, importPlanRel, importPlan)
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "import")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(import) error = %v", err)
	}
	writeStageApproval(t, root, "import", hash)

	_, err = PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "import", WorkerExecutorPrepareSpec{})
	if err == nil {
		t.Fatal("PrepareWorkerExecutor() expected unsupported import engine error")
	}
	assertContains(t, err.Error(), "import engine import-into is not supported by sqlserver2tidb-executor")
}

func TestPrepareWorkerExecutorAddsImportConnectionStringEnvAndBatchSize(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	must(t, GenerateDataPlansOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "import")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(import) error = %v", err)
	}
	writeStageApproval(t, root, "import", hash)

	spec, err := PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "import", WorkerExecutorPrepareSpec{
		TargetConnectionStringEnv: "TIDB_IMPORT_DSN",
		ImportBatchSize:           500,
	})
	if err != nil {
		t.Fatalf("PrepareWorkerExecutor() error = %v", err)
	}
	if len(spec.Commands) == 0 {
		t.Fatal("Commands is empty")
	}
	assertContains(t, spec.Commands[0].ShellCommand, "--target-connection-string-env TIDB_IMPORT_DSN")
	assertContains(t, spec.Commands[0].ShellCommand, "--import-batch-size 500")
}

func TestPrepareWorkerExecutorRejectsNegativeImportBatchSize(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	must(t, GenerateDataPlansOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "import")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(import) error = %v", err)
	}
	writeStageApproval(t, root, "import", hash)

	_, err = PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "import", WorkerExecutorPrepareSpec{
		ImportBatchSize: -1,
	})
	if err == nil {
		t.Fatal("PrepareWorkerExecutor() expected negative import batch size error")
	}
	assertContains(t, err.Error(), "import batch size must be non-negative")
}

func TestPrepareWorkerExecutorBuildsDDLCommand(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeStageApproval(t, root, "ddl", hash)

	spec, err := PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl", WorkerExecutorPrepareSpec{
		TargetConnectionStringEnv: "TIDB_DSN",
	})
	if err != nil {
		t.Fatalf("PrepareWorkerExecutor(ddl) error = %v", err)
	}
	if spec.Stage != "ddl" || spec.PayloadHash != hash || len(spec.Commands) != 1 {
		t.Fatalf("executor spec = %+v, want ddl with 1 command and hash %s", spec, hash)
	}
	command := spec.Commands[0]
	if command.ID != "schema/tidb-ddl/dbo.orders.sql" {
		t.Fatalf("command ID = %q, want DDL file path", command.ID)
	}
	assertContains(t, command.ShellCommand, "sqlserver2tidb-executor apply-ddl")
	assertContains(t, command.ShellCommand, "--ddl-file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/tidb-ddl/dbo.orders.sql")
	assertContains(t, command.ShellCommand, "--target-connection-string-env TIDB_DSN")
}

func TestPrepareWorkerExecutorRejectsEmptyExportPlan(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)

	_, err = PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export", WorkerExecutorPrepareSpec{})
	if err == nil {
		t.Fatal("PrepareWorkerExecutor() expected empty export plan error")
	}
	assertContains(t, err.Error(), "export plan contains no chunks")
}

func TestPrepareWorkerExecutorRejectsEmptyImportPlan(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "import")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(import) error = %v", err)
	}
	writeStageApproval(t, root, "import", hash)

	_, err = PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "import", WorkerExecutorPrepareSpec{})
	if err == nil {
		t.Fatal("PrepareWorkerExecutor() expected empty import plan error")
	}
	assertContains(t, err.Error(), "import plan contains no jobs")
}

func TestPrepareWorkerExecutorRejectsEmptyCDCPlan(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "cdc")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(cdc) error = %v", err)
	}
	writeStageApproval(t, root, "cdc", hash)

	_, err = PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "cdc", WorkerExecutorPrepareSpec{})
	if err == nil {
		t.Fatal("PrepareWorkerExecutor() expected empty cdc plan error")
	}
	assertContains(t, err.Error(), "cdc plan contains no tracked tables")
}

func TestPrepareWorkerExecutorBuildsCDCCommandsWhenApprovedHashMatches(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateCDCPlanOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "cdc")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(cdc) error = %v", err)
	}
	writeStageApproval(t, root, "cdc", hash)

	spec, err := PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "cdc", WorkerExecutorPrepareSpec{})
	if err != nil {
		t.Fatalf("PrepareWorkerExecutor() error = %v", err)
	}
	if spec.Stage != "cdc" || spec.PayloadHash != hash || len(spec.Commands) != 1 {
		t.Fatalf("executor spec = %+v, want cdc with 1 command and hash %s", spec, hash)
	}
	first := spec.Commands[0]
	if first.ID != "sales.dbo.orders" {
		t.Fatalf("first command ID = %q, want CDC source object", first.ID)
	}
	assertContains(t, first.ShellCommand, "sqlserver2tidb-executor cdc")
	assertContains(t, first.ShellCommand, "--source-object sales.dbo.orders")
	assertContains(t, first.ShellCommand, "--target-object app.orders")
	assertContains(t, first.ShellCommand, "--apply-batch-size 1000")
}

func TestPrepareWorkerExecutorBuildsValidationCountCommandsWhenApprovedHashMatches(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/validation-plan.yaml", `status: draft
checks:
  - id: orders-row-count
    type: row_count
    source_object: sales.dbo.orders
    target_object: app.orders
    predicate: "id >= 1"
    target_predicate: "id >= 1"
`)
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "validation")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(validation) error = %v", err)
	}
	writeStageApproval(t, root, "validation", hash)

	spec, err := PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "validation", WorkerExecutorPrepareSpec{
		SourceConnectionStringEnv: "SQLSERVER_VALIDATE_DSN",
		TargetConnectionStringEnv: "TIDB_VALIDATE_DSN",
	})
	if err != nil {
		t.Fatalf("PrepareWorkerExecutor() error = %v", err)
	}
	if spec.Stage != "validation" || spec.PayloadHash != hash || len(spec.Commands) != 1 {
		t.Fatalf("executor spec = %+v, want validation with 1 command and hash %s", spec, hash)
	}
	first := spec.Commands[0]
	if first.ID != "orders-row-count" {
		t.Fatalf("first command ID = %q, want validation check id", first.ID)
	}
	assertContains(t, first.ShellCommand, "sqlserver2tidb-executor validate-count")
	assertContains(t, first.ShellCommand, "--source-object sales.dbo.orders")
	assertContains(t, first.ShellCommand, "--target-object app.orders")
	assertContains(t, first.ShellCommand, "--predicate 'id >= 1'")
	assertContains(t, first.ShellCommand, "--target-predicate 'id >= 1'")
	assertContains(t, first.ShellCommand, "--source-connection-string-env SQLSERVER_VALIDATE_DSN")
	assertContains(t, first.ShellCommand, "--target-connection-string-env TIDB_VALIDATE_DSN")
}

func TestPrepareWorkerExecutorValidationRequiresSupportedRowCountChecks(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/validation-plan.yaml", `status: draft
checks: []
`)
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "validation")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(validation) error = %v", err)
	}
	writeStageApproval(t, root, "validation", hash)

	_, err = PrepareWorkerExecutor(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "validation", WorkerExecutorPrepareSpec{})
	if err == nil {
		t.Fatal("PrepareWorkerExecutor() expected no validation command error")
	}
	assertContains(t, err.Error(), "validation plan contains no supported row_count checks")
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

func TestRunImportWorkerRejectsEmptyImportPlan(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "import")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(import) error = %v", err)
	}
	writeStageApproval(t, root, "import", hash)

	_, err = RunImportWorker(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err == nil {
		t.Fatal("RunImportWorker() expected empty import plan error")
	}
	assertContains(t, err.Error(), "import plan contains no jobs")
}

func TestRunCDCWorkerRequiresApprovedCDCApproval(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateCDCPlanOnly(root))
	projectStateBefore := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/migration-state.yaml")
	clusterStateBefore := readFile(t, root, "clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml")
	evidenceBefore := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/cdc-catchup.json")

	_, err := RunCDCWorker(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err == nil {
		t.Fatal("RunCDCWorker() expected approval error")
	}
	assertContains(t, err.Error(), "cdc approval is not approved")
	projectStateAfter := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/migration-state.yaml")
	clusterStateAfter := readFile(t, root, "clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml")
	evidenceAfter := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/cdc-catchup.json")
	if projectStateAfter != projectStateBefore {
		t.Fatalf("cdc worker changed project state before approval\nbefore:\n%s\nafter:\n%s", projectStateBefore, projectStateAfter)
	}
	if clusterStateAfter != clusterStateBefore {
		t.Fatalf("cdc worker changed cluster checkpoint before approval\nbefore:\n%s\nafter:\n%s", clusterStateBefore, clusterStateAfter)
	}
	if evidenceAfter != evidenceBefore {
		t.Fatalf("cdc worker changed evidence before approval\nbefore:\n%s\nafter:\n%s", evidenceBefore, evidenceAfter)
	}
}

func TestRunCDCWorkerWritesPlannedStateWhenApprovedHashMatches(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateCDCPlanOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "cdc")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(cdc) error = %v", err)
	}
	writeStageApproval(t, root, "cdc", hash)

	result, err := RunCDCWorker(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err != nil {
		t.Fatalf("RunCDCWorker() error = %v", err)
	}
	if result.Stage != "cdc" || result.Status != "planned" || result.Items != 1 {
		t.Fatalf("RunCDCWorker() result = %+v, want cdc planned with 1 item", result)
	}
	if result.PayloadHash != hash {
		t.Fatalf("PayloadHash = %q, want %q", result.PayloadHash, hash)
	}

	projectState := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/migration-state.yaml")
	assertContains(t, projectState, "phase: cdc")
	assertContains(t, projectState, "status: planned")
	assertContains(t, projectState, "payload_hash: "+hash)
	assertContains(t, projectState, "cdc_plan: plan/cdc-plan.yaml")
	assertContains(t, projectState, "tracked_tables: 1")

	clusterState := readFile(t, root, "clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml")
	assertContains(t, clusterState, "phase: cdc")
	assertContains(t, clusterState, "status: planned")
	assertContains(t, clusterState, "project_id: sales-db-to-tidb-prod-a")
	assertContains(t, clusterState, "payload_hash: "+hash)
	assertContains(t, clusterState, "mode: sqlserver-cdc")

	evidence := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/cdc-catchup.json")
	assertContains(t, evidence, `"stage": "cdc"`)
	assertContains(t, evidence, `"status": "planned"`)
	assertContains(t, evidence, `"items": 1`)
	assertContains(t, evidence, `"payload_hash": "`+hash+`"`)
}

func TestRunCDCWorkerRejectsEmptyCDCPlan(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "cdc")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(cdc) error = %v", err)
	}
	writeStageApproval(t, root, "cdc", hash)

	_, err = RunCDCWorker(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	if err == nil {
		t.Fatal("RunCDCWorker() expected empty cdc plan error")
	}
	assertContains(t, err.Error(), "cdc plan contains no tracked tables")
}

func TestPlanWorkerReconcileReportsReadyAndBlockedProjectStages(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	must(t, GenerateDataPlansOnly(root))
	reviewExportPlanPredicates(t, root)
	must(t, GenerateCDCPlanOnly(root))
	exportHash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", exportHash)
	ddlHash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeStageApproval(t, root, "ddl", ddlHash)
	cdcHash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "cdc")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(cdc) error = %v", err)
	}
	writeStageApproval(t, root, "cdc", cdcHash)

	report, err := PlanWorkerReconcile(root)
	if err != nil {
		t.Fatalf("PlanWorkerReconcile() error = %v", err)
	}
	if report.Projects != 1 {
		t.Fatalf("Projects = %d, want 1", report.Projects)
	}
	if report.ReadyActions != 3 || report.BlockedActions != 2 {
		t.Fatalf("ready/blocked = %d/%d, want 3/2\nreport: %+v", report.ReadyActions, report.BlockedActions, report)
	}
	ddl := findReconcileAction(t, report.Actions, "ddl")
	if ddl.Status != "ready" || ddl.PayloadHash != ddlHash {
		t.Fatalf("ddl action = %+v, want ready with hash %s", ddl, ddlHash)
	}
	assertContains(t, ddl.Command, "worker-executor")
	assertContains(t, ddl.Command, "--stage ddl")
	export := findReconcileAction(t, report.Actions, "export")
	if export.Status != "ready" || export.PayloadHash != exportHash {
		t.Fatalf("export action = %+v, want ready with hash %s", export, exportHash)
	}
	assertContains(t, export.Command, "worker-export")
	cdc := findReconcileAction(t, report.Actions, "cdc")
	if cdc.Status != "ready" || cdc.PayloadHash != cdcHash {
		t.Fatalf("cdc action = %+v, want ready with hash %s", cdc, cdcHash)
	}
	assertContains(t, cdc.Command, "worker-cdc")
	importAction := findReconcileAction(t, report.Actions, "import")
	if importAction.Status != "blocked" {
		t.Fatalf("import action = %+v, want blocked", importAction)
	}
	assertContains(t, importAction.Reason, "import approval is not approved")
	validation := findReconcileAction(t, report.Actions, "validation")
	if validation.Status != "blocked" {
		t.Fatalf("validation action = %+v, want blocked", validation)
	}
	assertContains(t, validation.Reason, "validation approval is not approved")
}

func TestExecuteNextWorkerReconcileSkipsDDLExecutorActions(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateSchemaDraftOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeStageApproval(t, root, "ddl", hash)

	_, err = ExecuteNextWorkerReconcile(root, WorkerReconcileExecuteSpec{
		Holder: "agent-a",
	})
	if err == nil {
		t.Fatal("ExecuteNextWorkerReconcile() expected no metadata action error")
	}
	assertContains(t, err.Error(), "no ready metadata worker actions")
	assertContains(t, err.Error(), "worker-executor")
}

func TestExecuteNextWorkerReconcileAcquiresLeaseAndRunsFirstReadyAction(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))
	reviewExportPlanPredicates(t, root)
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)

	result, err := ExecuteNextWorkerReconcile(root, WorkerReconcileExecuteSpec{
		Holder: "agent-a",
	})
	if err != nil {
		t.Fatalf("ExecuteNextWorkerReconcile() error = %v", err)
	}
	if result.Action.Stage != "export" || result.Action.Status != "ready" {
		t.Fatalf("action = %+v, want ready export", result.Action)
	}
	if result.Status != "planned" || result.StateFile != "state/export-chunks.yaml" || result.EvidenceFile != "evidence/precheck.json" {
		t.Fatalf("result = %+v, want planned export state/evidence", result)
	}

	lease := readFile(t, root, "clusters/prod-sqlserver-a/state/worker-lease.yaml")
	assertContains(t, lease, "source_cluster_id: prod-sqlserver-a")
	assertContains(t, lease, "holder: agent-a")
	assertContains(t, lease, "phase: export")
	assertContains(t, lease, "project_id: sales-db-to-tidb-prod-a")
	assertContains(t, lease, "lease_id: ")
	assertContains(t, lease, "expires_at: ")

	state := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml")
	assertContains(t, state, "phase: export")
	assertContains(t, state, "status: planned")
	assertContains(t, state, "payload_hash: "+hash)
	evidence := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/precheck.json")
	assertContains(t, evidence, `"stage": "export"`)
	assertContains(t, evidence, `"status": "planned"`)
}

func TestExecuteNextWorkerReconcileWritesStatePRDraftWhenRequested(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))
	reviewExportPlanPredicates(t, root)
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)

	result, err := ExecuteNextWorkerReconcile(root, WorkerReconcileExecuteSpec{
		Holder:        "agent-a",
		CreatePRDraft: true,
	})
	if err != nil {
		t.Fatalf("ExecuteNextWorkerReconcile() error = %v", err)
	}
	if result.PRBodyFile != "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/reconcile-export-state-pr.md" {
		t.Fatalf("PRBodyFile = %q, want reconcile export state PR draft", result.PRBodyFile)
	}
	if result.BranchName != "agent/sales-db-to-tidb-prod-a/reconcile-export-state" {
		t.Fatalf("BranchName = %q, want reconcile export state branch", result.BranchName)
	}
	if result.PRTitle != "[worker-state:export] sales-db-to-tidb-prod-a" {
		t.Fatalf("PRTitle = %q, want worker-state export title", result.PRTitle)
	}

	body := readFile(t, root, result.PRBodyFile)
	assertContains(t, body, "# PR Draft: [worker-state:export] sales-db-to-tidb-prod-a")
	assertContains(t, body, "Source cluster: `prod-sqlserver-a`")
	assertContains(t, body, "Project: `sales-db-to-tidb-prod-a`")
	assertContains(t, body, "Stage: `export`")
	assertContains(t, body, "Status: `planned`")
	assertContains(t, body, "Payload hash: `"+hash+"`")
	assertContains(t, body, "Lease ID: `")
	assertContains(t, body, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml")
	assertContains(t, body, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/precheck.json")
	assertContains(t, body, "clusters/prod-sqlserver-a/state/worker-lease.yaml")
	assertContains(t, body, "gh pr create --base main --head agent/sales-db-to-tidb-prod-a/reconcile-export-state")
}

func TestPrepareWorkerStatePRCreateBuildsGitAndGitHubCommands(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))
	reviewExportPlanPredicates(t, root)
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)
	must(t, func() error {
		_, err := ExecuteNextWorkerReconcile(root, WorkerReconcileExecuteSpec{
			Holder:        "agent-a",
			CreatePRDraft: true,
		})
		return err
	}())

	spec, err := PrepareWorkerStatePRCreate(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("PrepareWorkerStatePRCreate() error = %v", err)
	}
	if spec.Title != "[worker-state:export] sales-db-to-tidb-prod-a" {
		t.Fatalf("Title = %q, want worker state title", spec.Title)
	}
	if spec.BranchName != "agent/sales-db-to-tidb-prod-a/reconcile-export-state" {
		t.Fatalf("BranchName = %q, want worker state branch", spec.BranchName)
	}
	if spec.BodyFile != "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/reconcile-export-state-pr.md" {
		t.Fatalf("BodyFile = %q, want worker state PR body", spec.BodyFile)
	}
	wantFiles := []string{
		"clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml",
		"clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/precheck.json",
		"clusters/prod-sqlserver-a/state/worker-lease.yaml",
		"clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/reconcile-export-state-pr.md",
	}
	if !reflect.DeepEqual(spec.Files, wantFiles) {
		t.Fatalf("Files = %#v, want %#v", spec.Files, wantFiles)
	}
	assertContains(t, spec.ShellCommands[0], "git switch -c agent/sales-db-to-tidb-prod-a/reconcile-export-state")
	assertContains(t, spec.ShellCommands[1], "git add clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml")
	assertContains(t, spec.ShellCommands[1], "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/reconcile-export-state-pr.md")
	assertContains(t, spec.ShellCommands[2], "git commit -m '[worker-state:export] sales-db-to-tidb-prod-a'")
	assertContains(t, spec.ShellCommands[3], "git push -u origin agent/sales-db-to-tidb-prod-a/reconcile-export-state")
	assertContains(t, spec.ShellCommands[4], "gh pr create --base main --head agent/sales-db-to-tidb-prod-a/reconcile-export-state")
}

func TestPrepareWorkerStatePRCreateIncludesExecutorEvidenceWhenPresent(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))
	reviewExportPlanPredicates(t, root)
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)
	must(t, func() error {
		_, err := ExecuteNextWorkerReconcile(root, WorkerReconcileExecuteSpec{
			Holder:        "agent-a",
			CreatePRDraft: true,
		})
		return err
	}())
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json", `{
  "stage": "export",
  "status": "succeeded"
}
`)

	spec, err := PrepareWorkerStatePRCreate(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("PrepareWorkerStatePRCreate() error = %v", err)
	}
	assertStringSliceContains(t, spec.Files, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json")
	assertContains(t, spec.ShellCommands[1], "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json")
}

func TestPrepareWorkerStatePRCreatePlansBodyRefreshForExecutorEvidence(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))
	reviewExportPlanPredicates(t, root)
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)
	must(t, func() error {
		_, err := ExecuteNextWorkerReconcile(root, WorkerReconcileExecuteSpec{
			Holder:        "agent-a",
			CreatePRDraft: true,
		})
		return err
	}())
	bodyFile := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/reconcile-export-state-pr.md"
	bodyBefore := readFile(t, root, bodyFile)
	executorEvidence := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json"
	if strings.Contains(bodyBefore, executorEvidence) {
		t.Fatalf("state PR body unexpectedly contains executor evidence before it exists")
	}
	writeFileForTest(t, root, executorEvidence, `{
  "stage": "export",
  "status": "succeeded"
}
`)

	spec, err := PrepareWorkerStatePRCreate(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("PrepareWorkerStatePRCreate() error = %v", err)
	}
	if !spec.BodyFileNeedsUpdate {
		t.Fatal("BodyFileNeedsUpdate = false, want true")
	}
	assertContains(t, spec.BodyFileContent, executorEvidence)
	if got := readFile(t, root, bodyFile); got != bodyBefore {
		t.Fatal("PrepareWorkerStatePRCreate mutated the PR body; dry-run preparation must be read-only")
	}
	must(t, RefreshWorkerStatePRBody(root, spec))
	assertContains(t, readFile(t, root, bodyFile), executorEvidence)
}

func TestPrepareWorkerStatePRCreateRequiresGeneratedStateDraft(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())

	_, err := PrepareWorkerStatePRCreate(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err == nil {
		t.Fatal("PrepareWorkerStatePRCreate() expected missing draft error")
	}
	assertContains(t, err.Error(), "worker state PR draft clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/reconcile-export-state-pr.md does not exist")
	assertContains(t, err.Error(), "run worker-reconcile --execute-next --state-pr-draft first")
}

func TestPrepareWorkerStatePRCreateRejectsDDLExecutorStage(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())

	_, err := PrepareWorkerStatePRCreate(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err == nil {
		t.Fatal("PrepareWorkerStatePRCreate() expected unsupported ddl stage error")
	}
	assertContains(t, err.Error(), `unsupported worker state PR stage "ddl"`)
	assertContains(t, err.Error(), "only generated for export, import, cdc, and validation")
}

func TestExecuteNextWorkerReconcileBlocksWhenLeaseHeldByAnotherHolder(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateDataPlansOnly(root))
	hash, err := ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "export")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(export) error = %v", err)
	}
	writeStageApproval(t, root, "export", hash)
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/state/worker-lease.yaml", `source_cluster_id: prod-sqlserver-a
holder: other-agent
lease_id: existing-lease
phase: export
project_id: sales-db-to-tidb-prod-a
expires_at: "2999-01-01T00:00:00Z"
renewed_at: "2026-06-26T00:00:00Z"
`)
	stateBefore := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml")
	evidenceBefore := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/precheck.json")

	_, err = ExecuteNextWorkerReconcile(root, WorkerReconcileExecuteSpec{
		Holder: "agent-a",
	})
	if err == nil {
		t.Fatal("ExecuteNextWorkerReconcile() expected lease error")
	}
	assertContains(t, err.Error(), `worker lease for source cluster "prod-sqlserver-a" is held by "other-agent"`)
	stateAfter := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml")
	evidenceAfter := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/precheck.json")
	if stateAfter != stateBefore {
		t.Fatalf("execute-next changed export state while lease was held\nbefore:\n%s\nafter:\n%s", stateBefore, stateAfter)
	}
	if evidenceAfter != evidenceBefore {
		t.Fatalf("execute-next changed export evidence while lease was held\nbefore:\n%s\nafter:\n%s", evidenceBefore, evidenceAfter)
	}
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

func TestRunValidationWorkerFailsInvalidValidationPlanRowCountCheck(t *testing.T) {
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
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/validation-plan.yaml", `status: draft
checks:
  - id: orders-row-count
    type: row_count
    source_object: sales.dbo.orders
`)
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
	assertContains(t, state, "name: validation_plan_row_count_checks_valid")
	assertContains(t, state, "row_count check orders-row-count target_object is required")

	report := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/validation-report.md")
	assertContains(t, report, "- Status: `failed`")
	assertContains(t, report, "validation_plan_row_count_checks_valid")
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

func findReconcileAction(t *testing.T, actions []WorkerReconcileAction, stage string) WorkerReconcileAction {
	t.Helper()
	for _, action := range actions {
		if action.SourceClusterID == "prod-sqlserver-a" && action.ProjectID == "sales-db-to-tidb-prod-a" && action.Stage == stage {
			return action
		}
	}
	t.Fatalf("expected reconcile action for stage %q\n%+v", stage, actions)
	return WorkerReconcileAction{}
}

func writeFileForTest(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func replaceTopLevelLineForTest(t *testing.T, content, key, replacement string) string {
	t.Helper()
	prefix := key + ":"
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = replacement
			return strings.Join(lines, "\n")
		}
	}
	t.Fatalf("top-level key %q not found in content:\n%s", key, content)
	return content
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
		ObjectURIPrefix: "https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
		ChunkSizeRows:   1000000,
		ExportFormat:    "csv",
		ImportEngine:    "sql-insert",
	})
	return err
}

func reviewExportPlanPredicates(t *testing.T, root string) {
	t.Helper()
	rel := "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml"
	plan := readFile(t, root, rel)
	var reviewed strings.Builder
	for _, line := range strings.Split(plan, "\n") {
		if strings.Contains(line, "predicate: \"TODO: choose stable split predicate") {
			prefix := line[:strings.Index(line, "predicate:")]
			line = prefix + "predicate: id >= 0"
		}
		reviewed.WriteString(line)
		reviewed.WriteByte('\n')
	}
	writeFileForTest(t, root, rel, reviewed.String())
}

func GenerateCDCPlanOnly(root string) error {
	_, err := GenerateCDCPlan(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", CDCPlanSpec{
		Mode:                   "sqlserver-cdc",
		RetentionHoursRequired: 168,
		ApplyBatchSize:         1000,
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
