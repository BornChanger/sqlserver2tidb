package gitops

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ValidationReport struct {
	Valid        bool
	Errors       []string
	CheckedDirs  int
	CheckedFiles int
}

var requiredGlobalFiles = []string{
	"global/policies/approval-policy.yaml",
	"global/policies/execution-policy.yaml",
	"global/policies/file-schema-policy.yaml",
	"global/schemas/cluster.schema.json",
	"global/schemas/project.schema.json",
	"global/schemas/migration-plan.schema.json",
	"global/schemas/export-plan.schema.json",
	"global/schemas/import-plan.schema.json",
	"global/schemas/cdc-plan.schema.json",
	"global/schemas/validation-plan.schema.json",
	"global/templates/project.yaml",
	"global/templates/migration-plan.yaml",
	"global/templates/cutover-runbook.md",
}

var requiredGlobalDirs = []string{
	"clusters",
	"global/policies",
	"global/schemas",
	"global/templates",
}

var requiredFileSchemaPolicyMappings = []struct {
	Key  string
	Path string
}{
	{Key: "cluster", Path: "global/schemas/cluster.schema.json"},
	{Key: "project", Path: "global/schemas/project.schema.json"},
	{Key: "migration_plan", Path: "global/schemas/migration-plan.schema.json"},
	{Key: "export_plan", Path: "global/schemas/export-plan.schema.json"},
	{Key: "import_plan", Path: "global/schemas/import-plan.schema.json"},
	{Key: "cdc_plan", Path: "global/schemas/cdc-plan.schema.json"},
	{Key: "validation_plan", Path: "global/schemas/validation-plan.schema.json"},
}

var requiredClusterFiles = []string{
	"cluster.yaml",
	"source-profile.yaml",
	"inventory/inventory.json",
	"inventory/compatibility-report.md",
	"inventory/schema-issues.yaml",
	"state/cdc-checkpoint.yaml",
	"state/worker-lease.yaml",
}

var requiredClusterDirs = []string{
	"inventory/source-ddl",
	"state",
	"projects",
}

var clusterStateFiles = []string{
	"state/cdc-checkpoint.yaml",
	"state/worker-lease.yaml",
}

var requiredProjectFiles = []string{
	"project.yaml",
	"schema/conversion-report.md",
	"schema/schema-diff.json",
	"plan/migration-plan.yaml",
	"plan/export-plan.yaml",
	"plan/import-plan.yaml",
	"plan/cdc-plan.yaml",
	"plan/validation-plan.yaml",
	"plan/cutover-runbook.md",
	"state/migration-state.yaml",
	"state/export-chunks.yaml",
	"state/import-jobs.yaml",
	"state/validation-status.yaml",
	"evidence/precheck.json",
	"evidence/import-summary.json",
	"evidence/cdc-catchup.json",
	"evidence/validation-report.md",
	"evidence/cutover-evidence.md",
	"evidence/post-cutover-report.md",
	"approvals/ddl-approval.yaml",
	"approvals/export-approval.yaml",
	"approvals/import-approval.yaml",
	"approvals/cdc-approval.yaml",
	"approvals/validation-approval.yaml",
	"approvals/cutover-approval.yaml",
}

var requiredProjectDirs = []string{
	"schema/tidb-ddl",
	"plan",
	"state",
	"evidence",
	"approvals",
}

var projectStateFiles = []string{
	"state/migration-state.yaml",
	"state/export-chunks.yaml",
	"state/import-jobs.yaml",
	"state/validation-status.yaml",
}

type projectApprovalFile struct {
	Stage string
	Rel   string
}

var projectApprovalFiles = []projectApprovalFile{
	{Stage: "ddl", Rel: "approvals/ddl-approval.yaml"},
	{Stage: "export", Rel: "approvals/export-approval.yaml"},
	{Stage: "import", Rel: "approvals/import-approval.yaml"},
	{Stage: "cdc", Rel: "approvals/cdc-approval.yaml"},
	{Stage: "validation", Rel: "approvals/validation-approval.yaml"},
	{Stage: "cutover", Rel: "approvals/cutover-approval.yaml"},
}

var projectEvidenceJSONFiles = []string{
	"evidence/precheck.json",
	"evidence/import-summary.json",
	"evidence/cdc-catchup.json",
}

func ValidateRepo(root string) (ValidationReport, error) {
	report := ValidationReport{Valid: true}
	if err := report.checkDir(root, "."); err != nil {
		return report, err
	}

	for _, rel := range requiredGlobalDirs {
		report.requireDir(root, rel)
	}
	for _, rel := range requiredGlobalFiles {
		report.requireFile(root, rel)
	}
	validateFileSchemaPolicy(root, &report)

	if err := validateClusters(root, &report); err != nil {
		return report, err
	}

	report.Valid = len(report.Errors) == 0
	return report, nil
}

