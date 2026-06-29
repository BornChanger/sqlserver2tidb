package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BornChanger/sqlserver2tidb/internal/gitops"
)

func TestRunVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("version code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "sqlserver2tidb version dev") {
		t.Fatalf("version output = %q, want version", output)
	}
	if !strings.Contains(output, "commit: unknown") {
		t.Fatalf("version output = %q, want commit", output)
	}
}

func TestRunInitRepoCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run() code = %d, stderr = %s", code, stderr.String())
	}

	assertExists(t, root, "global/policies/approval-policy.yaml")
	assertExists(t, root, "clusters")
}

func TestRunCreateClusterAndProjectCommands(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--port", "1433",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team,sre-team",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}

	code = Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team,app-team",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}

	assertExists(t, root, "clusters/prod-sqlserver-a/cluster.yaml")
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/project.yaml")
}

func TestRunValidateRepoCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"validate-repo", "--root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("validate-repo code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "repository is valid") {
		t.Fatalf("validate-repo stdout = %q, want valid message", stdout.String())
	}
}

func TestRunValidateRepoCommandReportsInvalidRepository(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if err := os.Remove(filepath.Join(root, "global", "policies", "execution-policy.yaml")); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"validate-repo", "--root", root}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("validate-repo code = 0, want non-zero")
	}
	if !strings.Contains(stderr.String(), "missing required file: global/policies/execution-policy.yaml") {
		t.Fatalf("validate-repo stderr = %q, want missing file message", stderr.String())
	}
}

func TestRunDoctorCommandReportsRepositoryAndOptionalTools(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}

	restoreLookPath := stubLookPath(map[string]string{
		"git": "/usr/bin/git",
		"gh":  "/usr/bin/gh",
	})
	defer restoreLookPath()

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"doctor", "--root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "doctor completed") {
		t.Fatalf("doctor stdout = %q, want completion header", output)
	}
	if !strings.Contains(output, "repository: valid") {
		t.Fatalf("doctor stdout = %q, want repository validity", output)
	}
	if !strings.Contains(output, "git: found (/usr/bin/git)") {
		t.Fatalf("doctor stdout = %q, want git found", output)
	}
	if !strings.Contains(output, "gh: found (/usr/bin/gh)") {
		t.Fatalf("doctor stdout = %q, want gh found", output)
	}
	if !strings.Contains(output, "sqlserver2tidb-executor: missing") {
		t.Fatalf("doctor stdout = %q, want missing executor warning", output)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"doctor", "--root", root, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor json code = %d, stderr = %s", code, stderr.String())
	}
	var report struct {
		Repository struct {
			Valid        bool     `json:"valid"`
			CheckedDirs  int      `json:"checked_dirs"`
			CheckedFiles int      `json:"checked_files"`
			Errors       []string `json:"errors"`
		} `json:"repository"`
		Tools []struct {
			Name  string `json:"name"`
			Found bool   `json:"found"`
			Path  string `json:"path,omitempty"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("doctor json stdout = %q, unmarshal error = %v", stdout.String(), err)
	}
	if !report.Repository.Valid || report.Repository.CheckedFiles == 0 {
		t.Fatalf("doctor json report = %+v, want valid repository with checked files", report)
	}
	if len(report.Tools) != 3 {
		t.Fatalf("doctor json tools = %+v, want 3 tools", report.Tools)
	}
	if report.Tools[0].Name != "git" || !report.Tools[0].Found || report.Tools[0].Path != "/usr/bin/git" {
		t.Fatalf("doctor json tools = %+v, want git found", report.Tools)
	}
	if report.Tools[2].Name != "sqlserver2tidb-executor" || report.Tools[2].Found {
		t.Fatalf("doctor json tools = %+v, want executor missing", report.Tools)
	}
	if strings.Contains(stdout.String(), "doctor completed") {
		t.Fatalf("doctor json stdout = %q, should not include text header", stdout.String())
	}
}

func TestRunDoctorRequireToolsFailsOnMissingTool(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}

	restoreLookPath := stubLookPath(map[string]string{
		"git": "/usr/bin/git",
	})
	defer restoreLookPath()

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"doctor", "--root", root, "--require-tools"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("doctor code = 0, want failure when required tools are missing\nstdout=%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "doctor: required tools missing") {
		t.Fatalf("doctor stderr = %q, want missing tools error", stderr.String())
	}
	if !strings.Contains(stdout.String(), "gh: missing") {
		t.Fatalf("doctor stdout = %q, want missing gh", stdout.String())
	}
	if !strings.Contains(stdout.String(), "sqlserver2tidb-executor: missing") {
		t.Fatalf("doctor stdout = %q, want missing executor", stdout.String())
	}
}

func TestRunDiscoverSQLServerDryRunCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"discover-sqlserver", "--root", root, "--source-cluster-id", "prod-sqlserver-a", "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("discover-sqlserver code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "SQL Server discovery dry run for prod-sqlserver-a") {
		t.Fatalf("discover-sqlserver stdout = %q, want dry-run header", output)
	}
	if !strings.Contains(output, "No database connection will be opened.") {
		t.Fatalf("discover-sqlserver stdout = %q, want no connection message", output)
	}
	if !strings.Contains(output, "sys.tables") {
		t.Fatalf("discover-sqlserver stdout = %q, want catalog query list", output)
	}
	if !strings.Contains(output, "clusters/prod-sqlserver-a/inventory/inventory.json") {
		t.Fatalf("discover-sqlserver stdout = %q, want target inventory file", output)
	}
}

func TestRunDiscoverSQLServerRequiresConnectionStringEnv(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := Run([]string{"discover-sqlserver", "--root", root, "--source-cluster-id", "prod-sqlserver-a"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("discover-sqlserver code = 0, want non-zero without --dry-run or --connection-string-env")
	}
	if !strings.Contains(stderr.String(), "requires --connection-string-env unless --dry-run is set") {
		t.Fatalf("discover-sqlserver stderr = %q, want connection env requirement", stderr.String())
	}
}

func TestRunDiscoverSQLServerRequiresConnectionStringEnvValue(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := Run([]string{"discover-sqlserver", "--root", root, "--source-cluster-id", "prod-sqlserver-a", "--connection-string-env", "SQLSERVER2TIDB_TEST_DSN_MISSING"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("discover-sqlserver code = 0, want non-zero for missing env value")
	}
	if !strings.Contains(stderr.String(), "environment variable SQLSERVER2TIDB_TEST_DSN_MISSING is not set") {
		t.Fatalf("discover-sqlserver stderr = %q, want missing env message", stderr.String())
	}
}

func TestRunAnalyzeCompatibilityCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"analyze-compatibility", "--root", root, "--source-cluster-id", "prod-sqlserver-a"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("analyze-compatibility code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "compatibility analysis completed for prod-sqlserver-a") {
		t.Fatalf("analyze-compatibility stdout = %q, want completed message", stdout.String())
	}
	if !strings.Contains(stdout.String(), "blockers: 1") {
		t.Fatalf("analyze-compatibility stdout = %q, want blocker count", stdout.String())
	}
	assertExists(t, root, "clusters/prod-sqlserver-a/inventory/schema-issues.yaml")
	assertExists(t, root, "clusters/prod-sqlserver-a/inventory/compatibility-report.md")
}

func TestRunAnalyzeCompatibilityReportsMissingCluster(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"analyze-compatibility", "--root", root, "--source-cluster-id", "missing-cluster"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("analyze-compatibility code = 0, want non-zero")
	}
	if !strings.Contains(stderr.String(), `source cluster "missing-cluster" does not exist`) {
		t.Fatalf("analyze-compatibility stderr = %q, want missing cluster message", stderr.String())
	}
}

func TestRunGenerateSchemaDraftCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
                {"name": "payload", "type": "xml"}
              ]
            }
          ]
        }
      ]
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"generate-schema-draft",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("generate-schema-draft code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "schema draft generated for sales-db-to-tidb-prod-a") {
		t.Fatalf("generate-schema-draft stdout = %q, want completed message", stdout.String())
	}
	if !strings.Contains(stdout.String(), "manual review items: 1") {
		t.Fatalf("generate-schema-draft stdout = %q, want manual review count", stdout.String())
	}
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/tidb-ddl/dbo.orders.sql")
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/conversion-report.md")
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/schema-diff.json")
}

func TestRunGenerateDataPlansCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
        }
      ]
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"generate-data-plans",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--object-uri-prefix", "https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
		"--chunk-size-rows", "1000000",
		"--export-format", "csv",
		"--import-engine", "sql-insert",
		"--compression", "gzip",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("generate-data-plans code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "data movement plans generated for sales-db-to-tidb-prod-a") {
		t.Fatalf("generate-data-plans stdout = %q, want generated message", output)
	}
	if !strings.Contains(output, "export chunks: 3") {
		t.Fatalf("generate-data-plans stdout = %q, want export chunk count", output)
	}
	if !strings.Contains(output, "import jobs: 3") {
		t.Fatalf("generate-data-plans stdout = %q, want import job count", output)
	}
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml")
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml")
	gzipExportPlan := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml")
	if !strings.Contains(gzipExportPlan, "compression: gzip") || !strings.Contains(gzipExportPlan, ".csv.gz") {
		t.Fatalf("export plan = %q, want gzip compression and .csv.gz object names", gzipExportPlan)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"generate-data-plans",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--object-uri-prefix", "https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
		"--chunk-size-rows", "3000000",
		"--export-format", "csv",
		"--import-engine", "sql-insert",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("generate-data-plans single chunk code = %d, stderr = %s", code, stderr.String())
	}
	output = stdout.String()
	if !strings.Contains(output, "export chunks: 1") {
		t.Fatalf("generate-data-plans stdout = %q, want single export chunk count", output)
	}
	exportPlan := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml")
	if !strings.Contains(exportPlan, `predicate: "1 = 1"`) {
		t.Fatalf("export plan = %q, want trivial single-chunk predicate", exportPlan)
	}
	if strings.Contains(exportPlan, "TODO") {
		t.Fatalf("single-chunk export plan should not contain TODO predicate:\n%s", exportPlan)
	}
}

func TestRunGeneratePRDraftCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"generate-pr-draft",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "schema",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("generate-pr-draft code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "PR draft generated for schema") {
		t.Fatalf("generate-pr-draft stdout = %q, want generated message", stdout.String())
	}
	if !strings.Contains(stdout.String(), "agent/sales-db-to-tidb-prod-a/schema") {
		t.Fatalf("generate-pr-draft stdout = %q, want branch name", stdout.String())
	}
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/schema-pr.md")
}

func TestRunCreatePRDryRunCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"generate-pr-draft",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "schema",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-pr-draft code = %d, stderr = %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"create-pr",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "schema",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("create-pr code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "dry run: not calling GitHub") {
		t.Fatalf("create-pr stdout = %q, want dry-run message", output)
	}
	if !strings.Contains(output, "gh pr create --base main --head agent/sales-db-to-tidb-prod-a/schema") {
		t.Fatalf("create-pr stdout = %q, want gh command", output)
	}
	if !strings.Contains(output, "--title '[schema] sales-db-to-tidb-prod-a'") {
		t.Fatalf("create-pr stdout = %q, want quoted title", output)
	}
}

func TestRunWorkerValidateCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{
		"generate-schema-draft",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-schema-draft code = %d, stderr = %s", code, stderr.String())
	}
	setCLIReviewPlanStatus(t, root, "validation", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "validation",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash code = %d, stderr = %s", code, stderr.String())
	}
	payloadHash := parsePayloadHash(t, stdout.String())
	writeCLIValidationApproval(t, root, payloadHash)

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-validate",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-validate code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "validation worker completed for sales-db-to-tidb-prod-a") {
		t.Fatalf("worker-validate stdout = %q, want completed message", stdout.String())
	}
	if !strings.Contains(stdout.String(), "status: passed") {
		t.Fatalf("worker-validate stdout = %q, want passed status", stdout.String())
	}
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/validation-status.yaml")
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/validation-report.md")
}

func TestRunWorkerExportAndImportCommands(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
        }
      ]
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{
		"generate-schema-draft",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-schema-draft code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"generate-data-plans",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--object-uri-prefix", "https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
		"--chunk-size-rows", "1000000",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-data-plans code = %d, stderr = %s", code, stderr.String())
	}
	reviewCLIExportPlanPredicates(t, root)
	setCLIReviewPlanStatus(t, root, "export", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash export code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "export", parsePayloadHash(t, stdout.String()))

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-executor export code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "worker executor dry run") {
		t.Fatalf("worker-executor stdout = %q, want dry-run header", stdout.String())
	}
	if !strings.Contains(stdout.String(), "stage: export") {
		t.Fatalf("worker-executor stdout = %q, want export stage", stdout.String())
	}
	if !strings.Contains(stdout.String(), "commands: 3") {
		t.Fatalf("worker-executor stdout = %q, want command count", stdout.String())
	}
	if !strings.Contains(stdout.String(), "sqlserver2tidb-executor export") {
		t.Fatalf("worker-executor stdout = %q, want executor command", stdout.String())
	}
	if !strings.Contains(stdout.String(), "--chunk-id dbo.orders.000001") {
		t.Fatalf("worker-executor stdout = %q, want first chunk id", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-export",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-export code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "export worker completed for sales-db-to-tidb-prod-a") {
		t.Fatalf("worker-export stdout = %q, want completed message", stdout.String())
	}
	if !strings.Contains(stdout.String(), "chunks: 3") {
		t.Fatalf("worker-export stdout = %q, want chunk count", stdout.String())
	}
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml")
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/precheck.json")

	setCLIReviewPlanStatus(t, root, "import", "reviewed")
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "import",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash import code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "import", parsePayloadHash(t, stdout.String()))

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "import",
		"--target-connection-string-env", "TIDB_IMPORT_DSN",
		"--import-batch-size", "500",
		"--require-empty-target",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-executor import code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "--target-connection-string-env TIDB_IMPORT_DSN") {
		t.Fatalf("worker-executor stdout = %q, want target connection env", stdout.String())
	}
	if !strings.Contains(stdout.String(), "--import-batch-size 500") {
		t.Fatalf("worker-executor stdout = %q, want import batch size", stdout.String())
	}
	if !strings.Contains(stdout.String(), "--require-empty-target") {
		t.Fatalf("worker-executor stdout = %q, want require empty target flag", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-import",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-import code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "import worker completed for sales-db-to-tidb-prod-a") {
		t.Fatalf("worker-import stdout = %q, want completed message", stdout.String())
	}
	if !strings.Contains(stdout.String(), "jobs: 3") {
		t.Fatalf("worker-import stdout = %q, want job count", stdout.String())
	}
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/import-jobs.yaml")
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/import-summary.json")
}

func TestRunWorkerExecutorExecutePassesExecuteFlagToExternalExecutor(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
              "row_count": 1,
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
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{
		"generate-data-plans",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--object-uri-prefix", "file:///tmp/sqlserver2tidb-test/full",
		"--chunk-size-rows", "1000",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-data-plans code = %d, stderr = %s", code, stderr.String())
	}
	reviewCLIExportPlanPredicates(t, root)
	setCLIReviewPlanStatus(t, root, "export", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash export code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "export", parsePayloadHash(t, stdout.String()))

	fakeExecutor := filepath.Join(root, "fake-executor")
	if err := os.WriteFile(fakeExecutor, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> executor-args.log\nprintf 'fake executor completed: %s\\n' \"$1\"\nprintf 'exported rows: 2\\n'\nprintf 'output bytes: 128\\n'\nprintf 'output sha256: sha256:1111111111111111111111111111111111111111111111111111111111111111\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
		"--executor-binary", "./fake-executor",
		"--execute",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-executor execute code = %d, stderr = %s", code, stderr.String())
	}

	argsLog, err := os.ReadFile(filepath.Join(root, "executor-args.log"))
	if err != nil {
		t.Fatalf("read executor args log: %v", err)
	}
	if !strings.Contains(string(argsLog), "export --execute --root .") {
		t.Fatalf("executor args log = %q, want external executor --execute flag", string(argsLog))
	}
	evidence := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json")
	if !strings.Contains(evidence, `"stage": "export"`) {
		t.Fatalf("executor evidence = %q, want export stage", evidence)
	}
	if !strings.Contains(evidence, `"status": "succeeded"`) {
		t.Fatalf("executor evidence = %q, want succeeded status", evidence)
	}
	if !strings.Contains(evidence, `"id": "dbo.orders.000001"`) {
		t.Fatalf("executor evidence = %q, want command id", evidence)
	}
	if !strings.Contains(evidence, `"exit_code": 0`) {
		t.Fatalf("executor evidence = %q, want zero exit code", evidence)
	}
	if !strings.Contains(evidence, `"started_at": "`) {
		t.Fatalf("executor evidence = %q, want command started_at", evidence)
	}
	if !strings.Contains(evidence, `"completed_at": "`) {
		t.Fatalf("executor evidence = %q, want command completed_at", evidence)
	}
	if !strings.Contains(evidence, `"duration_ms": `) {
		t.Fatalf("executor evidence = %q, want command duration_ms", evidence)
	}
	if !strings.Contains(evidence, `fake executor completed: export`) {
		t.Fatalf("executor evidence = %q, want executor output", evidence)
	}
	if !strings.Contains(stdout.String(), "wrote evidence/executor-export-run.json") {
		t.Fatalf("worker-executor stdout = %q, want evidence path", stdout.String())
	}
}

