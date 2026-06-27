package gitops

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type ValidationWorkerResult struct {
	SourceClusterID string
	ProjectID       string
	PayloadHash     string
	Passed          bool
	Checks          []ValidationCheckResult
}

type ValidationCheckResult struct {
	Name    string
	Status  string
	Message string
}

type DataWorkerResult struct {
	SourceClusterID string
	ProjectID       string
	Stage           string
	PayloadHash     string
	Status          string
	Items           int
	StateFile       string
	EvidenceFile    string
}

type approvalMetadata struct {
	Action      string
	Status      string
	PayloadHash string
	ApprovedBy  []string
	ApprovedAt  string
}

var validationPayloadFiles = []string{
	"project.yaml",
	"schema/conversion-report.md",
	"schema/schema-diff.json",
	"schema/tidb-ddl/",
	"plan/validation-plan.yaml",
}

var stagePayloadFiles = map[string][]string{
	"ddl": {
		"project.yaml",
		"schema/conversion-report.md",
		"schema/schema-diff.json",
		"schema/tidb-ddl/",
	},
	"validation": validationPayloadFiles,
	"export": {
		"project.yaml",
		"plan/export-plan.yaml",
	},
	"import": {
		"project.yaml",
		"schema/tidb-ddl/",
		"plan/export-plan.yaml",
		"plan/import-plan.yaml",
	},
	"cdc": {
		"project.yaml",
		"plan/cdc-plan.yaml",
	},
}

func ComputePayloadHashForStage(root, sourceClusterID, projectID, stage string) (string, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return "", err
	}
	stage = strings.ToLower(strings.TrimSpace(stage))
	payloadFiles, ok := stagePayloadFiles[stage]
	if !ok {
		return "", fmt.Errorf("payload hash is not supported for stage %q", stage)
	}

	projectRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID))
	files, err := collectPayloadFiles(root, projectRel, payloadFiles)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", fmt.Errorf("read payload file %s: %w", rel, err)
		}
		hasher.Write([]byte(rel))
		hasher.Write([]byte{0})
		hasher.Write(data)
		hasher.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

func RunValidationWorker(root, sourceClusterID, projectID string) (ValidationWorkerResult, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return ValidationWorkerResult{}, err
	}
	payloadHash, err := requireApprovedStage(root, sourceClusterID, projectID, "validation")
	if err != nil {
		return ValidationWorkerResult{}, err
	}

	result := ValidationWorkerResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		PayloadHash:     payloadHash,
		Passed:          true,
	}
	projectDir := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID)
	validationPlanPath := filepath.Join(projectDir, "plan", "validation-plan.yaml")
	if err := requireExecutablePlanStatus(validationPlanPath, "validation plan"); err != nil {
		return ValidationWorkerResult{}, err
	}

	diff, diffErr := readSchemaDiffForValidation(filepath.Join(projectDir, "schema", "schema-diff.json"))
	if diffErr != nil {
		result.addCheck("schema_diff_parseable", false, diffErr.Error())
	} else {
		result.addCheck("schema_diff_parseable", true, "schema-diff.json is parseable")
	}
	if hasSQL, err := hasSQLFiles(filepath.Join(projectDir, "schema", "tidb-ddl")); err != nil {
		result.addCheck("schema_ddl_present", false, err.Error())
	} else if !hasSQL {
		result.addCheck("schema_ddl_present", false, "schema/tidb-ddl has no SQL files")
	} else {
		result.addCheck("schema_ddl_present", true, "schema/tidb-ddl contains SQL files")
	}
	if diffErr == nil {
		if diff.Summary.ManualReviewItems > 0 {
			result.addCheck("schema_manual_review_cleared", false, fmt.Sprintf("%d manual review items remain", diff.Summary.ManualReviewItems))
		} else {
			result.addCheck("schema_manual_review_cleared", true, "no manual review items remain")
		}
	}
	if info, err := os.Stat(filepath.Join(projectDir, "schema", "conversion-report.md")); err != nil || info.IsDir() {
		result.addCheck("schema_conversion_report_present", false, "schema/conversion-report.md is missing")
	} else {
		result.addCheck("schema_conversion_report_present", true, "schema/conversion-report.md exists")
	}
	if info, err := os.Stat(validationPlanPath); err != nil || info.IsDir() {
		result.addCheck("validation_plan_present", false, "plan/validation-plan.yaml is missing")
	} else {
		result.addCheck("validation_plan_present", true, "plan/validation-plan.yaml exists")
		if err := validateValidationPlanContent(validationPlanPath); err != nil {
			result.addCheck("validation_plan_checks_valid", false, err.Error())
		} else {
			summary, err := summarizeValidationPlanCheckTypes(validationPlanPath)
			if err != nil {
				result.addCheck("validation_plan_checks_valid", false, err.Error())
			} else {
				result.addCheck("validation_plan_checks_valid", true, fmt.Sprintf("validation checks are structurally valid (%s)", summary))
			}
		}
	}

	if err := writeValidationWorkerState(projectDir, result); err != nil {
		return ValidationWorkerResult{}, err
	}
	if err := writeValidationWorkerReport(projectDir, result); err != nil {
		return ValidationWorkerResult{}, err
	}
	return result, nil
}

