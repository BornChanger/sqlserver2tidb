package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BornChanger/sqlserver2tidb/internal/buildinfo"
	"github.com/BornChanger/sqlserver2tidb/internal/gitops"
	sqlservercatalog "github.com/BornChanger/sqlserver2tidb/internal/sqlserver"
)

var lookPath = exec.LookPath

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "init-repo":
		return runInitRepo(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "validate-repo":
		return runValidateRepo(args[1:], stdout, stderr)
	case "discover-sqlserver":
		return runDiscoverSQLServer(args[1:], stdout, stderr)
	case "analyze-compatibility":
		return runAnalyzeCompatibility(args[1:], stdout, stderr)
	case "generate-schema-draft":
		return runGenerateSchemaDraft(args[1:], stdout, stderr)
	case "generate-data-plans":
		return runGenerateDataPlans(args[1:], stdout, stderr)
	case "generate-cdc-plan":
		return runGenerateCDCPlan(args[1:], stdout, stderr)
	case "prepare-cdc-range":
		return runPrepareCDCRange(args[1:], stdout, stderr)
	case "prepare-cdc-iteration":
		return runPrepareCDCIteration(args[1:], stdout, stderr)
	case "generate-validation-plan":
		return runGenerateValidationPlan(args[1:], stdout, stderr)
	case "generate-pr-draft":
		return runGeneratePRDraft(args[1:], stdout, stderr)
	case "create-pr":
		return runCreatePR(args[1:], stdout, stderr)
	case "create-worker-state-pr":
		return runCreateWorkerStatePR(args[1:], stdout, stderr)
	case "generate-executor-evidence-pr-draft":
		return runGenerateExecutorEvidencePRDraft(args[1:], stdout, stderr)
	case "create-executor-evidence-pr":
		return runCreateExecutorEvidencePR(args[1:], stdout, stderr)
	case "compute-payload-hash":
		return runComputePayloadHash(args[1:], stdout, stderr)
	case "worker-export":
		return runWorkerExport(args[1:], stdout, stderr)
	case "worker-import":
		return runWorkerImport(args[1:], stdout, stderr)
	case "worker-cdc":
		return runWorkerCDC(args[1:], stdout, stderr)
	case "worker-validate":
		return runWorkerValidate(args[1:], stdout, stderr)
	case "worker-executor":
		return runWorkerExecutor(args[1:], stdout, stderr)
	case "advance-cdc-checkpoint":
		return runAdvanceCDCCheckpoint(args[1:], stdout, stderr)
	case "worker-reconcile":
		return runWorkerReconcile(args[1:], stdout, stderr)
	case "worker-agent":
		return runWorkerAgent(args[1:], stdout, stderr)
	case "create-cluster":
		return runCreateCluster(args[1:], stdout, stderr)
	case "create-project":
		return runCreateProject(args[1:], stdout, stderr)
	case "version", "-v", "--version":
		fmt.Fprint(stdout, buildinfo.Format("sqlserver2tidb"))
		return 0
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runInitRepo(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init-repo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := gitops.InitRepo(*root); err != nil {
		fmt.Fprintf(stderr, "init repo: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "initialized migration repository at %s\n", *root)
	return 0
}

func runValidateRepo(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate-repo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	report, err := gitops.ValidateRepo(*root)
	if err != nil {
		fmt.Fprintf(stderr, "validate repo: %v\n", err)
		return 1
	}
	if !report.Valid {
		fmt.Fprintf(stderr, "repository validation failed at %s:\n", *root)
		for _, message := range report.Errors {
			fmt.Fprintf(stderr, "- %s\n", message)
		}
		return 1
	}
	fmt.Fprintf(stdout, "repository is valid at %s (%d dirs, %d files checked)\n", *root, report.CheckedDirs, report.CheckedFiles)
	return 0
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	requireTools := fs.Bool("require-tools", false, "return non-zero when local helper tools are missing")
	jsonOutput := fs.Bool("json", false, "write doctor report as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	report, err := gitops.ValidateRepo(*root)
	if err != nil {
		fmt.Fprintf(stderr, "doctor: validate repo: %v\n", err)
		return 1
	}

	tools := []string{"git", "gh", "sqlserver2tidb-executor"}
	type doctorToolReport struct {
		Name  string `json:"name"`
		Found bool   `json:"found"`
		Path  string `json:"path,omitempty"`
	}
	doctorReport := struct {
		Repository struct {
			Valid        bool     `json:"valid"`
			CheckedDirs  int      `json:"checked_dirs"`
			CheckedFiles int      `json:"checked_files"`
			Errors       []string `json:"errors"`
		} `json:"repository"`
		Tools []doctorToolReport `json:"tools"`
	}{}
	doctorReport.Repository.Valid = report.Valid
	doctorReport.Repository.CheckedDirs = report.CheckedDirs
	doctorReport.Repository.CheckedFiles = report.CheckedFiles
	doctorReport.Repository.Errors = report.Errors

	missingTools := make([]string, 0, len(tools))
	for _, tool := range tools {
		toolReport := doctorToolReport{Name: tool}
		path, err := lookPath(tool)
		if err != nil {
			missingTools = append(missingTools, tool)
			doctorReport.Tools = append(doctorReport.Tools, toolReport)
			continue
		}
		toolReport.Found = true
		toolReport.Path = path
		doctorReport.Tools = append(doctorReport.Tools, toolReport)
	}

	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(doctorReport); err != nil {
			fmt.Fprintf(stderr, "doctor json: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintln(stdout, "doctor completed")
		if report.Valid {
			fmt.Fprintf(stdout, "repository: valid (%d dirs, %d files checked)\n", report.CheckedDirs, report.CheckedFiles)
		} else {
			fmt.Fprintf(stdout, "repository: invalid (%d errors)\n", len(report.Errors))
			for _, message := range report.Errors {
				fmt.Fprintf(stdout, "- %s\n", message)
			}
		}
		fmt.Fprintln(stdout, "tools:")
		for _, tool := range doctorReport.Tools {
			if !tool.Found {
				fmt.Fprintf(stdout, "- %s: missing\n", tool.Name)
				continue
			}
			fmt.Fprintf(stdout, "- %s: found (%s)\n", tool.Name, tool.Path)
		}
	}

	if !report.Valid {
		return 1
	}
	if *requireTools && len(missingTools) > 0 {
		fmt.Fprintf(stderr, "doctor: required tools missing: %s\n", strings.Join(missingTools, ", "))
		return 1
	}
	return 0
}

func runDiscoverSQLServer(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("discover-sqlserver", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	dryRun := fs.Bool("dry-run", false, "print discovery plan without connecting to SQL Server or writing files")
	connectionStringEnv := fs.String("connection-string-env", "", "environment variable containing the SQL Server connection string")
	timeout := fs.Duration("timeout", 30*time.Second, "SQL Server discovery timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dryRun {
		plan, err := gitops.BuildSQLServerDiscoveryDryRunPlan(*root, *sourceClusterID)
		if err != nil {
			fmt.Fprintf(stderr, "discover sqlserver: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "SQL Server discovery dry run for %s\n", plan.SourceClusterID)
		fmt.Fprintln(stdout, "No database connection will be opened.")
		fmt.Fprintf(stdout, "Writes files: %t\n", plan.WritesFiles)
		fmt.Fprintln(stdout, "\nTarget files:")
		for _, target := range plan.TargetFiles {
			fmt.Fprintf(stdout, "- %s\n", target)
		}
		fmt.Fprintln(stdout, "\nCatalog queries:")
		for _, query := range plan.CatalogQueries {
			fmt.Fprintf(stdout, "- %s\n", query)
		}
		return 0
	}
	if strings.TrimSpace(*connectionStringEnv) == "" {
		fmt.Fprintln(stderr, "discover-sqlserver: requires --connection-string-env unless --dry-run is set")
		return 2
	}
	connectionString := os.Getenv(*connectionStringEnv)
	if strings.TrimSpace(connectionString) == "" {
		fmt.Fprintf(stderr, "discover-sqlserver: environment variable %s is not set\n", *connectionStringEnv)
		return 1
	}
	reader, err := sqlservercatalog.NewCatalogReader(connectionString)
	if err != nil {
		fmt.Fprintf(stderr, "discover sqlserver: %v\n", err)
		return 1
	}
	defer reader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := gitops.ExecuteSQLServerDiscovery(ctx, *root, *sourceClusterID, reader)
	if err != nil {
		fmt.Fprintf(stderr, "discover sqlserver: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "SQL Server discovery completed for %s\n", result.SourceClusterID)
	fmt.Fprintf(stdout, "databases: %d, tables: %d, columns: %d, source DDL files: %d\n",
		result.Databases,
		result.Tables,
		result.Columns,
		result.SourceDDLs,
	)
	fmt.Fprintf(stdout, "wrote %s\n", result.InventoryFile)
	return 0
}

func runAnalyzeCompatibility(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("analyze-compatibility", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	report, err := gitops.AnalyzeSQLServerCompatibility(*root, *sourceClusterID)
	if err != nil {
		fmt.Fprintf(stderr, "analyze compatibility: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "compatibility analysis completed for %s\n", report.SourceClusterID)
	fmt.Fprintf(stdout, "findings: %d, blockers: %d, warnings: %d, info: %d\n",
		report.Summary.TotalFindings,
		report.Summary.Blockers,
		report.Summary.Warnings,
		report.Summary.Info,
	)
	fmt.Fprintf(stdout, "wrote %s\n", "inventory/schema-issues.yaml")
	fmt.Fprintf(stdout, "wrote %s\n", "inventory/compatibility-report.md")
	return 0
}

func runGenerateSchemaDraft(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate-schema-draft", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.GenerateSchemaDraft(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "generate schema draft: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "schema draft generated for %s under source cluster %s\n", result.ProjectID, result.SourceClusterID)
	fmt.Fprintf(stdout, "tables: %d, columns: %d, manual review items: %d\n",
		result.Tables,
		result.Columns,
		result.ManualReviewItems,
	)
	fmt.Fprintf(stdout, "wrote %s\n", "schema/tidb-ddl")
	fmt.Fprintf(stdout, "wrote %s\n", "schema/conversion-report.md")
	fmt.Fprintf(stdout, "wrote %s\n", "schema/schema-diff.json")
	return 0
}

func runGenerateDataPlans(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate-data-plans", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	objectURIPrefix := fs.String("object-uri-prefix", "", "CSV output URI prefix for exported full-load files; use file:// or HTTP(S) for the included executor")
	chunkSizeRows := fs.Int64("chunk-size-rows", 1000000, "estimated rows per export chunk")
	exportFormat := fs.String("export-format", "csv", "export file format")
	importEngine := fs.String("import-engine", "sql-insert", "TiDB import engine")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.GenerateDataMovementPlans(*root, *sourceClusterID, *projectID, gitops.DataMovementPlanSpec{
		ObjectURIPrefix: *objectURIPrefix,
		ChunkSizeRows:   *chunkSizeRows,
		ExportFormat:    *exportFormat,
		ImportEngine:    *importEngine,
	})
	if err != nil {
		fmt.Fprintf(stderr, "generate data plans: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "data movement plans generated for %s under source cluster %s\n", result.ProjectID, result.SourceClusterID)
	fmt.Fprintf(stdout, "tables: %d\n", result.Tables)
	fmt.Fprintf(stdout, "export chunks: %d\n", result.ExportChunks)
	fmt.Fprintf(stdout, "import jobs: %d\n", result.ImportJobs)
	fmt.Fprintf(stdout, "wrote %s\n", "plan/export-plan.yaml")
	fmt.Fprintf(stdout, "wrote %s\n", "plan/import-plan.yaml")
	return 0
}

func runGenerateCDCPlan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate-cdc-plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	mode := fs.String("mode", "sqlserver-cdc", "CDC mode")
	retentionHours := fs.Int("retention-hours", 168, "required CDC retention hours")
	applyBatchSize := fs.Int("apply-batch-size", 1000, "planned TiDB CDC apply batch size")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.GenerateCDCPlan(*root, *sourceClusterID, *projectID, gitops.CDCPlanSpec{
		Mode:                   *mode,
		RetentionHoursRequired: *retentionHours,
		ApplyBatchSize:         *applyBatchSize,
	})
	if err != nil {
		fmt.Fprintf(stderr, "generate cdc plan: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cdc plan generated for %s under source cluster %s\n", result.ProjectID, result.SourceClusterID)
	fmt.Fprintf(stdout, "mode: %s\n", result.Mode)
	fmt.Fprintf(stdout, "tracked tables: %d\n", result.Tables)
	fmt.Fprintf(stdout, "wrote %s\n", "plan/cdc-plan.yaml")
	return 0
}

func runPrepareCDCRange(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prepare-cdc-range", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	fromLSN := fs.String("from-lsn", "", "initial CDC from LSN for tables without checkpoint state")
	toLSN := fs.String("to-lsn", "", "CDC to LSN for the next reviewed range")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.PrepareCDCPlanRange(*root, *sourceClusterID, *projectID, gitops.CDCPlanRangeSpec{
		FromLSN: *fromLSN,
		ToLSN:   *toLSN,
	})
	if err != nil {
		fmt.Fprintf(stderr, "prepare cdc range: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cdc range prepared for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "updated tables: %d\n", result.UpdatedTables)
	fmt.Fprintf(stdout, "wrote %s\n", "plan/cdc-plan.yaml")
	return 0
}

func runPrepareCDCIteration(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prepare-cdc-iteration", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	maxLSN := fs.String("max-lsn", "", "latest SQL Server CDC max LSN for this iteration")
	fromLSN := fs.String("from-lsn", "", "initial CDC from LSN for tables without checkpoint state")
	prDraft := fs.Bool("pr-draft", false, "write a CDC range PR draft alongside the updated plan")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.PrepareCDCIteration(*root, *sourceClusterID, *projectID, gitops.CDCIterationSpec{
		MaxLSN:         *maxLSN,
		InitialFromLSN: *fromLSN,
		WritePRDraft:   *prDraft,
	})
	if err != nil {
		fmt.Fprintf(stderr, "prepare cdc iteration: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cdc iteration prepared for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", result.Status)
	fmt.Fprintf(stdout, "max_lsn: %s\n", result.MaxLSN)
	fmt.Fprintf(stdout, "updated tables: %d\n", result.UpdatedTables)
	if result.Status == gitops.CDCIterationStatusRangePrepared {
		fmt.Fprintf(stdout, "wrote %s\n", "plan/cdc-plan.yaml")
	}
	if result.PRBodyFile != "" {
		fmt.Fprintf(stdout, "PR draft: %s\n", result.PRBodyFile)
	}
	return 0
}

func runGenerateValidationPlan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate-validation-plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	includeChecksum := fs.Bool("include-checksum", false, "include reviewed scalar-query checksum checks for tables with exact numeric columns")
	includeSampledHash := fs.Bool("include-sampled-hash", false, "include reviewed scalar-query sampled_hash checks for tables with an integer sample column")
	includeBucketedCount := fs.Bool("include-bucketed-count", false, "include reviewed scalar-query bucketed_count checks for tables with an integer bucket column")
	sampleModulo := fs.Int("sample-modulo", 100, "modulo used by sampled_hash checks")
	bucketCount := fs.Int("bucket-count", 16, "bucket count used by bucketed_count checks (max 1024)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.GenerateValidationPlanWithSpec(*root, *sourceClusterID, *projectID, gitops.ValidationPlanSpec{
		IncludeChecksum:      *includeChecksum,
		IncludeSampledHash:   *includeSampledHash,
		IncludeBucketedCount: *includeBucketedCount,
		SampleModulo:         *sampleModulo,
		BucketCount:          *bucketCount,
	})
	if err != nil {
		fmt.Fprintf(stderr, "generate validation plan: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "validation plan generated for %s under source cluster %s\n", result.ProjectID, result.SourceClusterID)
	fmt.Fprintf(stdout, "checks: %d\n", result.Checks)
	fmt.Fprintf(stdout, "wrote %s\n", "plan/validation-plan.yaml")
	return 0
}

func runGeneratePRDraft(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate-pr-draft", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id; omit for cluster-level stages such as discovery")
	stage := fs.String("stage", "", "PR stage: discovery, schema, plan, export, import, cdc, validation, cutover")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.GeneratePRDraft(*root, *sourceClusterID, *projectID, *stage)
	if err != nil {
		fmt.Fprintf(stderr, "generate PR draft: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "PR draft generated for %s\n", result.Stage)
	fmt.Fprintf(stdout, "title: %s\n", result.Title)
	fmt.Fprintf(stdout, "branch: %s\n", result.BranchName)
	fmt.Fprintf(stdout, "body file: %s\n", result.BodyFile)
	fmt.Fprintf(stdout, "files to review: %d\n", len(result.Files))
	return 0
}

func runCreatePR(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create-pr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id; omit for cluster-level stages such as discovery")
	stage := fs.String("stage", "", "PR stage: discovery, schema, plan, export, import, cdc, validation, cutover")
	execute := fs.Bool("execute", false, "call gh pr create; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	spec, err := gitops.PrepareGitHubPRCreate(*root, *sourceClusterID, *projectID, *stage)
	if err != nil {
		fmt.Fprintf(stderr, "create PR: %v\n", err)
		return 1
	}
	if !*execute {
		fmt.Fprintln(stdout, "dry run: not calling GitHub")
		fmt.Fprintf(stdout, "command: %s\n", spec.ShellCommand)
		fmt.Fprintf(stdout, "title: %s\n", spec.Title)
		fmt.Fprintf(stdout, "body file: %s\n", spec.BodyFile)
		return 0
	}

	cmd := exec.Command("gh", spec.Args...)
	cmd.Dir = *root
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		fmt.Fprint(stdout, string(output))
	}
	if err != nil {
		fmt.Fprintf(stderr, "create PR: gh pr create failed: %v\n", err)
		return 1
	}
	return 0
}

func runCreateWorkerStatePR(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create-worker-state-pr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "worker state PR stage: export, import, cdc, validation")
	execute := fs.Bool("execute", false, "create git branch, commit state files, and call gh pr create; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	spec, err := gitops.PrepareWorkerStatePRCreate(*root, *sourceClusterID, *projectID, *stage)
	if err != nil {
		fmt.Fprintf(stderr, "create worker state PR: %v\n", err)
		return 1
	}
	if !*execute {
		fmt.Fprintln(stdout, "dry run: not changing git or calling GitHub")
		for _, command := range spec.ShellCommands {
			fmt.Fprintf(stdout, "command: %s\n", command)
		}
		if spec.BodyFileNeedsUpdate {
			fmt.Fprintln(stdout, "body file update: needed; execute mode refreshes it before commit")
		} else {
			fmt.Fprintln(stdout, "body file update: not needed")
		}
		fmt.Fprintf(stdout, "title: %s\n", spec.Title)
		fmt.Fprintf(stdout, "branch: %s\n", spec.BranchName)
		fmt.Fprintf(stdout, "body file: %s\n", spec.BodyFile)
		fmt.Fprintf(stdout, "files to commit: %d\n", len(spec.Files))
		return 0
	}

	if err := gitops.RefreshWorkerStatePRBody(*root, spec); err != nil {
		fmt.Fprintf(stderr, "create worker state PR: %v\n", err)
		return 1
	}
	if spec.BodyFileNeedsUpdate {
		fmt.Fprintf(stdout, "updated %s\n", spec.BodyFile)
	}

	for _, gitArgs := range spec.GitArgs {
		cmd := exec.Command("git", gitArgs...)
		cmd.Dir = *root
		output, err := cmd.CombinedOutput()
		if len(output) > 0 {
			fmt.Fprint(stdout, string(output))
		}
		if err != nil {
			fmt.Fprintf(stderr, "create worker state PR: git %s failed: %v\n", strings.Join(gitArgs, " "), err)
			return 1
		}
	}
	cmd := exec.Command("gh", spec.GitHubArgs...)
	cmd.Dir = *root
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		fmt.Fprint(stdout, string(output))
	}
	if err != nil {
		fmt.Fprintf(stderr, "create worker state PR: gh pr create failed: %v\n", err)
		return 1
	}
	return 0
}

func runGenerateExecutorEvidencePRDraft(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate-executor-evidence-pr-draft", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "executor evidence PR stage: ddl, export, import, cdc, validation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	draft, err := gitops.GenerateExecutorEvidencePRDraft(*root, *sourceClusterID, *projectID, *stage)
	if err != nil {
		fmt.Fprintf(stderr, "generate executor evidence PR draft: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "executor evidence PR draft generated")
	fmt.Fprintf(stdout, "title: %s\n", draft.Title)
	fmt.Fprintf(stdout, "branch: %s\n", draft.BranchName)
	fmt.Fprintf(stdout, "body file: %s\n", draft.BodyFile)
	fmt.Fprintf(stdout, "files to review: %d\n", len(draft.Files))
	return 0
}

func runCreateExecutorEvidencePR(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create-executor-evidence-pr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "executor evidence PR stage: ddl, export, import, cdc, validation")
	execute := fs.Bool("execute", false, "create git branch, commit executor evidence, and call gh pr create; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	spec, err := gitops.PrepareExecutorEvidencePRCreate(*root, *sourceClusterID, *projectID, *stage)
	if err != nil {
		fmt.Fprintf(stderr, "create executor evidence PR: %v\n", err)
		return 1
	}
	if !*execute {
		fmt.Fprintln(stdout, "dry run: not changing git or calling GitHub")
		for _, command := range spec.ShellCommands {
			fmt.Fprintf(stdout, "command: %s\n", command)
		}
		fmt.Fprintf(stdout, "title: %s\n", spec.Title)
		fmt.Fprintf(stdout, "branch: %s\n", spec.BranchName)
		fmt.Fprintf(stdout, "body file: %s\n", spec.BodyFile)
		fmt.Fprintf(stdout, "files to commit: %d\n", len(spec.Files))
		return 0
	}

	for _, gitArgs := range spec.GitArgs {
		cmd := exec.Command("git", gitArgs...)
		cmd.Dir = *root
		output, err := cmd.CombinedOutput()
		if len(output) > 0 {
			fmt.Fprint(stdout, string(output))
		}
		if err != nil {
			fmt.Fprintf(stderr, "create executor evidence PR: git %s failed: %v\n", strings.Join(gitArgs, " "), err)
			return 1
		}
	}
	cmd := exec.Command("gh", spec.GitHubArgs...)
	cmd.Dir = *root
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		fmt.Fprint(stdout, string(output))
	}
	if err != nil {
		fmt.Fprintf(stderr, "create executor evidence PR: gh pr create failed: %v\n", err)
		return 1
	}
	return 0
}

func runComputePayloadHash(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compute-payload-hash", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "stage to hash: export, import, cdc, or validation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	hash, err := gitops.ComputePayloadHashForStage(*root, *sourceClusterID, *projectID, *stage)
	if err != nil {
		fmt.Fprintf(stderr, "compute payload hash: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "stage: %s\n", *stage)
	fmt.Fprintf(stdout, "source cluster: %s\n", *sourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", *projectID)
	fmt.Fprintf(stdout, "payload hash: %s\n", hash)
	return 0
}

func runWorkerValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.RunValidationWorker(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "worker validate: %v\n", err)
		return 1
	}
	status := "passed"
	exitCode := 0
	if !result.Passed {
		status = "failed"
		exitCode = 1
	}
	fmt.Fprintf(stdout, "validation worker completed for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", status)
	fmt.Fprintf(stdout, "checks: %d\n", len(result.Checks))
	fmt.Fprintf(stdout, "payload hash: %s\n", result.PayloadHash)
	fmt.Fprintf(stdout, "wrote %s\n", "state/validation-status.yaml")
	fmt.Fprintf(stdout, "wrote %s\n", "evidence/validation-report.md")
	return exitCode
}

func runWorkerExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.RunExportWorker(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "worker export: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "export worker completed for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", result.Status)
	fmt.Fprintf(stdout, "chunks: %d\n", result.Items)
	fmt.Fprintf(stdout, "payload hash: %s\n", result.PayloadHash)
	fmt.Fprintf(stdout, "wrote %s\n", result.StateFile)
	fmt.Fprintf(stdout, "wrote %s\n", result.EvidenceFile)
	return 0
}

func runWorkerImport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.RunImportWorker(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "worker import: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "import worker completed for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", result.Status)
	fmt.Fprintf(stdout, "jobs: %d\n", result.Items)
	fmt.Fprintf(stdout, "payload hash: %s\n", result.PayloadHash)
	fmt.Fprintf(stdout, "wrote %s\n", result.StateFile)
	fmt.Fprintf(stdout, "wrote %s\n", result.EvidenceFile)
	return 0
}

func runWorkerCDC(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-cdc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.RunCDCWorker(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "worker cdc: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cdc worker completed for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", result.Status)
	fmt.Fprintf(stdout, "tracked tables: %d\n", result.Items)
	fmt.Fprintf(stdout, "payload hash: %s\n", result.PayloadHash)
	fmt.Fprintf(stdout, "wrote %s\n", result.StateFile)
	fmt.Fprintf(stdout, "wrote %s\n", "state/cdc-checkpoint.yaml")
	fmt.Fprintf(stdout, "wrote %s\n", result.EvidenceFile)
	return 0
}

func runWorkerExecutor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-executor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "executor stage: ddl, export, import, cdc, or validation")
	executorBinary := fs.String("executor-binary", "", "external executor binary; default is sqlserver2tidb-executor")
	sourceConnectionStringEnv := fs.String("source-connection-string-env", "", "environment variable containing the SQL Server connection string for export execution")
	targetConnectionStringEnv := fs.String("target-connection-string-env", "", "environment variable containing the TiDB/MySQL connection string for import execution")
	importBatchSize := fs.Int("import-batch-size", 0, "rows to commit per import transaction; default is executor-defined")
	requireEmptyTarget := fs.Bool("require-empty-target", false, "pass executor --require-empty-target to sql-insert import commands")
	commandTimeout := fs.Duration("command-timeout", 0, "maximum runtime per external executor command; 0 disables timeout")
	commandRetries := fs.Int("command-retries", 0, "number of retries for a failed external executor command")
	retryBackoff := fs.Duration("retry-backoff", time.Second, "fixed backoff between external executor command retries")
	resume := fs.Bool("resume", false, "skip matching successful commands from existing executor evidence for the current payload hash")
	execute := fs.Bool("execute", false, "run external executor commands with executor --execute; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *commandTimeout < 0 {
		fmt.Fprintln(stderr, "worker executor: --command-timeout must be non-negative")
		return 2
	}
	if *commandRetries < 0 {
		fmt.Fprintln(stderr, "worker executor: --command-retries must be non-negative")
		return 2
	}
	if *retryBackoff < 0 {
		fmt.Fprintln(stderr, "worker executor: --retry-backoff must be non-negative")
		return 2
	}
	spec, err := gitops.PrepareWorkerExecutor(*root, *sourceClusterID, *projectID, *stage, gitops.WorkerExecutorPrepareSpec{
		Binary:                    *executorBinary,
		SourceConnectionStringEnv: *sourceConnectionStringEnv,
		TargetConnectionStringEnv: *targetConnectionStringEnv,
		ImportBatchSize:           *importBatchSize,
		RequireEmptyTarget:        *requireEmptyTarget,
	})
	if err != nil {
		fmt.Fprintf(stderr, "worker executor: %v\n", err)
		return 1
	}
	if !*execute {
		fmt.Fprintln(stdout, "worker executor dry run")
		fmt.Fprintf(stdout, "stage: %s\n", spec.Stage)
		fmt.Fprintf(stdout, "payload hash: %s\n", spec.PayloadHash)
		fmt.Fprintf(stdout, "commands: %d\n", len(spec.Commands))
		for _, command := range spec.Commands {
			fmt.Fprintf(stdout, "command: %s\n", command.ShellCommand)
		}
		return 0
	}

	results := make([]workerExecutorRunCommandEvidence, 0, len(spec.Commands))
	resumeCommands := map[string]workerExecutorRunCommandEvidence{}
	if *resume {
		resumeCommands, err = loadWorkerExecutorResumeCommands(*root, spec)
		if err != nil {
			fmt.Fprintf(stderr, "worker executor: %v\n", err)
			return 1
		}
	}
	failedCommands := 0
	for _, command := range spec.Commands {
		if len(command.Args) == 0 {
			fmt.Fprintf(stderr, "worker executor: empty command for %s\n", command.ID)
			return 1
		}
		args := withExternalExecutorExecuteFlag(command.Args)
		if previous, ok := resumeCommands[command.ID]; ok && isReusableWorkerExecutorCommandEvidence(spec.Stage, previous, args) {
			previous = normalizeWorkerExecutorCommandEvidence(previous)
			results = append(results, previous)
			fmt.Fprintf(stdout, "resumed command: %s\n", command.ID)
			continue
		}
		maxAttempts := *commandRetries + 1
		attempts := make([]workerExecutorRunCommandAttemptEvidence, 0, maxAttempts)
		var commandErr error
		var parseErr error
		var timedOut bool
		var commandEvidence workerExecutorRunCommandEvidence
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			commandContext := context.Background()
			var cancel context.CancelFunc
			if *commandTimeout > 0 {
				commandContext, cancel = context.WithTimeout(commandContext, *commandTimeout)
			}
			cmd := newWorkerExecutorCommand(commandContext, args[0], args[1:]...)
			cmd.Dir = *root
			startedAt := time.Now().UTC()
			output, err := cmd.CombinedOutput()
			completedAt := time.Now().UTC()
			timedOut = commandContext.Err() == context.DeadlineExceeded
			if cancel != nil {
				cancel()
			}
			if len(output) > 0 {
				fmt.Fprint(stdout, string(output))
			}
			commandEvidenceError := ""
			if timedOut {
				commandEvidenceError = fmt.Sprintf("command timed out after %s", commandTimeout.String())
			}
			attemptEvidence := workerExecutorRunCommandAttemptEvidence{
				Attempt:     attempt,
				ExitCode:    exitCodeForCommandError(err),
				Output:      string(output),
				Error:       commandEvidenceError,
				StartedAt:   startedAt.Format(time.RFC3339Nano),
				CompletedAt: completedAt.Format(time.RFC3339Nano),
				DurationMs:  completedAt.Sub(startedAt).Milliseconds(),
			}
			attempts = append(attempts, attemptEvidence)
			cdcAppliedChanges, errParse := workerExecutorCDCAppliedChanges(spec.Stage, string(output))
			commandEvidence = workerExecutorRunCommandEvidence{
				ID:                command.ID,
				Args:              args,
				ShellCommand:      renderArgsForEvidence(args),
				ExitCode:          attemptEvidence.ExitCode,
				Output:            attemptEvidence.Output,
				Error:             attemptEvidence.Error,
				AttemptCount:      len(attempts),
				StartedAt:         attemptEvidence.StartedAt,
				CompletedAt:       attemptEvidence.CompletedAt,
				DurationMs:        attemptEvidence.DurationMs,
				CDCAppliedChanges: cdcAppliedChanges,
			}
			if len(attempts) > 1 {
				commandEvidence.Attempts = attempts
			}
			commandErr = err
			parseErr = errParse
			if commandErr == nil {
				break
			}
			if attempt == maxAttempts {
				break
			}
			if *retryBackoff > 0 {
				fmt.Fprintf(stderr, "worker executor: command %s attempt %d/%d failed: %v; retrying after %s\n", command.ID, attempt, maxAttempts, commandErr, retryBackoff.String())
				time.Sleep(*retryBackoff)
			} else {
				fmt.Fprintf(stderr, "worker executor: command %s attempt %d/%d failed: %v; retrying\n", command.ID, attempt, maxAttempts, commandErr)
			}
		}
		results = append(results, commandEvidence)
		if commandErr != nil {
			if spec.Stage == "validation" {
				failedCommands++
				continue
			}
			if _, evidenceErr := writeWorkerExecutorRunEvidence(*root, spec, "failed", results); evidenceErr != nil {
				fmt.Fprintf(stderr, "worker executor: %v\n", evidenceErr)
			}
			if timedOut {
				fmt.Fprintf(stderr, "worker executor: command %s timed out after %s\n", command.ID, commandTimeout.String())
				return 1
			}
			fmt.Fprintf(stderr, "worker executor: command %s failed: %v\n", command.ID, commandErr)
			return 1
		}
		if parseErr != nil {
			results[len(results)-1].Error = parseErr.Error()
			if _, evidenceErr := writeWorkerExecutorRunEvidence(*root, spec, "failed", results); evidenceErr != nil {
				fmt.Fprintf(stderr, "worker executor: %v\n", evidenceErr)
			}
			fmt.Fprintf(stderr, "worker executor: command %s: %v\n", command.ID, parseErr)
			return 1
		}
	}
	if failedCommands > 0 {
		if _, evidenceErr := writeWorkerExecutorRunEvidence(*root, spec, "failed", results); evidenceErr != nil {
			fmt.Fprintf(stderr, "worker executor: %v\n", evidenceErr)
		}
		fmt.Fprintf(stderr, "worker executor: validation completed with %d failed command(s)\n", failedCommands)
		return 1
	}
	evidenceFile, err := writeWorkerExecutorRunEvidence(*root, spec, "succeeded", results)
	if err != nil {
		fmt.Fprintf(stderr, "worker executor: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "worker executor completed for %s\n", spec.ProjectID)
	fmt.Fprintf(stdout, "stage: %s\n", spec.Stage)
	fmt.Fprintf(stdout, "commands: %d\n", len(spec.Commands))
	fmt.Fprintf(stdout, "wrote %s\n", evidenceFile)
	return 0
}

func loadWorkerExecutorResumeCommands(root string, spec gitops.WorkerExecutorSpec) (map[string]workerExecutorRunCommandEvidence, error) {
	path := filepath.Join(root, filepath.FromSlash(workerExecutorRunEvidenceRel(spec)))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]workerExecutorRunCommandEvidence{}, nil
		}
		return nil, fmt.Errorf("read resume executor evidence: %w", err)
	}
	var evidence workerExecutorRunEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		return nil, fmt.Errorf("parse resume executor evidence: %w", err)
	}
	if evidence.Stage != spec.Stage {
		return nil, fmt.Errorf("resume executor evidence stage %q does not match current stage %q", evidence.Stage, spec.Stage)
	}
	if evidence.SourceClusterID != spec.SourceClusterID {
		return nil, fmt.Errorf("resume executor evidence source_cluster_id %q does not match current source cluster %q", evidence.SourceClusterID, spec.SourceClusterID)
	}
	if evidence.ProjectID != spec.ProjectID {
		return nil, fmt.Errorf("resume executor evidence project_id %q does not match current project %q", evidence.ProjectID, spec.ProjectID)
	}
	if evidence.PayloadHash != spec.PayloadHash {
		return map[string]workerExecutorRunCommandEvidence{}, nil
	}
	commands := make(map[string]workerExecutorRunCommandEvidence, len(evidence.Commands))
	for _, command := range evidence.Commands {
		commands[command.ID] = command
	}
	return commands, nil
}

func isReusableWorkerExecutorCommandEvidence(stage string, command workerExecutorRunCommandEvidence, expectedArgs []string) bool {
	if command.ExitCode != 0 || strings.TrimSpace(command.Error) != "" {
		return false
	}
	if !stringSlicesEqual(command.Args, expectedArgs) {
		return false
	}
	if command.ShellCommand != renderArgsForEvidence(expectedArgs) {
		return false
	}
	if stage == "cdc" && command.CDCAppliedChanges == nil {
		return false
	}
	return true
}

func normalizeWorkerExecutorCommandEvidence(command workerExecutorRunCommandEvidence) workerExecutorRunCommandEvidence {
	if command.AttemptCount == 0 {
		if len(command.Attempts) > 0 {
			command.AttemptCount = len(command.Attempts)
		} else {
			command.AttemptCount = 1
		}
	}
	return command
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

type workerExecutorRunCommandEvidence struct {
	ID                string                                    `json:"id"`
	Args              []string                                  `json:"args"`
	ShellCommand      string                                    `json:"shell_command"`
	ExitCode          int                                       `json:"exit_code"`
	Output            string                                    `json:"output"`
	Error             string                                    `json:"error,omitempty"`
	AttemptCount      int                                       `json:"attempt_count"`
	Attempts          []workerExecutorRunCommandAttemptEvidence `json:"attempts,omitempty"`
	StartedAt         string                                    `json:"started_at"`
	CompletedAt       string                                    `json:"completed_at"`
	DurationMs        int64                                     `json:"duration_ms"`
	CDCAppliedChanges *int                                      `json:"cdc_applied_changes,omitempty"`
}

type workerExecutorRunCommandAttemptEvidence struct {
	Attempt     int    `json:"attempt"`
	ExitCode    int    `json:"exit_code"`
	Output      string `json:"output"`
	Error       string `json:"error,omitempty"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	DurationMs  int64  `json:"duration_ms"`
}

type workerExecutorRunEvidence struct {
	Stage           string                             `json:"stage"`
	Status          string                             `json:"status"`
	ProjectID       string                             `json:"project_id"`
	SourceClusterID string                             `json:"source_cluster_id"`
	PayloadHash     string                             `json:"payload_hash"`
	Commands        []workerExecutorRunCommandEvidence `json:"commands"`
	GeneratedAt     string                             `json:"generated_at"`
}

func writeWorkerExecutorRunEvidence(root string, spec gitops.WorkerExecutorSpec, status string, commands []workerExecutorRunCommandEvidence) (string, error) {
	rel := workerExecutorRunEvidenceRel(spec)
	evidence := workerExecutorRunEvidence{
		Stage:           spec.Stage,
		Status:          status,
		ProjectID:       spec.ProjectID,
		SourceClusterID: spec.SourceClusterID,
		PayloadHash:     spec.PayloadHash,
		Commands:        commands,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal executor evidence: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), data, 0o644); err != nil {
		return "", fmt.Errorf("write executor evidence: %w", err)
	}
	return filepath.ToSlash(filepath.Join("evidence", "executor-"+spec.Stage+"-run.json")), nil
}

func workerExecutorRunEvidenceRel(spec gitops.WorkerExecutorSpec) string {
	return filepath.ToSlash(filepath.Join("clusters", spec.SourceClusterID, "projects", spec.ProjectID, "evidence", "executor-"+spec.Stage+"-run.json"))
}

func exitCodeForCommandError(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}

func renderArgsForEvidence(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuoteForEvidence(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuoteForEvidence(arg string) string {
	if arg == "" {
		return "''"
	}
	for _, r := range arg {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '-', '_', '.', '/', ':', '=':
			continue
		default:
			return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
		}
	}
	return arg
}

func workerExecutorCDCAppliedChanges(stage, output string) (*int, error) {
	if stage != "cdc" {
		return nil, nil
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "applied changes:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "applied changes:"))
		appliedChanges, err := strconv.Atoi(value)
		if err != nil || appliedChanges < 0 {
			return nil, fmt.Errorf("CDC applied changes output %q must contain a non-negative integer", line)
		}
		return &appliedChanges, nil
	}
	return nil, fmt.Errorf("CDC executor output must include applied changes: N")
}

func withExternalExecutorExecuteFlag(args []string) []string {
	out := append([]string(nil), args...)
	for _, arg := range out[1:] {
		if arg == "--execute" {
			return out
		}
	}
	if len(out) >= 2 {
		return append(append(out[:2:2], "--execute"), out[2:]...)
	}
	return append(out, "--execute")
}

func runAdvanceCDCCheckpoint(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("advance-cdc-checkpoint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	status := fs.String("status", "running", "checkpoint status to write: running or caught_up")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.AdvanceCDCCheckpointFromExecutorEvidence(*root, *sourceClusterID, *projectID, gitops.CDCCheckpointAdvanceSpec{
		Status: *status,
	})
	if err != nil {
		fmt.Fprintf(stderr, "advance cdc checkpoint: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cdc checkpoint advanced for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", result.Status)
	fmt.Fprintf(stdout, "updated tables: %d\n", result.UpdatedTables)
	fmt.Fprintf(stdout, "applied changes: %d\n", result.AppliedChanges)
	fmt.Fprintf(stdout, "wrote %s\n", result.CheckpointFile)
	return 0
}

func runWorkerReconcile(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-reconcile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "optional source cluster id to scope reconcile actions")
	dryRun := fs.Bool("dry-run", false, "plan worker actions without executing them")
	executeNext := fs.Bool("execute-next", false, "execute the first ready metadata-only worker action")
	loop := fs.Bool("loop", false, "execute ready metadata-only worker actions until none remain or max iterations is reached")
	jsonOutput := fs.Bool("json", false, "write dry-run report as JSON")
	maxIterations := fs.Int("max-iterations", 0, "maximum loop iterations; 0 means continue until no ready metadata-only actions remain")
	interval := fs.Duration("interval", 5*time.Second, "sleep interval between loop iterations")
	holder := fs.String("holder", "", "worker lease holder id for --execute-next or --loop")
	leaseTTL := fs.Duration("lease-ttl", 15*time.Minute, "worker lease ttl for --execute-next or --loop")
	statePRDraft := fs.Bool("state-pr-draft", false, "write PR drafts for worker state and evidence changes after --execute-next or --loop")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	selectedModes := 0
	for _, enabled := range []bool{*dryRun, *executeNext, *loop} {
		if enabled {
			selectedModes++
		}
	}
	if selectedModes != 1 {
		fmt.Fprintln(stderr, "worker-reconcile requires exactly one of --dry-run, --execute-next, or --loop")
		return 2
	}
	if *jsonOutput && !*dryRun {
		fmt.Fprintln(stderr, "worker-reconcile --json is only supported with --dry-run")
		return 2
	}
	if *loop {
		if *maxIterations < 0 {
			fmt.Fprintln(stderr, "worker-reconcile --max-iterations must be non-negative")
			return 2
		}
		if *interval < 0 {
			fmt.Fprintln(stderr, "worker-reconcile --interval must be non-negative")
			return 2
		}
		return runWorkerReconcileLoop(*root, gitops.WorkerReconcileExecuteSpec{
			Holder:          *holder,
			LeaseTTL:        *leaseTTL,
			CreatePRDraft:   *statePRDraft,
			SourceClusterID: *sourceClusterID,
		}, *maxIterations, *interval, stdout, stderr)
	}
	if *executeNext {
		result, err := gitops.ExecuteNextWorkerReconcile(*root, gitops.WorkerReconcileExecuteSpec{
			Holder:          *holder,
			LeaseTTL:        *leaseTTL,
			CreatePRDraft:   *statePRDraft,
			SourceClusterID: *sourceClusterID,
		})
		if err != nil {
			fmt.Fprintf(stderr, "worker reconcile: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "worker reconcile execute next")
		fmt.Fprintf(stdout, "selected: %s/%s %s\n", result.Action.SourceClusterID, result.Action.ProjectID, result.Action.Stage)
		fmt.Fprintf(stdout, "status: %s\n", result.Status)
		fmt.Fprintf(stdout, "payload hash: %s\n", result.Action.PayloadHash)
		fmt.Fprintf(stdout, "lease id: %s\n", result.LeaseID)
		fmt.Fprintf(stdout, "wrote %s\n", result.StateFile)
		fmt.Fprintf(stdout, "wrote %s\n", result.EvidenceFile)
		fmt.Fprintf(stdout, "wrote %s\n", result.LeaseFile)
		if result.PRBodyFile != "" {
			fmt.Fprintf(stdout, "state PR draft: %s\n", result.PRBodyFile)
			fmt.Fprintf(stdout, "branch: %s\n", result.BranchName)
		}
		return 0
	}
	report, err := gitops.PlanWorkerReconcileWithSpec(*root, gitops.WorkerReconcilePlanSpec{SourceClusterID: *sourceClusterID})
	if err != nil {
		fmt.Fprintf(stderr, "worker reconcile: %v\n", err)
		return 1
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "worker reconcile json: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdout, "worker reconcile dry run")
	fmt.Fprintf(stdout, "projects: %d\n", report.Projects)
	fmt.Fprintf(stdout, "ready actions: %d\n", report.ReadyActions)
	fmt.Fprintf(stdout, "blocked actions: %d\n", report.BlockedActions)
	for _, action := range report.Actions {
		fmt.Fprintf(stdout, "- [%s] %s/%s %s\n", action.Status, action.SourceClusterID, action.ProjectID, action.Stage)
		if action.PayloadHash != "" {
			fmt.Fprintf(stdout, "  payload hash: %s\n", action.PayloadHash)
		}
		if action.Reason != "" {
			fmt.Fprintf(stdout, "  reason: %s\n", action.Reason)
		}
		if action.Status == "ready" {
			fmt.Fprintf(stdout, "  command: %s\n", action.Command)
		}
	}
	return 0
}

func runWorkerReconcileLoop(root string, spec gitops.WorkerReconcileExecuteSpec, maxIterations int, interval time.Duration, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "worker reconcile loop")
	executed := 0
	for iteration := 1; maxIterations == 0 || iteration <= maxIterations; iteration++ {
		result, err := gitops.ExecuteNextWorkerReconcile(root, spec)
		if err != nil {
			message := err.Error()
			if isNoReadyWorkerActionsError(message) {
				fmt.Fprintf(stdout, "iteration %d: %s\n", iteration, message)
				fmt.Fprintf(stdout, "executed actions: %d\n", executed)
				return 0
			}
			fmt.Fprintf(stderr, "worker reconcile loop: %v\n", err)
			return 1
		}
		executed++
		fmt.Fprintf(stdout, "iteration %d: selected %s/%s %s\n", iteration, result.Action.SourceClusterID, result.Action.ProjectID, result.Action.Stage)
		fmt.Fprintf(stdout, "  status: %s\n", result.Status)
		fmt.Fprintf(stdout, "  payload hash: %s\n", result.Action.PayloadHash)
		fmt.Fprintf(stdout, "  lease id: %s\n", result.LeaseID)
		fmt.Fprintf(stdout, "  wrote %s\n", result.StateFile)
		fmt.Fprintf(stdout, "  wrote %s\n", result.EvidenceFile)
		fmt.Fprintf(stdout, "  wrote %s\n", result.LeaseFile)
		if result.PRBodyFile != "" {
			fmt.Fprintf(stdout, "  state PR draft: %s\n", result.PRBodyFile)
			fmt.Fprintf(stdout, "  branch: %s\n", result.BranchName)
		}
		if maxIterations == 0 || iteration < maxIterations {
			time.Sleep(interval)
		}
	}
	fmt.Fprintf(stdout, "executed actions: %d\n", executed)
	return 0
}

func isNoReadyWorkerActionsError(message string) bool {
	return strings.Contains(message, "no ready worker actions") || strings.Contains(message, "no ready metadata worker actions")
}

func runWorkerAgent(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "optional source cluster id to scope reconcile actions")
	holder := fs.String("holder", "", "worker lease holder id")
	leaseTTL := fs.Duration("lease-ttl", 15*time.Minute, "worker lease ttl")
	maxIterations := fs.Int("max-iterations", 0, "maximum loop iterations; 0 means continue until no ready metadata-only actions remain")
	interval := fs.Duration("interval", 5*time.Second, "sleep interval between loop iterations")
	poll := fs.Bool("poll", false, "keep polling after idle no-ready scans")
	idleIterations := fs.Int("idle-iterations", 0, "maximum consecutive idle polls in --poll mode; 0 means unlimited")
	statePRDraft := fs.Bool("state-pr-draft", false, "write PR drafts for worker state and evidence changes")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*holder) == "" {
		fmt.Fprintln(stderr, "worker-agent requires --holder")
		return 2
	}
	if *maxIterations < 0 {
		fmt.Fprintln(stderr, "worker-agent --max-iterations must be non-negative")
		return 2
	}
	if *interval < 0 {
		fmt.Fprintln(stderr, "worker-agent --interval must be non-negative")
		return 2
	}
	if *idleIterations < 0 {
		fmt.Fprintln(stderr, "worker-agent --idle-iterations must be non-negative")
		return 2
	}
	fmt.Fprintln(stdout, "worker agent")
	fmt.Fprintf(stdout, "holder: %s\n", *holder)
	spec := gitops.WorkerReconcileExecuteSpec{
		Holder:          *holder,
		LeaseTTL:        *leaseTTL,
		CreatePRDraft:   *statePRDraft,
		SourceClusterID: *sourceClusterID,
	}
	if *poll {
		return runWorkerAgentPoll(*root, spec, *maxIterations, *interval, *idleIterations, stdout, stderr)
	}
	return runWorkerReconcileLoop(*root, spec, *maxIterations, *interval, stdout, stderr)
}

func runWorkerAgentPoll(root string, spec gitops.WorkerReconcileExecuteSpec, maxIterations int, interval time.Duration, idleIterations int, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "worker agent poll")
	executed := 0
	idle := 0
	for {
		result, err := gitops.ExecuteNextWorkerReconcile(root, spec)
		if err != nil {
			message := err.Error()
			if isNoReadyWorkerActionsError(message) {
				idle++
				fmt.Fprintf(stdout, "idle iteration %d: %s\n", idle, message)
				if idleIterations > 0 && idle >= idleIterations {
					fmt.Fprintf(stdout, "executed actions: %d\n", executed)
					return 0
				}
				time.Sleep(interval)
				continue
			}
			fmt.Fprintf(stderr, "worker agent poll: %v\n", err)
			return 1
		}
		idle = 0
		executed++
		fmt.Fprintf(stdout, "iteration %d: selected %s/%s %s\n", executed, result.Action.SourceClusterID, result.Action.ProjectID, result.Action.Stage)
		fmt.Fprintf(stdout, "  status: %s\n", result.Status)
		fmt.Fprintf(stdout, "  payload hash: %s\n", result.Action.PayloadHash)
		fmt.Fprintf(stdout, "  lease id: %s\n", result.LeaseID)
		fmt.Fprintf(stdout, "  wrote %s\n", result.StateFile)
		fmt.Fprintf(stdout, "  wrote %s\n", result.EvidenceFile)
		fmt.Fprintf(stdout, "  wrote %s\n", result.LeaseFile)
		if result.PRBodyFile != "" {
			fmt.Fprintf(stdout, "  state PR draft: %s\n", result.PRBodyFile)
			fmt.Fprintf(stdout, "  branch: %s\n", result.BranchName)
		}
		if maxIterations > 0 && executed >= maxIterations {
			fmt.Fprintf(stdout, "executed actions: %d\n", executed)
			return 0
		}
		time.Sleep(interval)
	}
}

func runCreateCluster(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create-cluster", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	clusterID := fs.String("cluster-id", "", "upstream SQL Server cluster id")
	displayName := fs.String("display-name", "", "display name")
	listener := fs.String("listener", "", "SQL Server listener or hostname")
	port := fs.String("port", "1433", "SQL Server port")
	secretRef := fs.String("secret-ref", "", "secret reference, not a plaintext secret")
	cdcMode := fs.String("cdc-mode", "sqlserver-cdc", "CDC mode")
	retentionHours := fs.Int("retention-hours", 168, "required CDC retention hours")
	owners := fs.String("owner", "", "comma-separated owners")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	parsedPort, err := strconv.Atoi(*port)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --port %q: %v\n", *port, err)
		return 2
	}
	spec := gitops.ClusterSpec{
		ClusterID:              *clusterID,
		DisplayName:            *displayName,
		Listener:               *listener,
		Port:                   parsedPort,
		SecretRef:              *secretRef,
		CDCMode:                *cdcMode,
		RetentionHoursRequired: *retentionHours,
		Owners:                 splitCSV(*owners),
	}
	if err := gitops.CreateCluster(*root, spec); err != nil {
		fmt.Fprintf(stderr, "create cluster: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "created cluster %s\n", *clusterID)
	return 0
}

func runCreateProject(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create-project", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	displayName := fs.String("display-name", "", "display name")
	sourceDatabase := fs.String("source-database", "", "source SQL Server database")
	sourceSchemas := fs.String("source-schema", "", "comma-separated source schemas")
	targetName := fs.String("target-name", "", "target TiDB cluster name")
	targetDatabase := fs.String("target-database", "", "target TiDB database")
	targetSecretRef := fs.String("target-secret-ref", "", "target TiDB secret reference")
	mode := fs.String("mode", "short-downtime", "migration mode")
	owners := fs.String("owner", "", "comma-separated owners")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	spec := gitops.ProjectSpec{
		SourceClusterID: *sourceClusterID,
		ProjectID:       *projectID,
		DisplayName:     *displayName,
		SourceDatabase:  *sourceDatabase,
		SourceSchemas:   splitCSV(*sourceSchemas),
		TargetName:      *targetName,
		TargetDatabase:  *targetDatabase,
		TargetSecretRef: *targetSecretRef,
		Mode:            *mode,
		Owners:          splitCSV(*owners),
	}
	if err := gitops.CreateProject(*root, spec); err != nil {
		fmt.Fprintf(stderr, "create project: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "created project %s under source cluster %s\n", *projectID, *sourceClusterID)
	return 0
}

func splitCSV(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `sqlserver2tidb manages GitOps metadata for SQL Server to TiDB migrations.

Usage:
  sqlserver2tidb version
  sqlserver2tidb init-repo --root .
  sqlserver2tidb doctor --root .
  sqlserver2tidb validate-repo --root .
  sqlserver2tidb discover-sqlserver --root . --source-cluster-id prod-sqlserver-a --dry-run
  sqlserver2tidb discover-sqlserver --root . --source-cluster-id prod-sqlserver-a --connection-string-env SQLSERVER2TIDB_SQLSERVER_DSN
  sqlserver2tidb analyze-compatibility --root . --source-cluster-id prod-sqlserver-a
  sqlserver2tidb generate-schema-draft --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb generate-data-plans --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --object-uri-prefix https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full
  sqlserver2tidb generate-cdc-plan --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb prepare-cdc-range --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --to-lsn 0x00000027000001f40003
  sqlserver2tidb prepare-cdc-iteration --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --max-lsn 0x00000027000001f40004 --pr-draft
  sqlserver2tidb generate-validation-plan --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb generate-pr-draft --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage schema
  sqlserver2tidb create-pr --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage schema
  sqlserver2tidb create-worker-state-pr --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export
  sqlserver2tidb generate-executor-evidence-pr-draft --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage ddl
  sqlserver2tidb create-executor-evidence-pr --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage ddl
  sqlserver2tidb compute-payload-hash --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export
  sqlserver2tidb worker-export --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-import --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-cdc --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-validate --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-executor --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export
  sqlserver2tidb worker-executor --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage import --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING --import-batch-size 1000
  sqlserver2tidb worker-executor --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage validation --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING
  sqlserver2tidb advance-cdc-checkpoint --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --status running
  sqlserver2tidb worker-reconcile --root . --source-cluster-id prod-sqlserver-a --dry-run
  sqlserver2tidb worker-reconcile --root . --source-cluster-id prod-sqlserver-a --execute-next --holder agent-a --state-pr-draft
  sqlserver2tidb worker-reconcile --root . --source-cluster-id prod-sqlserver-a --loop --holder agent-a --max-iterations 10 --interval 5s
  sqlserver2tidb worker-agent --root . --source-cluster-id prod-sqlserver-a --holder agent-a --max-iterations 0 --interval 5s --poll --idle-iterations 0 --state-pr-draft
  sqlserver2tidb create-cluster --cluster-id prod-sqlserver-a --display-name "prod SQL Server A" --listener sqlserver-a.internal --secret-ref vault://...
  sqlserver2tidb create-project --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --source-database sales --source-schema dbo --target-name tidb-prod-a --target-database app --target-secret-ref vault://...
`)
}
