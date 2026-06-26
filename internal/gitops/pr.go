package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type PRDraftResult struct {
	SourceClusterID string
	ProjectID       string
	Stage           string
	Title           string
	BranchName      string
	BodyFile        string
	Files           []string
	Reviewers       []string
}

type prStageDefinition struct {
	Stage           string
	Scope           string
	Reviewers       []string
	ApprovalFiles   []string
	ProjectFiles    []string
	ClusterFiles    []string
	Checklist       []string
	RequiresProject bool
}

var prStageDefinitions = map[string]prStageDefinition{
	"discovery": {
		Stage:     "discovery",
		Scope:     "cluster",
		Reviewers: []string{"DBA"},
		ClusterFiles: []string{
			"inventory/inventory.json",
			"inventory/source-ddl/",
			"inventory/schema-issues.yaml",
			"inventory/compatibility-report.md",
		},
		Checklist: []string{
			"Confirm discovery used a read-only SQL Server account.",
			"Confirm inventory scope matches the intended source cluster.",
			"Confirm no plaintext secrets are included.",
		},
	},
	"schema": {
		Stage:           "schema",
		Scope:           "project",
		RequiresProject: true,
		Reviewers:       []string{"DBA", "App Owner"},
		ApprovalFiles:   []string{"approvals/ddl-approval.yaml"},
		ProjectFiles: []string{
			"schema/tidb-ddl/",
			"schema/conversion-report.md",
			"schema/schema-diff.json",
			"approvals/ddl-approval.yaml",
		},
		Checklist: []string{
			"Confirm generated TiDB DDL is reviewed by DBA and App Owner.",
			"Confirm manual-review items in schema-diff.json have an owner.",
			"Confirm no plaintext secrets are included.",
		},
	},
	"plan": {
		Stage:           "plan",
		Scope:           "project",
		RequiresProject: true,
		Reviewers:       []string{"DBA", "SRE", "App Owner"},
		ProjectFiles: []string{
			"plan/migration-plan.yaml",
			"plan/export-plan.yaml",
			"plan/import-plan.yaml",
			"plan/cdc-plan.yaml",
			"plan/validation-plan.yaml",
			"plan/cutover-runbook.md",
		},
		Checklist: []string{
			"Confirm migration mode and phase gates are explicit.",
			"Confirm rollback boundary and cutover owner are documented.",
			"Confirm no plaintext secrets are included.",
		},
	},
	"export": {
		Stage:           "export",
		Scope:           "project",
		RequiresProject: true,
		Reviewers:       []string{"DBA", "SRE"},
		ApprovalFiles:   []string{"approvals/export-approval.yaml"},
		ProjectFiles: []string{
			"plan/export-plan.yaml",
			"state/export-chunks.yaml",
			"evidence/precheck.json",
			"approvals/export-approval.yaml",
		},
		Checklist: []string{
			"Confirm source read permissions and export chunking strategy.",
			"Confirm exported file destination and checksum strategy.",
			"Confirm no plaintext secrets are included.",
		},
	},
	"import": {
		Stage:           "import",
		Scope:           "project",
		RequiresProject: true,
		Reviewers:       []string{"DBA", "SRE"},
		ApprovalFiles:   []string{"approvals/import-approval.yaml"},
		ProjectFiles: []string{
			"plan/import-plan.yaml",
			"state/import-jobs.yaml",
			"evidence/import-summary.json",
			"approvals/import-approval.yaml",
		},
		Checklist: []string{
			"Confirm target table state and import capacity.",
			"Confirm import jobs are idempotent or explicitly resumable.",
			"Confirm no plaintext secrets are included.",
		},
	},
	"cdc": {
		Stage:           "cdc",
		Scope:           "project",
		RequiresProject: true,
		Reviewers:       []string{"DBA", "SRE"},
		ApprovalFiles:   []string{"approvals/cdc-approval.yaml"},
		ProjectFiles: []string{
			"plan/cdc-plan.yaml",
			"state/migration-state.yaml",
			"approvals/cdc-approval.yaml",
		},
		Checklist: []string{
			"Confirm SQL Server CDC retention is sufficient.",
			"Confirm checkpoint ownership is cluster-level.",
			"Confirm no plaintext secrets are included.",
		},
	},
	"validation": {
		Stage:           "validation",
		Scope:           "project",
		RequiresProject: true,
		Reviewers:       []string{"DBA", "App Owner"},
		ProjectFiles: []string{
			"plan/validation-plan.yaml",
			"state/validation-status.yaml",
			"evidence/validation-report.md",
		},
		Checklist: []string{
			"Confirm validation checks cover row counts and representative business invariants.",
			"Confirm pass/fail is determined by deterministic checks.",
			"Confirm no plaintext secrets are included.",
		},
	},
	"cutover": {
		Stage:           "cutover",
		Scope:           "project",
		RequiresProject: true,
		Reviewers:       []string{"DBA", "SRE", "App Owner"},
		ApprovalFiles:   []string{"approvals/cutover-approval.yaml"},
		ProjectFiles: []string{
			"plan/cutover-runbook.md",
			"state/migration-state.yaml",
			"evidence/cdc-catchup.json",
			"evidence/cutover-evidence.md",
			"evidence/post-cutover-report.md",
			"approvals/cutover-approval.yaml",
		},
		Checklist: []string{
			"Confirm import, CDC catch-up, and validation gates are satisfied.",
			"Confirm application owner approved the cutover window.",
			"Confirm rollback boundary is explicit.",
			"Confirm no plaintext secrets are included.",
		},
	},
}