func validateFileSchemaPolicy(root string, report *ValidationReport) {
	path := filepath.Join(root, "global", "policies", "file-schema-policy.yaml")
	mappings, err := readFileSchemaPolicyMappings(path)
	if err != nil {
		if !os.IsNotExist(err) {
			report.addError(fmt.Sprintf("cannot read file schema policy: %v", err))
		}
		return
	}
	for _, required := range requiredFileSchemaPolicyMappings {
		actual := mappings[required.Key]
		if actual == "" {
			report.addError(fmt.Sprintf("file schema policy missing mapping %s: %s", required.Key, required.Path))
			continue
		}
		if actual != required.Path {
			report.addError(fmt.Sprintf("file schema policy mapping %s = %s, want %s", required.Key, actual, required.Path))
		}
	}
}

func readFileSchemaPolicyMappings(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	mappings := make(map[string]string)
	inSchemas := false
	for _, raw := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "schemas:" {
			inSchemas = true
			continue
		}
		if !inSchemas {
			continue
		}
		if raw[0] != ' ' && raw[0] != '\t' {
			inSchemas = false
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		mappings[strings.TrimSpace(key)] = trimYAMLScalar(value)
	}
	return mappings, nil
}

func validateClusters(root string, report *ValidationReport) error {
	clustersDir := filepath.Join(root, "clusters")
	entries, err := os.ReadDir(clustersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("clusters", name))
		if !entry.IsDir() {
			report.addError("unexpected non-directory in clusters: " + rel)
			continue
		}
		if !idPattern.MatchString(name) {
			report.addError(fmt.Sprintf("invalid source cluster directory id: %s", rel))
		}
		validateClusterDir(root, rel, report)
	}
	return nil
}

func validateClusterDir(root, clusterRel string, report *ValidationReport) {
	for _, rel := range requiredClusterDirs {
		report.requireDir(root, filepath.ToSlash(filepath.Join(clusterRel, rel)))
	}
	for _, rel := range requiredClusterFiles {
		report.requireFile(root, filepath.ToSlash(filepath.Join(clusterRel, rel)))
	}
	inventoryRel := filepath.ToSlash(filepath.Join(clusterRel, "inventory", "inventory.json"))
	inventoryPath := filepath.Join(root, filepath.FromSlash(inventoryRel))
	if info, err := os.Stat(inventoryPath); err == nil && !info.IsDir() {
		if _, err := readSQLServerInventory(inventoryPath); err != nil {
			report.addError(fmt.Sprintf("invalid inventory %s: %v", inventoryRel, err))
		}
	}
	clusterMetadataRel := filepath.ToSlash(filepath.Join(clusterRel, "cluster.yaml"))
	clusterMetadataPath := filepath.Join(root, filepath.FromSlash(clusterMetadataRel))
	var clusterMeta *clusterMetadata
	if info, err := os.Stat(clusterMetadataPath); err == nil && !info.IsDir() {
		expectedClusterID := filepath.Base(filepath.FromSlash(clusterRel))
		meta, err := readClusterMetadata(clusterMetadataPath)
		if err != nil {
			report.addError(fmt.Sprintf("invalid cluster metadata %s: %v", clusterMetadataRel, err))
		} else {
			clusterMeta = &meta
			if err := validateClusterMetadata(meta, expectedClusterID); err != nil {
				report.addError(fmt.Sprintf("invalid cluster metadata %s: %v", clusterMetadataRel, err))
			}
			sourceProfileRel := filepath.ToSlash(filepath.Join(clusterRel, "source-profile.yaml"))
			sourceProfilePath := filepath.Join(root, filepath.FromSlash(sourceProfileRel))
			if info, err := os.Stat(sourceProfilePath); err == nil && !info.IsDir() {
				if err := validateSourceProfileMetadataContent(sourceProfilePath, meta); err != nil {
					report.addError(fmt.Sprintf("invalid source profile %s: %v", sourceProfileRel, err))
				}
			}
			for _, stateRel := range clusterStateFiles {
				rel := filepath.ToSlash(filepath.Join(clusterRel, stateRel))
				path := filepath.Join(root, filepath.FromSlash(rel))
				if info, err := os.Stat(path); err == nil && !info.IsDir() {
					var err error
					switch stateRel {
					case "state/cdc-checkpoint.yaml":
						err = validateCDCCheckpointMetadataContent(path, meta)
					case "state/worker-lease.yaml":
						err = validateWorkerLeaseMetadataContent(path, meta)
					default:
						err = validateSourceClusterOwnedYAMLContent(path, meta)
					}
					if err != nil {
						report.addError(fmt.Sprintf("invalid cluster state %s: %v", rel, err))
					}
				}
			}
		}
	}
	validateProjects(root, filepath.ToSlash(filepath.Join(clusterRel, "projects")), clusterMeta, report)
}

func validateClusterMetadataContent(path, expectedClusterID string) error {
	meta, err := readClusterMetadata(path)
	if err != nil {
		return err
	}
	return validateClusterMetadata(meta, expectedClusterID)
}

func validateClusterMetadata(meta clusterMetadata, expectedClusterID string) error {
	if meta.ClusterID != expectedClusterID {
		return fmt.Errorf("cluster_id %q does not match directory id %q", meta.ClusterID, expectedClusterID)
	}
	return validateCluster(ClusterSpec{
		ClusterID:              meta.ClusterID,
		DisplayName:            meta.DisplayName,
		Listener:               meta.Listener,
		Port:                   meta.Port,
		SecretRef:              meta.SecretRef,
		CDCMode:                meta.CDCMode,
		RetentionHoursRequired: meta.RetentionHoursRequired,
		Owners:                 meta.Owners,
	})
}