func RunExportWorker(root, sourceClusterID, projectID string) (DataWorkerResult, error) {
	return runDataWorker(root, sourceClusterID, projectID, "export")
}

func RunImportWorker(root, sourceClusterID, projectID string) (DataWorkerResult, error) {
	return runDataWorker(root, sourceClusterID, projectID, "import")
}

func RunCDCWorker(root, sourceClusterID, projectID string) (DataWorkerResult, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return DataWorkerResult{}, err
	}
	payloadHash, err := requireApprovedStage(root, sourceClusterID, projectID, "cdc")
	if err != nil {
		return DataWorkerResult{}, err
	}

	clusterDir := filepath.Join(root, "clusters", sourceClusterID)
	projectDir := filepath.Join(clusterDir, "projects", projectID)
	if err := requireExecutablePlanStatus(filepath.Join(projectDir, "plan", "cdc-plan.yaml"), "cdc plan"); err != nil {
		return DataWorkerResult{}, err
	}
	plan, err := readCDCPlanSummary(filepath.Join(projectDir, "plan", "cdc-plan.yaml"))
	if err != nil {
		return DataWorkerResult{}, err
	}
	if err := validateCDCPlanSummary(plan); err != nil {
		return DataWorkerResult{}, err
	}
	result := DataWorkerResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Stage:           "cdc",
		PayloadHash:     payloadHash,
		Status:          "planned",
		Items:           len(plan.Tables),
		StateFile:       "state/migration-state.yaml",
		EvidenceFile:    "evidence/cdc-catchup.json",
	}
	if err := writeCDCProjectState(projectDir, result); err != nil {
		return DataWorkerResult{}, err
	}
	if err := writeCDCClusterCheckpoint(clusterDir, result, plan); err != nil {
		return DataWorkerResult{}, err
	}
	if err := writeDataWorkerEvidence(projectDir, result); err != nil {
		return DataWorkerResult{}, err
	}
	return result, nil
}

