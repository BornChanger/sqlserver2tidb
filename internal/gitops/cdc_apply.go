package gitops

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

type CDCPlanApplyStatus struct {
	Needed bool
	Reason string
	Tables int
}

type CDCPlanApplyStatusSpec struct {
	MinLSNs map[string]string
}

func CheckCDCPlanApplyStatus(root, sourceClusterID, projectID string) (CDCPlanApplyStatus, error) {
	return CheckCDCPlanApplyStatusWithSpec(root, sourceClusterID, projectID, CDCPlanApplyStatusSpec{})
}

func CheckCDCPlanApplyStatusWithSpec(root, sourceClusterID, projectID string, spec CDCPlanApplyStatusSpec) (CDCPlanApplyStatus, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return CDCPlanApplyStatus{}, err
	}
	projectDir := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID)
	plan, err := readCDCPlanSummary(filepath.Join(projectDir, "plan", "cdc-plan.yaml"))
	if err != nil {
		return CDCPlanApplyStatus{}, err
	}
	if err := validateCDCPlanSummaryForExecutor(plan); err != nil {
		return CDCPlanApplyStatus{}, err
	}
	checkpointEntries, err := readCDCCheckpointEntries(filepath.Join(root, "clusters", sourceClusterID, "state", "cdc-checkpoint.yaml"))
	if err != nil {
		return CDCPlanApplyStatus{}, err
	}
	checkpoints := make(map[string]cdcCheckpointEntry, len(checkpointEntries))
	for _, entry := range checkpointEntries {
		if strings.TrimSpace(entry.SourceObject) != "" {
			checkpoints[entry.SourceObject] = entry
		}
	}

	needsApply := false
	for _, table := range plan.Tables {
		toLSNBytes, err := parseCDCPlanLSN(table.ToLSN, "to_lsn")
		if err != nil {
			return CDCPlanApplyStatus{}, fmt.Errorf("cdc tracked table %s %w", table.SourceObject, err)
		}
		checkpoint, ok := checkpoints[table.SourceObject]
		if !ok || strings.TrimSpace(checkpoint.ToLSN) == "" {
			if err := requireCDCMinLSNCoversFromLSN(table.SourceObject, table.FromLSN, spec.MinLSNs); err != nil {
				return CDCPlanApplyStatus{}, err
			}
			needsApply = true
			continue
		}
		if strings.TrimSpace(checkpoint.TargetObject) != "" && checkpoint.TargetObject != table.TargetObject {
			return CDCPlanApplyStatus{}, fmt.Errorf("cdc checkpoint target_object %s for %s does not match plan target_object %s", checkpoint.TargetObject, table.SourceObject, table.TargetObject)
		}
		checkpointLSNBytes, err := parseCDCPlanLSN(checkpoint.ToLSN, "checkpoint to_lsn")
		if err != nil {
			return CDCPlanApplyStatus{}, fmt.Errorf("cdc checkpoint for %s %w", table.SourceObject, err)
		}
		if bytes.Compare(checkpointLSNBytes, toLSNBytes) >= 0 {
			continue
		}
		if strings.TrimSpace(checkpoint.ToLSN) != strings.TrimSpace(table.FromLSN) {
			return CDCPlanApplyStatus{}, fmt.Errorf("cdc tracked table %s checkpoint to_lsn %s does not match plan from_lsn %s", table.SourceObject, checkpoint.ToLSN, table.FromLSN)
		}
		if err := requireCDCMinLSNCoversFromLSN(table.SourceObject, table.FromLSN, spec.MinLSNs); err != nil {
			return CDCPlanApplyStatus{}, err
		}
		needsApply = true
	}
	if !needsApply {
		return CDCPlanApplyStatus{
			Needed: false,
			Reason: "current CDC range already checkpointed",
			Tables: len(plan.Tables),
		}, nil
	}
	return CDCPlanApplyStatus{
		Needed: true,
		Tables: len(plan.Tables),
	}, nil
}

func requireCDCMinLSNCoversFromLSN(sourceObject, fromLSN string, minLSNs map[string]string) error {
	minLSN := strings.TrimSpace(minLSNs[sourceObject])
	if minLSN == "" {
		return nil
	}
	fromLSNBytes, err := parseCDCPlanLSN(fromLSN, "from_lsn")
	if err != nil {
		return fmt.Errorf("cdc tracked table %s %w", sourceObject, err)
	}
	minLSNBytes, err := parseCDCPlanLSN(minLSN, "min_lsn")
	if err != nil {
		return fmt.Errorf("cdc tracked table %s %w", sourceObject, err)
	}
	if bytes.Compare(fromLSNBytes, minLSNBytes) < 0 {
		return fmt.Errorf("SQL Server CDC retention no longer covers %s: from_lsn %s is before min_lsn %s", sourceObject, fromLSN, minLSN)
	}
	return nil
}