func validateSourceProfileMetadataContent(path string, cluster clusterMetadata) error {
	meta, err := readSourceProfileMetadata(path)
	if err != nil {
		return err
	}
	if meta.ClusterID != cluster.ClusterID {
		return fmt.Errorf("cluster_id %q does not match cluster metadata %q", meta.ClusterID, cluster.ClusterID)
	}
	if meta.Listener != cluster.Listener {
		return fmt.Errorf("listener %q does not match cluster listener %q", meta.Listener, cluster.Listener)
	}
	if meta.Port != cluster.Port {
		return fmt.Errorf("port %d does not match cluster port %d", meta.Port, cluster.Port)
	}
	if meta.SecretRef != cluster.SecretRef {
		return fmt.Errorf("secret_ref %q does not match cluster secret_ref %q", meta.SecretRef, cluster.SecretRef)
	}
	return nil
}

func validateSourceClusterOwnedYAMLContent(path string, cluster clusterMetadata) error {
	sourceClusterID, err := readPlanTopLevelScalar(path, "source_cluster_id")
	if err != nil {
		return err
	}
	if sourceClusterID != cluster.ClusterID {
		return fmt.Errorf("source_cluster_id %q does not match cluster metadata %q", sourceClusterID, cluster.ClusterID)
	}
	return nil
}

func validateCDCCheckpointMetadataContent(path string, cluster clusterMetadata) error {
	if err := validateSourceClusterOwnedYAMLContent(path, cluster); err != nil {
		return err
	}
	if err := validateCDCCheckpointMode(path, "capture_mode", cluster); err != nil {
		return err
	}
	if err := validateCDCCheckpointMode(path, "mode", cluster); err != nil {
		return err
	}
	status, err := readPlanTopLevelScalar(path, "status")
	if err != nil {
		return err
	}
	if !isSupportedCDCCheckpointStatus(status) {
		return fmt.Errorf("unsupported CDC checkpoint status %q; supported statuses: not_started, planned, running, caught_up, failed", status)
	}
	if err := validateCDCCheckpointUpdatedAt(path); err != nil {
		return err
	}
	return nil
}

func validateCDCCheckpointMode(path, key string, cluster clusterMetadata) error {
	mode, err := readPlanTopLevelScalar(path, key)
	if err != nil {
		return err
	}
	if strings.TrimSpace(mode) != "" && mode != cluster.CDCMode {
		return fmt.Errorf("%s %q does not match cluster cdc mode %q", key, mode, cluster.CDCMode)
	}
	return nil
}

func isSupportedCDCCheckpointStatus(status string) bool {
	switch status {
	case "not_started", "planned", "running", "caught_up", "failed":
		return true
	default:
		return false
	}
}

func validateCDCCheckpointUpdatedAt(path string) error {
	updatedAt, err := readPlanTopLevelScalar(path, "updated_at")
	if err != nil {
		return err
	}
	if strings.TrimSpace(updatedAt) == "" {
		return errors.New("CDC checkpoint updated_at is required")
	}
	if _, err := time.Parse(time.RFC3339, updatedAt); err != nil {
		return errors.New("CDC checkpoint updated_at must be RFC3339")
	}
	return nil
}

func validateWorkerLeaseMetadataContent(path string, cluster clusterMetadata) error {
	if err := validateSourceClusterOwnedYAMLContent(path, cluster); err != nil {
		return err
	}
	phase, err := readPlanTopLevelScalar(path, "phase")
	if err != nil {
		return err
	}
	if !isSupportedWorkerLeasePhase(phase) {
		return fmt.Errorf("unsupported worker lease phase %q; supported phases: idle, export, import, cdc, validation", phase)
	}
	if phase == "idle" {
		return nil
	}
	if err := requireWorkerLeaseScalar(path, "holder"); err != nil {
		return err
	}
	if err := requireWorkerLeaseScalar(path, "lease_id"); err != nil {
		return err
	}
	expiresAt, err := requireWorkerLeaseTime(path, "expires_at")
	if err != nil {
		return err
	}
	renewedAt, err := requireWorkerLeaseTime(path, "renewed_at")
	if err != nil {
		return err
	}
	if expiresAt.Before(renewedAt) {
		return errors.New("active worker lease expires_at must not be before renewed_at")
	}
	return nil
}

func requireWorkerLeaseScalar(path, key string) error {
	value, err := readPlanTopLevelScalar(path, key)
	if err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("active worker lease requires %s", key)
	}
	return nil
}

func requireWorkerLeaseTime(path, key string) (time.Time, error) {
	value, err := readPlanTopLevelScalar(path, key)
	if err != nil {
		return time.Time{}, err
	}
	if strings.TrimSpace(value) == "" {
		return time.Time{}, fmt.Errorf("active worker lease requires %s", key)
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("active worker lease %s must be RFC3339", key)
	}
	return parsed, nil
}

func isSupportedWorkerLeasePhase(phase string) bool {
	switch phase {
	case "idle", "export", "import", "cdc", "validation":
		return true
	default:
		return false
	}
}

