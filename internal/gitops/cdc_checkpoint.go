package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CDCCheckpointAdvanceSpec struct {
	Status string
}

type CDCCheckpointAdvanceResult struct {
	SourceClusterID string
	ProjectID       string
	PayloadHash     string
	Status          string
	CheckpointFile  string
	UpdatedTables   int
	AppliedChanges  int
}

type cdcCheckpointSnapshot struct {
	SourceObject   string
	TargetObject   string
	FromLSN        string
	ToLSN          string
	AppliedChanges int
	CompletedAt    string
}

func AdvanceCDCCheckpointFromExecutorEvidence(root, sourceClusterID, projectID string, spec CDCCheckpointAdvanceSpec) (CDCCheckpointAdvanceResult, error) {
	ctx, err := loadExecutorEvidencePRContext(root, sourceClusterID, projectID, "cdc")
	if err != nil {
		return CDCCheckpointAdvanceResult{}, err
	}
	if ctx.evidence.Status != "succeeded" {
		return CDCCheckpointAdvanceResult{}, fmt.Errorf("cdc executor evidence status is %q, want succeeded", ctx.evidence.Status)
	}
	status := strings.TrimSpace(spec.Status)
	if status == "" {
		status = "running"
	}
	if !isSupportedCDCCheckpointStatus(status) {
		return CDCCheckpointAdvanceResult{}, fmt.Errorf("unsupported CDC checkpoint status %q; supported statuses: not_started, planned, running, caught_up, failed", status)
	}

	projectDir := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID)
	plan, err := readCDCPlanSummary(filepath.Join(projectDir, "plan", "cdc-plan.yaml"))
	if err != nil {
		return CDCCheckpointAdvanceResult{}, err
	}
	planTables := make(map[string]cdcTrackedTableState, len(plan.Tables))
	for _, table := range plan.Tables {
		planTables[table.SourceObject] = table
	}

	snapshots := make([]cdcCheckpointSnapshot, 0, len(ctx.evidence.Commands))
	appliedChanges := 0
	for _, command := range ctx.evidence.Commands {
		snapshot, err := cdcCheckpointSnapshotFromEvidenceCommand(command)
		if err != nil {
			return CDCCheckpointAdvanceResult{}, err
		}
		planTable, ok := planTables[snapshot.SourceObject]
		if !ok {
			return CDCCheckpointAdvanceResult{}, fmt.Errorf("cdc executor evidence command %s source_object %s is not tracked by the current CDC plan", command.ID, snapshot.SourceObject)
		}
		if snapshot.TargetObject != planTable.TargetObject {
			return CDCCheckpointAdvanceResult{}, fmt.Errorf("cdc executor evidence command %s target_object %s does not match current CDC plan target_object %s", command.ID, snapshot.TargetObject, planTable.TargetObject)
		}
		if snapshot.FromLSN != planTable.FromLSN {
			return CDCCheckpointAdvanceResult{}, fmt.Errorf("cdc executor evidence command %s from_lsn %s does not match current CDC plan from_lsn %s", command.ID, snapshot.FromLSN, planTable.FromLSN)
		}
		if snapshot.ToLSN != planTable.ToLSN {
			return CDCCheckpointAdvanceResult{}, fmt.Errorf("cdc executor evidence command %s to_lsn %s does not match current CDC plan to_lsn %s", command.ID, snapshot.ToLSN, planTable.ToLSN)
		}
		snapshots = append(snapshots, snapshot)
		appliedChanges += snapshot.AppliedChanges
	}

	rel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "state", "cdc-checkpoint.yaml"))
	if err := writeAdvancedCDCCheckpoint(filepath.Join(root, filepath.FromSlash(rel)), ctx, plan, status, snapshots); err != nil {
		return CDCCheckpointAdvanceResult{}, err
	}
	return CDCCheckpointAdvanceResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		PayloadHash:     ctx.payloadHash,
		Status:          status,
		CheckpointFile:  rel,
		UpdatedTables:   len(snapshots),
		AppliedChanges:  appliedChanges,
	}, nil
}

