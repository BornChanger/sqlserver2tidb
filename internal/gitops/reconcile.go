package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type WorkerReconcileReport struct {
	Projects       int
	ReadyActions   int
	BlockedActions int
	Actions        []WorkerReconcileAction
}

type WorkerReconcileAction struct {
	SourceClusterID string
	ProjectID       string
	Stage           string
	Status          string
	Reason          string
	PayloadHash     string
	Command         string
}

type WorkerReconcileExecuteSpec struct {
	Holder        string
	LeaseTTL      time.Duration
	CreatePRDraft bool
}

type WorkerReconcileExecutionResult struct {
	Action       WorkerReconcileAction
	LeaseID      string
	LeaseFile    string
	Status       string
	StateFile    string
	EvidenceFile string
	PRTitle      string
	BranchName   string
	PRBodyFile   string
}

type workerLeaseMetadata struct {
	SourceClusterID string
	Holder          string
	LeaseID         string
	Phase           string
	ProjectID       string
	ExpiresAt       string
	RenewedAt       string
}

var workerReconcileStages = []string{"ddl", "export", "import", "cdc", "validation"}
var workerReconcileExecutableStages = map[string]bool{
	"export":     true,
	"import":     true,
	"cdc":        true,
	"validation": true,
}

func PlanWorkerReconcile(root string) (WorkerReconcileReport, error) {
	clustersDir := filepath.Join(root, "clusters")
	clusterEntries, err := os.ReadDir(clustersDir)
	if err != nil {
		return WorkerReconcileReport{}, fmt.Errorf("read clusters directory: %w", err)
	}

	var report WorkerReconcileReport
	for _, clusterEntry := range clusterEntries {
		if !clusterEntry.IsDir() {
			continue
		}
		sourceClusterID := clusterEntry.Name()
		projectsDir := filepath.Join(clustersDir, sourceClusterID, "projects")
		projectEntries, err := os.ReadDir(projectsDir)
		if err != nil {
			return WorkerReconcileReport{}, fmt.Errorf("read projects for source cluster %q: %w", sourceClusterID, err)
		}
		for _, projectEntry := range projectEntries {
			if !projectEntry.IsDir() {
				continue
			}
			projectID := projectEntry.Name()
			if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
				return WorkerReconcileReport{}, err
			}
			report.Projects++
			for _, stage := range workerReconcileStages {
				action := planWorkerReconcileAction(root, sourceClusterID, projectID, stage)
				if action.Status == "ready" {
					report.ReadyActions++
				} else {
					report.BlockedActions++
				}
				report.Actions = append(report.Actions, action)
			}
		}
	}
	return report, nil
}

func ExecuteNextWorkerReconcile(root string, spec WorkerReconcileExecuteSpec) (WorkerReconcileExecutionResult, error) {
	spec.Holder = strings.TrimSpace(spec.Holder)
	if spec.Holder == "" {
		return WorkerReconcileExecutionResult{}, fmt.Errorf("worker reconcile holder is required")
	}
	if spec.LeaseTTL <= 0 {
		spec.LeaseTTL = 15 * time.Minute
	}

	report, err := PlanWorkerReconcile(root)
	if err != nil {
		return WorkerReconcileExecutionResult{}, err
	}
	var selected WorkerReconcileAction
	for _, action := range report.Actions {
		if action.Status == "ready" && workerReconcileExecutableStages[action.Stage] {
			selected = action
			break
		}
	}
	if selected.Stage == "" {
		if report.ReadyActions > 0 {
			return WorkerReconcileExecutionResult{}, fmt.Errorf("no ready metadata worker actions; ready executor-only actions must be run with worker-executor")
		}
		return WorkerReconcileExecutionResult{}, fmt.Errorf("no ready worker actions")
	}

	lease, err := acquireWorkerLease(root, selected, spec)
	if err != nil {
		return WorkerReconcileExecutionResult{}, err
	}
	result, err := executeWorkerReconcileAction(root, selected)
	if err != nil {
		return WorkerReconcileExecutionResult{}, err
	}
	result.LeaseID = lease.LeaseID
	result.LeaseFile = "state/worker-lease.yaml"
	if spec.CreatePRDraft {
		draft, err := writeWorkerStatePRDraft(root, result)
		if err != nil {
			return WorkerReconcileExecutionResult{}, err
		}
		result.PRTitle = draft.Title
		result.BranchName = draft.BranchName
		result.PRBodyFile = draft.BodyFile
	}
	return result, nil
}

func planWorkerReconcileAction(root, sourceClusterID, projectID, stage string) WorkerReconcileAction {
	action := WorkerReconcileAction{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Stage:           stage,
		Status:          "blocked",
		Command:         workerCommandForStage(stage, sourceClusterID, projectID),
	}
	payloadHash, err := requireApprovedStage(root, sourceClusterID, projectID, stage)
	if err != nil {
		action.Reason = err.Error()
		return action
	}
	switch stage {
	case "ddl":
		schemaDiffPath := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID, "schema", "schema-diff.json")
		if err := requireReviewedSchemaDiff(schemaDiffPath); err != nil {
			action.Reason = err.Error()
			return action
		}
	case "export", "import", "cdc", "validation":
		planPath := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID, "plan", stage+"-plan.yaml")
		if err := requireExecutablePlanStatus(planPath, stage+" plan"); err != nil {
			action.Reason = err.Error()
			return action
		}
	}
	action.Status = "ready"
	action.PayloadHash = payloadHash
	return action
}

