package gitops

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type CDCHealthSpec struct {
	MaxLSN           string
	MinLSNs          map[string]string
	MaxCheckpointAge time.Duration
	Now              time.Time
}

type CDCHealthReport struct {
	SourceClusterID      string           `json:"source_cluster_id"`
	ProjectID            string           `json:"project_id"`
	Status               string           `json:"status"`
	GeneratedAt          string           `json:"generated_at"`
	CheckpointStatus     string           `json:"checkpoint_status"`
	CheckpointUpdatedAt  string           `json:"checkpoint_updated_at"`
	CheckpointAgeSeconds *int64           `json:"checkpoint_age_seconds,omitempty"`
	MaxLSN               string           `json:"max_lsn,omitempty"`
	TrackedTables        int              `json:"tracked_tables"`
	LaggingTables        int              `json:"lagging_tables"`
	ExpiredTables        int              `json:"expired_tables"`
	Tables               []CDCHealthTable `json:"tables"`
	Alerts               []CDCHealthAlert `json:"alerts"`
}

type CDCHealthTable struct {
	SourceObject      string `json:"source_object"`
	TargetObject      string `json:"target_object"`
	CheckpointFromLSN string `json:"checkpoint_from_lsn,omitempty"`
	CheckpointToLSN   string `json:"checkpoint_to_lsn,omitempty"`
	PlanFromLSN       string `json:"plan_from_lsn,omitempty"`
	PlanToLSN         string `json:"plan_to_lsn,omitempty"`
	MinLSN            string `json:"min_lsn,omitempty"`
	MaxLSN            string `json:"max_lsn,omitempty"`
	LagStatus         string `json:"lag_status"`
	RetentionStatus   string `json:"retention_status"`
	AppliedChanges    *int   `json:"applied_changes,omitempty"`
	CompletedAt       string `json:"completed_at,omitempty"`
}

type CDCHealthAlert struct {
	Severity     string `json:"severity"`
	Code         string `json:"code"`
	SourceObject string `json:"source_object,omitempty"`
	Message      string `json:"message"`
}