func runDataWorker(root, sourceClusterID, projectID, stage string) (DataWorkerResult, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return DataWorkerResult{}, err
	}
	payloadHash, err := requireApprovedStage(root, sourceClusterID, projectID, stage)
	if err != nil {
		return DataWorkerResult{}, err
	}

	projectDir := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID)
	if stage == "export" {
		if err := requireExecutablePlanStatus(filepath.Join(projectDir, "plan", "export-plan.yaml"), "export plan"); err != nil {
			return DataWorkerResult{}, err
		}
	}
	if stage == "import" {
		if err := requireExecutablePlanStatus(filepath.Join(projectDir, "plan", "import-plan.yaml"), "import plan"); err != nil {
			return DataWorkerResult{}, err
		}
	}
	result := DataWorkerResult{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Stage:           stage,
		PayloadHash:     payloadHash,
		Status:          "planned",
	}
	switch stage {
	case "export":
		chunks, err := readExportPlanChunks(filepath.Join(projectDir, "plan", "export-plan.yaml"))
		if err != nil {
			return DataWorkerResult{}, err
		}
		if err := validateExportPlanChunks(chunks); err != nil {
			return DataWorkerResult{}, err
		}
		result.Items = len(chunks)
		result.StateFile = "state/export-chunks.yaml"
		result.EvidenceFile = "evidence/precheck.json"
		if err := writeExportWorkerState(projectDir, result, chunks); err != nil {
			return DataWorkerResult{}, err
		}
	case "import":
		jobs, err := readImportPlanJobs(filepath.Join(projectDir, "plan", "import-plan.yaml"))
		if err != nil {
			return DataWorkerResult{}, err
		}
		if err := validateImportPlanJobs(jobs); err != nil {
			return DataWorkerResult{}, err
		}
		result.Items = len(jobs)
		result.StateFile = "state/import-jobs.yaml"
		result.EvidenceFile = "evidence/import-summary.json"
		if err := writeImportWorkerState(projectDir, result, jobs); err != nil {
			return DataWorkerResult{}, err
		}
	default:
		return DataWorkerResult{}, fmt.Errorf("unsupported data worker stage %q", stage)
	}
	if err := writeDataWorkerEvidence(projectDir, result); err != nil {
		return DataWorkerResult{}, err
	}
	return result, nil
}

func (result *ValidationWorkerResult) addCheck(name string, passed bool, message string) {
	status := "passed"
	if !passed {
		status = "failed"
		result.Passed = false
	}
	result.Checks = append(result.Checks, ValidationCheckResult{
		Name:    name,
		Status:  status,
		Message: message,
	})
}

func summarizeValidationPlanCheckTypes(path string) (string, error) {
	checks, err := readValidationPlanChecks(path)
	if err != nil {
		return "", err
	}
	counts := map[string]int{
		"row_count":    0,
		"checksum":     0,
		"sampled_hash": 0,
		"business_sql": 0,
	}
	for _, check := range checks {
		switch check.Type {
		case "row_count", "row-count":
			counts["row_count"]++
		case "checksum", "sampled_hash", "business_sql":
			counts[check.Type]++
		}
	}
	parts := make([]string, 0, len(counts))
	for _, checkType := range []string{"row_count", "checksum", "sampled_hash", "business_sql"} {
		if counts[checkType] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[checkType], checkType))
		}
	}
	if len(parts) == 0 {
		return "0 supported checks", nil
	}
	return strings.Join(parts, ", "), nil
}

func validateProjectAddress(root, sourceClusterID, projectID string) error {
	if !idPattern.MatchString(sourceClusterID) {
		return fmt.Errorf("invalid source cluster id %q", sourceClusterID)
	}
	if !idPattern.MatchString(projectID) {
		return fmt.Errorf("invalid project id %q", projectID)
	}
	clusterDir := filepath.Join(root, "clusters", sourceClusterID)
	if info, err := os.Stat(clusterDir); err != nil || !info.IsDir() {
		return fmt.Errorf("source cluster %q does not exist", sourceClusterID)
	}
	projectDir := filepath.Join(clusterDir, "projects", projectID)
	if info, err := os.Stat(projectDir); err != nil || !info.IsDir() {
		return fmt.Errorf("project %q does not exist under source cluster %q", projectID, sourceClusterID)
	}
	project, err := readProjectMetadata(filepath.Join(projectDir, "project.yaml"))
	if err != nil {
		return err
	}
	if project.ProjectID != projectID {
		return fmt.Errorf("project.yaml project_id %q does not match %q", project.ProjectID, projectID)
	}
	if project.SourceClusterID != sourceClusterID {
		return fmt.Errorf("project.yaml source_cluster_id %q does not match %q", project.SourceClusterID, sourceClusterID)
	}
	return nil
}

