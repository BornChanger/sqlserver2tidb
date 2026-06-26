package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		"--object-uri-prefix", "s3://migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
		"--chunk-size-rows", "1000000",
		"--export-format", "parquet",
		"--import-engine", "import-into",
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
		"--object-uri-prefix", "s3://migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
		"--chunk-size-rows", "1000000",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-data-plans code = %d, stderr = %s", code, stderr.String())
	}
	reviewCLIExportPlanPredicates(t, root)

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
	if err := os.WriteFile(fakeExecutor, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> executor-args.log\nprintf 'fake executor completed: %s\\n' \"$1\"\n"), 0o755); err != nil {
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
	if !strings.Contains(evidence, `fake executor completed: export`) {
		t.Fatalf("executor evidence = %q, want executor output", evidence)
	}
	if !strings.Contains(stdout.String(), "wrote evidence/executor-export-run.json") {
		t.Fatalf("worker-executor stdout = %q, want evidence path", stdout.String())
	}
}

func TestRunWorkerExecutorExecuteWritesFailedEvidenceOnCommandFailure(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	createCLIProjectWithOneExportChunk(t, root, &stdout, &stderr)
	reviewCLIExportPlanPredicates(t, root)

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
	if err := os.WriteFile(validationPlanPath, []byte(`status: draft
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
		"--object-uri-prefix", "s3://migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-data-plans code = %d, stderr = %s", code, stderr.String())
	}
	reviewCLIExportPlanPredicates(t, root)
	if code := Run([]string{
		"generate-cdc-plan",
		"--root", root,
		"--source-cluster-id", "prod-sqlserver-a",
		"--project-id", "sales-db-to-tidb-prod-a",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-cdc-plan code = %d, stderr = %s", code, stderr.String())
	}

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
		"--object-uri-prefix", "s3://migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate-data-plans code = %d, stderr = %s", code, stderr.String())
	}
	reviewCLIExportPlanPredicates(t, root)

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
  "status": "succeeded"
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
  "payload_hash": "`+hash+`"
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

func writeCLIStageApproval(t *testing.T, root, stage, payloadHash string) {
	t.Helper()
	path := filepath.Join(root, "clusters", "prod-sqlserver-a", "projects", "sales-db-to-tidb-prod-a", "approvals", stage+"-approval.yaml")
	content := `approval_id: ` + stage + `-cli-test
project_id: sales-db-to-tidb-prod-a
source_cluster_id: prod-sqlserver-a
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