func TestRunWorkerExecutorExecuteRecordsDataMetricsInEvidence(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)
	reviewCLIExportPlanPredicates(t, root)
	setCLIReviewPlanStatus(t, root, "export", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash export code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "export", parsePayloadHash(t, stdout.String()))

	fakeExecutor := filepath.Join(root, "fake-executor-metrics")
	if err := os.WriteFile(fakeExecutor, []byte("#!/bin/sh\nprintf 'executor export completed: sales.dbo.orders -> file:///tmp/orders.csv\\n'\nprintf 'exported rows: 2\\n'\nprintf 'output bytes: 128\\n'\nprintf 'output sha256: sha256:1111111111111111111111111111111111111111111111111111111111111111\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
		"--executor-binary", "./fake-executor-metrics",
		"--execute",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-executor execute code = %d, stderr = %s", code, stderr.String())
	}

	evidence := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json")
	if !strings.Contains(evidence, `"data_rows": 2`) {
		t.Fatalf("executor evidence = %q, want structured data rows", evidence)
	}
	if !strings.Contains(evidence, `"data_bytes": 128`) {
		t.Fatalf("executor evidence = %q, want structured data bytes", evidence)
	}
	if !strings.Contains(evidence, `"data_sha256": "sha256:1111111111111111111111111111111111111111111111111111111111111111"`) {
		t.Fatalf("executor evidence = %q, want structured data sha256", evidence)
	}
}

