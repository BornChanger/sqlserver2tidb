package gitops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	validateProjects(root, filepath.ToSlash(filepath.Join(clusterRel, "projects")), report)
}

func validateProjects(root, projectsRel string, report *ValidationReport) {
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
		validateProjectContent(root, projectRel, report)
	}
}

func validateProjectContent(root, projectRel string, report *ValidationReport) {
	projectMetadataRel := filepath.ToSlash(filepath.Join(projectRel, "project.yaml"))
	projectMetadataPath := filepath.Join(root, filepath.FromSlash(projectMetadataRel))
	if info, err := os.Stat(projectMetadataPath); err == nil && !info.IsDir() {
		if err := validateProjectMetadataContent(projectMetadataPath); err != nil {
			report.addError(fmt.Sprintf("invalid project metadata %s: %v", projectMetadataRel, err))
		}
	}

	exportPlanRel := filepath.ToSlash(filepath.Join(projectRel, "plan", "export-plan.yaml"))
	exportPlanPath := filepath.Join(root, filepath.FromSlash(exportPlanRel))
	exportPlanExists := false
	if info, err := os.Stat(exportPlanPath); err == nil && !info.IsDir() {
		exportPlanExists = true
		if err := validateExportPlanContent(exportPlanPath); err != nil {
			report.addError(fmt.Sprintf("invalid export plan %s: %v", exportPlanRel, err))
		}
	}

	importPlanRel := filepath.ToSlash(filepath.Join(projectRel, "plan", "import-plan.yaml"))
	importPlanPath := filepath.Join(root, filepath.FromSlash(importPlanRel))
	if info, err := os.Stat(importPlanPath); err == nil && !info.IsDir() {
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
		if err := validateCDCPlanContent(cdcPlanPath); err != nil {
			report.addError(fmt.Sprintf("invalid cdc plan %s: %v", cdcPlanRel, err))
		}
	}

	validationPlanRel := filepath.ToSlash(filepath.Join(projectRel, "plan", "validation-plan.yaml"))
	validationPlanPath := filepath.Join(root, filepath.FromSlash(validationPlanRel))
	info, err := os.Stat(validationPlanPath)
	if err != nil || info.IsDir() {
		return
	}
	if err := validateValidationPlanContent(validationPlanPath); err != nil {
		report.addError(fmt.Sprintf("invalid validation plan %s: %v", validationPlanRel, err))
	}
}

func validateProjectMetadataContent(path string) error {
	meta, err := readProjectMetadata(path)
	if err != nil {
		return err
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

func validateCDCPlanContent(path string) error {
	plan, err := readCDCPlanSummary(path)
	if err != nil {
		return err
	}
	if len(plan.Tables) == 0 {
		return nil
	}
	return validateCDCPlanSummary(plan)
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
