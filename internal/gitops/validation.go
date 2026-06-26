package gitops

import (
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

	if err := validateClusters(root, &report); err != nil {
		return report, err
	}

	report.Valid = len(report.Errors) == 0
	return report, nil
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

func validateValidationPlanContent(path string) error {
	checks, err := readValidationPlanChecks(path)
	if err != nil {
		return err
	}
	for _, check := range checks {
		if check.Type != "row_count" && check.Type != "row-count" {
			continue
		}
		if strings.TrimSpace(check.ID) == "" {
			return fmt.Errorf("row_count check id is required")
		}
		if strings.TrimSpace(check.SourceObject) == "" {
			return fmt.Errorf("row_count check %s source_object is required", check.ID)
		}
		if strings.TrimSpace(check.TargetObject) == "" {
			return fmt.Errorf("row_count check %s target_object is required", check.ID)
		}
	}
	return nil
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