func TestRunWorkerExecutorExecuteRejectsMissingRequiredDataAudit(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)
	reviewCLIExportPlanPredicates(t, root)
	setCLIReviewPlanStatus(t, root, "export", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash export code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "export", parsePayloadHash(t, stdout.String()))

	fakeExecutor := filepath.Join(root, "fake-executor-missing-data-audit")
	if err := os.WriteFile(fakeExecutor, []byte("#!/bin/sh\nprintf 'executor export completed: sales.dbo.orders -> file:///tmp/orders.csv\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
		"--executor-binary", "./fake-executor-missing-data-audit",
		"--execute",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("worker-executor execute code = 0, want missing data audit failure")
	}
	if !strings.Contains(stderr.String(), "export executor output must include exported rows: N, output bytes: N, and output sha256: sha256:<digest>") {
		t.Fatalf("worker-executor stderr = %q, want missing data audit error", stderr.String())
	}

	evidence := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json")
	if !strings.Contains(evidence, `"status": "failed"`) {
		t.Fatalf("executor evidence = %q, want failed status", evidence)
	}
	if !strings.Contains(evidence, "must include exported rows") {
		t.Fatalf("executor evidence = %q, want missing data audit command error", evidence)
	}
}

func TestWorkerExecutorDataMetricsRejectsPartialOrInvalidOutput(t *testing.T) {
	_, _, err := workerExecutorDataMetrics("export", "exported rows: 2\n")
	if err == nil {
		t.Fatal("workerExecutorDataMetrics() error = nil, want partial metric error")
	}
	if !strings.Contains(err.Error(), "must include both exported rows: N and output bytes: N") {
		t.Fatalf("workerExecutorDataMetrics() error = %v, want partial metric error", err)
	}

	_, _, err = workerExecutorDataMetrics("import", "imported rows: -1\ninput bytes: 128\n")
	if err == nil {
		t.Fatal("workerExecutorDataMetrics() error = nil, want invalid metric error")
	}
	if !strings.Contains(err.Error(), `metric "imported rows:" must contain a non-negative integer`) {
		t.Fatalf("workerExecutorDataMetrics() error = %v, want invalid imported rows error", err)
	}
}

func TestReusableWorkerExecutorCommandEvidenceRequiresDataAuditForExportAndSQLInsertImport(t *testing.T) {
	exportArgs := []string{"sqlserver2tidb-executor", "export", "--execute"}
	exportEvidence := workerExecutorRunCommandEvidence{
		Args:         exportArgs,
		ShellCommand: renderArgsForEvidence(exportArgs),
		ExitCode:     0,
	}
	if isReusableWorkerExecutorCommandEvidence("export", exportEvidence, exportArgs) {
		t.Fatal("export evidence without data audit was reusable, want rerun")
	}

	dataRows := int64(2)
	dataBytes := int64(128)
	exportEvidence.DataRows = &dataRows
	exportEvidence.DataBytes = &dataBytes
	exportEvidence.DataSHA256 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	if !isReusableWorkerExecutorCommandEvidence("export", exportEvidence, exportArgs) {
		t.Fatal("export evidence with complete data audit was not reusable")
	}

	sqlInsertArgs := []string{"sqlserver2tidb-executor", "import", "--execute", "--engine", "sql-insert"}
	sqlInsertEvidence := workerExecutorRunCommandEvidence{
		Args:         sqlInsertArgs,
		ShellCommand: renderArgsForEvidence(sqlInsertArgs),
		ExitCode:     0,
	}
	if isReusableWorkerExecutorCommandEvidence("import", sqlInsertEvidence, sqlInsertArgs) {
		t.Fatal("sql-insert import evidence without data audit was reusable, want rerun")
	}

	importIntoArgs := []string{"sqlserver2tidb-executor", "import", "--execute", "--engine", "tidb-import-into", "--source-uri", "file:///tmp/orders.csv"}
	importIntoEvidence := workerExecutorRunCommandEvidence{
		Args:         importIntoArgs,
		ShellCommand: renderArgsForEvidence(importIntoArgs),
		ExitCode:     0,
	}
	if isReusableWorkerExecutorCommandEvidence("import", importIntoEvidence, importIntoArgs) {
		t.Fatal("local tidb-import-into evidence without data audit was reusable, want rerun")
	}

	s3ImportIntoArgs := []string{"sqlserver2tidb-executor", "import", "--execute", "--engine", "tidb-import-into", "--source-uri", "s3://migration-bucket/orders.csv"}
	s3ImportIntoEvidence := workerExecutorRunCommandEvidence{
		Args:         s3ImportIntoArgs,
		ShellCommand: renderArgsForEvidence(s3ImportIntoArgs),
		ExitCode:     0,
	}
	if isReusableWorkerExecutorCommandEvidence("import", s3ImportIntoEvidence, s3ImportIntoArgs) {
		t.Fatal("S3 tidb-import-into evidence without data audit was reusable, want rerun")
	}

	remoteImportIntoArgs := []string{"sqlserver2tidb-executor", "import", "--execute", "--engine", "tidb-import-into", "--source-uri", "gs://migration-bucket/orders.csv"}
	remoteImportIntoEvidence := workerExecutorRunCommandEvidence{
		Args:         remoteImportIntoArgs,
		ShellCommand: renderArgsForEvidence(remoteImportIntoArgs),
		ExitCode:     0,
	}
	if !isReusableWorkerExecutorCommandEvidence("import", remoteImportIntoEvidence, remoteImportIntoArgs) {
		t.Fatal("GCS tidb-import-into evidence without data audit was not reusable")
	}
}

func TestRunWorkerExecutorExecuteWritesFailedEvidenceOnCommandFailure(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)
	reviewCLIExportPlanPredicates(t, root)
	setCLIReviewPlanStatus(t, root, "export", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash export code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "export", parsePayloadHash(t, stdout.String()))

	fakeExecutor := filepath.Join(root, "fake-executor-fails")
	if err := os.WriteFile(fakeExecutor, []byte("#!/bin/sh\nprintf 'fake executor failed\\n'\nexit 17\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
		"--executor-binary", "./fake-executor-fails",
		"--execute",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("worker-executor execute code = 0, want failure")
	}
	if !strings.Contains(stderr.String(), "worker executor: command dbo.orders.000001 failed") {
		t.Fatalf("worker-executor stderr = %q, want failed command", stderr.String())
	}
	evidence := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json")
	if !strings.Contains(evidence, `"status": "failed"`) {
		t.Fatalf("executor evidence = %q, want failed status", evidence)
	}
	if !strings.Contains(evidence, `"exit_code": 17`) {
		t.Fatalf("executor evidence = %q, want exit code 17", evidence)
	}
	if !strings.Contains(evidence, `fake executor failed`) {
		t.Fatalf("executor evidence = %q, want failure output", evidence)
	}
}

func TestRunWorkerExecutorExecuteRetriesTransientCommandFailure(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)
	reviewCLIExportPlanPredicates(t, root)
	setCLIReviewPlanStatus(t, root, "export", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash export code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "export", parsePayloadHash(t, stdout.String()))

	fakeExecutor := filepath.Join(root, "fake-executor-fails-once")
	if err := os.WriteFile(fakeExecutor, []byte("#!/bin/sh\nif [ ! -f retry-marker ]; then\n  touch retry-marker\n  printf 'attempt 1 failed\\n'\n  exit 17\nfi\nprintf 'attempt 2 succeeded\\n'\nprintf 'exported rows: 2\\n'\nprintf 'output bytes: 128\\n'\nprintf 'output sha256: sha256:1111111111111111111111111111111111111111111111111111111111111111\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
		"--executor-binary", "./fake-executor-fails-once",
		"--command-retries", "1",
		"--retry-backoff", "1ms",
		"--execute",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-executor execute code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "attempt 1 failed") || !strings.Contains(stdout.String(), "attempt 2 succeeded") {
		t.Fatalf("worker-executor stdout = %q, want both attempt outputs", stdout.String())
	}

	var evidence struct {
		Status   string `json:"status"`
		Commands []struct {
			ExitCode     int `json:"exit_code"`
			AttemptCount int `json:"attempt_count"`
			Attempts     []struct {
				Attempt  int    `json:"attempt"`
				ExitCode int    `json:"exit_code"`
				Output   string `json:"output"`
			} `json:"attempts"`
		} `json:"commands"`
	}
	evidenceJSON := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json")
	if err := json.Unmarshal([]byte(evidenceJSON), &evidence); err != nil {
		t.Fatalf("parse executor evidence: %v\n%s", err, evidenceJSON)
	}
	if evidence.Status != "succeeded" {
		t.Fatalf("executor evidence status = %q, want succeeded", evidence.Status)
	}
	if len(evidence.Commands) != 1 {
		t.Fatalf("executor evidence commands = %d, want 1", len(evidence.Commands))
	}
	command := evidence.Commands[0]
	if command.ExitCode != 0 || command.AttemptCount != 2 || len(command.Attempts) != 2 {
		t.Fatalf("executor evidence command = %+v, want final success with 2 attempts", command)
	}
	if command.Attempts[0].Attempt != 1 || command.Attempts[0].ExitCode != 17 || !strings.Contains(command.Attempts[0].Output, "attempt 1 failed") {
		t.Fatalf("first attempt evidence = %+v, want failed attempt", command.Attempts[0])
	}
	if command.Attempts[1].Attempt != 2 || command.Attempts[1].ExitCode != 0 || !strings.Contains(command.Attempts[1].Output, "attempt 2 succeeded") {
		t.Fatalf("second attempt evidence = %+v, want successful retry", command.Attempts[1])
	}
}

