package gitops

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	CDCIterationStatusRangePrepared = "range_prepared"
	CDCIterationStatusCaughtUp      = "caught_up"
)

type CDCIterationSpec struct {
	MaxLSN         string
	InitialFromLSN string
	WritePRDraft   bool
}

type CDCIterationResult struct {
	SourceClusterID string
	ProjectID       string
	Status          string
	MaxLSN          string
	PlanFile        string
	PRBodyFile      string
	UpdatedTables   int
	Ranges          []CDCIterationTableRange
}

type CDCIterationTableRange struct {
	SourceObject string
	TargetObject string
	FromLSN      string
	ToLSN        string
}

func PrepareCDCIteration(root, sourceClusterID, projectID string, spec CDCIterationSpec) (CDCIterationResult, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return CDCIterationResult{}, err
	}
	maxLSN := strings.TrimSpace(spec.MaxLSN)
	if maxLSN == "" {
		return CDCIterationResult{}, fmt.Errorf("max_lsn is required")
	}
	maxLSNBytes, err := parseCDCPlanLSN(maxLSN, "max_lsn")
	if err != nil {
		return CDCIterationResult{}, err
	}
	initialFromLSN := strings.TrimSpace(spec.InitialFromLSN)
	if initialFromLSN != "" {
		if err := validateCDCPlanLSN(initialFromLSN, "from_lsn"); err != nil {
			return CDCIterationResult{}, err
		}
	}

	planRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID, "plan", "cdc-plan.yaml"))
	planPath := filepath.Join(root, filepath.FromSlash(planRel))
	plan, err := readCDCPlanSummary(planPath)
	if err != nil {
		return CDCIterationResult{}, err
	}
	if err := validateCDCPlanSummaryForExecution(plan, true); err != nil {
		return CDCIterationResult{}, err
	}

	checkpointEntries, err := readCDCCheckpointEntries(filepath.Join(root, "clusters", sourceClusterID, "state", "cdc-checkpoint.yaml"))
	if err != nil {
		return CDCIterationResult{}, err
	}
	checkpoints := make(map[string]cdcCheckpointEntry, len(checkpointEntries))
	for _, entry := range checkpointEntries {
		if strings.TrimSpace(entry.SourceObject) != "" {
			checkpoints[entry.SourceObject] = entry
		}
	}

	result := CDCIterationResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Status:          CDCIterationStatusRangePrepared,
		MaxLSN:          maxLSN,
		PlanFile:        planRel,
	}
	ranges := make(map[string]cdcPlanRange, len(plan.Tables))
	allCaughtUp := true
	for _, table := range plan.Tables {
		fromLSN := ""
		if checkpoint, ok := checkpoints[table.SourceObject]; ok {
			if strings.TrimSpace(checkpoint.TargetObject) != "" && checkpoint.TargetObject != table.TargetObject {
				return CDCIterationResult{}, fmt.Errorf("cdc checkpoint target_object %s for %s does not match plan target_object %s", checkpoint.TargetObject, table.SourceObject, table.TargetObject)
			}
			fromLSN = strings.TrimSpace(checkpoint.ToLSN)
		}
		if fromLSN == "" {
			fromLSN = initialFromLSN
		}
		if fromLSN == "" {
			return CDCIterationResult{}, fmt.Errorf("cdc tracked table %s has no checkpoint to_lsn; pass --from-lsn for initial range", table.SourceObject)
		}
		fromLSNBytes, err := parseCDCPlanLSN(fromLSN, "from_lsn")
		if err != nil {
			return CDCIterationResult{}, fmt.Errorf("cdc tracked table %s %w", table.SourceObject, err)
		}
		if bytes.Compare(fromLSNBytes, maxLSNBytes) > 0 {
			return CDCIterationResult{}, fmt.Errorf("cdc tracked table %s checkpoint to_lsn %s is ahead of max_lsn %s", table.SourceObject, fromLSN, maxLSN)
		}
		if bytes.Compare(fromLSNBytes, maxLSNBytes) < 0 {
			allCaughtUp = false
		}
		ranges[table.SourceObject] = cdcPlanRange{
			FromLSN: fromLSN,
			ToLSN:   maxLSN,
		}
		result.Ranges = append(result.Ranges, CDCIterationTableRange{
			SourceObject: table.SourceObject,
			TargetObject: table.TargetObject,
			FromLSN:      fromLSN,
			ToLSN:        maxLSN,
		})
	}

	if allCaughtUp {
		result.Status = CDCIterationStatusCaughtUp
		result.UpdatedTables = 0
		result.Ranges = nil
		return result, nil
	}

	data, err := os.ReadFile(planPath)
	if err != nil {
		return CDCIterationResult{}, fmt.Errorf("read cdc plan: %w", err)
	}
	updated := renderCDCPlanRangeUpdate(string(data), ranges)
	if err := os.WriteFile(planPath, []byte(updated), 0o644); err != nil {
		return CDCIterationResult{}, fmt.Errorf("write cdc plan: %w", err)
	}
	result.UpdatedTables = len(ranges)
	if spec.WritePRDraft {
		prBodyFile, err := writeCDCIterationPRDraft(root, result)
		if err != nil {
			return CDCIterationResult{}, err
		}
		result.PRBodyFile = prBodyFile
	}
	return result, nil
}

func writeCDCIterationPRDraft(root string, result CDCIterationResult) (string, error) {
	rel := filepath.ToSlash(filepath.Join("clusters", result.SourceClusterID, "projects", result.ProjectID, "prs", "cdc-range-pr.md"))
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create cdc range PR draft directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(renderCDCIterationPRDraft(result)), 0o644); err != nil {
		return "", fmt.Errorf("write cdc range PR draft: %w", err)
	}
	return rel, nil
}

func renderCDCIterationPRDraft(result CDCIterationResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# PR Draft: [cdc-range] %s\n\n", result.ProjectID)
	fmt.Fprintf(&b, "- Source cluster: `%s`\n", result.SourceClusterID)
	fmt.Fprintf(&b, "- Project: `%s`\n", result.ProjectID)
	fmt.Fprintf(&b, "- Status: `%s`\n", result.Status)
	fmt.Fprintf(&b, "- Max LSN: `%s`\n", result.MaxLSN)
	fmt.Fprintf(&b, "- Plan file: `%s`\n\n", result.PlanFile)
	b.WriteString("## CDC Ranges\n\n")
	b.WriteString("| Source object | Target object | From LSN | To LSN |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, tableRange := range result.Ranges {
		fmt.Fprintf(&b, "| %s | %s | `%s` | `%s` |\n", tableRange.SourceObject, tableRange.TargetObject, tableRange.FromLSN, tableRange.ToLSN)
	}
	b.WriteString("\n## Review Checklist\n\n")
	b.WriteString("- [ ] Confirm SQL Server CDC retention still covers the full range.\n")
	b.WriteString("- [ ] Confirm the range is safe to execute after this PR is merged.\n")
	b.WriteString("- [ ] Update `approvals/cdc-approval.yaml` with the new payload hash before worker execution.\n")
	return b.String()
}