func validateProjects(root, projectsRel string, cluster *clusterMetadata, report *ValidationReport) {
	projectsDir := filepath.Join(root, filepath.FromSlash(projectsRel))
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		report.addError(fmt.Sprintf("cannot read projects directory: %s: %v", projectsRel, err))
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		projectRel := filepath.ToSlash(filepath.Join(projectsRel, name))
		if !entry.IsDir() {
			report.addError("unexpected non-directory in projects: " + projectRel)
			continue
		}
		if !idPattern.MatchString(name) {
			report.addError(fmt.Sprintf("invalid project directory id: %s", projectRel))
		}
		for _, rel := range requiredProjectDirs {
			report.requireDir(root, filepath.ToSlash(filepath.Join(projectRel, rel)))
		}
		for _, rel := range requiredProjectFiles {
			report.requireFile(root, filepath.ToSlash(filepath.Join(projectRel, rel)))
		}
		validateProjectContent(root, projectRel, cluster, report)
	}
}

func validateProjectContent(root, projectRel string, cluster *clusterMetadata, report *ValidationReport) {
	projectMetadataRel := filepath.ToSlash(filepath.Join(projectRel, "project.yaml"))
	projectMetadataPath := filepath.Join(root, filepath.FromSlash(projectMetadataRel))
	var projectMeta projectMetadata
	projectMetaLoaded := false
	if info, err := os.Stat(projectMetadataPath); err == nil && !info.IsDir() {
		expectedProjectID := filepath.Base(filepath.FromSlash(projectRel))
		expectedClusterID := filepath.Base(filepath.Dir(filepath.Dir(filepath.FromSlash(projectRel))))
		meta, err := readProjectMetadata(projectMetadataPath)
		if err != nil {
			report.addError(fmt.Sprintf("invalid project metadata %s: %v", projectMetadataRel, err))
		} else {
			projectMeta = meta
			projectMetaLoaded = true
			if err := validateProjectMetadata(meta, expectedClusterID, expectedProjectID); err != nil {
				report.addError(fmt.Sprintf("invalid project metadata %s: %v", projectMetadataRel, err))
			}
		}
	}

	schemaDiffRel := filepath.ToSlash(filepath.Join(projectRel, "schema", "schema-diff.json"))
	schemaDiffPath := filepath.Join(root, filepath.FromSlash(schemaDiffRel))
	if info, err := os.Stat(schemaDiffPath); err == nil && !info.IsDir() {
		var projectForSchema *projectMetadata
		if projectMetaLoaded {
			projectForSchema = &projectMeta
		}
		if err := validateSchemaDiffContent(schemaDiffPath, projectForSchema); err != nil {
			report.addError(fmt.Sprintf("invalid schema diff %s: %v", schemaDiffRel, err))
		}
	}

	var projectForEvidence *projectMetadata
	if projectMetaLoaded {
		projectForEvidence = &projectMeta
	}
	for _, evidenceRel := range projectEvidenceJSONFiles {
		rel := filepath.ToSlash(filepath.Join(projectRel, evidenceRel))
		path := filepath.Join(root, filepath.FromSlash(rel))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			if err := validateEvidenceJSONContent(path, projectForEvidence); err != nil {
				report.addError(fmt.Sprintf("invalid evidence %s: %v", rel, err))
			}
		}
	}
	evidenceDirRel := filepath.ToSlash(filepath.Join(projectRel, "evidence"))
	evidenceDir := filepath.Join(root, filepath.FromSlash(evidenceDirRel))
	if entries, err := os.ReadDir(evidenceDir); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasPrefix(name, "executor-") || !strings.HasSuffix(name, "-run.json") {
				continue
			}
			stage := strings.TrimSuffix(strings.TrimPrefix(name, "executor-"), "-run.json")
			rel := filepath.ToSlash(filepath.Join(evidenceDirRel, name))
			path := filepath.Join(root, filepath.FromSlash(rel))
			if err := validateExecutorEvidenceContent(path, projectForEvidence, stage); err != nil {
				report.addError(fmt.Sprintf("invalid executor evidence %s: %v", rel, err))
			}
		}
	}

	migrationPlanRel := filepath.ToSlash(filepath.Join(projectRel, "plan", "migration-plan.yaml"))
	migrationPlanPath := filepath.Join(root, filepath.FromSlash(migrationPlanRel))
	if projectMetaLoaded {
		if info, err := os.Stat(migrationPlanPath); err == nil && !info.IsDir() {
			if err := validateMigrationPlanContent(migrationPlanPath, projectMeta); err != nil {
				report.addError(fmt.Sprintf("invalid migration plan %s: %v", migrationPlanRel, err))
			}
		}
		for _, stateRel := range projectStateFiles {
			rel := filepath.ToSlash(filepath.Join(projectRel, stateRel))
			path := filepath.Join(root, filepath.FromSlash(rel))
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				if err := validateProjectStateContent(path, stateRel, projectMeta); err != nil {
					report.addError(fmt.Sprintf("invalid state file %s: %v", rel, err))
				}
			}
		}
		for _, approval := range projectApprovalFiles {
			rel := filepath.ToSlash(filepath.Join(projectRel, approval.Rel))
			path := filepath.Join(root, filepath.FromSlash(rel))
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				if err := validateApprovalMetadataContent(path, projectMeta, approval.Stage); err != nil {
					report.addError(fmt.Sprintf("invalid approval %s: %v", rel, err))
				}
			}
		}
	}

	exportPlanRel := filepath.ToSlash(filepath.Join(projectRel, "plan", "export-plan.yaml"))
	exportPlanPath := filepath.Join(root, filepath.FromSlash(exportPlanRel))
	exportPlanExists := false
	if info, err := os.Stat(exportPlanPath); err == nil && !info.IsDir() {
		exportPlanExists = true
		if projectMetaLoaded {
			if err := validateOptionalProjectOwnedYAMLContent(exportPlanPath, projectMeta); err != nil {
				report.addError(fmt.Sprintf("invalid export plan %s: %v", exportPlanRel, err))
			}
		}
		if err := validateExportPlanContent(exportPlanPath); err != nil {
			report.addError(fmt.Sprintf("invalid export plan %s: %v", exportPlanRel, err))
		}
	}

	importPlanRel := filepath.ToSlash(filepath.Join(projectRel, "plan", "import-plan.yaml"))
	importPlanPath := filepath.Join(root, filepath.FromSlash(importPlanRel))
	if info, err := os.Stat(importPlanPath); err == nil && !info.IsDir() {
		if projectMetaLoaded {
			if err := validateOptionalProjectOwnedYAMLContent(importPlanPath, projectMeta); err != nil {
				report.addError(fmt.Sprintf("invalid import plan %s: %v", importPlanRel, err))
			}
		}
		if err := validateImportPlanContent(importPlanPath); err != nil {
			report.addError(fmt.Sprintf("invalid import plan %s: %v", importPlanRel, err))
		}
		if exportPlanExists {
			if err := validateImportPlanExportDependencies(exportPlanPath, importPlanPath); err != nil {
				report.addError(fmt.Sprintf("invalid import plan %s: %v", importPlanRel, err))
			}
		}
	}

	cdcPlanRel := filepath.ToSlash(filepath.Join(projectRel, "plan", "cdc-plan.yaml"))
	cdcPlanPath := filepath.Join(root, filepath.FromSlash(cdcPlanRel))
	if info, err := os.Stat(cdcPlanPath); err == nil && !info.IsDir() {
		var projectForCDC *projectMetadata
		if projectMetaLoaded {
			projectForCDC = &projectMeta
		}
		if err := validateCDCPlanContent(cdcPlanPath, projectForCDC, cluster); err != nil {
			report.addError(fmt.Sprintf("invalid cdc plan %s: %v", cdcPlanRel, err))
		}
	}

	validationPlanRel := filepath.ToSlash(filepath.Join(projectRel, "plan", "validation-plan.yaml"))
	validationPlanPath := filepath.Join(root, filepath.FromSlash(validationPlanRel))
	info, err := os.Stat(validationPlanPath)
	if err != nil || info.IsDir() {
		return
	}
	if projectMetaLoaded {
		if err := validateOptionalProjectOwnedYAMLContent(validationPlanPath, projectMeta); err != nil {
			report.addError(fmt.Sprintf("invalid validation plan %s: %v", validationPlanRel, err))
		}
	}
	if err := validateValidationPlanContent(validationPlanPath); err != nil {
		report.addError(fmt.Sprintf("invalid validation plan %s: %v", validationPlanRel, err))
	}
}

