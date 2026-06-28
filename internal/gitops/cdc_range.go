package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CDCPlanRangeSpec struct {
	FromLSN string
	ToLSN   string
}

type CDCPlanRangeResult struct {
	SourceClusterID string
	ProjectID       string
	PlanFile        string
	UpdatedTables   int
}

type cdcPlanRange struct {
	FromLSN string
	ToLSN   string
}

func PrepareCDCPlanRange(root, sourceClusterID, projectID string, spec CDCPlanRangeSpec) (CDCPlanRangeResult, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return CDCPlanRangeResult{}, err
	}
	toLSN := strings.TrimSpace(spec.ToLSN)
	if toLSN == "" {
		return CDCPlanRangeResult{}, fmt.Errorf("to_lsn is required")
	}
	if err := validateCDCPlanLSN(toLSN, "to_lsn"); err != nil {
		return CDCPlanRangeResult{}, err
	}
	fromLSNFallback := strings.TrimSpace(spec.FromLSN)
	if fromLSNFallback != "" {
		if err := validateCDCPlanLSN(fromLSNFallback, "from_lsn"); err != nil {
			return CDCPlanRangeResult{}, err
		}
	}

	planRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID, "plan", "cdc-plan.yaml"))
	planPath := filepath.Join(root, filepath.FromSlash(planRel))
	plan, err := readCDCPlanSummary(planPath)
	if err != nil {
		return CDCPlanRangeResult{}, err
	}
	if err := validateCDCPlanSummaryForExecution(plan, true); err != nil {
		return CDCPlanRangeResult{}, err
	}

	checkpointEntries, err := readCDCCheckpointEntries(filepath.Join(root, "clusters", sourceClusterID, "state", "cdc-checkpoint.yaml"))
	if err != nil {
		return CDCPlanRangeResult{}, err
	}
	checkpointToLSN := make(map[string]string, len(checkpointEntries))
	for _, entry := range checkpointEntries {
		if strings.TrimSpace(entry.SourceObject) != "" && strings.TrimSpace(entry.ToLSN) != "" {
			checkpointToLSN[entry.SourceObject] = entry.ToLSN
		}
	}

	ranges := make(map[string]cdcPlanRange, len(plan.Tables))
	for _, table := range plan.Tables {
		fromLSN := strings.TrimSpace(checkpointToLSN[table.SourceObject])
		if fromLSN == "" {
			fromLSN = fromLSNFallback
		}
		if fromLSN == "" {
			return CDCPlanRangeResult{}, fmt.Errorf("cdc tracked table %s has no checkpoint to_lsn; pass --from-lsn for initial range", table.SourceObject)
		}
		if err := validateCDCPlanLSN(fromLSN, "from_lsn"); err != nil {
			return CDCPlanRangeResult{}, fmt.Errorf("cdc tracked table %s %w", table.SourceObject, err)
		}
		if err := validateCDCPlanLSNRange(fromLSN, toLSN); err != nil {
			return CDCPlanRangeResult{}, fmt.Errorf("cdc tracked table %s %w", table.SourceObject, err)
		}
		ranges[table.SourceObject] = cdcPlanRange{
			FromLSN: fromLSN,
			ToLSN:   toLSN,
		}
	}

	data, err := os.ReadFile(planPath)
	if err != nil {
		return CDCPlanRangeResult{}, fmt.Errorf("read cdc plan: %w", err)
	}
	updated := renderCDCPlanRangeUpdate(string(data), ranges)
	if err := os.WriteFile(planPath, []byte(updated), 0o644); err != nil {
		return CDCPlanRangeResult{}, fmt.Errorf("write cdc plan: %w", err)
	}
	return CDCPlanRangeResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		PlanFile:        planRel,
		UpdatedTables:   len(ranges),
	}, nil
}

func renderCDCPlanRangeUpdate(plan string, ranges map[string]cdcPlanRange) string {
	lines := strings.Split(plan, "\n")
	currentSource := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "- source_object:"):
			currentSource = trimYAMLScalar(strings.TrimPrefix(trimmed, "- source_object:"))
		case strings.HasPrefix(line, "status:"):
			lines[i] = "status: draft"
		case currentSource != "" && strings.HasPrefix(trimmed, "from_lsn:"):
			if planRange, ok := ranges[currentSource]; ok {
				lines[i] = line[:strings.Index(line, "from_lsn:")] + "from_lsn: " + planRange.FromLSN
			}
		case currentSource != "" && strings.HasPrefix(trimmed, "to_lsn:"):
			if planRange, ok := ranges[currentSource]; ok {
				lines[i] = line[:strings.Index(line, "to_lsn:")] + "to_lsn: " + planRange.ToLSN
			}
		case currentSource != "" && strings.HasPrefix(trimmed, "status:"):
			lines[i] = line[:strings.Index(line, "status:")] + "status: draft"
		}
	}
	return strings.Join(lines, "\n")
}
