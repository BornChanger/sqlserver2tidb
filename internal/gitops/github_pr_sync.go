package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type GitHubPRApprovalSyncSpec struct {
	SourceClusterID    string
	ProjectID          string
	Stage              string
	PRNumber           int
	PRState            string
	ReviewDecision     string
	ChecksStatus       string
	MergedAt           string
	ApprovedBy         []string
	ChangedFiles       []string
	AutomationActor    string
	AutomationWorkflow string
	AutomationRunID    string
	AutomationRunURL   string
	AutomationCommit   string
	MergeMethod        string
	BaseBranch         string
}

type GitHubPRApprovalSyncResult struct {
	SourceClusterID string
	ProjectID       string
	Stage           string
	ApprovalStage   string
	PRNumber        int
	ApprovalFile    string
	PayloadHash     string
	ApprovedBy      []string
	ApprovedAt      string
}

func SyncGitHubPRApproval(root string, spec GitHubPRApprovalSyncSpec) (GitHubPRApprovalSyncResult, error) {
	sourceClusterID := strings.TrimSpace(spec.SourceClusterID)
	projectID := strings.TrimSpace(spec.ProjectID)
	stage := strings.ToLower(strings.TrimSpace(spec.Stage))
	if !idPattern.MatchString(sourceClusterID) {
		return GitHubPRApprovalSyncResult{}, fmt.Errorf("invalid source cluster id %q", sourceClusterID)
	}
	if !idPattern.MatchString(projectID) {
		return GitHubPRApprovalSyncResult{}, fmt.Errorf("invalid project id %q", projectID)
	}
	definition, ok := prStageDefinitions[stage]
	if !ok {
		return GitHubPRApprovalSyncResult{}, fmt.Errorf("unsupported PR stage %q", stage)
	}
	if !definition.RequiresProject || len(definition.ApprovalFiles) == 0 {
		return GitHubPRApprovalSyncResult{}, fmt.Errorf("%s PR does not produce a project approval file", stage)
	}
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return GitHubPRApprovalSyncResult{}, err
	}
	approvalStage := approvalStageForPRStage(stage)
	if spec.PRNumber <= 0 {
		return GitHubPRApprovalSyncResult{}, fmt.Errorf("GitHub PR number must be positive")
	}
	if strings.ToUpper(strings.TrimSpace(spec.PRState)) != "MERGED" {
		return GitHubPRApprovalSyncResult{}, fmt.Errorf("PR state is %s, want MERGED", strings.TrimSpace(spec.PRState))
	}
	if strings.ToUpper(strings.TrimSpace(spec.ReviewDecision)) != "APPROVED" {
		return GitHubPRApprovalSyncResult{}, fmt.Errorf("PR review decision is %s, want APPROVED", strings.TrimSpace(spec.ReviewDecision))
	}
	if strings.ToUpper(strings.TrimSpace(spec.ChecksStatus)) != "PASSED" {
		return GitHubPRApprovalSyncResult{}, fmt.Errorf("PR checks status is %s, want PASSED", strings.TrimSpace(spec.ChecksStatus))
	}
	approvedAt, err := parsePRMergedAt(spec.MergedAt)
	if err != nil {
		return GitHubPRApprovalSyncResult{}, err
	}
	approvedBy := normalizeApprovers(spec.ApprovedBy)
	if len(approvedBy) == 0 {
		return GitHubPRApprovalSyncResult{}, fmt.Errorf("PR has no approving reviewers")
	}
	if err := validatePRChangedFiles(sourceClusterID, projectID, definition, spec.ChangedFiles); err != nil {
		return GitHubPRApprovalSyncResult{}, err
	}
	payloadHash, err := ComputePayloadHashForStage(root, sourceClusterID, projectID, approvalStage)
	if err != nil {
		return GitHubPRApprovalSyncResult{}, err
	}
	approvalFile := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID, "approvals", approvalStage+"-approval.yaml"))
	result := GitHubPRApprovalSyncResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Stage:           stage,
		ApprovalStage:   approvalStage,
		PRNumber:        spec.PRNumber,
		ApprovalFile:    approvalFile,
		PayloadHash:     payloadHash,
		ApprovedBy:      approvedBy,
		ApprovedAt:      approvedAt,
	}
	current, err := existingGitHubSyncedApprovalCurrent(filepath.Join(root, filepath.FromSlash(approvalFile)), projectID, sourceClusterID, approvalStage, payloadHash, approvedAt, spec)
	if err != nil {
		return GitHubPRApprovalSyncResult{}, err
	}
	if current {
		return result, nil
	}
	body := renderGitHubSyncedApproval(projectID, sourceClusterID, approvalStage, payloadHash, approvedBy, approvedAt, spec)
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(approvalFile)), []byte(body), 0o644); err != nil {
		return GitHubPRApprovalSyncResult{}, fmt.Errorf("write approval file: %w", err)
	}
	return result, nil
}

func approvalStageForPRStage(stage string) string {
	if stage == "schema" {
		return "ddl"
	}
	return stage
}

func parsePRMergedAt(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("PR merged_at is required")
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return "", fmt.Errorf("PR merged_at must be RFC3339: %w", err)
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func normalizeApprovers(values []string) []string {
	seen := map[string]bool{}
	approvers := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		approvers = append(approvers, trimmed)
	}
	return approvers
}

func validatePRChangedFiles(sourceClusterID, projectID string, definition prStageDefinition, changedFiles []string) error {
	if len(changedFiles) == 0 {
		return fmt.Errorf("PR changed file list is required")
	}
	projectRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID))
	allowed := prefixPaths(projectRel, definition.ProjectFiles)
	for _, file := range changedFiles {
		normalized := filepath.ToSlash(strings.TrimSpace(file))
		if normalized == "" {
			continue
		}
		if !prFileAllowed(normalized, allowed) {
			return fmt.Errorf("PR changed file %s is outside %s review scope", normalized, definition.Stage)
		}
	}
	return nil
}