func validateProjectMetadataContent(path, expectedClusterID, expectedProjectID string) error {
	meta, err := readProjectMetadata(path)
	if err != nil {
		return err
	}
	return validateProjectMetadata(meta, expectedClusterID, expectedProjectID)
}

func validateProjectMetadata(meta projectMetadata, expectedClusterID, expectedProjectID string) error {
	if strings.TrimSpace(meta.ProjectID) == "" {
		return errors.New("project id is required")
	}
	if meta.ProjectID != expectedProjectID {
		return fmt.Errorf("project_id %q does not match directory id %q", meta.ProjectID, expectedProjectID)
	}
	if strings.TrimSpace(meta.SourceClusterID) == "" {
		return errors.New("source cluster id is required")
	}
	if meta.SourceClusterID != expectedClusterID {
		return fmt.Errorf("source_cluster_id %q does not match parent cluster id %q", meta.SourceClusterID, expectedClusterID)
	}
	if strings.TrimSpace(meta.SourceDatabase) == "" {
		return errors.New("source database is required")
	}
	if len(meta.SourceSchemas) == 0 {
		return errors.New("at least one source schema is required")
	}
	if strings.TrimSpace(meta.TargetDatabase) == "" {
		return errors.New("target database is required")
	}
	if strings.TrimSpace(meta.Mode) == "" {
		return errors.New("migration mode is required")
	}
	if !isSupportedMigrationMode(meta.Mode) {
		return fmt.Errorf("unsupported migration mode %q; supported modes: offline, short-downtime, low-downtime", meta.Mode)
	}
	if err := validateOwners("project", meta.Owners); err != nil {
		return err
	}
	return nil
}