func cdcCheckpointSnapshotFromEvidenceCommand(command executorEvidenceCommandSummary) (cdcCheckpointSnapshot, error) {
	if command.CDCAppliedChanges == nil {
		return cdcCheckpointSnapshot{}, fmt.Errorf("cdc executor evidence command %s cdc_applied_changes is required to advance checkpoint", command.ID)
	}
	sourceObject, err := executorEvidenceArgValue(command.Args, "--source-object")
	if err != nil {
		return cdcCheckpointSnapshot{}, fmt.Errorf("cdc executor evidence command %s: %w", command.ID, err)
	}
	targetObject, err := executorEvidenceArgValue(command.Args, "--target-object")
	if err != nil {
		return cdcCheckpointSnapshot{}, fmt.Errorf("cdc executor evidence command %s: %w", command.ID, err)
	}
	fromLSN, err := executorEvidenceArgValue(command.Args, "--from-lsn")
	if err != nil {
		return cdcCheckpointSnapshot{}, fmt.Errorf("cdc executor evidence command %s: %w", command.ID, err)
	}
	if err := validateCDCPlanLSN(fromLSN, "from_lsn"); err != nil {
		return cdcCheckpointSnapshot{}, fmt.Errorf("cdc executor evidence command %s: %w", command.ID, err)
	}
	toLSN, err := executorEvidenceArgValue(command.Args, "--to-lsn")
	if err != nil {
		return cdcCheckpointSnapshot{}, fmt.Errorf("cdc executor evidence command %s: %w", command.ID, err)
	}
	if err := validateCDCPlanLSN(toLSN, "to_lsn"); err != nil {
		return cdcCheckpointSnapshot{}, fmt.Errorf("cdc executor evidence command %s: %w", command.ID, err)
	}
	if err := validateCDCPlanLSNRange(fromLSN, toLSN); err != nil {
		return cdcCheckpointSnapshot{}, fmt.Errorf("cdc executor evidence command %s: %w", command.ID, err)
	}
	return cdcCheckpointSnapshot{
		SourceObject:   sourceObject,
		TargetObject:   targetObject,
		FromLSN:        fromLSN,
		ToLSN:          toLSN,
		AppliedChanges: *command.CDCAppliedChanges,
		CompletedAt:    command.CompletedAt,
	}, nil
}

func executorEvidenceArgValue(args []string, name string) (string, error) {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == name {
			value := strings.TrimSpace(args[i+1])
			if value == "" {
				return "", fmt.Errorf("%s is required", name)
			}
			return value, nil
		}
	}
	return "", fmt.Errorf("%s is required", name)
}

func writeAdvancedCDCCheckpoint(path string, ctx executorEvidencePRContext, plan cdcPlanSummary, status string, snapshots []cdcCheckpointSnapshot) error {
	var b strings.Builder
	fmt.Fprintf(&b, "source_cluster_id: %s\n", ctx.sourceClusterID)
	b.WriteString("phase: cdc\n")
	fmt.Fprintf(&b, "status: %s\n", status)
	fmt.Fprintf(&b, "project_id: %s\n", ctx.projectID)
	fmt.Fprintf(&b, "payload_hash: %s\n", ctx.payloadHash)
	fmt.Fprintf(&b, "mode: %s\n", plan.Mode)
	b.WriteString("checkpoint_scope: source-cluster\n")
	b.WriteString("checkpoints:\n")
	for _, snapshot := range snapshots {
		fmt.Fprintf(&b, "  - source_object: %s\n", snapshot.SourceObject)
		fmt.Fprintf(&b, "    target_object: %s\n", snapshot.TargetObject)
		fmt.Fprintf(&b, "    from_lsn: %s\n", snapshot.FromLSN)
		fmt.Fprintf(&b, "    to_lsn: %s\n", snapshot.ToLSN)
		fmt.Fprintf(&b, "    applied_changes: %d\n", snapshot.AppliedChanges)
		fmt.Fprintf(&b, "    completed_at: %s\n", quoteYAML(snapshot.CompletedAt))
	}
	fmt.Fprintf(&b, "updated_at: %s\n", quoteYAML(nowUTC()))
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write cdc checkpoint: %w", err)
	}
	return nil
}
