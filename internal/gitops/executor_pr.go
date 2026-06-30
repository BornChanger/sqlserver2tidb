package gitops

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BornChanger/sqlserver2tidb/internal/redact"
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
	GeneratedAt     string                           `json:"generated_at"`
	Commands        []executorEvidenceCommandSummary `json:"commands"`
}

type executorEvidenceCommandSummary struct {
	ID                string                                  `json:"id"`
	Args              []string                                `json:"args"`
	ShellCommand      string                                  `json:"shell_command"`
	ExitCode          *int                                    `json:"exit_code"`
	Output            string                                  `json:"output"`
	Error             string                                  `json:"error"`
	AttemptCount      *int                                    `json:"attempt_count"`
	Attempts          []executorEvidenceCommandAttemptSummary `json:"attempts"`
	StartedAt         string                                  `json:"started_at"`
	CompletedAt       string                                  `json:"completed_at"`
	DurationMs        *int64                                  `json:"duration_ms"`
	CDCAppliedChanges *int                                    `json:"cdc_applied_changes"`
	DataRows          *int64                                  `json:"data_rows"`
	DataBytes         *int64                                  `json:"data_bytes"`
	DataSHA256        string                                  `json:"data_sha256"`
}

type executorEvidenceCommandAttemptSummary struct {
	Attempt     int    `json:"attempt"`
	ExitCode    *int   `json:"exit_code"`
	Output      string `json:"output"`
	Error       string `json:"error"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	DurationMs  *int64 `json:"duration_ms"`
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
		Files:           executorEvidenceDraftFiles(ctx),
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
	currentBody, err := os.ReadFile(bodyPath)
	if err != nil {
		return ExecutorEvidencePRCreateSpec{}, fmt.Errorf("read executor evidence PR draft %s: %w", bodyFile, err)
	}
	expectedDraft := ExecutorEvidencePRDraft{
		SourceClusterID: ctx.sourceClusterID,
		ProjectID:       ctx.projectID,
		Stage:           ctx.stage,
		Title:           title,
		BranchName:      branchName,
		BodyFile:        bodyFile,
		Files:           executorEvidenceDraftFiles(ctx),
	}
	if string(currentBody) != renderExecutorEvidencePRDraftMarkdown(ctx, expectedDraft) {
		return ExecutorEvidencePRCreateSpec{}, fmt.Errorf("executor evidence PR draft %s is stale; run generate-executor-evidence-pr-draft again", bodyFile)
	}

	files := executorEvidenceCreateFiles(ctx, bodyFile)
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

	approvalStage := executorEvidenceApprovalStage(stage)
	payloadHash, err := requireApprovedStage(root, sourceClusterID, projectID, approvalStage)
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
	if err := validateExecutorEvidenceGeneratedAt(evidence.GeneratedAt); err != nil {
		return executorEvidencePRContext{}, err
	}
	if err := validateExecutorEvidenceCommands(stage, status, evidence.Commands); err != nil {
		return executorEvidencePRContext{}, err
	}
	if err := requireExecutorInstructionReviewed(root, sourceClusterID, projectID, stage); err != nil {
		return executorEvidencePRContext{}, err
	}

	return executorEvidencePRContext{
		sourceClusterID: sourceClusterID,
		projectID:       projectID,
		stage:           stage,
		payloadHash:     payloadHash,
		evidenceFile:    evidenceFile,
		approvalFile:    executorEvidenceApprovalFile(sourceClusterID, projectID, approvalStage),
		evidence:        evidence,
	}, nil
}

func requireExecutorInstructionReviewed(root, sourceClusterID, projectID, stage string) error {
	projectDir := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID)
	switch stage {
	case "ddl":
		return requireReviewedSchemaDiff(filepath.Join(projectDir, "schema", "schema-diff.json"))
	case "export", "import", "cdc", "validation":
		return requireExecutablePlanStatus(filepath.Join(projectDir, "plan", stage+"-plan.yaml"), stage+" plan")
	case "cdc-enable":
		return requireExecutablePlanStatus(filepath.Join(projectDir, "plan", "cdc-plan.yaml"), "cdc plan")
	default:
		return nil
	}
}

func validateExecutorEvidenceCommands(stage, status string, commands []executorEvidenceCommandSummary) error {
	if len(commands) == 0 {
		return fmt.Errorf("executor evidence commands must contain at least one command")
	}
	hasFailedCommand := false
	seenCommandIDs := make(map[string]struct{}, len(commands))
	for i, command := range commands {
		commandID := strings.TrimSpace(command.ID)
		if commandID == "" {
			return fmt.Errorf("executor evidence command %d id is required", i+1)
		}
		if _, ok := seenCommandIDs[commandID]; ok {
			return fmt.Errorf("executor evidence command id %s is duplicated", commandID)
		}
		seenCommandIDs[commandID] = struct{}{}
		if strings.TrimSpace(command.ShellCommand) == "" {
			return fmt.Errorf("executor evidence command %s shell_command is required", commandID)
		}
		if command.ExitCode == nil {
			return fmt.Errorf("executor evidence command %s exit_code is required", commandID)
		}
		if strings.TrimSpace(status) == "succeeded" && *command.ExitCode != 0 {
			return fmt.Errorf("executor evidence status succeeded conflicts with command %s exit_code %d", commandID, *command.ExitCode)
		}
		if strings.TrimSpace(status) == "succeeded" && strings.TrimSpace(command.Error) != "" {
			return fmt.Errorf("executor evidence status succeeded conflicts with command %s error %q", commandID, command.Error)
		}
		if *command.ExitCode != 0 {
			hasFailedCommand = true
		}
		if strings.TrimSpace(command.Error) != "" {
			hasFailedCommand = true
		}
		if len(command.Args) == 0 {
			return fmt.Errorf("executor evidence command %s args must contain at least one argument", commandID)
		}
		for argIndex, arg := range command.Args {
			if strings.TrimSpace(arg) == "" {
				return fmt.Errorf("executor evidence command %s args[%d] is required", commandID, argIndex)
			}
		}
		expectedShellCommand := renderShellCommand(command.Args)
		if command.ShellCommand != expectedShellCommand {
			return fmt.Errorf("executor evidence command %s shell_command does not match args; want %q", commandID, expectedShellCommand)
		}
		startedAt, err := parseExecutorEvidenceCommandTime(commandID, "started_at", command.StartedAt)
		if err != nil {
			return err
		}
		completedAt, err := parseExecutorEvidenceCommandTime(commandID, "completed_at", command.CompletedAt)
		if err != nil {
			return err
		}
		if completedAt.Before(startedAt) {
			return fmt.Errorf("executor evidence command %s completed_at is before started_at", commandID)
		}
		if command.DurationMs == nil {
			return fmt.Errorf("executor evidence command %s duration_ms is required", commandID)
		}
		if *command.DurationMs < 0 {
			return fmt.Errorf("executor evidence command %s duration_ms must be non-negative", commandID)
		}
		if command.CDCAppliedChanges != nil && *command.CDCAppliedChanges < 0 {
			return fmt.Errorf("executor evidence command %s cdc_applied_changes must be non-negative", commandID)
		}
		if (command.DataRows == nil) != (command.DataBytes == nil) {
			return fmt.Errorf("executor evidence command %s data_rows and data_bytes must be provided together", commandID)
		}
		if strings.TrimSpace(command.DataSHA256) != "" && (command.DataRows == nil || command.DataBytes == nil) {
			return fmt.Errorf("executor evidence command %s data_sha256 requires data_rows and data_bytes", commandID)
		}
		if command.DataRows != nil && *command.DataRows < 0 {
			return fmt.Errorf("executor evidence command %s data_rows must be non-negative", commandID)
		}
		if command.DataBytes != nil && *command.DataBytes < 0 {
			return fmt.Errorf("executor evidence command %s data_bytes must be non-negative", commandID)
		}
		if strings.TrimSpace(command.DataSHA256) != "" && !isValidPayloadHash(command.DataSHA256) {
			return fmt.Errorf("executor evidence command %s data_sha256 %q must use sha256:<64 hex chars>", commandID, command.DataSHA256)
		}
		if executorEvidenceCommandRequiresDataAudit(stage, status, command.Args) && !executorEvidenceCommandHasCompleteDataAudit(command) {
			return fmt.Errorf("executor evidence command %s requires data_rows, data_bytes, and data_sha256 for %s data audit", commandID, strings.TrimSpace(stage))
		}
		if err := validateExecutorEvidenceCommandAttempts(commandID, command.AttemptCount, command.Attempts); err != nil {
			return err
		}
	}
	if strings.TrimSpace(status) == "failed" && !hasFailedCommand {
		return fmt.Errorf("executor evidence status failed requires at least one non-zero command exit_code or command error")
	}
	return nil
}

func executorEvidenceCommandRequiresDataAudit(stage, status string, args []string) bool {
	if strings.TrimSpace(status) != "succeeded" {
		return false
	}
	switch strings.TrimSpace(stage) {
	case "export":
		return true
	case "import":
		switch executorEvidenceImportEngine(args) {
		case importEngineSQLInsert:
			return true
		case importEngineTiDBImportInto:
			return executorEvidenceImportSourceNeedsLocalAudit(executorEvidenceOptionalArgValue(args, "--source-uri"))
		case importEngineTiDBLightning:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func executorEvidenceCommandHasCompleteDataAudit(command executorEvidenceCommandSummary) bool {
	if command.DataRows == nil || command.DataBytes == nil {
		return false
	}
	return isValidPayloadHash(strings.TrimSpace(command.DataSHA256))
}

func executorEvidenceImportEngine(args []string) string {
	engine := importEngineSQLInsert
	if value := executorEvidenceOptionalArgValue(args, "--engine"); value != "" {
		engine = value
	}
	return normalizeImportEngine(engine)
}

func executorEvidenceOptionalArgValue(args []string, flagName string) string {
	for i, arg := range args {
		if arg == flagName && i+1 < len(args) {
			return strings.TrimSpace(args[i+1])
		}
		if strings.HasPrefix(arg, flagName+"=") {
			return strings.TrimSpace(strings.TrimPrefix(arg, flagName+"="))
		}
	}
	return ""
}

func executorEvidenceImportSourceNeedsLocalAudit(sourceURI string) bool {
	sourceURI = strings.TrimSpace(sourceURI)
	if sourceURI == "" {
		return true
	}
	parsed, err := url.Parse(sourceURI)
	if err != nil {
		return true
	}
	switch parsed.Scheme {
	case "", "file", "s3", "gs":
		return true
	default:
		return true
	}
}

func validateExecutorEvidenceCommandAttempts(commandID string, attemptCount *int, attempts []executorEvidenceCommandAttemptSummary) error {
	if attemptCount == nil {
		if len(attempts) > 0 {
			return fmt.Errorf("executor evidence command %s attempts require attempt_count", commandID)
		}
		return nil
	}
	if *attemptCount < 1 {
		return fmt.Errorf("executor evidence command %s attempt_count must be at least 1", commandID)
	}
	if len(attempts) > 0 && len(attempts) != *attemptCount {
		return fmt.Errorf("executor evidence command %s attempt_count %d does not match attempts length %d", commandID, *attemptCount, len(attempts))
	}
	for i, attempt := range attempts {
		expectedAttempt := i + 1
		if attempt.Attempt != expectedAttempt {
			return fmt.Errorf("executor evidence command %s attempts[%d].attempt = %d, want %d", commandID, i, attempt.Attempt, expectedAttempt)
		}
		if attempt.ExitCode == nil {
			return fmt.Errorf("executor evidence command %s attempt %d exit_code is required", commandID, expectedAttempt)
		}
		startedAt, err := parseExecutorEvidenceCommandTime(commandID, fmt.Sprintf("attempt %d started_at", expectedAttempt), attempt.StartedAt)
		if err != nil {
			return err
		}
		completedAt, err := parseExecutorEvidenceCommandTime(commandID, fmt.Sprintf("attempt %d completed_at", expectedAttempt), attempt.CompletedAt)
		if err != nil {
			return err
		}
		if completedAt.Before(startedAt) {
			return fmt.Errorf("executor evidence command %s attempt %d completed_at is before started_at", commandID, expectedAttempt)
		}
		if attempt.DurationMs == nil {
			return fmt.Errorf("executor evidence command %s attempt %d duration_ms is required", commandID, expectedAttempt)
		}
		if *attempt.DurationMs < 0 {
			return fmt.Errorf("executor evidence command %s attempt %d duration_ms must be non-negative", commandID, expectedAttempt)
		}
	}
	return nil
}

func validateExecutorEvidenceGeneratedAt(generatedAt string) error {
	if strings.TrimSpace(generatedAt) == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, generatedAt); err != nil {
		return fmt.Errorf("executor evidence generated_at must be RFC3339")
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
	case "ddl", "export", "import", "cdc", "cdc-enable", "validation":
		return true
	default:
		return false
	}
}

func executorEvidenceApprovalStage(stage string) string {
	if stage == "cdc-enable" {
		return "cdc"
	}
	return stage
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

func executorEvidenceCheckpointFile(sourceClusterID string) string {
	return filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "state", "cdc-checkpoint.yaml"))
}

func executorEvidencePRBodyFile(sourceClusterID, projectID, stage string) string {
	return filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID, "prs", "executor-"+stage+"-evidence-pr.md"))
}

func executorEvidenceDraftFiles(ctx executorEvidencePRContext) []string {
	files := []string{
		ctx.evidenceFile,
		ctx.approvalFile,
	}
	if ctx.stage == "cdc" {
		files = append(files, executorEvidenceCheckpointFile(ctx.sourceClusterID))
	}
	return files
}

func executorEvidenceCreateFiles(ctx executorEvidencePRContext, bodyFile string) []string {
	files := []string{
		ctx.evidenceFile,
		bodyFile,
	}
	if ctx.stage == "cdc" {
		files = append(files, executorEvidenceCheckpointFile(ctx.sourceClusterID))
	}
	return files
}

func renderExecutorEvidencePRDraftMarkdown(ctx executorEvidencePRContext, draft ExecutorEvidencePRDraft) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# PR Draft: %s\n\n", draft.Title)
	b.WriteString("## Summary\n\n")
	fmt.Fprintf(&b, "- Source cluster: `%s`\n", ctx.sourceClusterID)
	fmt.Fprintf(&b, "- Project: `%s`\n", ctx.projectID)
	fmt.Fprintf(&b, "- Stage: `%s`\n", ctx.stage)
	fmt.Fprintf(&b, "- Status: `%s`\n", ctx.evidence.Status)
	if strings.TrimSpace(ctx.evidence.GeneratedAt) != "" {
		fmt.Fprintf(&b, "- Generated at: `%s`\n", ctx.evidence.GeneratedAt)
	}
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
	b.WriteString("- [ ] Confirm command output redaction markers are expected and no plaintext secrets are visible.\n")
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
	includeCDC := executorEvidenceTableIncludesCDCAppliedChanges(commands)
	includeDataMetrics := executorEvidenceTableIncludesDataMetrics(commands)
	includeError := executorEvidenceTableIncludesCommandError(commands)
	includeAttempts := executorEvidenceTableIncludesAttempts(commands)
	headers := []string{"Command ID", "Exit code"}
	alignments := []string{"---", "---:"}
	if includeAttempts {
		headers = append(headers, "Attempts")
		alignments = append(alignments, "---:")
	}
	headers = append(headers, "Started at", "Completed at", "Duration ms")
	alignments = append(alignments, "---", "---", "---:")
	if includeDataMetrics {
		headers = append(headers, "Data rows", "Data bytes", "Data SHA256")
		alignments = append(alignments, "---:", "---:", "---")
	}
	headers = append(headers, "Output summary")
	alignments = append(alignments, "---")
	if includeError {
		headers = append(headers, "Command error")
		alignments = append(alignments, "---")
	}
	if includeCDC {
		headers = append(headers, "CDC applied changes")
		alignments = append(alignments, "---:")
	}
	writeMarkdownTableRow(b, headers)
	writeMarkdownTableRow(b, alignments)
	for _, command := range commands {
		duration := ""
		if command.DurationMs != nil {
			duration = fmt.Sprintf("%d", *command.DurationMs)
		}
		exitCode := ""
		if command.ExitCode != nil {
			exitCode = fmt.Sprintf("%d", *command.ExitCode)
		}
		cells := []string{
			command.ID,
			exitCode,
		}
		if includeAttempts {
			cells = append(cells, executorEvidenceAttemptCountSummary(command))
		}
		cells = append(cells,
			command.StartedAt,
			command.CompletedAt,
			duration,
		)
		if includeDataMetrics {
			dataRows := ""
			if command.DataRows != nil {
				dataRows = fmt.Sprintf("%d", *command.DataRows)
			}
			dataBytes := ""
			if command.DataBytes != nil {
				dataBytes = fmt.Sprintf("%d", *command.DataBytes)
			}
			cells = append(cells, dataRows, dataBytes, strings.TrimSpace(command.DataSHA256))
		}
		cells = append(cells, executorEvidenceOutputSummary(command.Output))
		if includeError {
			cells = append(cells, redact.Text(strings.TrimSpace(command.Error)))
		}
		if includeCDC {
			cdcAppliedChanges := ""
			if command.CDCAppliedChanges != nil {
				cdcAppliedChanges = fmt.Sprintf("%d", *command.CDCAppliedChanges)
			}
			cells = append(cells, cdcAppliedChanges)
		}
		writeMarkdownTableRow(b, cells)
	}
}

func writeMarkdownTableRow(b *strings.Builder, cells []string) {
	b.WriteString("|")
	for _, cell := range cells {
		fmt.Fprintf(b, " %s |", escapeMarkdownTableCell(cell))
	}
	b.WriteString("\n")
}

func executorEvidenceAttemptCountSummary(command executorEvidenceCommandSummary) string {
	if command.AttemptCount != nil {
		return fmt.Sprintf("%d", *command.AttemptCount)
	}
	if len(command.Attempts) > 0 {
		return fmt.Sprintf("%d", len(command.Attempts))
	}
	return ""
}

func executorEvidenceTableIncludesCommandError(commands []executorEvidenceCommandSummary) bool {
	for _, command := range commands {
		if strings.TrimSpace(command.Error) != "" {
			return true
		}
	}
	return false
}

func executorEvidenceTableIncludesAttempts(commands []executorEvidenceCommandSummary) bool {
	for _, command := range commands {
		if command.AttemptCount != nil || len(command.Attempts) > 0 {
			return true
		}
	}
	return false
}

func executorEvidenceOutputSummary(output string) string {
	const maxSummaryLength = 240
	summary := strings.Join(strings.Fields(redact.Text(output)), " ")
	if summary == "" {
		return "(empty)"
	}
	if len(summary) <= maxSummaryLength {
		return summary
	}
	return summary[:maxSummaryLength-3] + "..."
}

func executorEvidenceTableIncludesCDCAppliedChanges(commands []executorEvidenceCommandSummary) bool {
	for _, command := range commands {
		if command.CDCAppliedChanges != nil {
			return true
		}
	}
	return false
}

func executorEvidenceTableIncludesDataMetrics(commands []executorEvidenceCommandSummary) bool {
	for _, command := range commands {
		if command.DataRows != nil || command.DataBytes != nil || strings.TrimSpace(command.DataSHA256) != "" {
			return true
		}
	}
	return false
}

func escapeMarkdownTableCell(value string) string {
	replacer := strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ")
	return replacer.Replace(value)
}