func validateSchemaDiffContent(path string, project *projectMetadata) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read schema diff: %w", err)
	}
	var doc struct {
		ProjectID       string `json:"project_id"`
		SourceClusterID string `json:"source_cluster_id"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse schema diff JSON: %w", err)
	}
	if project == nil {
		return nil
	}
	if strings.TrimSpace(doc.ProjectID) != "" && doc.ProjectID != project.ProjectID {
		return fmt.Errorf("project_id %q does not match project metadata %q", doc.ProjectID, project.ProjectID)
	}
	if strings.TrimSpace(doc.SourceClusterID) != "" && doc.SourceClusterID != project.SourceClusterID {
		return fmt.Errorf("source_cluster_id %q does not match project metadata %q", doc.SourceClusterID, project.SourceClusterID)
	}
	return nil
}

func validateEvidenceJSONContent(path string, project *projectMetadata) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read evidence: %w", err)
	}
	var doc struct {
		ProjectID       string `json:"project_id"`
		SourceClusterID string `json:"source_cluster_id"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse evidence JSON: %w", err)
	}
	if project == nil {
		return nil
	}
	if strings.TrimSpace(doc.ProjectID) != "" && doc.ProjectID != project.ProjectID {
		return fmt.Errorf("project_id %q does not match project metadata %q", doc.ProjectID, project.ProjectID)
	}
	if strings.TrimSpace(doc.SourceClusterID) != "" && doc.SourceClusterID != project.SourceClusterID {
		return fmt.Errorf("source_cluster_id %q does not match project metadata %q", doc.SourceClusterID, project.SourceClusterID)
	}
	return nil
}

func validateExecutorEvidenceContent(path string, project *projectMetadata, expectedStage string) error {
	if !isExecutorEvidenceStage(expectedStage) {
		return fmt.Errorf("unsupported executor evidence stage %q", expectedStage)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read executor evidence: %w", err)
	}
	var evidence executorEvidenceSummary
	if err := json.Unmarshal(data, &evidence); err != nil {
		return fmt.Errorf("parse executor evidence JSON: %w", err)
	}
	if evidence.Stage != expectedStage {
		return fmt.Errorf("stage %q does not match evidence file stage %q", evidence.Stage, expectedStage)
	}
	if project != nil {
		if evidence.ProjectID != project.ProjectID {
			return fmt.Errorf("project_id %q does not match project metadata %q", evidence.ProjectID, project.ProjectID)
		}
		if evidence.SourceClusterID != project.SourceClusterID {
			return fmt.Errorf("source_cluster_id %q does not match project metadata %q", evidence.SourceClusterID, project.SourceClusterID)
		}
	}
	status := strings.TrimSpace(evidence.Status)
	if status == "" {
		return errors.New("executor evidence status is required")
	}
	if !isExecutorEvidenceRunStatus(status) {
		return fmt.Errorf("executor evidence status %q is unsupported", evidence.Status)
	}
	if strings.TrimSpace(evidence.PayloadHash) != "" && !isValidPayloadHash(evidence.PayloadHash) {
		return fmt.Errorf("payload_hash %q must use sha256:<64 hex chars>", evidence.PayloadHash)
	}
	return validateExecutorEvidenceCommands(status, evidence.Commands)
}

func validateMigrationPlanContent(path string, project projectMetadata) error {
	planVersion, err := readPlanTopLevelScalar(path, "plan_version")
	if err != nil {
		return err
	}
	if strings.TrimSpace(planVersion) == "" {
		return errors.New("plan_version is required")
	}
	version, err := strconv.Atoi(planVersion)
	if err != nil {
		return fmt.Errorf("parse plan_version %q: %w", planVersion, err)
	}
	if version < 1 {
		return errors.New("plan_version must be greater than or equal to 1")
	}

	projectID, err := readPlanTopLevelScalar(path, "project_id")
	if err != nil {
		return err
	}
	if strings.TrimSpace(projectID) == "" {
		return errors.New("project_id is required")
	}
	if projectID != project.ProjectID {
		return fmt.Errorf("project_id %q does not match project metadata %q", projectID, project.ProjectID)
	}

	sourceClusterID, err := readPlanTopLevelScalar(path, "source_cluster_id")
	if err != nil {
		return err
	}
	if strings.TrimSpace(sourceClusterID) == "" {
		return errors.New("source_cluster_id is required")
	}
	if sourceClusterID != project.SourceClusterID {
		return fmt.Errorf("source_cluster_id %q does not match project metadata %q", sourceClusterID, project.SourceClusterID)
	}

	mode, err := readPlanTopLevelScalar(path, "mode")
	if err != nil {
		return err
	}
	if strings.TrimSpace(mode) == "" {
		return errors.New("migration mode is required")
	}
	if !isSupportedMigrationMode(mode) {
		return fmt.Errorf("unsupported migration mode %q; supported modes: offline, short-downtime, low-downtime", mode)
	}
	if mode != project.Mode {
		return fmt.Errorf("mode %q does not match project metadata %q", mode, project.Mode)
	}

	return nil
}

