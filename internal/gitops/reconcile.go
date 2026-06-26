package gitops

import (
	"fmt"
	"os"
	"path/filepath"
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

var workerReconcileStages = []string{"export", "import", "cdc", "validation"}

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
	action.Status = "ready"
	action.PayloadHash = payloadHash
	return action
}

func workerCommandForStage(stage, sourceClusterID, projectID string) string {
	command := "worker-" + stage
	if stage == "validation" {
		command = "worker-validate"
	}
	return fmt.Sprintf("sqlserver2tidb %s --root . --source-cluster-id %s --project-id %s", command, sourceClusterID, projectID)
}
