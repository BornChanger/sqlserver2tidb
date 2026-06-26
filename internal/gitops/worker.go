package gitops

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

type approvalMetadata struct {
	Action      string
	Status      string
	PayloadHash string
	ApprovedBy  []string
}

var validationPayloadFiles = []string{
	"project.yaml",
	"schema/conversion-report.md",
	"schema/schema-diff.json",
	"schema/tidb-ddl/",
	"plan/validation-plan.yaml",
}

func ComputePayloadHashForStage(root, sourceClusterID, projectID, stage string) (string, error) {
	if err := validateProjectAddress(root, sourceClusterID, projectID); err != nil {
		return "", err
	}
	stage = strings.ToLower(strings.TrimSpace(stage))
	if stage != "validation" {
		return "", fmt.Errorf("payload hash is not supported for stage %q", stage)
	}

	projectRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID))
	files, err := collectPayloadFiles(root, projectRel, validationPayloadFiles)
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
	payloadHash, err := requireApprovedValidation(root, sourceClusterID, projectID)
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
	if info, err := os.Stat(filepath.Join(projectDir, "plan", "validation-plan.yaml")); err != nil || info.IsDir() {
		result.addCheck("validation_plan_present", false, "plan/validation-plan.yaml is missing")
	} else {
		result.addCheck("validation_plan_present", true, "plan/validation-plan.yaml exists")
	}

	if err := writeValidationWorkerState(projectDir, result); err != nil {
		return ValidationWorkerResult{}, err
	}
	if err := writeValidationWorkerReport(projectDir, result); err != nil {
		return ValidationWorkerResult{}, err
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

func requireApprovedValidation(root, sourceClusterID, projectID string) (string, error) {
	approvalPath := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID, "approvals", "validation-approval.yaml")
	approval, err := readApprovalMetadata(approvalPath)
	if err != nil {
		return "", err
	}
	if approval.Action != "validation" {
		return "", fmt.Errorf("validation approval action is %q, want validation", approval.Action)
	}
	if approval.Status != "approved" {
		return "", fmt.Errorf("validation approval is not approved")
	}
	if len(approval.ApprovedBy) == 0 {
		return "", fmt.Errorf("validation approval has no approved_by reviewers")
	}
	actualHash, err := ComputePayloadHashForStage(root, sourceClusterID, projectID, "validation")
	if err != nil {
		return "", err
	}
	if approval.PayloadHash != actualHash {
		return "", fmt.Errorf("validation approval payload hash mismatch: expected %s, actual %s", approval.PayloadHash, actualHash)
	}
	return actualHash, nil
}

func readApprovalMetadata(path string) (approvalMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return approvalMetadata{}, fmt.Errorf("read validation approval: %w", err)
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
		case "approved_by":
			listKey = "approved_by"
			if value == "[]" {
				approval.ApprovedBy = nil
			}
		}
	}
	return approval, nil
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

func validationStatus(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}