func validateProjectOwnedYAMLContent(path string, project projectMetadata) error {
	projectID, err := readPlanTopLevelScalar(path, "project_id")
	if err != nil {
		return err
	}
	if projectID != project.ProjectID {
		return fmt.Errorf("project_id %q does not match project metadata %q", projectID, project.ProjectID)
	}

	sourceClusterID, err := readPlanTopLevelScalar(path, "source_cluster_id")
	if err != nil {
		return err
	}
	if sourceClusterID != project.SourceClusterID {
		return fmt.Errorf("source_cluster_id %q does not match project metadata %q", sourceClusterID, project.SourceClusterID)
	}

	return nil
}

func validateProjectStateContent(path, stateRel string, project projectMetadata) error {
	if err := validateProjectOwnedYAMLContent(path, project); err != nil {
		return err
	}
	if stateRel != "state/migration-state.yaml" {
		return nil
	}
	phase, err := readPlanTopLevelScalar(path, "phase")
	if err != nil {
		return err
	}
	if !isSupportedMigrationStatePhase(phase) {
		return fmt.Errorf("unsupported migration state phase %q; supported phases: planning, ddl, export, import, cdc, validation, cutover, completed", phase)
	}
	return nil
}

func isSupportedMigrationStatePhase(phase string) bool {
	switch phase {
	case "planning", "ddl", "export", "import", "cdc", "validation", "cutover", "completed":
		return true
	default:
		return false
	}
}

func validateOptionalProjectOwnedYAMLContent(path string, project projectMetadata) error {
	projectID, err := readPlanTopLevelScalar(path, "project_id")
	if err != nil {
		return err
	}
	if strings.TrimSpace(projectID) != "" && projectID != project.ProjectID {
		return fmt.Errorf("project_id %q does not match project metadata %q", projectID, project.ProjectID)
	}

	sourceClusterID, err := readPlanTopLevelScalar(path, "source_cluster_id")
	if err != nil {
		return err
	}
	if strings.TrimSpace(sourceClusterID) != "" && sourceClusterID != project.SourceClusterID {
		return fmt.Errorf("source_cluster_id %q does not match project metadata %q", sourceClusterID, project.SourceClusterID)
	}

	return nil
}

func validateApprovalMetadataContent(path string, project projectMetadata, expectedAction string) error {
	projectID, err := readPlanTopLevelScalar(path, "project_id")
	if err != nil {
		return err
	}
	if projectID != project.ProjectID {
		return fmt.Errorf("project_id %q does not match project metadata %q", projectID, project.ProjectID)
	}

	sourceClusterID, err := readPlanTopLevelScalar(path, "source_cluster_id")
	if err != nil {
		return err
	}
	if sourceClusterID != project.SourceClusterID {
		return fmt.Errorf("source_cluster_id %q does not match project metadata %q", sourceClusterID, project.SourceClusterID)
	}

	approval, err := readApprovalMetadata(path)
	if err != nil {
		return err
	}
	if approval.Action != expectedAction {
		return fmt.Errorf("action %q does not match approval file stage %q", approval.Action, expectedAction)
	}
	if !isSupportedApprovalStatus(approval.Status) {
		return fmt.Errorf("unsupported approval status %q; supported statuses: pending, approved, rejected", approval.Status)
	}
	if strings.TrimSpace(approval.PayloadHash) != "" && !isValidPayloadHash(approval.PayloadHash) {
		return fmt.Errorf("payload_hash %q must use sha256:<64 hex chars>", approval.PayloadHash)
	}
	if approval.Status == "approved" {
		if strings.TrimSpace(approval.PayloadHash) == "" {
			return errors.New("approved approval requires payload_hash")
		}
		if len(approval.ApprovedBy) == 0 {
			return errors.New("approved approval requires at least one approved_by reviewer")
		}
	}
	return nil
}

func isSupportedApprovalStatus(status string) bool {
	switch status {
	case "pending", "approved", "rejected":
		return true
	default:
		return false
	}
}

