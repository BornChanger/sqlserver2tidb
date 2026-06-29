package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type GitHubPRApprovalSyncSpec struct {
	SourceClusterID string
	ProjectID       string
	Stage           string
	PRNumber        int
	PRState         string
	ReviewDecision  string
	ChecksStatus    string
	MergedAt        string
	ApprovedBy      []string
	ChangedFiles    []string
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
	body := renderGitHubSyncedApproval(projectID, sourceClusterID, approvalStage, payloadHash, approvedBy, approvedAt, spec)
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(approvalFile)), []byte(body), 0o644); err != nil {
		return GitHubPRApprovalSyncResult{}, fmt.Errorf("write approval file: %w", err)
	}
	return GitHubPRApprovalSyncResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Stage:           stage,
		ApprovalStage:   approvalStage,
		PRNumber:        spec.PRNumber,
		ApprovalFile:    approvalFile,
		PayloadHash:     payloadHash,
		ApprovedBy:      approvedBy,
		ApprovedAt:      approvedAt,
	}, nil
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
	return b.String()
}