func requireExecutablePlanStatus(path, planKind string) error {
	status, err := readPlanTopLevelScalar(path, "status")
	if err != nil {
		return err
	}
	if status != "reviewed" && status != "approved" {
		return fmt.Errorf("%s status is %q, want reviewed or approved", planKind, status)
	}
	return nil
}

func requireApprovedValidation(root, sourceClusterID, projectID string) (string, error) {
	return requireApprovedStage(root, sourceClusterID, projectID, "validation")
}

func requireApprovedStage(root, sourceClusterID, projectID, stage string) (string, error) {
	approvalPath := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID, "approvals", stage+"-approval.yaml")
	approval, err := readApprovalMetadata(approvalPath)
	if err != nil {
		return "", err
	}
	if approval.Action != stage {
		return "", fmt.Errorf("%s approval action is %q, want %s", stage, approval.Action, stage)
	}
	if approval.Status != "approved" {
		return "", fmt.Errorf("%s approval is not approved", stage)
	}
	if len(approval.ApprovedBy) == 0 {
		return "", fmt.Errorf("%s approval has no approved_by reviewers", stage)
	}
	actualHash, err := ComputePayloadHashForStage(root, sourceClusterID, projectID, stage)
	if err != nil {
		return "", err
	}
	if approval.PayloadHash != actualHash {
		return "", fmt.Errorf("%s approval payload hash mismatch: expected %s, actual %s", stage, approval.PayloadHash, actualHash)
	}
	return actualHash, nil
}

func readApprovalMetadata(path string) (approvalMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return approvalMetadata{}, fmt.Errorf("read approval: %w", err)
	}
	var approval approvalMetadata
	listKey := ""
	for _, raw := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			if listKey == "approved_by" {
				approval.ApprovedBy = append(approval.ApprovedBy, trimYAMLScalar(strings.TrimPrefix(trimmed, "- ")))
			}
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := trimYAMLScalar(parts[1])
		listKey = ""
		switch key {
		case "action":
			approval.Action = value
		case "status":
			approval.Status = value
		case "payload_hash":
			approval.PayloadHash = value
		case "approved_at":
			approval.ApprovedAt = value
		case "approved_by":
			listKey = "approved_by"
			if value == "[]" {
				approval.ApprovedBy = nil
			}
		}
	}
	return approval, nil
}

func readExportPlanChunks(path string) ([]dataExportChunkState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read export plan: %w", err)
	}
	var chunks []dataExportChunkState
	var currentSource, currentTarget string
	for _, raw := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(trimmed, "- source_object:"):
			currentSource = trimYAMLScalar(strings.TrimPrefix(trimmed, "- source_object:"))
			currentTarget = ""
		case strings.HasPrefix(trimmed, "target_object:"):
			currentTarget = trimYAMLScalar(strings.TrimPrefix(trimmed, "target_object:"))
		case strings.HasPrefix(trimmed, "- id:"):
			chunks = append(chunks, dataExportChunkState{
				ID:           trimYAMLScalar(strings.TrimPrefix(trimmed, "- id:")),
				SourceObject: currentSource,
				TargetObject: currentTarget,
				Status:       "planned",
			})
		case strings.HasPrefix(trimmed, "estimated_rows:") && len(chunks) > 0:
			value := trimYAMLScalar(strings.TrimPrefix(trimmed, "estimated_rows:"))
			rows, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parse export chunk estimated_rows %q: %w", value, err)
			}
			chunks[len(chunks)-1].EstimatedRows = rows
		case strings.HasPrefix(trimmed, "output_uri:") && len(chunks) > 0:
			chunks[len(chunks)-1].OutputURI = trimYAMLScalar(strings.TrimPrefix(trimmed, "output_uri:"))
		case strings.HasPrefix(trimmed, "predicate:") && len(chunks) > 0:
			chunks[len(chunks)-1].Predicate = trimYAMLScalar(strings.TrimPrefix(trimmed, "predicate:"))
		}
	}
	return chunks, nil
}