func GeneratePRDraft(root, sourceClusterID, projectID, stage string) (PRDraftResult, error) {
	sourceClusterID = strings.TrimSpace(sourceClusterID)
	projectID = strings.TrimSpace(projectID)
	stage = strings.ToLower(strings.TrimSpace(stage))

	if !idPattern.MatchString(sourceClusterID) {
		return PRDraftResult{}, fmt.Errorf("invalid source cluster id %q", sourceClusterID)
	}
	definition, ok := prStageDefinitions[stage]
	if !ok {
		return PRDraftResult{}, fmt.Errorf("unsupported PR stage %q", stage)
	}

	clusterDir := filepath.Join(root, "clusters", sourceClusterID)
	if info, err := os.Stat(clusterDir); err != nil || !info.IsDir() {
		return PRDraftResult{}, fmt.Errorf("source cluster %q does not exist", sourceClusterID)
	}

	if definition.RequiresProject {
		if projectID == "" {
			return PRDraftResult{}, fmt.Errorf("project id is required for %s PR draft", stage)
		}
		if !idPattern.MatchString(projectID) {
			return PRDraftResult{}, fmt.Errorf("invalid project id %q", projectID)
		}
		return generateProjectPRDraft(root, sourceClusterID, projectID, definition)
	}
	if projectID != "" {
		return PRDraftResult{}, fmt.Errorf("%s PR draft is cluster-level and does not accept project id", stage)
	}
	return generateClusterPRDraft(root, sourceClusterID, definition)
}

func generateProjectPRDraft(root, sourceClusterID, projectID string, definition prStageDefinition) (PRDraftResult, error) {
	projectDir := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID)
	if info, err := os.Stat(projectDir); err != nil || !info.IsDir() {
		return PRDraftResult{}, fmt.Errorf("project %q does not exist under source cluster %q", projectID, sourceClusterID)
	}
	if _, err := readProjectMetadata(filepath.Join(projectDir, "project.yaml")); err != nil {
		return PRDraftResult{}, err
	}

	title := fmt.Sprintf("[%s] %s", definition.Stage, projectID)
	branch := fmt.Sprintf("agent/%s/%s", projectID, definition.Stage)
	bodyRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID, "prs", definition.Stage+"-pr.md"))
	projectRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID))
	files := prefixPaths(projectRel, definition.ProjectFiles)

	result := PRDraftResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Stage:           definition.Stage,
		Title:           title,
		BranchName:      branch,
		BodyFile:        bodyRel,
		Files:           files,
		Reviewers:       append([]string(nil), definition.Reviewers...),
	}
	if err := writePRDraft(root, result, definition); err != nil {
		return PRDraftResult{}, err
	}
	return result, nil
}

