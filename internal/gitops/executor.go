package gitops

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

type WorkerExecutorPrepareSpec struct {
	Binary string
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
	if stage != "export" && stage != "import" && stage != "cdc" {
		return WorkerExecutorSpec{}, fmt.Errorf("worker executor is not supported for stage %q", stage)
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
		commands, err := prepareExportExecutorCommands(projectDir, binary, sourceClusterID, projectID)
		if err != nil {
			return WorkerExecutorSpec{}, err
		}
		result.Commands = commands
	case "import":
		commands, err := prepareImportExecutorCommands(projectDir, binary, sourceClusterID, projectID)
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
	}
	return result, nil
}

func prepareExportExecutorCommands(projectDir, binary, sourceClusterID, projectID string) ([]WorkerExecutorCommand, error) {
	chunks, err := readExportPlanChunks(filepath.Join(projectDir, "plan", "export-plan.yaml"))
	if err != nil {
		return nil, err
	}
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
		commands = append(commands, newWorkerExecutorCommand(binary, chunk.ID, args))
	}
	return commands, nil
}

func prepareImportExecutorCommands(projectDir, binary, sourceClusterID, projectID string) ([]WorkerExecutorCommand, error) {
	jobs, err := readImportPlanJobs(filepath.Join(projectDir, "plan", "import-plan.yaml"))
	if err != nil {
		return nil, err
	}
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
		commands = append(commands, newWorkerExecutorCommand(binary, job.ID, args))
	}
	return commands, nil
}

func prepareCDCExecutorCommands(projectDir, binary, sourceClusterID, projectID string) ([]WorkerExecutorCommand, error) {
	plan, err := readCDCPlanSummary(filepath.Join(projectDir, "plan", "cdc-plan.yaml"))
	if err != nil {
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

func newWorkerExecutorCommand(binary, id string, args []string) WorkerExecutorCommand {
	fullArgs := append([]string{binary}, args...)
	return WorkerExecutorCommand{
		ID:           id,
		Args:         fullArgs,
		ShellCommand: renderShellCommand(fullArgs),
	}
}
