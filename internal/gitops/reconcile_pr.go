package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type workerStatePRDraft struct {
	Title      string
	BranchName string
	BodyFile   string
	Files      []string
}

type WorkerStatePRCreateSpec struct {
	SourceClusterID string
	ProjectID       string
	Stage           string
	Title           string
	BranchName      string
	BodyFile        string
	Files           []string
	GitArgs         [][]string
	GitHubArgs      []string
	ShellCommands   []string
}

func writeWorkerStatePRDraft(root string, result WorkerReconcileExecutionResult) (workerStatePRDraft, error) {
	action := result.Action
	title := fmt.Sprintf("[worker-state:%s] %s", action.Stage, action.ProjectID)
	branchName := fmt.Sprintf("agent/%s/reconcile-%s-state", action.ProjectID, action.Stage)
	bodyFile := filepath.ToSlash(filepath.Join(
		"clusters",
		action.SourceClusterID,
		"projects",
		action.ProjectID,
		"prs",
		"reconcile-"+action.Stage+"-state-pr.md",
	))
	draft := workerStatePRDraft{
		Title:      title,
		BranchName: branchName,
		BodyFile:   bodyFile,
		Files:      workerStatePRFiles(result),
	}

	body := renderWorkerStatePRDraftMarkdown(result, draft)
	path := filepath.Join(root, filepath.FromSlash(bodyFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return workerStatePRDraft{}, fmt.Errorf("create worker state PR draft directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return workerStatePRDraft{}, fmt.Errorf("write worker state PR draft: %w", err)
	}
	return draft, nil
}

func PrepareWorkerStatePRCreate(root, sourceClusterID, projectID, stage string) (WorkerStatePRCreateSpec, error) {
	sourceClusterID = strings.TrimSpace(sourceClusterID)
	projectID = strings.TrimSpace(projectID)
	stage = strings.ToLower(strings.TrimSpace(stage))

	if !idPattern.MatchString(sourceClusterID) {
		return WorkerStatePRCreateSpec{}, fmt.Errorf("invalid source cluster id %q", sourceClusterID)
	}
	if !idPattern.MatchString(projectID) {
		return WorkerStatePRCreateSpec{}, fmt.Errorf("invalid project id %q", projectID)
	}
	if !isWorkerReconcileStage(stage) {
		return WorkerStatePRCreateSpec{}, fmt.Errorf("unsupported worker state PR stage %q", stage)
	}
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return WorkerStatePRCreateSpec{}, err
	}

	title := fmt.Sprintf("[worker-state:%s] %s", stage, projectID)
	branchName := fmt.Sprintf("agent/%s/reconcile-%s-state", projectID, stage)
	bodyFile := workerStatePRBodyFile(sourceClusterID, projectID, stage)
	bodyPath := filepath.Join(root, filepath.FromSlash(bodyFile))
	if info, err := os.Stat(bodyPath); err != nil || info.IsDir() {
		return WorkerStatePRCreateSpec{}, fmt.Errorf("worker state PR draft %s does not exist; run worker-reconcile --execute-next --state-pr-draft first", bodyFile)
	}

	files := workerStateCommitFiles(root, sourceClusterID, projectID, stage, bodyFile)
	for _, file := range files {
		path := filepath.Join(root, filepath.FromSlash(file))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return WorkerStatePRCreateSpec{}, fmt.Errorf("worker state PR file %s does not exist", file)
		}
	}

	gitArgs := [][]string{
		{"switch", "-c", branchName},
		append([]string{"add"}, files...),
		{"commit", "-m", title},
		{"push", "-u", "origin", branchName},
	}
	ghArgs := []string{
		"pr", "create",
		"--base", "main",
		"--head", branchName,
		"--title", title,
		"--body-file", bodyFile,
	}
	shellCommands := make([]string, 0, len(gitArgs)+1)
	for _, args := range gitArgs {
		shellCommands = append(shellCommands, renderShellCommand(append([]string{"git"}, args...)))
	}
	shellCommands = append(shellCommands, renderShellCommand(append([]string{"gh"}, ghArgs...)))

	return WorkerStatePRCreateSpec{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Stage:           stage,
		Title:           title,
		BranchName:      branchName,
		BodyFile:        bodyFile,
		Files:           files,
		GitArgs:         gitArgs,
		GitHubArgs:      ghArgs,
		ShellCommands:   shellCommands,
	}, nil
}

