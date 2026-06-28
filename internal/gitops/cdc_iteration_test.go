package gitops

import (
	"strings"
	"testing"
)

func TestPrepareCDCIterationWritesNextRangeAndPRDraft(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateCDCPlanOnly(root))
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml", `source_cluster_id: prod-sqlserver-a
phase: cdc
status: running
project_id: sales-db-to-tidb-prod-a
mode: sqlserver-cdc
checkpoint_scope: source-cluster
checkpoints:
  - source_object: sales.dbo.orders
    target_object: app.orders
    from_lsn: 0x00000027000001f40001
    to_lsn: 0x00000027000001f40002
    applied_changes: 2
    completed_at: "2026-01-02T03:04:06Z"
updated_at: "2026-01-02T03:04:07Z"
`)

	result, err := PrepareCDCIteration(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", CDCIterationSpec{
		MaxLSN:       "0x00000027000001f40003",
		WritePRDraft: true,
	})
	if err != nil {
		t.Fatalf("PrepareCDCIteration() error = %v", err)
	}
	if result.Status != "range_prepared" || result.UpdatedTables != 1 {
		t.Fatalf("PrepareCDCIteration() result = %+v, want one prepared range", result)
	}
	if result.PlanFile != "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml" {
		t.Fatalf("PlanFile = %q, want cdc plan path", result.PlanFile)
	}
	if result.PRBodyFile != "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/cdc-range-pr.md" {
		t.Fatalf("PRBodyFile = %q, want cdc range PR draft", result.PRBodyFile)
	}
	if len(result.Ranges) != 1 {
		t.Fatalf("Ranges = %+v, want one table range", result.Ranges)
	}
	tableRange := result.Ranges[0]
	if tableRange.SourceObject != "sales.dbo.orders" || tableRange.FromLSN != "0x00000027000001f40002" || tableRange.ToLSN != "0x00000027000001f40003" {
		t.Fatalf("Range = %+v, want checkpoint-to-max range", tableRange)
	}

	plan := readFile(t, root, result.PlanFile)
	assertContains(t, plan, "status: draft")
	assertContains(t, plan, "from_lsn: 0x00000027000001f40002")
	assertContains(t, plan, "to_lsn: 0x00000027000001f40003")

	body := readFile(t, root, result.PRBodyFile)
	assertContains(t, body, "# PR Draft: [cdc-range] sales-db-to-tidb-prod-a")
	assertContains(t, body, "Source cluster: `prod-sqlserver-a`")
	assertContains(t, body, "Project: `sales-db-to-tidb-prod-a`")
	assertContains(t, body, "Max LSN: `0x00000027000001f40003`")
	assertContains(t, body, "| sales.dbo.orders | app.orders | `0x00000027000001f40002` | `0x00000027000001f40003` |")
	assertContains(t, body, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml")
}

func TestPrepareCDCIterationReportsCaughtUpWithoutMutatingPlan(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateCDCPlanOnly(root))
	writeFileForTest(t, root, "clusters/prod-sqlserver-a/state/cdc-checkpoint.yaml", `source_cluster_id: prod-sqlserver-a
phase: cdc
status: running
project_id: sales-db-to-tidb-prod-a
mode: sqlserver-cdc
checkpoint_scope: source-cluster
checkpoints:
  - source_object: sales.dbo.orders
    target_object: app.orders
    from_lsn: 0x00000027000001f40001
    to_lsn: 0x00000027000001f40002
    applied_changes: 2
    completed_at: "2026-01-02T03:04:06Z"
updated_at: "2026-01-02T03:04:07Z"
`)
	planBefore := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml")

	result, err := PrepareCDCIteration(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", CDCIterationSpec{
		MaxLSN:       "0x00000027000001f40002",
		WritePRDraft: true,
	})
	if err != nil {
		t.Fatalf("PrepareCDCIteration() error = %v", err)
	}
	if result.Status != "caught_up" || result.UpdatedTables != 0 {
		t.Fatalf("PrepareCDCIteration() result = %+v, want caught_up without updates", result)
	}
	if result.PRBodyFile != "" {
		t.Fatalf("PRBodyFile = %q, want no PR draft when caught up", result.PRBodyFile)
	}
	planAfter := readFile(t, root, "clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/cdc-plan.yaml")
	if planAfter != planBefore {
		t.Fatalf("PrepareCDCIteration() mutated plan while caught up\nbefore:\n%s\nafter:\n%s", planBefore, planAfter)
	}
}

func TestPrepareCDCIterationRequiresInitialFromLSNWithoutCheckpoint(t *testing.T) {
	root := t.TempDir()
	createValidationWorkerProject(t, root, dataWorkerInventory())
	must(t, GenerateCDCPlanOnly(root))

	_, err := PrepareCDCIteration(root, "prod-sqlserver-a", "sales-db-to-tidb-prod-a", CDCIterationSpec{
		MaxLSN: "0x00000027000001f40002",
	})
	if err == nil {
		t.Fatal("PrepareCDCIteration() error = nil, want missing initial from_lsn error")
	}
	if !strings.Contains(err.Error(), "pass --from-lsn for initial range") {
		t.Fatalf("PrepareCDCIteration() error = %v, want missing initial from_lsn guidance", err)
	}
}