func EvaluateCDCHealth(root, sourceClusterID, projectID string, spec CDCHealthSpec) (CDCHealthReport, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return CDCHealthReport{}, err
	}
	now := spec.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if spec.MinLSNs == nil {
		spec.MinLSNs = map[string]string{}
	}
	maxLSN := strings.TrimSpace(spec.MaxLSN)
	var maxLSNBytes []byte
	if maxLSN != "" {
		var err error
		maxLSNBytes, err = parseCDCPlanLSN(maxLSN, "max_lsn")
		if err != nil {
			return CDCHealthReport{}, err
		}
	}
	for sourceObject, minLSN := range spec.MinLSNs {
		if strings.TrimSpace(minLSN) == "" {
			continue
		}
		if _, err := parseCDCPlanLSN(minLSN, "min_lsn"); err != nil {
			return CDCHealthReport{}, fmt.Errorf("cdc min_lsn for %s: %w", sourceObject, err)
		}
	}

	projectDir := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID)
	plan, err := readCDCPlanSummary(filepath.Join(projectDir, "plan", "cdc-plan.yaml"))
	if err != nil {
		return CDCHealthReport{}, err
	}
	checkpointPath := filepath.Join(root, "clusters", sourceClusterID, "state", "cdc-checkpoint.yaml")
	checkpointStatus, err := readPlanTopLevelScalar(checkpointPath, "status")
	if err != nil {
		return CDCHealthReport{}, err
	}
	checkpointUpdatedAt, err := readPlanTopLevelScalar(checkpointPath, "updated_at")
	if err != nil {
		return CDCHealthReport{}, err
	}
	checkpointEntries, err := readCDCCheckpointEntries(checkpointPath)
	if err != nil {
		return CDCHealthReport{}, err
	}
	checkpoints := make(map[string]cdcCheckpointEntry, len(checkpointEntries))
	for _, entry := range checkpointEntries {
		if strings.TrimSpace(entry.SourceObject) != "" {
			checkpoints[entry.SourceObject] = entry
		}
	}

	report := CDCHealthReport{
		SourceClusterID:     sourceClusterID,
		ProjectID:           projectID,
		Status:              "ok",
		GeneratedAt:         now.UTC().Format(time.RFC3339),
		CheckpointStatus:    strings.TrimSpace(checkpointStatus),
		CheckpointUpdatedAt: strings.TrimSpace(checkpointUpdatedAt),
		MaxLSN:              maxLSN,
		TrackedTables:       len(plan.Tables),
	}
	if strings.EqualFold(report.CheckpointStatus, "failed") {
		report.addAlert("critical", "checkpoint_failed", "", "CDC checkpoint status is failed")
	}
	if report.CheckpointUpdatedAt != "" {
		updatedAt, err := time.Parse(time.RFC3339, report.CheckpointUpdatedAt)
		if err != nil {
			return CDCHealthReport{}, fmt.Errorf("cdc checkpoint updated_at must be RFC3339: %w", err)
		}
		age := int64(now.Sub(updatedAt).Seconds())
		if age < 0 {
			age = 0
		}
		report.CheckpointAgeSeconds = &age
		if spec.MaxCheckpointAge > 0 && time.Duration(age)*time.Second > spec.MaxCheckpointAge {
			report.addAlert("critical", "checkpoint_stale", "", fmt.Sprintf("checkpoint age %s exceeds %s", (time.Duration(age)*time.Second).String(), spec.MaxCheckpointAge.String()))
		}
	}

	for _, table := range plan.Tables {
		healthTable := CDCHealthTable{
			SourceObject:    table.SourceObject,
			TargetObject:    table.TargetObject,
			PlanFromLSN:     table.FromLSN,
			PlanToLSN:       table.ToLSN,
			MinLSN:          strings.TrimSpace(spec.MinLSNs[table.SourceObject]),
			MaxLSN:          maxLSN,
			LagStatus:       "unknown",
			RetentionStatus: "unknown",
		}
		checkpoint, ok := checkpoints[table.SourceObject]
		if !ok {
			report.addAlert("critical", "checkpoint_missing", table.SourceObject, "CDC checkpoint entry is missing")
			report.Tables = append(report.Tables, healthTable)
			continue
		}
		healthTable.CheckpointFromLSN = checkpoint.FromLSN
		healthTable.CheckpointToLSN = checkpoint.ToLSN
		healthTable.AppliedChanges = checkpoint.AppliedChanges
		healthTable.CompletedAt = checkpoint.CompletedAt

		checkpointLSNBytes, err := parseCDCPlanLSN(checkpoint.ToLSN, "checkpoint to_lsn")
		if err != nil {
			return CDCHealthReport{}, fmt.Errorf("cdc checkpoint for %s %w", table.SourceObject, err)
		}
		if len(maxLSNBytes) > 0 {
			switch bytes.Compare(checkpointLSNBytes, maxLSNBytes) {
			case -1:
				healthTable.LagStatus = "behind"
				report.LaggingTables++
				report.addAlert("warning", "cdc_lag", table.SourceObject, fmt.Sprintf("checkpoint %s is behind max_lsn %s", checkpoint.ToLSN, maxLSN))
			case 0:
				healthTable.LagStatus = "caught_up"
			case 1:
				healthTable.LagStatus = "ahead"
				report.addAlert("critical", "checkpoint_ahead", table.SourceObject, fmt.Sprintf("checkpoint %s is ahead of max_lsn %s", checkpoint.ToLSN, maxLSN))
			}
		}
		if healthTable.MinLSN != "" {
			minLSNBytes, err := parseCDCPlanLSN(healthTable.MinLSN, "min_lsn")
			if err != nil {
				return CDCHealthReport{}, fmt.Errorf("cdc min_lsn for %s: %w", table.SourceObject, err)
			}
			if bytes.Compare(checkpointLSNBytes, minLSNBytes) < 0 {
				healthTable.RetentionStatus = "expired"
				report.ExpiredTables++
				report.addAlert("critical", "retention_expired", table.SourceObject, fmt.Sprintf("checkpoint %s is before SQL Server min_lsn %s", checkpoint.ToLSN, healthTable.MinLSN))
			} else {
				healthTable.RetentionStatus = "covered"
			}
		}
		report.Tables = append(report.Tables, healthTable)
	}
	report.finalizeStatus()
	return report, nil
}

func (report *CDCHealthReport) addAlert(severity, code, sourceObject, message string) {
	report.Alerts = append(report.Alerts, CDCHealthAlert{
		Severity:     severity,
		Code:         code,
		SourceObject: sourceObject,
		Message:      message,
	})
}

func (report *CDCHealthReport) finalizeStatus() {
	status := "ok"
	for _, alert := range report.Alerts {
		switch alert.Severity {
		case "critical":
			report.Status = "critical"
			return
		case "warning":
			status = "warning"
		}
	}
	report.Status = status
}