func generateClusterPRDraft(root, sourceClusterID string, definition prStageDefinition) (PRDraftResult, error) {
	title := fmt.Sprintf("[%s] %s", definition.Stage, sourceClusterID)
	branch := fmt.Sprintf("agent/%s/%s", sourceClusterID, definition.Stage)
	bodyRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "prs", definition.Stage+"-pr.md"))
	clusterRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID))

	result := PRDraftResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       "",
		Stage:           definition.Stage,
		Title:           title,
		BranchName:      branch,
		BodyFile:        bodyRel,
		Files:           prefixPaths(clusterRel, definition.ClusterFiles),
		Reviewers:       append([]string(nil), definition.Reviewers...),
	}
	if err := writePRDraft(root, result, definition); err != nil {
		return PRDraftResult{}, err
	}
	return result, nil
}

func writePRDraft(root string, result PRDraftResult, definition prStageDefinition) error {
	body := renderPRDraftMarkdown(result, definition)
	path := filepath.Join(root, filepath.FromSlash(result.BodyFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create PR draft directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write PR draft: %w", err)
	}
	return nil
}

func renderPRDraftMarkdown(result PRDraftResult, definition prStageDefinition) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# PR Draft: %s\n\n", result.Title)
	b.WriteString("## Summary\n\n")
	fmt.Fprintf(&b, "- Stage: `%s`\n", result.Stage)
	fmt.Fprintf(&b, "- Source cluster: `%s`\n", result.SourceClusterID)
	if result.ProjectID == "" {
		b.WriteString("- Project: cluster-level\n")
	} else {
		fmt.Fprintf(&b, "- Project: `%s`\n", result.ProjectID)
	}
	fmt.Fprintf(&b, "- Branch: `%s`\n", result.BranchName)
	b.WriteString("- Generated by: `sqlserver2tidb generate-pr-draft`\n\n")

	b.WriteString("## Files To Review\n\n")
	for _, file := range result.Files {
		fmt.Fprintf(&b, "- `%s`\n", file)
	}

	b.WriteString("\n## Required Reviewers\n\n")
	for _, reviewer := range result.Reviewers {
		fmt.Fprintf(&b, "- %s\n", reviewer)
	}

	if len(definition.ApprovalFiles) > 0 {
		b.WriteString("\n## Approval Files\n\n")
		for _, file := range definition.ApprovalFiles {
			if result.ProjectID == "" {
				fmt.Fprintf(&b, "- `%s`\n", filepath.ToSlash(filepath.Join("clusters", result.SourceClusterID, file)))
			} else {
				fmt.Fprintf(&b, "- `%s`\n", filepath.ToSlash(filepath.Join("clusters", result.SourceClusterID, "projects", result.ProjectID, file)))
			}
		}
	}

	b.WriteString("\n## Operator Checklist\n\n")
	for _, item := range definition.Checklist {
		fmt.Fprintf(&b, "- [ ] %s\n", item)
	}

	b.WriteString("\n## Suggested GitHub Command\n\n")
	b.WriteString("```bash\n")
	fmt.Fprintf(&b, "gh pr create --base main --head %s --title %q --body-file %s\n",
		result.BranchName,
		result.Title,
		result.BodyFile,
	)
	b.WriteString("```\n")
	return b.String()
}

func prefixPaths(prefix string, values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		joined := filepath.ToSlash(filepath.Join(prefix, value))
		if strings.HasSuffix(value, "/") && !strings.HasSuffix(joined, "/") {
			joined += "/"
		}
		out = append(out, joined)
	}
	return out
}