func workerStatePRFiles(result WorkerReconcileExecutionResult) []string {
	action := result.Action
	projectPrefix := filepath.ToSlash(filepath.Join("clusters", action.SourceClusterID, "projects", action.ProjectID))
	clusterPrefix := filepath.ToSlash(filepath.Join("clusters", action.SourceClusterID))

	files := make([]string, 0, 4)
	if result.StateFile != "" {
		files = append(files, filepath.ToSlash(filepath.Join(projectPrefix, result.StateFile)))
	}
	if result.EvidenceFile != "" {
		files = append(files, filepath.ToSlash(filepath.Join(projectPrefix, result.EvidenceFile)))
	}
	if action.Stage == "cdc" {
		files = append(files, filepath.ToSlash(filepath.Join(clusterPrefix, "state/cdc-checkpoint.yaml")))
	}
	if result.LeaseFile != "" {
		files = append(files, filepath.ToSlash(filepath.Join(clusterPrefix, result.LeaseFile)))
	}
	return files
}

func workerStateCommitFiles(root, sourceClusterID, projectID, stage, bodyFile string) []string {
	projectPrefix := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID))
	clusterPrefix := filepath.ToSlash(filepath.Join("clusters", sourceClusterID))

	files := []string{
		filepath.ToSlash(filepath.Join(projectPrefix, workerStateFileForStage(stage))),
		filepath.ToSlash(filepath.Join(projectPrefix, workerEvidenceFileForStage(stage))),
	}
	executorEvidence := filepath.ToSlash(filepath.Join(projectPrefix, "evidence", "executor-"+stage+"-run.json"))
	if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(executorEvidence))); err == nil && !info.IsDir() {
		files = append(files, executorEvidence)
	}
	if stage == "cdc" {
		files = append(files, filepath.ToSlash(filepath.Join(clusterPrefix, "state/cdc-checkpoint.yaml")))
	}
	files = append(files,
		filepath.ToSlash(filepath.Join(clusterPrefix, "state/worker-lease.yaml")),
		bodyFile,
	)
	return files
}

func workerStateFileForStage(stage string) string {
	switch stage {
	case "export":
		return "state/export-chunks.yaml"
	case "import":
		return "state/import-jobs.yaml"
	case "cdc":
		return "state/migration-state.yaml"
	case "validation":
		return "state/validation-status.yaml"
	default:
		return ""
	}
}

func workerEvidenceFileForStage(stage string) string {
	switch stage {
	case "export":
		return "evidence/precheck.json"
	case "import":
		return "evidence/import-summary.json"
	case "cdc":
		return "evidence/cdc-catchup.json"
	case "validation":
		return "evidence/validation-report.md"
	default:
		return ""
	}
}

func workerStatePRBodyFile(sourceClusterID, projectID, stage string) string {
	return filepath.ToSlash(filepath.Join(
		"clusters",
		sourceClusterID,
		"projects",
		projectID,
		"prs",
		"reconcile-"+stage+"-state-pr.md",
	))
}

func isWorkerReconcileStage(stage string) bool {
	for _, candidate := range workerReconcileStages {
		if stage == candidate {
			return true
		}
	}
	return false
}

func renderWorkerStatePRDraftMarkdown(result WorkerReconcileExecutionResult, draft workerStatePRDraft) string {
	action := result.Action

	var b strings.Builder
	fmt.Fprintf(&b, "# PR Draft: %s\n\n", draft.Title)
	b.WriteString("## Summary\n\n")
	fmt.Fprintf(&b, "- Source cluster: `%s`\n", action.SourceClusterID)
	fmt.Fprintf(&b, "- Project: `%s`\n", action.ProjectID)
	fmt.Fprintf(&b, "- Stage: `%s`\n", action.Stage)
	fmt.Fprintf(&b, "- Status: `%s`\n", result.Status)
	fmt.Fprintf(&b, "- Payload hash: `%s`\n", action.PayloadHash)
	fmt.Fprintf(&b, "- Lease ID: `%s`\n", result.LeaseID)
	fmt.Fprintf(&b, "- Branch: `%s`\n", draft.BranchName)
	b.WriteString("- Generated by: `sqlserver2tidb worker-reconcile --execute-next --state-pr-draft`\n\n")

	b.WriteString("## Files To Review\n\n")
	for _, file := range draft.Files {
		fmt.Fprintf(&b, "- `%s`\n", file)
	}

	b.WriteString("\n## Operator Checklist\n\n")
	b.WriteString("- [ ] Confirm the worker action matches the approved payload hash.\n")
	b.WriteString("- [ ] Confirm state and evidence files only contain migration metadata, not plaintext secrets.\n")
	b.WriteString("- [ ] Confirm the source-cluster lease holder and phase match the executed action.\n")
	if action.Stage == "cdc" {
		b.WriteString("- [ ] Confirm the source-cluster CDC checkpoint is consistent with the project CDC evidence.\n")
	}

	b.WriteString("\n## Suggested GitHub Command\n\n")
	b.WriteString("```bash\n")
	fmt.Fprintf(&b, "gh pr create --base main --head %s --title %s --body-file %s\n",
		draft.BranchName,
		shellQuote(draft.Title),
		draft.BodyFile,
	)
	b.WriteString("```\n")
	return b.String()
}
