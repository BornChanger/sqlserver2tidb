package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type WorkerExecutorPrepareSpec struct {
	Binary                    string
	SourceConnectionStringEnv string
	TargetConnectionStringEnv string
	ImportBatchSize           int
}

type WorkerExecutorSpec struct {
	SourceClusterID string
	ProjectID       string
	Stage           string
	PayloadHash     string
	Binary          string
	Commands        []WorkerExecutorCommand
}

type WorkerExecutorCommand struct {
	ID           string
	Args         []string
	ShellCommand string
}

func PrepareWorkerExecutor(root, sourceClusterID, projectID, stage string, spec WorkerExecutorPrepareSpec) (WorkerExecutorSpec, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return WorkerExecutorSpec{}, err
	}
	stage = strings.ToLower(strings.TrimSpace(stage))
	if stage != "export" && stage != "import" && stage != "cdc" && stage != "validation" {
		return WorkerExecutorSpec{}, fmt.Errorf("worker executor is not supported for stage %q", stage)
	}
	if spec.ImportBatchSize < 0 {
		return WorkerExecutorSpec{}, fmt.Errorf("import batch size must be non-negative")
	}
	binary := strings.TrimSpace(spec.Binary)
	if binary == "" {
		binary = "sqlserver2tidb-executor"
	}

	payloadHash, err := requireApprovedStage(root, sourceClusterID, projectID, stage)
	if err != nil {
		return WorkerExecutorSpec{}, err
	}

	projectDir := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID)
	result := WorkerExecutorSpec{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Stage:           stage,
		PayloadHash:     payloadHash,
		Binary:          binary,
	}
	switch stage {
	case "export":
		commands, err := prepareExportExecutorCommands(projectDir, binary, sourceClusterID, projectID, spec)
		if err != nil {
			return WorkerExecutorSpec{}, err
		}
		result.Commands = commands
	case "import":
		commands, err := prepareImportExecutorCommands(projectDir, binary, sourceClusterID, projectID, spec)
		if err != nil {
			return WorkerExecutorSpec{}, err
		}
		result.Commands = commands
	case "cdc":
		commands, err := prepareCDCExecutorCommands(projectDir, binary, sourceClusterID, projectID)
		if err != nil {
			return WorkerExecutorSpec{}, err
		}
		result.Commands = commands
	case "validation":
		commands, err := prepareValidationExecutorCommands(projectDir, binary, sourceClusterID, projectID, spec)
		if err != nil {
			return WorkerExecutorSpec{}, err
		}
		result.Commands = commands
	}
	return result, nil
}

func prepareExportExecutorCommands(projectDir, binary, sourceClusterID, projectID string, spec WorkerExecutorPrepareSpec) ([]WorkerExecutorCommand, error) {
	chunks, err := readExportPlanChunks(filepath.Join(projectDir, "plan", "export-plan.yaml"))
	if err != nil {
		return nil, err
	}
	if err := validateExportPlanChunks(chunks); err != nil {
		return nil, err
	}
	sourceConnectionStringEnv := strings.TrimSpace(spec.SourceConnectionStringEnv)
	commands := make([]WorkerExecutorCommand, 0, len(chunks))
	for _, chunk := range chunks {
		args := []string{
			"export",
			"--root", ".",
			"--source-cluster-id", sourceClusterID,
			"--project-id", projectID,
			"--chunk-id", chunk.ID,
			"--source-object", chunk.SourceObject,
			"--target-object", chunk.TargetObject,
			"--output-uri", chunk.OutputURI,
		}
		if chunk.Predicate != "" {
			args = append(args, "--predicate", chunk.Predicate)
		}
		if sourceConnectionStringEnv != "" {
			args = append(args, "--source-connection-string-env", sourceConnectionStringEnv)
		}
		commands = append(commands, newWorkerExecutorCommand(binary, chunk.ID, args))
	}
	return commands, nil
}

func prepareImportExecutorCommands(projectDir, binary, sourceClusterID, projectID string, spec WorkerExecutorPrepareSpec) ([]WorkerExecutorCommand, error) {
	jobs, err := readImportPlanJobs(filepath.Join(projectDir, "plan", "import-plan.yaml"))
	if err != nil {
		return nil, err
	}
	if err := validateImportPlanJobs(jobs); err != nil {
		return nil, err
	}
	targetConnectionStringEnv := strings.TrimSpace(spec.TargetConnectionStringEnv)
	commands := make([]WorkerExecutorCommand, 0, len(jobs))
	for _, job := range jobs {
		args := []string{
			"import",
			"--root", ".",
			"--source-cluster-id", sourceClusterID,
			"--project-id", projectID,
			"--job-id", job.ID,
			"--target-object", job.TargetObject,
			"--source-uri", job.SourceURI,
			"--depends-on-export-chunk", job.DependsOnExportChunk,
		}
		if targetConnectionStringEnv != "" {
			args = append(args, "--target-connection-string-env", targetConnectionStringEnv)
		}
		if spec.ImportBatchSize > 0 {
			args = append(args, "--import-batch-size", strconv.Itoa(spec.ImportBatchSize))
		}
		commands = append(commands, newWorkerExecutorCommand(binary, job.ID, args))
	}
	return commands, nil
}