func acquireWorkerLease(root string, action WorkerReconcileAction, spec WorkerReconcileExecuteSpec) (workerLeaseMetadata, error) {
	path := filepath.Join(root, "clusters", action.SourceClusterID, "state", "worker-lease.yaml")
	current, err := readWorkerLeaseMetadata(path)
	if err != nil {
		return workerLeaseMetadata{}, err
	}
	now := time.Now().UTC()
	if current.Holder != "" && current.Holder != spec.Holder {
		if strings.TrimSpace(current.ExpiresAt) == "" {
			return workerLeaseMetadata{}, fmt.Errorf("worker lease for source cluster %q is held by %q", action.SourceClusterID, current.Holder)
		}
		expiresAt, err := time.Parse(time.RFC3339, current.ExpiresAt)
		if err != nil {
			return workerLeaseMetadata{}, fmt.Errorf("parse worker lease expires_at for source cluster %q: %w", action.SourceClusterID, err)
		}
		if now.Before(expiresAt) {
			return workerLeaseMetadata{}, fmt.Errorf("worker lease for source cluster %q is held by %q until %s", action.SourceClusterID, current.Holder, current.ExpiresAt)
		}
	}

	lease := workerLeaseMetadata{
		SourceClusterID: action.SourceClusterID,
		Holder:          spec.Holder,
		LeaseID:         fmt.Sprintf("lease-%d", now.UnixNano()),
		Phase:           action.Stage,
		ProjectID:       action.ProjectID,
		ExpiresAt:       now.Add(spec.LeaseTTL).Format(time.RFC3339),
		RenewedAt:       now.Format(time.RFC3339),
	}
	if err := writeWorkerLeaseMetadata(path, lease); err != nil {
		return workerLeaseMetadata{}, err
	}
	return lease, nil
}

func executeWorkerReconcileAction(root string, action WorkerReconcileAction) (WorkerReconcileExecutionResult, error) {
	result := WorkerReconcileExecutionResult{
		Action: action,
	}
	switch action.Stage {
	case "export":
		workerResult, err := RunExportWorker(root, action.SourceClusterID, action.ProjectID)
		if err != nil {
			return WorkerReconcileExecutionResult{}, err
		}
		result.Status = workerResult.Status
		result.StateFile = workerResult.StateFile
		result.EvidenceFile = workerResult.EvidenceFile
	case "import":
		workerResult, err := RunImportWorker(root, action.SourceClusterID, action.ProjectID)
		if err != nil {
			return WorkerReconcileExecutionResult{}, err
		}
		result.Status = workerResult.Status
		result.StateFile = workerResult.StateFile
		result.EvidenceFile = workerResult.EvidenceFile
	case "cdc":
		workerResult, err := RunCDCWorker(root, action.SourceClusterID, action.ProjectID)
		if err != nil {
			return WorkerReconcileExecutionResult{}, err
		}
		result.Status = workerResult.Status
		result.StateFile = workerResult.StateFile
		result.EvidenceFile = workerResult.EvidenceFile
	case "validation":
		workerResult, err := RunValidationWorker(root, action.SourceClusterID, action.ProjectID)
		if err != nil {
			return WorkerReconcileExecutionResult{}, err
		}
		result.Status = validationStatus(workerResult.Passed)
		result.StateFile = "state/validation-status.yaml"
		result.EvidenceFile = "evidence/validation-report.md"
	default:
		return WorkerReconcileExecutionResult{}, fmt.Errorf("unsupported worker reconcile stage %q", action.Stage)
	}
	return result, nil
}

func readWorkerLeaseMetadata(path string) (workerLeaseMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return workerLeaseMetadata{}, fmt.Errorf("read worker lease: %w", err)
	}
	var lease workerLeaseMetadata
	for _, raw := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := trimYAMLScalar(parts[1])
		switch key {
		case "source_cluster_id":
			lease.SourceClusterID = value
		case "holder":
			lease.Holder = value
		case "lease_id":
			lease.LeaseID = value
		case "phase":
			lease.Phase = value
		case "project_id":
			lease.ProjectID = value
		case "expires_at":
			lease.ExpiresAt = value
		case "renewed_at":
			lease.RenewedAt = value
		}
	}
	return lease, nil
}

func writeWorkerLeaseMetadata(path string, lease workerLeaseMetadata) error {
	var b strings.Builder
	fmt.Fprintf(&b, "source_cluster_id: %s\n", lease.SourceClusterID)
	fmt.Fprintf(&b, "holder: %s\n", lease.Holder)
	fmt.Fprintf(&b, "lease_id: %s\n", lease.LeaseID)
	fmt.Fprintf(&b, "phase: %s\n", lease.Phase)
	fmt.Fprintf(&b, "project_id: %s\n", lease.ProjectID)
	fmt.Fprintf(&b, "expires_at: %s\n", quoteYAML(lease.ExpiresAt))
	fmt.Fprintf(&b, "renewed_at: %s\n", quoteYAML(lease.RenewedAt))
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write worker lease: %w", err)
	}
	return nil
}

func workerCommandForStage(stage, sourceClusterID, projectID string) string {
	if stage == "ddl" {
		return fmt.Sprintf("sqlserver2tidb worker-executor --root . --source-cluster-id %s --project-id %s --stage ddl", sourceClusterID, projectID)
	}
	command := "worker-" + stage
	if stage == "validation" {
		command = "worker-validate"
	}
	return fmt.Sprintf("sqlserver2tidb %s --root . --source-cluster-id %s --project-id %s", command, sourceClusterID, projectID)
}
