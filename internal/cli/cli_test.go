package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func assertExists(t *testing.T, root, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("expected %s to exist: %v", rel, err)
	}
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