func readPlanTopLevelScalar(path, key string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read plan: %w", err)
	}
	prefix := key + ":"
	for _, raw := range strings.Split(string(data), "\n") {
		if strings.TrimLeft(raw, " \t") != raw {
			continue
		}
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, prefix) {
			return trimYAMLScalar(strings.TrimPrefix(trimmed, prefix)), nil
		}
	}
	return "", nil
}

func validateExportPlanChunks(chunks []dataExportChunkState) error {
	if len(chunks) == 0 {
		return fmt.Errorf("export plan contains no chunks")
	}
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk.ID) == "" {
			return fmt.Errorf("export chunk id is required")
		}
		if strings.TrimSpace(chunk.SourceObject) == "" {
			return fmt.Errorf("export chunk %s source_object is required", chunk.ID)
		}
		if strings.TrimSpace(chunk.TargetObject) == "" {
			return fmt.Errorf("export chunk %s target_object is required", chunk.ID)
		}
		if strings.TrimSpace(chunk.OutputURI) == "" {
			return fmt.Errorf("export chunk %s output_uri is required", chunk.ID)
		}
		if containsTODOMarker(chunk.Predicate) {
			return fmt.Errorf("export chunk %s predicate still contains TODO", chunk.ID)
		}
	}
	return nil
}

func readImportPlanJobs(path string) ([]dataImportJobState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read import plan: %w", err)
	}
	var jobs []dataImportJobState
	collectingFields := false
	for _, raw := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(trimmed, "- id:"):
			collectingFields = false
			jobs = append(jobs, dataImportJobState{
				ID:     trimYAMLScalar(strings.TrimPrefix(trimmed, "- id:")),
				Status: "planned",
			})
		case strings.HasPrefix(trimmed, "target_object:") && len(jobs) > 0:
			collectingFields = false
			jobs[len(jobs)-1].TargetObject = trimYAMLScalar(strings.TrimPrefix(trimmed, "target_object:"))
		case strings.HasPrefix(trimmed, "source_uri:") && len(jobs) > 0:
			collectingFields = false
			jobs[len(jobs)-1].SourceURI = trimYAMLScalar(strings.TrimPrefix(trimmed, "source_uri:"))
		case strings.HasPrefix(trimmed, "depends_on_export_chunk:") && len(jobs) > 0:
			collectingFields = false
			jobs[len(jobs)-1].DependsOnExportChunk = trimYAMLScalar(strings.TrimPrefix(trimmed, "depends_on_export_chunk:"))
		case strings.HasPrefix(trimmed, "fields:") && len(jobs) > 0:
			collectingFields = true
		case collectingFields && strings.HasPrefix(trimmed, "- ") && len(jobs) > 0:
			field := trimYAMLScalar(strings.TrimPrefix(trimmed, "- "))
			if field != "" {
				jobs[len(jobs)-1].Fields = append(jobs[len(jobs)-1].Fields, field)
			}
		case trimmed != "":
			collectingFields = false
		}
	}
	return jobs, nil
}

func validateImportPlanJobs(jobs []dataImportJobState) error {
	if len(jobs) == 0 {
		return fmt.Errorf("import plan contains no jobs")
	}
	seenIDs := make(map[string]struct{}, len(jobs))
	for _, job := range jobs {
		jobID := strings.TrimSpace(job.ID)
		if jobID == "" {
			return fmt.Errorf("import job id is required")
		}
		if _, ok := seenIDs[jobID]; ok {
			return fmt.Errorf("duplicate import job id %s", jobID)
		}
		seenIDs[jobID] = struct{}{}
		if strings.TrimSpace(job.TargetObject) == "" {
			return fmt.Errorf("import job %s target_object is required", job.ID)
		}
		if strings.TrimSpace(job.SourceURI) == "" {
			return fmt.Errorf("import job %s source_uri is required", job.ID)
		}
		if strings.TrimSpace(job.DependsOnExportChunk) == "" {
			return fmt.Errorf("import job %s depends_on_export_chunk is required", job.ID)
		}
	}
	return nil
}