func isValidPayloadHash(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	digest := strings.TrimPrefix(value, prefix)
	if len(digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func validateExportPlanContent(path string) error {
	format, err := readPlanTopLevelScalar(path, "format")
	if err != nil {
		return err
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if format != "" && format != "csv" {
		return fmt.Errorf("export format %s is not supported by sqlserver2tidb-executor; supported format: csv", format)
	}
	chunks, err := readExportPlanChunks(path)
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		return nil
	}
	seenIDs := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		chunkID := strings.TrimSpace(chunk.ID)
		if chunkID == "" {
			return fmt.Errorf("export chunk id is required")
		}
		if _, ok := seenIDs[chunkID]; ok {
			return fmt.Errorf("duplicate export chunk id %s", chunkID)
		}
		seenIDs[chunkID] = struct{}{}
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

func validateImportPlanContent(path string) error {
	engine, err := readPlanTopLevelScalar(path, "engine")
	if err != nil {
		return err
	}
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine != "" && engine != "sql-insert" {
		return fmt.Errorf("import engine %s is not supported by sqlserver2tidb-executor; supported engine: sql-insert", engine)
	}
	jobs, err := readImportPlanJobs(path)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return nil
	}
	return validateImportPlanJobs(jobs)
}

func validateImportPlanExportDependencies(exportPlanPath, importPlanPath string) error {
	chunks, err := readExportPlanChunks(exportPlanPath)
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		return nil
	}
	jobs, err := readImportPlanJobs(importPlanPath)
	if err != nil {
		return err
	}
	exportChunkOutputURIs := make(map[string]string, len(chunks))
	for _, chunk := range chunks {
		chunkID := strings.TrimSpace(chunk.ID)
		if chunkID != "" {
			exportChunkOutputURIs[chunkID] = strings.TrimSpace(chunk.OutputURI)
		}
	}
	for _, job := range jobs {
		dependency := strings.TrimSpace(job.DependsOnExportChunk)
		if dependency == "" {
			continue
		}
		exportOutputURI, ok := exportChunkOutputURIs[dependency]
		if !ok {
			return fmt.Errorf("import job %s depends_on_export_chunk %s does not exist in export plan", job.ID, dependency)
		}
		sourceURI := strings.TrimSpace(job.SourceURI)
		if sourceURI != "" && exportOutputURI != "" && sourceURI != exportOutputURI {
			return fmt.Errorf("import job %s source_uri %s does not match export chunk %s output_uri %s", job.ID, sourceURI, dependency, exportOutputURI)
		}
	}
	return nil
}

func validateCDCPlanContent(path string, project *projectMetadata, cluster *clusterMetadata) error {
	if err := validateCDCPlanMetadataContent(path, project, cluster); err != nil {
		return err
	}
	plan, err := readCDCPlanSummary(path)
	if err != nil {
		return err
	}
	if len(plan.Tables) == 0 {
		return nil
	}
	return validateCDCPlanSummary(plan)
}

func validateCDCPlanMetadataContent(path string, project *projectMetadata, cluster *clusterMetadata) error {
	projectID, err := readPlanTopLevelScalar(path, "project_id")
	if err != nil {
		return err
	}
	if strings.TrimSpace(projectID) != "" && project != nil && projectID != project.ProjectID {
		return fmt.Errorf("project_id %q does not match project metadata %q", projectID, project.ProjectID)
	}

	sourceClusterID, err := readPlanTopLevelScalar(path, "source_cluster_id")
	if err != nil {
		return err
	}
	if strings.TrimSpace(sourceClusterID) != "" && project != nil && sourceClusterID != project.SourceClusterID {
		return fmt.Errorf("source_cluster_id %q does not match project metadata %q", sourceClusterID, project.SourceClusterID)
	}

	mode, err := readPlanTopLevelScalar(path, "mode")
	if err != nil {
		return err
	}
	if strings.TrimSpace(mode) != "" && cluster != nil && mode != cluster.CDCMode {
		return fmt.Errorf("mode %q does not match cluster cdc mode %q", mode, cluster.CDCMode)
	}

	const expectedCheckpointFile = "../../../state/cdc-checkpoint.yaml"
	checkpointFile, err := readPlanTopLevelScalar(path, "checkpoint_file")
	if err != nil {
		return err
	}
	if strings.TrimSpace(checkpointFile) != "" && checkpointFile != expectedCheckpointFile {
		return fmt.Errorf("checkpoint_file %q does not match %q", checkpointFile, expectedCheckpointFile)
	}

	return nil
}

func validateValidationPlanContent(path string) error {
	checks, err := readValidationPlanChecks(path)
	if err != nil {
		return err
	}
	seenIDs := make(map[string]struct{}, len(checks))
	for _, check := range checks {
		if check.Type != "row_count" && check.Type != "row-count" {
			continue
		}
		checkID := strings.TrimSpace(check.ID)
		if checkID == "" {
			return fmt.Errorf("row_count check id is required")
		}
		if _, ok := seenIDs[checkID]; ok {
			return fmt.Errorf("duplicate validation check id %s", checkID)
		}
		seenIDs[checkID] = struct{}{}
		if strings.TrimSpace(check.SourceObject) == "" {
			return fmt.Errorf("row_count check %s source_object is required", check.ID)
		}
		if strings.TrimSpace(check.TargetObject) == "" {
			return fmt.Errorf("row_count check %s target_object is required", check.ID)
		}
		if containsTODOMarker(check.Predicate) {
			return fmt.Errorf("row_count check %s predicate still contains TODO", check.ID)
		}
		if containsTODOMarker(check.TargetPredicate) {
			return fmt.Errorf("row_count check %s target_predicate still contains TODO", check.ID)
		}
	}
	return nil
}

func containsTODOMarker(value string) bool {
	return strings.Contains(strings.ToUpper(value), "TODO")
}

func (report *ValidationReport) requireDir(root, rel string) {
	if err := report.checkDir(root, rel); err != nil {
		report.addError("missing required directory: " + rel)
	}
}

func (report *ValidationReport) requireFile(root, rel string) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		report.addError("missing required file: " + rel)
		return
	}
	report.CheckedFiles++
}

func (report *ValidationReport) checkDir(root, rel string) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", rel)
	}
	report.CheckedDirs++
	return nil
}

func (report *ValidationReport) addError(message string) {
	report.Errors = append(report.Errors, message)
}
