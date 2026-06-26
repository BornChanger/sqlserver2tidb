package gitops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ExecutorEvidencePRDraft struct {
	SourceClusterID string
	ProjectID       string
	Stage           string
	Title           string
	BranchName      string
	BodyFile        string
	Files           []string
}

type ExecutorEvidencePRCreateSpec struct {
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

type executorEvidenceSummary struct {
	Stage           string                           `json:"stage"`
	Status          string                           `json:"status"`
	ProjectID       string                           `json:"project_id"`
	SourceClusterID string                           `json:"source_cluster_id"`
	PayloadHash     string                           `json:"payload_hash"`
	Commands        []executorEvidenceCommandSummary `json:"commands"`
}

type executorEvidenceCommandSummary struct {
	ID           string   `json:"id"`
	Args         []string `json:"args"`
	ShellCommand string   `json:"shell_command"`
	ExitCode     *int     `json:"exit_code"`
	StartedAt    string   `json:"started_at"`
	CompletedAt  string   `json:"completed_at"`
	DurationMs   *int64   `json:"duration_ms"`
}

func GenerateExecutorEvidencePRDraft(root, sourceClusterID, projectID, stage string) (ExecutorEvidencePRDraft, error) {
	ctx, err := loadExecutorEvidencePRContext(root, sourceClusterID, projectID, stage)
	if err != nil {
		return ExecutorEvidencePRDraft{}, err
	}
	draft := ExecutorEvidencePRDraft{
		SourceClusterID: ctx.sourceClusterID,
		ProjectID:       ctx.projectID,
		Stage:           ctx.stage,
		Title:           executorEvidencePRTitle(ctx.stage, ctx.projectID),
		BranchName:      executorEvidencePRBranch(ctx.stage, ctx.projectID),
		BodyFile:        executorEvidencePRBodyFile(ctx.sourceClusterID, ctx.projectID, ctx.stage),
		Files: []string{
			ctx.evidenceFile,
			ctx.approvalFile,
		},
	}

	body := renderExecutorEvidencePRDraftMarkdown(ctx, draft)
	path := filepath.Join(root, filepath.FromSlash(draft.BodyFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ExecutorEvidencePRDraft{}, fmt.Errorf("create executor evidence PR draft directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return ExecutorEvidencePRDraft{}, fmt.Errorf("write executor evidence PR draft: %w", err)
	}
	return draft, nil
}

func PrepareExecutorEvidencePRCreate(root, sourceClusterID, projectID, stage string) (ExecutorEvidencePRCreateSpec, error) {
	ctx, err := loadExecutorEvidencePRContext(root, sourceClusterID, projectID, stage)
	if err != nil {
		return ExecutorEvidencePRCreateSpec{}, err
	}
	title := executorEvidencePRTitle(ctx.stage, ctx.projectID)
	branchName := executorEvidencePRBranch(ctx.stage, ctx.projectID)
	bodyFile := executorEvidencePRBodyFile(ctx.sourceClusterID, ctx.projectID, ctx.stage)
	bodyPath := filepath.Join(root, filepath.FromSlash(bodyFile))
	if info, err := os.Stat(bodyPath); err != nil || info.IsDir() {
		return ExecutorEvidencePRCreateSpec{}, fmt.Errorf("executor evidence PR draft %s does not exist; run generate-executor-evidence-pr-draft first", bodyFile)
	}

	files := []string{
		ctx.evidenceFile,
		bodyFile,
	}
	for _, file := range files {
		path := filepath.Join(root, filepath.FromSlash(file))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return ExecutorEvidencePRCreateSpec{}, fmt.Errorf("executor evidence PR file %s does not exist", file)
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

	return ExecutorEvidencePRCreateSpec{
		SourceClusterID: ctx.sourceClusterID,
		ProjectID:       ctx.projectID,
		Stage:           ctx.stage,
		Title:           title,
		BranchName:      branchName,
		BodyFile:        bodyFile,
		Files:           files,
		GitArgs:         gitArgs,
		GitHubArgs:      ghArgs,
		ShellCommands:   shellCommands,
	}, nil
}

type executorEvidencePRContext struct {
	sourceClusterID string
	projectID       string
	stage           string
	payloadHash     string
	evidenceFile    string
	approvalFile    string
	evidence        executorEvidenceSummary
}

func loadExecutorEvidencePRContext(root, sourceClusterID, projectID, stage string) (executorEvidencePRContext, error) {
	sourceClusterID = strings.TrimSpace(sourceClusterID)
	projectID = strings.TrimSpace(projectID)
	stage = strings.ToLower(strings.TrimSpace(stage))
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return executorEvidencePRContext{}, err
	}
	if !isExecutorEvidenceStage(stage) {
		return executorEvidencePRContext{}, fmt.Errorf("unsupported executor evidence PR stage %q", stage)
	}

	payloadHash, err := requireApprovedStage(root, sourceClusterID, projectID, stage)
	if err != nil {
		return executorEvidencePRContext{}, err
	}
	evidenceFile := executorEvidenceFile(sourceClusterID, projectID, stage)
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(evidenceFile)))
	if err != nil {
		return executorEvidencePRContext{}, fmt.Errorf("read executor evidence %s: %w", evidenceFile, err)
	}
	var evidence executorEvidenceSummary
	if err := json.Unmarshal(data, &evidence); err != nil {
		return executorEvidencePRContext{}, fmt.Errorf("parse executor evidence %s: %w", evidenceFile, err)
	}
	if evidence.Stage != stage {
		return executorEvidencePRContext{}, fmt.Errorf("executor evidence stage %q does not match requested stage %q", evidence.Stage, stage)
	}
	if evidence.SourceClusterID != sourceClusterID {
		return executorEvidencePRContext{}, fmt.Errorf("executor evidence source_cluster_id %q does not match requested source cluster %q", evidence.SourceClusterID, sourceClusterID)
	}
	if evidence.ProjectID != projectID {
		return executorEvidencePRContext{}, fmt.Errorf("executor evidence project_id %q does not match requested project %q", evidence.ProjectID, projectID)
	}
	status := strings.TrimSpace(evidence.Status)
	if status == "" {
		return executorEvidencePRContext{}, fmt.Errorf("executor evidence status is required")
	}
	if !isExecutorEvidenceRunStatus(status) {
		return executorEvidencePRContext{}, fmt.Errorf("executor evidence status %q is unsupported", evidence.Status)
	}
	if evidence.PayloadHash != payloadHash {
		return executorEvidencePRContext{}, fmt.Errorf("executor evidence payload hash %s does not match current approved payload hash %s", evidence.PayloadHash, payloadHash)
	}
	if err := validateExecutorEvidenceCommands(status, evidence.Commands); err != nil {
		return executorEvidencePRContext{}, err
	}

	return executorEvidencePRContext{
		sourceClusterID: sourceClusterID,
		projectID:       projectID,
		stage:           stage,
		payloadHash:     payloadHash,
		evidenceFile:    evidenceFile,
		approvalFile:    executorEvidenceApprovalFile(sourceClusterID, projectID, stage),
		evidence:        evidence,
	}, nil
}

func validateExecutorEvidenceCommands(status string, commands []executorEvidenceCommandSummary) error {
	if len(commands) == 0 {
		return fmt.Errorf("executor evidence commands must contain at least one command")
	}
	hasFailedCommand := false
	for i, command := range commands {
		if strings.TrimSpace(command.ID) == "" {
			return fmt.Errorf("executor evidence command %d id is required", i+1)
		}
		if strings.TrimSpace(command.ShellCommand) == "" {
			return fmt.Errorf("executor evidence command %s shell_command is required", command.ID)
		}
		if command.ExitCode == nil {
			return fmt.Errorf("executor evidence command %s exit_code is required", command.ID)
		}
		if strings.TrimSpace(status) == "succeeded" && *command.ExitCode != 0 {
			return fmt.Errorf("executor evidence status succeeded conflicts with command %s exit_code %d", command.ID, *command.ExitCode)
		}
		if *command.ExitCode != 0 {
			hasFailedCommand = true
		}
		if len(command.Args) == 0 {
			return fmt.Errorf("executor evidence command %s args must contain at least one argument", command.ID)
		}
		for argIndex, arg := range command.Args {
			if strings.TrimSpace(arg) == "" {
				return fmt.Errorf("executor evidence command %s args[%d] is required", command.ID, argIndex)
			}
		}
		startedAt, err := parseExecutorEvidenceCommandTime(command.ID, "started_at", command.StartedAt)
		if err != nil {
			return err
		}
		completedAt, err := parseExecutorEvidenceCommandTime(command.ID, "completed_at", command.CompletedAt)
		if err != nil {
			return err
		}
		if completedAt.Before(startedAt) {
			return fmt.Errorf("executor evidence command %s completed_at is before started_at", command.ID)
		}
		if command.DurationMs == nil {
			return fmt.Errorf("executor evidence command %s duration_ms is required", command.ID)
		}
		if *command.DurationMs < 0 {
			return fmt.Errorf("executor evidence command %s duration_ms must be non-negative", command.ID)
		}
	}
	if strings.TrimSpace(status) == "failed" && !hasFailedCommand {
		return fmt.Errorf("executor evidence status failed requires at least one non-zero command exit_code")
	}
	return nil
}

func parseExecutorEvidenceCommandTime(commandID, field, value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, fmt.Errorf("executor evidence command %s %s is required", commandID, field)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("executor evidence command %s %s must be RFC3339Nano: %w", commandID, field, err)
	}
	return parsed, nil
}