type dataExportChunkState struct {
	ID            string
	SourceObject  string
	TargetObject  string
	OutputURI     string
	Predicate     string
	EstimatedRows int64
	Status        string
}

type dataImportJobState struct {
	ID                   string
	TargetObject         string
	SourceURI            string
	DependsOnExportChunk string
	Status               string
	Fields               []string
}

type cdcPlanSummary struct {
	Mode   string
	Tables []cdcTrackedTableState
}

type cdcTrackedTableState struct {
	SourceObject   string
	TargetObject   string
	ApplyBatchSize int
}

func readCDCPlanSummary(path string) (cdcPlanSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cdcPlanSummary{}, fmt.Errorf("read cdc plan: %w", err)
	}
	plan := cdcPlanSummary{Mode: "sqlserver-cdc"}
	for _, raw := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(trimmed, "mode:"):
			mode := trimYAMLScalar(strings.TrimPrefix(trimmed, "mode:"))
			if mode != "" {
				plan.Mode = mode
			}
		case strings.HasPrefix(trimmed, "- source_object:"):
			plan.Tables = append(plan.Tables, cdcTrackedTableState{
				SourceObject: trimYAMLScalar(strings.TrimPrefix(trimmed, "- source_object:")),
			})
		case strings.HasPrefix(trimmed, "target_object:") && len(plan.Tables) > 0:
			plan.Tables[len(plan.Tables)-1].TargetObject = trimYAMLScalar(strings.TrimPrefix(trimmed, "target_object:"))
		case strings.HasPrefix(trimmed, "apply_batch_size:") && len(plan.Tables) > 0:
			value := trimYAMLScalar(strings.TrimPrefix(trimmed, "apply_batch_size:"))
			batchSize, err := strconv.Atoi(value)
			if err != nil {
				return cdcPlanSummary{}, fmt.Errorf("parse cdc apply_batch_size %q: %w", value, err)
			}
			plan.Tables[len(plan.Tables)-1].ApplyBatchSize = batchSize
		}
	}
	return plan, nil
}

func validateCDCPlanSummary(plan cdcPlanSummary) error {
	if len(plan.Tables) == 0 {
		return fmt.Errorf("cdc plan contains no tracked tables")
	}
	seenSources := make(map[string]struct{}, len(plan.Tables))
	for _, table := range plan.Tables {
		sourceObject := strings.TrimSpace(table.SourceObject)
		if sourceObject == "" {
			return fmt.Errorf("cdc tracked table source_object is required")
		}
		if _, ok := seenSources[sourceObject]; ok {
			return fmt.Errorf("duplicate cdc tracked source_object %s", sourceObject)
		}
		seenSources[sourceObject] = struct{}{}
		if strings.TrimSpace(table.TargetObject) == "" {
			return fmt.Errorf("cdc tracked table %s target_object is required", table.SourceObject)
		}
		if table.ApplyBatchSize <= 0 {
			return fmt.Errorf("cdc tracked table %s apply_batch_size must be positive", table.SourceObject)
		}
	}
	return nil
}

func collectPayloadFiles(root, projectRel string, entries []string) ([]string, error) {
	var files []string
	for _, entry := range entries {
		entryRel := filepath.ToSlash(filepath.Join(projectRel, entry))
		if strings.HasSuffix(entry, "/") {
			dir := filepath.Join(root, filepath.FromSlash(entryRel))
			walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				files = append(files, filepath.ToSlash(rel))
				return nil
			})
			if walkErr != nil {
				return nil, fmt.Errorf("collect payload directory %s: %w", entryRel, walkErr)
			}
			continue
		}
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(entryRel))); err != nil || info.IsDir() {
			return nil, fmt.Errorf("payload file %s is missing", entryRel)
		}
		files = append(files, entryRel)
	}
	sort.Strings(files)
	return files, nil
}

