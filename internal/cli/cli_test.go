package cli

import (
	"bytes"
	"os"
	"path/filepath"
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