func isExecutorEvidenceRunStatus(status string) bool {
	switch status {
	case "succeeded", "failed":
		return true
	default:
		return false
	}
}

func isExecutorEvidenceStage(stage string) bool {
	switch stage {
	case "ddl", "export", "import", "cdc", "validation":
		return true
	default:
		return false
	}
}

func executorEvidencePRTitle(stage, projectID string) string {
	return fmt.Sprintf("[executor-evidence:%s] %s", stage, projectID)
}

func executorEvidencePRBranch(stage, projectID string) string {
	return fmt.Sprintf("agent/%s/executor-%s-evidence", projectID, stage)
}

func executorEvidenceFile(sourceClusterID, projectID, stage string) string {
	return filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID, "evidence", "executor-"+stage+"-run.json"))
}

func executorEvidenceApprovalFile(sourceClusterID, projectID, stage string) string {
	return filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID, "approvals", stage+"-approval.yaml"))
}

func executorEvidencePRBodyFile(sourceClusterID, projectID, stage string) string {
	return filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID, "prs", "executor-"+stage+"-evidence-pr.md"))
}

func renderExecutorEvidencePRDraftMarkdown(ctx executorEvidencePRContext, draft ExecutorEvidencePRDraft) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# PR Draft: %s\n\n", draft.Title)
	b.WriteString("## Summary\n\n")
	fmt.Fprintf(&b, "- Source cluster: `%s`\n", ctx.sourceClusterID)
	fmt.Fprintf(&b, "- Project: `%s`\n", ctx.projectID)
	fmt.Fprintf(&b, "- Stage: `%s`\n", ctx.stage)
	fmt.Fprintf(&b, "- Status: `%s`\n", ctx.evidence.Status)
	fmt.Fprintf(&b, "- Payload hash: `%s`\n", ctx.payloadHash)
	fmt.Fprintf(&b, "- Branch: `%s`\n", draft.BranchName)
	b.WriteString("- Generated by: `sqlserver2tidb generate-executor-evidence-pr-draft`\n\n")

	b.WriteString("## Files To Review\n\n")
	for _, file := range draft.Files {
		fmt.Fprintf(&b, "- `%s`\n", file)
	}

	writeExecutorEvidenceCommandTable(&b, ctx.evidence.Commands)

	b.WriteString("\n## Operator Checklist\n\n")
	b.WriteString("- [ ] Confirm the executor evidence corresponds to the approved payload hash.\n")
	b.WriteString("- [ ] Confirm command output does not include plaintext secrets.\n")
	b.WriteString("- [ ] Confirm the executor status and exit codes match operational expectations.\n")
	if ctx.stage == "ddl" {
		b.WriteString("- [ ] Confirm the applied DDL matches the reviewed TiDB DDL files.\n")
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

func writeExecutorEvidenceCommandTable(b *strings.Builder, commands []executorEvidenceCommandSummary) {
	b.WriteString("\n## Executor Commands\n\n")
	b.WriteString("| Command ID | Exit code | Started at | Completed at | Duration ms |\n")
	b.WriteString("| --- | ---: | --- | --- | ---: |\n")
	for _, command := range commands {
		duration := ""
		if command.DurationMs != nil {
			duration = fmt.Sprintf("%d", *command.DurationMs)
		}
		fmt.Fprintf(
			b,
			"| %s | %d | %s | %s | %s |\n",
			escapeMarkdownTableCell(command.ID),
			*command.ExitCode,
			escapeMarkdownTableCell(command.StartedAt),
			escapeMarkdownTableCell(command.CompletedAt),
			duration,
		)
	}
}

func escapeMarkdownTableCell(value string) string {
	replacer := strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ")
	return replacer.Replace(value)
}