func readSchemaDiffForValidation(path string) (schemaDiffDocument, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return schemaDiffDocument{}, fmt.Errorf("read schema-diff.json: %w", err)
	}
	var diff schemaDiffDocument
	if err := json.Unmarshal(data, &diff); err != nil {
		return schemaDiffDocument{}, fmt.Errorf("parse schema-diff.json: %w", err)
	}
	return diff, nil
}

func hasSQLFiles(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("read schema/tidb-ddl: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			return true, nil
		}
	}
	return false, nil
}

func writeValidationWorkerState(projectDir string, result ValidationWorkerResult) error {
	path := filepath.Join(projectDir, "state", "validation-status.yaml")
	status := validationStatus(result.Passed)
	var b strings.Builder
	fmt.Fprintf(&b, "project_id: %s\n", result.ProjectID)
	fmt.Fprintf(&b, "source_cluster_id: %s\n", result.SourceClusterID)
	b.WriteString("phase: validation\n")
	fmt.Fprintf(&b, "status: %s\n", status)
	fmt.Fprintf(&b, "payload_hash: %s\n", result.PayloadHash)
	fmt.Fprintf(&b, "updated_at: %s\n", quoteYAML(nowUTC()))
	b.WriteString("checks:\n")
	for _, check := range result.Checks {
		fmt.Fprintf(&b, "  - name: %s\n", check.Name)
		fmt.Fprintf(&b, "    status: %s\n", check.Status)
		fmt.Fprintf(&b, "    message: %s\n", quoteYAML(check.Message))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write validation status: %w", err)
	}
	return nil
}

func writeValidationWorkerReport(projectDir string, result ValidationWorkerResult) error {
	path := filepath.Join(projectDir, "evidence", "validation-report.md")
	status := validationStatus(result.Passed)
	var b strings.Builder
	b.WriteString("# Validation Report\n\n")
	fmt.Fprintf(&b, "- Source cluster: `%s`\n", result.SourceClusterID)
	fmt.Fprintf(&b, "- Project: `%s`\n", result.ProjectID)
	fmt.Fprintf(&b, "- Status: `%s`\n", status)
	fmt.Fprintf(&b, "- Payload hash: `%s`\n\n", result.PayloadHash)
	b.WriteString("## Checks\n\n")
	b.WriteString("| Check | Status | Message |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, check := range result.Checks {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", check.Name, check.Status, escapeMarkdownTable(check.Message))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write validation report: %w", err)
	}
	return nil
}