func prepareCDCExecutorCommands(projectDir, binary, sourceClusterID, projectID string) ([]WorkerExecutorCommand, error) {
	plan, err := readCDCPlanSummary(filepath.Join(projectDir, "plan", "cdc-plan.yaml"))
	if err != nil {
		return nil, err
	}
	if err := validateCDCPlanSummary(plan); err != nil {
		return nil, err
	}
	commands := make([]WorkerExecutorCommand, 0, len(plan.Tables))
	for _, table := range plan.Tables {
		args := []string{
			"cdc",
			"--root", ".",
			"--source-cluster-id", sourceClusterID,
			"--project-id", projectID,
			"--source-object", table.SourceObject,
			"--target-object", table.TargetObject,
			"--apply-batch-size", strconv.Itoa(table.ApplyBatchSize),
		}
		commands = append(commands, newWorkerExecutorCommand(binary, table.SourceObject, args))
	}
	return commands, nil
}

func prepareValidationExecutorCommands(projectDir, binary, sourceClusterID, projectID string, spec WorkerExecutorPrepareSpec) ([]WorkerExecutorCommand, error) {
	checks, err := readValidationPlanChecks(filepath.Join(projectDir, "plan", "validation-plan.yaml"))
	if err != nil {
		return nil, err
	}
	sourceConnectionStringEnv := strings.TrimSpace(spec.SourceConnectionStringEnv)
	targetConnectionStringEnv := strings.TrimSpace(spec.TargetConnectionStringEnv)
	commands := make([]WorkerExecutorCommand, 0, len(checks))
	for _, check := range checks {
		if check.Type != "row_count" && check.Type != "row-count" {
			continue
		}
		if strings.TrimSpace(check.ID) == "" {
			return nil, fmt.Errorf("validation row_count check id is required")
		}
		if strings.TrimSpace(check.SourceObject) == "" {
			return nil, fmt.Errorf("validation row_count check %q source_object is required", check.ID)
		}
		if strings.TrimSpace(check.TargetObject) == "" {
			return nil, fmt.Errorf("validation row_count check %q target_object is required", check.ID)
		}
		args := []string{
			"validate-count",
			"--root", ".",
			"--source-cluster-id", sourceClusterID,
			"--project-id", projectID,
			"--source-object", check.SourceObject,
			"--target-object", check.TargetObject,
		}
		if strings.TrimSpace(check.Predicate) != "" {
			args = append(args, "--predicate", check.Predicate)
		}
		if strings.TrimSpace(check.TargetPredicate) != "" {
			args = append(args, "--target-predicate", check.TargetPredicate)
		}
		if sourceConnectionStringEnv != "" {
			args = append(args, "--source-connection-string-env", sourceConnectionStringEnv)
		}
		if targetConnectionStringEnv != "" {
			args = append(args, "--target-connection-string-env", targetConnectionStringEnv)
		}
		commands = append(commands, newWorkerExecutorCommand(binary, check.ID, args))
	}
	if len(commands) == 0 {
		return nil, fmt.Errorf("validation plan contains no supported row_count checks")
	}
	return commands, nil
}

type validationPlanCheck struct {
	ID              string
	Type            string
	SourceObject    string
	TargetObject    string
	Predicate       string
	TargetPredicate string
}

func readValidationPlanChecks(path string) ([]validationPlanCheck, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read validation plan: %w", err)
	}
	var checks []validationPlanCheck
	for _, raw := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(trimmed, "- id:"):
			checks = append(checks, validationPlanCheck{
				ID: trimYAMLScalar(strings.TrimPrefix(trimmed, "- id:")),
			})
		case strings.HasPrefix(trimmed, "type:") && len(checks) > 0:
			checks[len(checks)-1].Type = strings.ToLower(trimYAMLScalar(strings.TrimPrefix(trimmed, "type:")))
		case strings.HasPrefix(trimmed, "source_object:") && len(checks) > 0:
			checks[len(checks)-1].SourceObject = trimYAMLScalar(strings.TrimPrefix(trimmed, "source_object:"))
		case strings.HasPrefix(trimmed, "target_object:") && len(checks) > 0:
			checks[len(checks)-1].TargetObject = trimYAMLScalar(strings.TrimPrefix(trimmed, "target_object:"))
		case strings.HasPrefix(trimmed, "predicate:") && len(checks) > 0:
			checks[len(checks)-1].Predicate = trimYAMLScalar(strings.TrimPrefix(trimmed, "predicate:"))
		case strings.HasPrefix(trimmed, "target_predicate:") && len(checks) > 0:
			checks[len(checks)-1].TargetPredicate = trimYAMLScalar(strings.TrimPrefix(trimmed, "target_predicate:"))
		}
	}
	return checks, nil
}

func newWorkerExecutorCommand(binary, id string, args []string) WorkerExecutorCommand {
	fullArgs := append([]string{binary}, args...)
	return WorkerExecutorCommand{
		ID:           id,
		Args:         fullArgs,
		ShellCommand: renderShellCommand(fullArgs),
	}
}