func TestRunWorkerExecutorExecuteWritesFailedEvidenceOnCommandTimeout(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)
	reviewCLIExportPlanPredicates(t, root)
	setCLIReviewPlanStatus(t, root, "export", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash export code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "export", parsePayloadHash(t, stdout.String()))

	fakeExecutor := filepath.Join(root, "fake-executor-sleeps")
	if err := os.WriteFile(fakeExecutor, []byte("#!/bin/sh\nprintf 'fake executor started\\n'\nsleep 2\nprintf 'fake executor finished\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
		"--executor-binary", "./fake-executor-sleeps",
		"--command-timeout", "10ms",
		"--execute",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("worker-executor execute code = 0, want timeout failure")
	}
	if !strings.Contains(stderr.String(), "command dbo.orders.000001 timed out after 10ms") {
		t.Fatalf("worker-executor stderr = %q, want timeout message", stderr.String())
	}
	evidence := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-export-run.json")
	if !strings.Contains(evidence, `"status": "failed"`) {
		t.Fatalf("executor evidence = %q, want failed status", evidence)
	}
	if !strings.Contains(evidence, `"error": "command timed out after 10ms"`) {
		t.Fatalf("executor evidence = %q, want timeout command error", evidence)
	}
}

func TestRunWorkerExecutorCommandTimeoutTerminatesProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell process group termination is Unix-specific")
	}
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)
	reviewCLIExportPlanPredicates(t, root)
	setCLIReviewPlanStatus(t, root, "export", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash export code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "export", parsePayloadHash(t, stdout.String()))

	fakeExecutor := filepath.Join(root, "fake-executor-spawns-child")
	if err := os.WriteFile(fakeExecutor, []byte("#!/bin/sh\n(sleep 0.5; printf child > child-marker) &\nsleep 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
		"--executor-binary", "./fake-executor-spawns-child",
		"--command-timeout", "100ms",
		"--execute",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("worker-executor execute code = 0, want timeout failure")
	}

	time.Sleep(800 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "child-marker")); err == nil {
		t.Fatalf("child process survived timeout and wrote marker")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat child marker: %v", err)
	}
}

func TestRunWorkerExecutorValidationExecuteContinuesAfterCommandFailure(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)
	if code := Run([]string{
		"generate-schema-draft",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-schema-draft code = %d, stderr = %s", code, stderr.String())
	}
	validationPlanPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "plan", "validation-plan.yaml")
	if err := os.WriteFile(validationPlanPath, []byte(`status: reviewed
checks:
  - id: orders-bucket-0
    type: bucketed_count
    source_sql: "SELECT COUNT(*) FROM sales.dbo.orders WHERE id % 2 = 0"
    target_sql: "SELECT COUNT(*) FROM app.orders WHERE id % 2 = 0"
  - id: orders-bucket-1
    type: bucketed_count
    source_sql: "SELECT COUNT(*) FROM sales.dbo.orders WHERE id % 2 = 1"
    target_sql: "SELECT COUNT(*) FROM app.orders WHERE id % 2 = 1"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "validation",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash validation code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "validation", parsePayloadHash(t, stdout.String()))

	fakeExecutor := filepath.Join(root, "fake-validation-executor")
	if err := os.WriteFile(fakeExecutor, []byte(`#!/bin/sh
printf '%s\n' "$*" >> validation-executor-args.log
case "$*" in
  *orders-bucket-0*)
    printf 'validation mismatch for bucket 0\n'
    exit 17
    ;;
  *orders-bucket-1*)
    printf 'validation matched for bucket 1\n'
    exit 0
    ;;
esac
printf 'unexpected command\n'
exit 99
`), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "validation",
		"--executor-binary", "./fake-validation-executor",
		"--execute",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("worker-executor validation execute code = 0, want failure")
	}
	if !strings.Contains(stderr.String(), "worker executor: validation completed with 1 failed command(s)") {
		t.Fatalf("worker-executor stderr = %q, want validation aggregate failure", stderr.String())
	}

	argsLog, err := os.ReadFile(filepath.Join(root, "validation-executor-args.log"))
	if err != nil {
		t.Fatalf("read validation executor args log: %v", err)
	}
	if !strings.Contains(string(argsLog), "--check-id orders-bucket-0") || !strings.Contains(string(argsLog), "--check-id orders-bucket-1") {
		t.Fatalf("validation executor args log = %q, want both validation commands executed", string(argsLog))
	}
	evidence := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-validation-run.json")
	if !strings.Contains(evidence, `"status": "failed"`) {
		t.Fatalf("executor evidence = %q, want failed status", evidence)
	}
	if !strings.Contains(evidence, `"id": "orders-bucket-0"`) || !strings.Contains(evidence, `"exit_code": 17`) {
		t.Fatalf("executor evidence = %q, want failed command evidence", evidence)
	}
	if !strings.Contains(evidence, `"id": "orders-bucket-1"`) || !strings.Contains(evidence, `"exit_code": 0`) {
		t.Fatalf("executor evidence = %q, want successful command evidence after failure", evidence)
	}
	if !strings.Contains(evidence, `validation mismatch for bucket 0`) || !strings.Contains(evidence, `validation matched for bucket 1`) {
		t.Fatalf("executor evidence = %q, want both command outputs", evidence)
	}
}

func TestRunWorkerExecutorResumeSkipsPreviouslySuccessfulCommands(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)
	if code := Run([]string{
		"generate-schema-draft",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-schema-draft code = %d, stderr = %s", code, stderr.String())
	}
	validationPlanPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "plan", "validation-plan.yaml")
	if err := os.WriteFile(validationPlanPath, []byte(`status: reviewed
checks:
  - id: orders-bucket-0
    type: bucketed_count
    source_sql: "SELECT COUNT(*) FROM sales.dbo.orders WHERE id % 2 = 0"
    target_sql: "SELECT COUNT(*) FROM app.orders WHERE id % 2 = 0"
  - id: orders-bucket-1
    type: bucketed_count
    source_sql: "SELECT COUNT(*) FROM sales.dbo.orders WHERE id % 2 = 1"
    target_sql: "SELECT COUNT(*) FROM app.orders WHERE id % 2 = 1"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "validation",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash validation code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "validation", parsePayloadHash(t, stdout.String()))

	fakeExecutor := filepath.Join(root, "fake-validation-executor-resumable")
	if err := os.WriteFile(fakeExecutor, []byte(`#!/bin/sh
printf '%s\n' "$*" >> validation-first-run.log
case "$*" in
  *orders-bucket-0*)
    printf 'bucket 0 matched on first run\n'
    exit 0
    ;;
  *orders-bucket-1*)
    printf 'bucket 1 transient mismatch on first run\n'
    exit 17
    ;;
esac
exit 99
`), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "validation",
		"--executor-binary", "./fake-validation-executor-resumable",
		"--execute",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("first worker-executor validation execute code = 0, want failure")
	}

	if err := os.WriteFile(fakeExecutor, []byte(`#!/bin/sh
printf '%s\n' "$*" >> validation-resume-run.log
case "$*" in
  *orders-bucket-0*)
    printf 'bucket 0 should have been skipped\n'
    exit 42
    ;;
  *orders-bucket-1*)
    printf 'bucket 1 matched on resume\n'
    exit 0
    ;;
esac
exit 99
`), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "validation",
		"--executor-binary", "./fake-validation-executor-resumable",
		"--resume",
		"--execute",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("resume worker-executor validation execute code = %d, stderr = %s", code, stderr.String())
	}
	resumeLog := readCLIRelFile(t, root, "validation-resume-run.log")
	if strings.Contains(resumeLog, "orders-bucket-0") {
		t.Fatalf("resume executor log = %q, want previously successful command skipped", resumeLog)
	}
	if !strings.Contains(resumeLog, "orders-bucket-1") {
		t.Fatalf("resume executor log = %q, want failed command retried", resumeLog)
	}
	evidence := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-validation-run.json")
	if !strings.Contains(evidence, `"status": "succeeded"`) {
		t.Fatalf("executor evidence = %q, want succeeded status", evidence)
	}
	if !strings.Contains(evidence, `bucket 0 matched on first run`) {
		t.Fatalf("executor evidence = %q, want copied skipped command evidence", evidence)
	}
	if !strings.Contains(evidence, `bucket 1 matched on resume`) {
		t.Fatalf("executor evidence = %q, want resumed command evidence", evidence)
	}
}