func prFileAllowed(file string, allowed []string) bool {
	for _, allowedPath := range allowed {
		allowedPath = filepath.ToSlash(allowedPath)
		if strings.HasSuffix(allowedPath, "/") {
			if strings.HasPrefix(file, allowedPath) {
				return true
			}
			continue
		}
		if file == allowedPath {
			return true
		}
	}
	return false
}

func renderGitHubSyncedApproval(projectID, sourceClusterID, approvalStage, payloadHash string, approvedBy []string, approvedAt string, spec GitHubPRApprovalSyncSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "approval_id: %s-github-pr-%d\n", approvalStage, spec.PRNumber)
	fmt.Fprintf(&b, "project_id: %s\n", projectID)
	fmt.Fprintf(&b, "source_cluster_id: %s\n", sourceClusterID)
	fmt.Fprintf(&b, "action: %s\n", approvalStage)
	fmt.Fprintf(&b, "payload_hash: %s\n", payloadHash)
	b.WriteString("required_reviewers:\n")
	b.WriteString("  - github-pr-review\n")
	b.WriteString("approved_by:\n")
	for _, approver := range approvedBy {
		fmt.Fprintf(&b, "  - %s\n", approver)
	}
	b.WriteString("status: approved\n")
	fmt.Fprintf(&b, "approved_at: %q\n", approvedAt)
	b.WriteString("github_pr:\n")
	fmt.Fprintf(&b, "  number: %d\n", spec.PRNumber)
	fmt.Fprintf(&b, "  state: %s\n", strings.ToUpper(strings.TrimSpace(spec.PRState)))
	fmt.Fprintf(&b, "  review_decision: %s\n", strings.ToUpper(strings.TrimSpace(spec.ReviewDecision)))
	fmt.Fprintf(&b, "  checks_status: %s\n", strings.ToUpper(strings.TrimSpace(spec.ChecksStatus)))
	fmt.Fprintf(&b, "  merged_at: %q\n", approvedAt)
	renderGitHubSyncedApprovalAutomation(&b, spec)
	return b.String()
}

func existingGitHubSyncedApprovalCurrent(path, projectID, sourceClusterID, approvalStage, payloadHash, approvedAt string, spec GitHubPRApprovalSyncSpec) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read existing approval file: %w", err)
	}
	approval, err := readApprovalMetadata(path)
	if err != nil {
		return false, err
	}
	if approval.Action != approvalStage || approval.Status != "approved" || approval.PayloadHash != payloadHash || approval.ApprovedAt != approvedAt || len(approval.ApprovedBy) == 0 {
		return false, nil
	}
	topLevel, sections := parseYAMLScalarSections(string(data))
	if topLevel["project_id"] != projectID || topLevel["source_cluster_id"] != sourceClusterID {
		return false, nil
	}
	githubPR := sections["github_pr"]
	if githubPR["number"] != fmt.Sprintf("%d", spec.PRNumber) {
		return false, nil
	}
	if strings.ToUpper(githubPR["state"]) != strings.ToUpper(strings.TrimSpace(spec.PRState)) {
		return false, nil
	}
	if strings.ToUpper(githubPR["review_decision"]) != strings.ToUpper(strings.TrimSpace(spec.ReviewDecision)) {
		return false, nil
	}
	if strings.ToUpper(githubPR["checks_status"]) != strings.ToUpper(strings.TrimSpace(spec.ChecksStatus)) {
		return false, nil
	}
	if githubPR["merged_at"] != approvedAt {
		return false, nil
	}
	return true, nil
}

func parseYAMLScalarSections(content string) (map[string]string, map[string]map[string]string) {
	topLevel := map[string]string{}
	sections := map[string]map[string]string{}
	currentSection := ""
	for _, raw := range strings.Split(content, "\n") {
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "- ") {
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := trimYAMLScalar(parts[1])
		isTopLevel := strings.TrimLeft(raw, " \t") == raw
		if isTopLevel {
			currentSection = ""
			if value == "" {
				currentSection = key
				if sections[currentSection] == nil {
					sections[currentSection] = map[string]string{}
				}
				continue
			}
			topLevel[key] = value
			continue
		}
		if currentSection == "" {
			continue
		}
		if sections[currentSection] == nil {
			sections[currentSection] = map[string]string{}
		}
		sections[currentSection][key] = value
	}
	return topLevel, sections
}

func renderGitHubSyncedApprovalAutomation(b *strings.Builder, spec GitHubPRApprovalSyncSpec) {
	values := []struct {
		key   string
		value string
		quote bool
	}{
		{key: "actor", value: spec.AutomationActor},
		{key: "workflow", value: spec.AutomationWorkflow},
		{key: "run_id", value: spec.AutomationRunID, quote: true},
		{key: "run_url", value: spec.AutomationRunURL, quote: true},
		{key: "workflow_sha", value: spec.AutomationCommit},
		{key: "merge_method", value: spec.MergeMethod},
		{key: "base_branch", value: spec.BaseBranch},
	}
	wroteHeader := false
	for _, item := range values {
		trimmed := strings.TrimSpace(item.value)
		if trimmed == "" {
			continue
		}
		if !wroteHeader {
			b.WriteString("automation:\n")
			wroteHeader = true
		}
		if item.quote {
			fmt.Fprintf(b, "  %s: %s\n", item.key, quoteYAML(trimmed))
			continue
		}
		fmt.Fprintf(b, "  %s: %s\n", item.key, trimmed)
	}
}