func writeExportWorkerState(projectDir string, result DataWorkerResult, chunks []dataExportChunkState) error {
	path := filepath.Join(projectDir, "state", "export-chunks.yaml")
	var b strings.Builder
	fmt.Fprintf(&b, "project_id: %s\n", result.ProjectID)
	fmt.Fprintf(&b, "source_cluster_id: %s\n", result.SourceClusterID)
	b.WriteString("phase: export\n")
	fmt.Fprintf(&b, "status: %s\n", result.Status)
	fmt.Fprintf(&b, "payload_hash: %s\n", result.PayloadHash)
	fmt.Fprintf(&b, "updated_at: %s\n", quoteYAML(nowUTC()))
	if len(chunks) == 0 {
		b.WriteString("chunks: []\n")
	} else {
		b.WriteString("chunks:\n")
		for _, chunk := range chunks {
			fmt.Fprintf(&b, "  - id: %s\n", chunk.ID)
			fmt.Fprintf(&b, "    status: %s\n", chunk.Status)
			fmt.Fprintf(&b, "    source_object: %s\n", chunk.SourceObject)
			fmt.Fprintf(&b, "    target_object: %s\n", chunk.TargetObject)
			fmt.Fprintf(&b, "    output_uri: %s\n", chunk.OutputURI)
			fmt.Fprintf(&b, "    estimated_rows: %d\n", chunk.EstimatedRows)
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write export chunks state: %w", err)
	}
	return nil
}

func writeImportWorkerState(projectDir string, result DataWorkerResult, jobs []dataImportJobState) error {
	path := filepath.Join(projectDir, "state", "import-jobs.yaml")
	var b strings.Builder
	fmt.Fprintf(&b, "project_id: %s\n", result.ProjectID)
	fmt.Fprintf(&b, "source_cluster_id: %s\n", result.SourceClusterID)
	b.WriteString("phase: import\n")
	fmt.Fprintf(&b, "status: %s\n", result.Status)
	fmt.Fprintf(&b, "payload_hash: %s\n", result.PayloadHash)
	fmt.Fprintf(&b, "updated_at: %s\n", quoteYAML(nowUTC()))
	if len(jobs) == 0 {
		b.WriteString("jobs: []\n")
	} else {
		b.WriteString("jobs:\n")
		for _, job := range jobs {
			fmt.Fprintf(&b, "  - id: %s\n", job.ID)
			fmt.Fprintf(&b, "    status: %s\n", job.Status)
			fmt.Fprintf(&b, "    target_object: %s\n", job.TargetObject)
			fmt.Fprintf(&b, "    source_uri: %s\n", job.SourceURI)
			fmt.Fprintf(&b, "    depends_on_export_chunk: %s\n", job.DependsOnExportChunk)
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write import jobs state: %w", err)
	}
	return nil
}

func writeCDCProjectState(projectDir string, result DataWorkerResult) error {
	path := filepath.Join(projectDir, "state", "migration-state.yaml")
	var b strings.Builder
	fmt.Fprintf(&b, "project_id: %s\n", result.ProjectID)
	fmt.Fprintf(&b, "source_cluster_id: %s\n", result.SourceClusterID)
	b.WriteString("phase: cdc\n")
	fmt.Fprintf(&b, "status: %s\n", result.Status)
	fmt.Fprintf(&b, "payload_hash: %s\n", result.PayloadHash)
	b.WriteString("cdc_plan: plan/cdc-plan.yaml\n")
	fmt.Fprintf(&b, "tracked_tables: %d\n", result.Items)
	fmt.Fprintf(&b, "updated_at: %s\n", quoteYAML(nowUTC()))
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write cdc project state: %w", err)
	}
	return nil
}

func writeCDCClusterCheckpoint(clusterDir string, result DataWorkerResult, plan cdcPlanSummary) error {
	path := filepath.Join(clusterDir, "state", "cdc-checkpoint.yaml")
	var b strings.Builder
	fmt.Fprintf(&b, "source_cluster_id: %s\n", result.SourceClusterID)
	b.WriteString("phase: cdc\n")
	fmt.Fprintf(&b, "status: %s\n", result.Status)
	fmt.Fprintf(&b, "project_id: %s\n", result.ProjectID)
	fmt.Fprintf(&b, "payload_hash: %s\n", result.PayloadHash)
	fmt.Fprintf(&b, "mode: %s\n", plan.Mode)
	b.WriteString("checkpoint_scope: source-cluster\n")
	b.WriteString("checkpoints: []\n")
	fmt.Fprintf(&b, "updated_at: %s\n", quoteYAML(nowUTC()))
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write cdc checkpoint: %w", err)
	}
	return nil
}

func writeDataWorkerEvidence(projectDir string, result DataWorkerResult) error {
	path := filepath.Join(projectDir, filepath.FromSlash(result.EvidenceFile))
	evidence := struct {
		Stage           string `json:"stage"`
		Status          string `json:"status"`
		ProjectID       string `json:"project_id"`
		SourceClusterID string `json:"source_cluster_id"`
		PayloadHash     string `json:"payload_hash"`
		Items           int    `json:"items"`
		GeneratedAt     string `json:"generated_at"`
	}{
		Stage:           result.Stage,
		Status:          result.Status,
		ProjectID:       result.ProjectID,
		SourceClusterID: result.SourceClusterID,
		PayloadHash:     result.PayloadHash,
		Items:           result.Items,
		GeneratedAt:     nowUTC(),
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s evidence: %w", result.Stage, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s evidence: %w", result.Stage, err)
	}
	return nil
}

func validationStatus(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}