func TestRunWorkerExecutorCDCExecuteRecordsAppliedChangesInEvidence(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
              "row_count": 1,
              "columns": [
                {"name": "id", "type": "int"},
                {"name": "customer_name", "type": "nvarchar"}
              ],
              "indexes": [
                {"name": "PK_orders", "columns": ["id"], "unique": true, "primary_key": true}
              ]
            }
          ]
        }
      ]
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{
		"generate-cdc-plan",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-cdc-plan code = %d, stderr = %s", code, stderr.String())
	}
	setCLICDCPlanLSNRange(t, root, "0x00000000000000000001", "0x00000000000000000002")
	setCLIReviewPlanStatus(t, root, "cdc", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "cdc",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash cdc code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "cdc", parsePayloadHash(t, stdout.String()))

	fakeExecutor := filepath.Join(root, "fake-executor-cdc")
	if err := os.WriteFile(fakeExecutor, []byte("#!/bin/sh\nprintf 'executor cdc completed: sales.dbo.orders -> app.orders\\n'\nprintf 'applied changes: 2\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "cdc",
		"--executor-binary", "./fake-executor-cdc",
		"--execute",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-executor cdc execute code = %d, stderr = %s", code, stderr.String())
	}

	evidence := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-cdc-run.json")
	if !strings.Contains(evidence, `"stage": "cdc"`) {
		t.Fatalf("executor evidence = %q, want cdc stage", evidence)
	}
	if !strings.Contains(evidence, `"cdc_applied_changes": 2`) {
		t.Fatalf("executor evidence = %q, want structured CDC applied changes", evidence)
	}
	if !strings.Contains(evidence, `applied changes: 2`) {
		t.Fatalf("executor evidence = %q, want executor output", evidence)
	}
	if !strings.Contains(stdout.String(), "wrote evidence/executor-cdc-run.json") {
		t.Fatalf("worker-executor stdout = %q, want evidence path", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"advance-cdc-checkpoint",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--status", "running",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("advance-cdc-checkpoint code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "cdc checkpoint advanced for sales-db-to-tidb-prod-a") {
		t.Fatalf("advance-cdc-checkpoint stdout = %q, want completed message", stdout.String())
	}
	if !strings.Contains(stdout.String(), "applied changes: 2") {
		t.Fatalf("advance-cdc-checkpoint stdout = %q, want applied changes", stdout.String())
	}
	checkpoint := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml")
	if !strings.Contains(checkpoint, "status: running") {
		t.Fatalf("cdc checkpoint = %q, want running status", checkpoint)
	}
	if !strings.Contains(checkpoint, "to_lsn: 0x00000000000000000002") {
		t.Fatalf("cdc checkpoint = %q, want advanced to_lsn", checkpoint)
	}
	if !strings.Contains(checkpoint, "applied_changes: 2") {
		t.Fatalf("cdc checkpoint = %q, want applied changes", checkpoint)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"prepare-cdc-range",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--to-lsn", "0x00000000000000000003",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("prepare-cdc-range code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "cdc range prepared for sales-db-to-tidb-prod-a") {
		t.Fatalf("prepare-cdc-range stdout = %q, want completed message", stdout.String())
	}
	plan := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml")
	if !strings.Contains(plan, "status: draft") {
		t.Fatalf("cdc plan = %q, want draft status", plan)
	}
	if !strings.Contains(plan, "from_lsn: 0x00000000000000000002") {
		t.Fatalf("cdc plan = %q, want checkpoint to_lsn as next from_lsn", plan)
	}
	if !strings.Contains(plan, "to_lsn: 0x00000000000000000003") {
		t.Fatalf("cdc plan = %q, want new to_lsn", plan)
	}
}

func TestRunWorkerExecutorCDCExecuteFailsWithEvidenceWhenAppliedChangesMissing(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
              "row_count": 1,
              "columns": [
                {"name": "id", "type": "int"},
                {"name": "customer_name", "type": "nvarchar"}
              ],
              "indexes": [
                {"name": "PK_orders", "columns": ["id"], "unique": true, "primary_key": true}
              ]
            }
          ]
        }
      ]
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{
		"generate-cdc-plan",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-cdc-plan code = %d, stderr = %s", code, stderr.String())
	}
	setCLICDCPlanLSNRange(t, root, "0x00000000000000000001", "0x00000000000000000002")
	setCLIReviewPlanStatus(t, root, "cdc", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "cdc",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash cdc code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "cdc", parsePayloadHash(t, stdout.String()))

	fakeExecutor := filepath.Join(root, "fake-executor-cdc")
	if err := os.WriteFile(fakeExecutor, []byte("#!/bin/sh\nprintf 'executor cdc completed without applied-change summary\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "cdc",
		"--executor-binary", "./fake-executor-cdc",
		"--execute",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("worker-executor cdc execute code = 0, want failure")
	}
	if !strings.Contains(stderr.String(), "applied changes") {
		t.Fatalf("worker-executor stderr = %q, want applied changes parse failure", stderr.String())
	}
	evidence := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/executor-cdc-run.json")
	if !strings.Contains(evidence, `"status": "failed"`) {
		t.Fatalf("executor evidence = %q, want failed status", evidence)
	}
	if !strings.Contains(evidence, `executor cdc completed without applied-change summary`) {
		t.Fatalf("executor evidence = %q, want executor output", evidence)
	}
	if !strings.Contains(evidence, `"exit_code": 0`) {
		t.Fatalf("executor evidence = %q, want successful process exit code recorded", evidence)
	}
	if !strings.Contains(evidence, `"error": "CDC executor output must include applied changes: N"`) {
		t.Fatalf("executor evidence = %q, want command error", evidence)
	}
	if strings.Contains(evidence, `"cdc_applied_changes"`) {
		t.Fatalf("executor evidence = %q, want no structured CDC applied changes", evidence)
	}
}

func TestRenderArgsForEvidenceShellQuotesArguments(t *testing.T) {
	got := renderArgsForEvidence([]string{
		"./fake executor",
		"validate-query",
		"--source-sql",
		"SELECT 'x y'",
		"--empty",
		"",
	})
	want := `'./fake executor' validate-query --source-sql 'SELECT '"'"'x y'"'"'' --empty ''`
	if got != want {
		t.Fatalf("renderArgsForEvidence() = %q, want %q", got, want)
	}
}

func TestRunGenerateCDCPlanAndWorkerCDCCommands(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
              ],
              "indexes": [
                {"name": "PK_orders", "columns": ["id"], "unique": true, "primary_key": true}
              ]
            }
          ]
        }
      ]
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"generate-cdc-plan",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--mode", "sqlserver-cdc",
		"--retention-hours", "168",
		"--apply-batch-size", "1000",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("generate-cdc-plan code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "cdc plan generated for sales-db-to-tidb-prod-a") {
		t.Fatalf("generate-cdc-plan stdout = %q, want completed message", stdout.String())
	}
	if !strings.Contains(stdout.String(), "tracked tables: 1") {
		t.Fatalf("generate-cdc-plan stdout = %q, want tracked table count", stdout.String())
	}
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml")
	setCLIReviewPlanStatus(t, root, "cdc", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "cdc",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash cdc code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "cdc", parsePayloadHash(t, stdout.String()))

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-cdc",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-cdc code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "cdc worker completed for sales-db-to-tidb-prod-a") {
		t.Fatalf("worker-cdc stdout = %q, want completed message", stdout.String())
	}
	if !strings.Contains(stdout.String(), "status: planned") {
		t.Fatalf("worker-cdc stdout = %q, want planned status", stdout.String())
	}
	if !strings.Contains(stdout.String(), "tracked tables: 1") {
		t.Fatalf("worker-cdc stdout = %q, want tracked table count", stdout.String())
	}
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/migration-state.yaml")
	assertExists(t, root, "clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml")
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/cdc-catchup.json")
}

func TestRunPrepareCDCIterationCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
              ],
              "indexes": [
                {"name": "PK_orders", "columns": ["id"], "unique": true, "primary_key": true}
              ]
            }
          ]
        }
      ]
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{
		"generate-cdc-plan",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-cdc-plan code = %d, stderr = %s", code, stderr.String())
	}
	checkpointPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "state", "cdc-checkpoint.yaml")
	if err := os.WriteFile(checkpointPath, []byte(`source_cluster_id: prod-sqlserver-a
phase: cdc
status: running
project_id: sales-db-to-tidb-prod-a
mode: sqlserver-cdc
checkpoint_scope: source-cluster
checkpoints:
  - source_object: sales.dbo.orders
    target_object: app.orders
    from_lsn: 0x00000000000000000001
    to_lsn: 0x00000000000000000002
    applied_changes: 2
    completed_at: "2026-01-02T03:04:06Z"
updated_at: "2026-01-02T03:04:07Z"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"prepare-cdc-iteration",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--max-lsn", "0x00000000000000000003",
		"--pr-draft",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("prepare-cdc-iteration code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "cdc iteration prepared for sales-db-to-tidb-prod-a") {
		t.Fatalf("prepare-cdc-iteration stdout = %q, want prepared message", output)
	}
	if !strings.Contains(output, "status: range_prepared") {
		t.Fatalf("prepare-cdc-iteration stdout = %q, want range_prepared status", output)
	}
	if !strings.Contains(output, "PR draft: clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/cdc-range-pr.md") {
		t.Fatalf("prepare-cdc-iteration stdout = %q, want PR draft path", output)
	}
	plan := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml")
	if !strings.Contains(plan, "from_lsn: 0x00000000000000000002") || !strings.Contains(plan, "to_lsn: 0x00000000000000000003") {
		t.Fatalf("cdc plan = %q, want next range", plan)
	}
	body := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/cdc-range-pr.md")
	if !strings.Contains(body, "# PR Draft: [cdc-range] sales-db-to-tidb-prod-a") {
		t.Fatalf("cdc range PR draft = %q, want title", body)
	}
}

func TestRunGenerateValidationPlanCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
        }
      ]
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"generate-validation-plan",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("generate-validation-plan code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "validation plan generated for sales-db-to-tidb-prod-a") {
		t.Fatalf("generate-validation-plan stdout = %q, want completed message", stdout.String())
	}
	if !strings.Contains(stdout.String(), "checks: 1") {
		t.Fatalf("generate-validation-plan stdout = %q, want check count", stdout.String())
	}
	planPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "plan", "validation-plan.yaml")
	plan, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read validation plan: %v", err)
	}
	if !strings.Contains(string(plan), "type: row_count") {
		t.Fatalf("validation plan = %q, want row_count check", string(plan))
	}
}

func TestRunGenerateValidationPlanCommandIncludesChecksumChecks(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
                {"name": "total", "type": "decimal(18,2)"}
              ]
            }
          ]
        }
      ]
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"generate-validation-plan",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--include-checksum",
		"--include-sampled-hash",
		"--sample-modulo", "50",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("generate-validation-plan code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "checks: 3") {
		t.Fatalf("generate-validation-plan stdout = %q, want check count", stdout.String())
	}
	planPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "plan", "validation-plan.yaml")
	plan, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read validation plan: %v", err)
	}
	if !strings.Contains(string(plan), "type: checksum") {
		t.Fatalf("validation plan = %q, want checksum check", string(plan))
	}
	if !strings.Contains(string(plan), "type: sampled_hash") {
		t.Fatalf("validation plan = %q, want sampled_hash check", string(plan))
	}
	if !strings.Contains(string(plan), "WHERE CAST([id] AS BIGINT) % 50 = 0") {
		t.Fatalf("validation plan = %q, want sampled hash predicate", string(plan))
	}
}

func TestRunGenerateValidationPlanCommandIncludesBucketedCountChecks(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
                {"name": "total", "type": "decimal(18,2)"}
              ]
            }
          ]
        }
      ]
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"generate-validation-plan",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--include-bucketed-count",
		"--bucket-count", "8",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("generate-validation-plan code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "checks: 9") {
		t.Fatalf("generate-validation-plan stdout = %q, want row-count plus 8 bucketed checks", stdout.String())
	}
	planPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "plan", "validation-plan.yaml")
	plan, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read validation plan: %v", err)
	}
	if !strings.Contains(string(plan), "type: bucketed_count") {
		t.Fatalf("validation plan = %q, want bucketed_count check", string(plan))
	}
	if !strings.Contains(string(plan), "WHERE ABS(CAST([id] AS BIGINT)) % 8 = 7") {
		t.Fatalf("validation plan = %q, want final SQL Server bucket predicate", string(plan))
	}
	if !strings.Contains(string(plan), "WHERE MOD(ABS(CAST(`id` AS SIGNED)), 8) = 7") {
		t.Fatalf("validation plan = %q, want final TiDB bucket predicate", string(plan))
	}
}

func TestRunWorkerExecutorValidationDryRunCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	validationPlanPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "plan", "validation-plan.yaml")
	if err := os.WriteFile(validationPlanPath, []byte(`status: reviewed
checks:
  - id: orders-row-count
    type: row_count
    source_object: sales.dbo.orders
    target_object: app.orders
    predicate: "id >= 1"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "validation",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash validation code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "validation", parsePayloadHash(t, stdout.String()))

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "validation",
		"--source-connection-string-env", "SQLSERVER_VALIDATE_DSN",
		"--target-connection-string-env", "TIDB_VALIDATE_DSN",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-executor validation code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "stage: validation") {
		t.Fatalf("worker-executor stdout = %q, want validation stage", stdout.String())
	}
	if !strings.Contains(stdout.String(), "sqlserver2tidb-executor validate-count") {
		t.Fatalf("worker-executor stdout = %q, want validate-count command", stdout.String())
	}
	if !strings.Contains(stdout.String(), "--source-connection-string-env SQLSERVER_VALIDATE_DSN") {
		t.Fatalf("worker-executor stdout = %q, want source connection env", stdout.String())
	}
	if !strings.Contains(stdout.String(), "--target-connection-string-env TIDB_VALIDATE_DSN") {
		t.Fatalf("worker-executor stdout = %q, want target connection env", stdout.String())
	}
}

func TestRunWorkerExecutorDDLDryRunCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"generate-schema-draft",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("generate-schema-draft code = %d, stderr = %s", code, stderr.String())
	}
	setCLISchemaDiffStatus(t, root, "reviewed")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "ddl",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash ddl code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "ddl", parsePayloadHash(t, stdout.String()))

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-executor",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "ddl",
		"--target-connection-string-env", "TIDB_DDL_DSN",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-executor ddl code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "stage: ddl") {
		t.Fatalf("worker-executor stdout = %q, want ddl stage", output)
	}
	if !strings.Contains(output, "sqlserver2tidb-executor apply-ddl") {
		t.Fatalf("worker-executor stdout = %q, want apply-ddl command", output)
	}
	if !strings.Contains(output, "--ddl-file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/tidb-ddl/dbo.orders.sql") {
		t.Fatalf("worker-executor stdout = %q, want ddl file", output)
	}
	if !strings.Contains(output, "--target-connection-string-env TIDB_DDL_DSN") {
		t.Fatalf("worker-executor stdout = %q, want target connection env", output)
	}
}

func TestRunWorkerReconcileDryRunCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
        }
      ]
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{
		"generate-schema-draft",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-schema-draft code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"generate-data-plans",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--object-uri-prefix", "https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-data-plans code = %d, stderr = %s", code, stderr.String())
	}
	defaultExportPlan := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/export-plan.yaml")
	if !strings.Contains(defaultExportPlan, "format: csv") {
		t.Fatalf("default export plan = %s, want format: csv", defaultExportPlan)
	}
	defaultImportPlan := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml")
	if !strings.Contains(defaultImportPlan, "engine: sql-insert") {
		t.Fatalf("default import plan = %s, want engine: sql-insert", defaultImportPlan)
	}
	reviewCLIExportPlanPredicates(t, root)
	setCLIReviewPlanStatus(t, root, "export", "reviewed")
	if code := Run([]string{
		"generate-cdc-plan",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-cdc-plan code = %d, stderr = %s", code, stderr.String())
	}
	setCLIReviewPlanStatus(t, root, "cdc", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash export code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "export", parsePayloadHash(t, stdout.String()))

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "cdc",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash cdc code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "cdc", parsePayloadHash(t, stdout.String()))

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-reconcile",
		"--root", root,
		"--dry-run",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-reconcile code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "worker reconcile dry run") {
		t.Fatalf("worker-reconcile stdout = %q, want dry-run header", stdout.String())
	}
	if !strings.Contains(stdout.String(), "projects: 1") {
		t.Fatalf("worker-reconcile stdout = %q, want project count", stdout.String())
	}
	if !strings.Contains(stdout.String(), "ready actions: 2") {
		t.Fatalf("worker-reconcile stdout = %q, want ready count", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[ready] prod-sqlserver-a/sales-db-to-tidb-prod-a export") {
		t.Fatalf("worker-reconcile stdout = %q, want ready export action", stdout.String())
	}
	if !strings.Contains(stdout.String(), "command: sqlserver2tidb worker-export") {
		t.Fatalf("worker-reconcile stdout = %q, want worker-export command", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[ready] prod-sqlserver-a/sales-db-to-tidb-prod-a cdc") {
		t.Fatalf("worker-reconcile stdout = %q, want ready cdc action", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[blocked] prod-sqlserver-a/sales-db-to-tidb-prod-a import") {
		t.Fatalf("worker-reconcile stdout = %q, want blocked import action", stdout.String())
	}
	if !strings.Contains(stdout.String(), "import approval is not approved") {
		t.Fatalf("worker-reconcile stdout = %q, want import block reason", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-reconcile",
		"--root", root,
		"--dry-run",
		"--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-reconcile json code = %d, stderr = %s", code, stderr.String())
	}
	var report struct {
		Projects       int `json:"projects"`
		ReadyActions   int `json:"ready_actions"`
		BlockedActions int `json:"blocked_actions"`
		Actions        []struct {
			SourceClusterID string `json:"source_cluster_id"`
			ProjectID       string `json:"project_id"`
			Stage           string `json:"stage"`
			Status          string `json:"status"`
			Reason          string `json:"reason"`
			Command         string `json:"command"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("worker-reconcile json stdout = %q, unmarshal error = %v", stdout.String(), err)
	}
	if report.Projects != 1 || report.ReadyActions != 2 || report.BlockedActions == 0 {
		t.Fatalf("worker-reconcile json report = %+v, want project/ready/blocked counts", report)
	}
	if len(report.Actions) == 0 || report.Actions[0].SourceClusterID != "prod-sqlserver-a" {
		t.Fatalf("worker-reconcile json report = %+v, want source cluster id", report)
	}
	if strings.Contains(stdout.String(), "worker reconcile dry run") {
		t.Fatalf("worker-reconcile json stdout = %q, should not include text header", stdout.String())
	}
}

func TestRunWorkerReconcileExecuteNextCommand(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"init-repo", "--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
        }
      ]
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{
		"generate-data-plans",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--object-uri-prefix", "https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-data-plans code = %d, stderr = %s", code, stderr.String())
	}
	reviewCLIExportPlanPredicates(t, root)
	setCLIReviewPlanStatus(t, root, "export", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash export code = %d, stderr = %s", code, stderr.String())
	}
	exportPayloadHash := parsePayloadHash(t, stdout.String())
	writeCLIStageApproval(t, root, "export", exportPayloadHash)

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-reconcile",
		"--root", root,
		"--execute-next",
		"--holder", "agent-a",
		"--state-pr-draft",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-reconcile execute-next code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "worker reconcile execute next") {
		t.Fatalf("worker-reconcile stdout = %q, want execute-next header", stdout.String())
	}
	if !strings.Contains(stdout.String(), "selected: prod-sqlserver-a/sales-db-to-tidb-prod-a export") {
		t.Fatalf("worker-reconcile stdout = %q, want selected export", stdout.String())
	}
	if !strings.Contains(stdout.String(), "status: planned") {
		t.Fatalf("worker-reconcile stdout = %q, want planned status", stdout.String())
	}
	if !strings.Contains(stdout.String(), "wrote state/worker-lease.yaml") {
		t.Fatalf("worker-reconcile stdout = %q, want lease write", stdout.String())
	}
	if !strings.Contains(stdout.String(), "state PR draft: clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/reconcile-export-state-pr.md") {
		t.Fatalf("worker-reconcile stdout = %q, want state PR draft path", stdout.String())
	}
	if !strings.Contains(stdout.String(), "branch: agent/sales-db-to-tidb-prod-a/reconcile-export-state") {
		t.Fatalf("worker-reconcile stdout = %q, want state PR draft branch", stdout.String())
	}
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml")
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/evidence/precheck.json")
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/reconcile-export-state-pr.md")
	prBody, err := os.ReadFile(filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "prs", "reconcile-export-state-pr.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prBody), "[worker-state:export] sales-db-to-tidb-prod-a") {
		t.Fatalf("state PR draft = %q, want worker-state title", string(prBody))
	}
	executorEvidenceRel := filepath.Join("clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "evidence", "executor-export-run.json")
	if err := os.WriteFile(filepath.Join(root, executorEvidenceRel), []byte(`{
  "stage": "export",
  "status": "succeeded",
  "project_id": "sales-db-to-tidb-prod-a",
  "source_cluster_id": "prod-sqlserver-a",
  "payload_hash": "`+exportPayloadHash+`",
  "commands": [
    {
      "id": "export:dbo.orders:chunk-0001",
      "args": ["sqlserver2tidb-executor", "export", "--execute"],
      "shell_command": "sqlserver2tidb-executor export --execute",
      "exit_code": 0,
      "output": "exported\n",
      "started_at": "2026-01-02T03:04:05Z",
      "completed_at": "2026-01-02T03:04:06Z",
      "duration_ms": 1000,
      "data_rows": 2,
      "data_bytes": 128,
      "data_sha256": "sha256:1111111111111111111111111111111111111111111111111111111111111111"
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	lease, err := os.ReadFile(filepath.Join(root, "clusters", "prod-sqlserver-a", "state", "worker-lease.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lease), "holder: agent-a") {
		t.Fatalf("worker lease = %q, want holder agent-a", string(lease))
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"create-worker-state-pr",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("create-worker-state-pr code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "dry run: not changing git or calling GitHub") {
		t.Fatalf("create-worker-state-pr stdout = %q, want dry-run message", output)
	}
	if !strings.Contains(output, "git switch -c agent/sales-db-to-tidb-prod-a/reconcile-export-state") {
		t.Fatalf("create-worker-state-pr stdout = %q, want git switch command", output)
	}
	if !strings.Contains(output, "git commit -m '[worker-state:export] sales-db-to-tidb-prod-a'") {
		t.Fatalf("create-worker-state-pr stdout = %q, want git commit command", output)
	}
	if !strings.Contains(output, "git push -u origin agent/sales-db-to-tidb-prod-a/reconcile-export-state") {
		t.Fatalf("create-worker-state-pr stdout = %q, want git push command", output)
	}
	if !strings.Contains(output, "gh pr create --base main --head agent/sales-db-to-tidb-prod-a/reconcile-export-state") {
		t.Fatalf("create-worker-state-pr stdout = %q, want gh pr command", output)
	}
	if !strings.Contains(output, filepath.ToSlash(executorEvidenceRel)) {
		t.Fatalf("create-worker-state-pr stdout = %q, want executor evidence in git add command", output)
	}
	if !strings.Contains(output, "body file update: needed; execute mode refreshes it before commit") {
		t.Fatalf("create-worker-state-pr stdout = %q, want body update notice", output)
	}
	prBodyAfterDryRun, err := os.ReadFile(filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "prs", "reconcile-export-state-pr.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(prBodyAfterDryRun) != string(prBody) {
		t.Fatal("create-worker-state-pr dry-run mutated the PR body")
	}
}

func TestRunWorkerReconcileLoopExecutesUntilNoReadyMetadataActions(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)
	reviewCLIExportPlanPredicates(t, root)
	setCLIReviewPlanStatus(t, root, "export", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash export code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "export", parsePayloadHash(t, stdout.String()))

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-reconcile",
		"--root", root,
		"--loop",
		"--max-iterations", "3",
		"--interval", "1ms",
		"--holder", "agent-a",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-reconcile loop code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "worker reconcile loop") {
		t.Fatalf("worker-reconcile loop stdout = %q, want loop header", output)
	}
	if !strings.Contains(output, "iteration 1: selected prod-sqlserver-a/sales-db-to-tidb-prod-a export") {
		t.Fatalf("worker-reconcile loop stdout = %q, want selected export", output)
	}
	if !strings.Contains(output, "iteration 2: no ready worker actions") {
		t.Fatalf("worker-reconcile loop stdout = %q, want no ready stop", output)
	}
	if !strings.Contains(output, "executed actions: 1") {
		t.Fatalf("worker-reconcile loop stdout = %q, want executed count", output)
	}
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml")
}

func TestRunWorkerAgentExecutesReconcileLoop(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)
	reviewCLIExportPlanPredicates(t, root)
	setCLIReviewPlanStatus(t, root, "export", "reviewed")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"compute-payload-hash",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "export",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("compute-payload-hash export code = %d, stderr = %s", code, stderr.String())
	}
	writeCLIStageApproval(t, root, "export", parsePayloadHash(t, stdout.String()))

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-agent",
		"--root", root,
		"--max-iterations", "3",
		"--interval", "1ms",
		"--holder", "agent-a",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-agent code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "worker agent") {
		t.Fatalf("worker-agent stdout = %q, want agent header", output)
	}
	if !strings.Contains(output, "worker reconcile loop") {
		t.Fatalf("worker-agent stdout = %q, want reconcile loop output", output)
	}
	if !strings.Contains(output, "iteration 1: selected prod-sqlserver-a/sales-db-to-tidb-prod-a export") {
		t.Fatalf("worker-agent stdout = %q, want selected export", output)
	}
	if !strings.Contains(output, "iteration 2: no ready worker actions") {
		t.Fatalf("worker-agent stdout = %q, want no ready stop", output)
	}
	if !strings.Contains(output, "executed actions: 1") {
		t.Fatalf("worker-agent stdout = %q, want executed count", output)
	}
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/export-chunks.yaml")
}

func TestRunWorkerAgentPollsWhenNoReadyActions(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"worker-agent",
		"--root", root,
		"--holder", "agent-a",
		"--poll",
		"--idle-iterations", "2",
		"--interval", "1ms",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-agent poll code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "worker agent poll") {
		t.Fatalf("worker-agent poll stdout = %q, want poll header", output)
	}
	if !strings.Contains(output, "idle iteration 1: no ready worker actions") {
		t.Fatalf("worker-agent poll stdout = %q, want first idle iteration", output)
	}
	if !strings.Contains(output, "idle iteration 2: no ready worker actions") {
		t.Fatalf("worker-agent poll stdout = %q, want second idle iteration", output)
	}
	if !strings.Contains(output, "executed actions: 0") {
		t.Fatalf("worker-agent poll stdout = %q, want executed count", output)
	}
}

func TestRunWorkerAgentFiltersSourceCluster(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIReadyExportProjectForCluster(t, root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a")
	createCLIReadyExportProjectForCluster(t, root, "prod-sqlserver-b", "billing-db-to-tidb-prod-b")

	code := Run([]string{
		"worker-reconcile",
		"--root", root,
		"--dry-run",
		"--json",
		"--source-cluster-id", "prod-sqlserver-b",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-reconcile filtered json code = %d, stderr = %s", code, stderr.String())
	}
	var report struct {
		Projects int `json:"projects"`
		Actions  []struct {
			SourceClusterID string `json:"source_cluster_id"`
			ProjectID       string `json:"project_id"`
			Stage           string `json:"stage"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("worker-reconcile filtered json stdout = %q, unmarshal error = %v", stdout.String(), err)
	}
	if report.Projects != 1 {
		t.Fatalf("filtered report projects = %d, want 1", report.Projects)
	}
	for _, action := range report.Actions {
		if action.SourceClusterID != "prod-sqlserver-b" || action.ProjectID != "billing-db-to-tidb-prod-b" {
			t.Fatalf("filtered action = %+v, want only prod-sqlserver-b/billing-db-to-tidb-prod-b", action)
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"worker-agent",
		"--root", root,
		"--holder", "agent-b",
		"--source-cluster-id", "prod-sqlserver-b",
		"--max-iterations", "1",
		"--interval", "1ms",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("worker-agent filtered code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "iteration 1: selected prod-sqlserver-b/billing-db-to-tidb-prod-b export") {
		t.Fatalf("worker-agent filtered stdout = %q, want selected prod-sqlserver-b export", output)
	}
	leaseA := readCLIRelFile(t, root, "clusters/prod-sqlserver-a/state/worker-lease.yaml")
	if strings.Contains(leaseA, "holder: agent-b") {
		t.Fatalf("prod-sqlserver-a lease = %q, should not be touched by filtered worker-agent", leaseA)
	}
	leaseB := readCLIRelFile(t, root, "clusters/prod-sqlserver-b/state/worker-lease.yaml")
	if !strings.Contains(leaseB, "holder: agent-b") || !strings.Contains(leaseB, "project_id: billing-db-to-tidb-prod-b") {
		t.Fatalf("prod-sqlserver-b lease = %q, want holder/project", leaseB)
	}
}

func TestRunExecutorEvidencePRDraftAndCreateDryRunCommands(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{
		"generate-schema-draft",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-schema-draft code = %d, stderr = %s", code, stderr.String())
	}
	setCLISchemaDiffStatus(t, root, "reviewed")
	hash, err := gitops.ComputePayloadHashForStage(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", "ddl")
	if err != nil {
		t.Fatalf("ComputePayloadHashForStage(ddl) error = %v", err)
	}
	writeCLIStageApproval(t, root, "ddl", hash)
	evidenceRel := filepath.Join("clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "evidence", "executor-ddl-run.json")
	if err := os.WriteFile(filepath.Join(root, evidenceRel), []byte(`{
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
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"generate-executor-evidence-pr-draft",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "ddl",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("generate-executor-evidence-pr-draft code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "executor evidence PR draft generated") {
		t.Fatalf("generate-executor-evidence-pr-draft stdout = %q, want generated message", output)
	}
	if !strings.Contains(output, "body file: clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/executor-ddl-evidence-pr.md") {
		t.Fatalf("generate-executor-evidence-pr-draft stdout = %q, want body file", output)
	}
	assertExists(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/executor-ddl-evidence-pr.md")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"create-executor-evidence-pr",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--stage", "ddl",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("create-executor-evidence-pr code = %d, stderr = %s", code, stderr.String())
	}
	output = stdout.String()
	if !strings.Contains(output, "dry run: not changing git or calling GitHub") {
		t.Fatalf("create-executor-evidence-pr stdout = %q, want dry-run message", output)
	}
	if !strings.Contains(output, "git switch -c agent/sales-db-to-tidb-prod-a/executor-ddl-evidence") {
		t.Fatalf("create-executor-evidence-pr stdout = %q, want git switch command", output)
	}
	if !strings.Contains(output, "git commit -m '[executor-evidence:ddl] sales-db-to-tidb-prod-a'") {
		t.Fatalf("create-executor-evidence-pr stdout = %q, want git commit command", output)
	}
	if !strings.Contains(output, "gh pr create --base main --head agent/sales-db-to-tidb-prod-a/executor-ddl-evidence") {
		t.Fatalf("create-executor-evidence-pr stdout = %q, want gh pr command", output)
	}
	if !strings.Contains(output, filepath.ToSlash(evidenceRel)) {
		t.Fatalf("create-executor-evidence-pr stdout = %q, want executor evidence file", output)
	}
}

func TestRunUnknownCommandReturnsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"unknown"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("Run() expected non-zero code for unknown command")
	}
	if stderr.Len() == 0 {
		t.Fatal("Run() expected stderr for unknown command")
	}
}

func TestRunHelpUsesExecutableCSVDataPlanExample(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("help code = %d, stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "--object-uri-prefix https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full") {
		t.Fatalf("help output = %q, want HTTP(S) CSV object URI prefix example", output)
	}
	if strings.Contains(output, "s3://bucket/prefix") {
		t.Fatalf("help output = %q, still contains unsupported s3 example", output)
	}
}

func createCLIProjectWithOneExportChunk(t *testing.T, root string, stdout, stderr *bytes.Buffer) {
	t.Helper()
	if code := Run([]string{"init-repo", "--root", root}, stdout, stderr); code != 0 {
		t.Fatalf("init-repo code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-cluster",
		"--root", root,
		"--cluster-id", "prod-sqlserver-a",
		"--display-name", "prod SQL Server A",
		"--listener", "sqlserver-a.internal",
		"--secret-ref", "vault://migration/prod-sqlserver-a/readonly",
		"--owner", "dba-team",
	}, stdout, stderr); code != 0 {
		t.Fatalf("create-cluster code = %d, stderr = %s", code, stderr.String())
	}
	if code := Run([]string{
		"create-project",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--display-name", "sales DB to TiDB prod A",
		"--source-database", "sales",
		"--source-schema", "dbo",
		"--target-name", "tidb-prod-a",
		"--target-database", "app",
		"--target-secret-ref", "vault://migration/tidb-prod-a/migrate-user",
		"--owner", "dba-team",
	}, stdout, stderr); code != 0 {
		t.Fatalf("create-project code = %d, stderr = %s", code, stderr.String())
	}
	inventoryPath := filepath.Join(root, "clusters", "prod-sqlserver-a", "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
              "row_count": 1,
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
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{
		"generate-data-plans",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
		"--object-uri-prefix", "file:///tmp/sqlserver2tidb-test/full",
		"--chunk-size-rows", "1000",
	}, stdout, stderr); code != 0 {
		t.Fatalf("generate-data-plans code = %d, stderr = %s", code, stderr.String())
	}
}

func createCLIReadyExportProjectForCluster(t *testing.T, root, sourceClusterID, projectID string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "global")); os.IsNotExist(err) {
		if err := gitops.InitRepo(root); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if err := gitops.CreateCluster(root, gitops.ClusterSpec{
		ClusterID:              sourceClusterID,
		DisplayName:            sourceClusterID,
		Listener:               sourceClusterID + ".internal",
		Port:                   1433,
		SecretRef:              "vault://migration/" + sourceClusterID + "/readonly",
		CDCMode:                "sqlserver-cdc",
		RetentionHoursRequired: 168,
		Owners:                 []string{"dba-team"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := gitops.CreateProject(root, gitops.ProjectSpec{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		DisplayName:     projectID,
		SourceDatabase:  "sales",
		SourceSchemas:   []string{"dbo"},
		TargetName:      "tidb-prod",
		TargetDatabase:  "app",
		TargetSecretRef: "vault://migration/tidb-prod/migrate-user",
		Mode:            "short-downtime",
		Owners:          []string{"dba-team"},
	}); err != nil {
		t.Fatal(err)
	}
	inventoryPath := filepath.Join(root, "clusters", sourceClusterID, "inventory", "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{
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
              "row_count": 1,
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
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitops.GenerateDataMovementPlans(root, sourceClusterID, projectID, gitops.DataMovementPlanSpec{
		ObjectURIPrefix: "file:///tmp/sqlserver2tidb-test/" + sourceClusterID + "/" + projectID + "/full",
		ChunkSizeRows:   1000,
		ExportFormat:    "csv",
		ImportEngine:    "sql-insert",
	}); err != nil {
		t.Fatal(err)
	}
	setCLIReviewPlanStatusForProject(t, root, sourceClusterID, projectID, "export", "reviewed")
	hash, err := gitops.ComputePayloadHashForStage(root, sourceClusterID, projectID, "export")
	if err != nil {
		t.Fatal(err)
	}
	writeCLIStageApprovalForProject(t, root, sourceClusterID, projectID, "export", hash)
}

func assertExists(t *testing.T, root, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("expected %s to exist: %v", rel, err)
	}
}

func readCLIRelFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func parsePayloadHash(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "payload hash: ") {
			return strings.TrimPrefix(line, "payload hash: ")
		}
	}
	t.Fatalf("payload hash not found in output:\n%s", output)
	return ""
}

func writeCLIValidationApproval(t *testing.T, root, payloadHash string) {
	t.Helper()
	writeCLIStageApproval(t, root, "validation", payloadHash)
}

func reviewCLIExportPlanPredicates(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "plan", "export-plan.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var reviewed strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "predicate: \"TODO: choose stable split predicate") {
			prefix := line[:strings.Index(line, "predicate:")]
			line = prefix + "predicate: id >= 0"
		}
		reviewed.WriteString(line)
		reviewed.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(reviewed.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setCLIReviewPlanStatus(t *testing.T, root, stage, status string) {
	t.Helper()
	setCLIReviewPlanStatusForProject(t, root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", stage, status)
}

func setCLIReviewPlanStatusForProject(t *testing.T, root, sourceClusterID, projectID, stage, status string) {
	t.Helper()
	path := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID, "plan", stage+"-plan.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plan := string(data)
	updated := strings.Replace(plan, "status: draft", "status: "+status, 1)
	if updated == plan {
		t.Fatalf("plan %s does not contain draft status", path)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setCLICDCPlanLSNRange(t *testing.T, root, fromLSN, toLSN string) {
	t.Helper()
	path := filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "plan", "cdc-plan.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plan := string(data)
	updated := strings.Replace(plan, `from_lsn: ""`, "from_lsn: "+fromLSN, 1)
	updated = strings.Replace(updated, `to_lsn: ""`, "to_lsn: "+toLSN, 1)
	if updated == plan {
		t.Fatalf("cdc plan %s does not contain empty LSN placeholders", path)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setCLISchemaDiffStatus(t *testing.T, root, status string) {
	t.Helper()
	path := filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "schema", "schema-diff.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	diff := string(data)
	updated := strings.Replace(diff, `"status": "draft-generated"`, `"status": "`+status+`"`, 1)
	if updated == diff {
		t.Fatalf("schema diff %s does not contain draft-generated status", path)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCLIStageApproval(t *testing.T, root, stage, payloadHash string) {
	t.Helper()
	writeCLIStageApprovalForProject(t, root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", stage, payloadHash)
}

func writeCLIStageApprovalForProject(t *testing.T, root, sourceClusterID, projectID, stage, payloadHash string) {
	t.Helper()
	path := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID, "approvals", stage+"-approval.yaml")
	content := `approval_id: ` + stage + `-cli-test
project_id: ` + projectID + `
source_cluster_id: ` + sourceClusterID + `
action: ` + stage + `
payload_hash: ` + payloadHash + `
required_reviewers:
  - dba-team
approved_by:
  - dba-team
status: approved
approved_at: "2026-06-26T00:00:00Z"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stubLookPath(paths map[string]string) func() {
	old := lookPath
	lookPath = func(file string) (string, error) {
		if path, ok := paths[file]; ok {
			return path, nil
		}
		return "", exec.ErrNotFound
	}
	return func() {
		lookPath = old
	}
}
