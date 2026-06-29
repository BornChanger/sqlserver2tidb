package cli

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	case "cdc-orchestrator":
		return runCDCOrchestrator(args[1:], stdout, stderr)
	case "cdc-health":
		return runCDCHealth(args[1:], stdout, stderr)
	case "generate-validation-plan":
		return runGenerateValidationPlan(args[1:], stdout, stderr)
	case "generate-pr-draft":
		return runGeneratePRDraft(args[1:], stdout, stderr)
	case "create-pr":
		return runCreatePR(args[1:], stdout, stderr)
	case "sync-github-pr-approval":
		return runSyncGitHubPRApproval(args[1:], stdout, stderr)
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
	case "worker-cutover":
		return runWorkerCutover(args[1:], stdout, stderr)
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
	objectURIPrefix := fs.String("object-uri-prefix", "", "CSV output URI prefix for exported full-load files; sql-insert supports file/http(s)/s3/gs/azblob, tidb-import-into supports file/s3/gs, tidb-lightning supports file/s3/gs/azblob")
	chunkSizeRows := fs.Int64("chunk-size-rows", 1000000, "estimated rows per export chunk")
	exportFormat := fs.String("export-format", "csv", "export file format")
	importEngine := fs.String("import-engine", "sql-insert", "TiDB import engine: sql-insert, tidb-import-into, or tidb-lightning")
	compression := fs.String("compression", "none", "export/import compression: none or gzip")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.GenerateDataMovementPlans(*root, *sourceClusterID, *projectID, gitops.DataMovementPlanSpec{
		ObjectURIPrefix: *objectURIPrefix,
		ChunkSizeRows:   *chunkSizeRows,
		ExportFormat:    *exportFormat,
		ImportEngine:    *importEngine,
		Compression:     *compression,
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

func runCDCOrchestrator(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cdc-orchestrator", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	executorBinary := fs.String("executor-binary", "sqlserver2tidb-executor", "executor binary used to probe SQL Server CDC max LSN")
	sourceConnectionStringEnv := fs.String("source-connection-string-env", "SQLSERVER2TIDB_SOURCE_CONNECTION_STRING", "environment variable containing the SQL Server CDC connection string")
	targetConnectionStringEnv := fs.String("target-connection-string-env", "SQLSERVER2TIDB_TARGET_CONNECTION_STRING", "environment variable containing the TiDB/MySQL connection string for CDC apply")
	maxLSNOverride := fs.String("max-lsn", "", "skip executor probing and use this SQL Server CDC max LSN")
	fromLSN := fs.String("from-lsn", "", "initial CDC from LSN for tables without checkpoint state")
	prDraft := fs.Bool("pr-draft", false, "write a CDC range PR draft when a new range is prepared")
	skipRetentionCheck := fs.Bool("skip-retention-check", false, "skip per-table SQL Server CDC min LSN retention checks")
	applyApproved := fs.Bool("apply-approved", false, "execute an already approved CDC range before probing the next SQL Server max LSN")
	checkpointStatus := fs.String("checkpoint-status", "running", "checkpoint status to write after approved CDC apply: running or caught_up")
	commandTimeout := fs.Duration("command-timeout", 0, "maximum runtime per CDC apply executor command; 0 disables the timeout")
	commandRetries := fs.Int("command-retries", 0, "number of retries for a failed CDC apply executor command")
	retryBackoff := fs.Duration("retry-backoff", time.Second, "fixed backoff between CDC apply executor command retries")
	resume := fs.Bool("resume", false, "skip matching successful CDC apply commands from existing executor evidence")
	minAppliedChanges := fs.Int("min-applied-changes", 0, "minimum total CDC applied changes required before the orchestrator can exit successfully")
	poll := fs.Bool("poll", false, "continue polling when the project is caught up")
	maxIterations := fs.Int("max-iterations", 0, "maximum probe iterations; 0 means unlimited")
	interval := fs.Duration("interval", 5*time.Second, "sleep interval between caught-up polling iterations")
	idleIterations := fs.Int("idle-iterations", 0, "maximum consecutive caught-up polls in --poll mode; 0 means unlimited")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *maxIterations < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --max-iterations must be non-negative")
		return 2
	}
	if *interval < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --interval must be non-negative")
		return 2
	}
	if *idleIterations < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --idle-iterations must be non-negative")
		return 2
	}
	if *commandTimeout < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --command-timeout must be non-negative")
		return 2
	}
	if *commandRetries < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --command-retries must be non-negative")
		return 2
	}
	if *retryBackoff < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --retry-backoff must be non-negative")
		return 2
	}
	if *minAppliedChanges < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --min-applied-changes must be non-negative")
		return 2
	}

	fmt.Fprintln(stdout, "cdc orchestrator")
	prepared := 0
	applied := 0
	appliedChanges := 0
	idle := 0
	finish := func() int {
		fmt.Fprintf(stdout, "prepared iterations: %d\n", prepared)
		fmt.Fprintf(stdout, "applied iterations: %d\n", applied)
		fmt.Fprintf(stdout, "applied changes: %d\n", appliedChanges)
		if appliedChanges < *minAppliedChanges {
			fmt.Fprintf(stderr, "cdc orchestrator: applied changes %d below required minimum %d\n", appliedChanges, *minAppliedChanges)
			return 1
		}
		return 0
	}
	for iteration := 1; ; iteration++ {
		if *maxIterations > 0 && iteration > *maxIterations {
			return finish()
		}
		if *applyApproved {
			status, err := runCDCOrchestratorApplyApproved(cdcOrchestratorApplySpec{
				Root:                      *root,
				SourceClusterID:           *sourceClusterID,
				ProjectID:                 *projectID,
				ExecutorBinary:            *executorBinary,
				SourceConnectionStringEnv: *sourceConnectionStringEnv,
				TargetConnectionStringEnv: *targetConnectionStringEnv,
				CheckpointStatus:          *checkpointStatus,
				CommandTimeout:            *commandTimeout,
				CommandRetries:            *commandRetries,
				RetryBackoff:              *retryBackoff,
				Resume:                    *resume,
			}, stdout, stderr)
			if err != nil {
				fmt.Fprintf(stderr, "cdc orchestrator: %v\n", err)
				return 1
			}
			if status.Applied {
				applied++
				appliedChanges += status.AppliedChanges
			}
		}
		bounds, err := cdcOrchestratorProbeLSNBounds(*root, *sourceClusterID, *projectID, *executorBinary, *sourceConnectionStringEnv, *maxLSNOverride, *skipRetentionCheck)
		if err != nil {
			fmt.Fprintf(stderr, "cdc orchestrator: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "iteration %d: max_lsn %s\n", iteration, bounds.MaxLSN)
		result, err := gitops.PrepareCDCIteration(*root, *sourceClusterID, *projectID, gitops.CDCIterationSpec{
			MaxLSN:         bounds.MaxLSN,
			InitialFromLSN: *fromLSN,
			WritePRDraft:   *prDraft,
			MinLSNs:        bounds.MinLSNs,
		})
		if err != nil {
			fmt.Fprintf(stderr, "cdc orchestrator: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "status: %s\n", result.Status)
		if result.Status == gitops.CDCIterationStatusRangePrepared {
			prepared++
			fmt.Fprintf(stdout, "updated tables: %d\n", result.UpdatedTables)
			fmt.Fprintf(stdout, "wrote %s\n", "plan/cdc-plan.yaml")
			if result.PRBodyFile != "" {
				fmt.Fprintf(stdout, "PR draft: %s\n", result.PRBodyFile)
			}
			return finish()
		}
		if result.Status != gitops.CDCIterationStatusCaughtUp {
			fmt.Fprintf(stderr, "cdc orchestrator: unsupported cdc iteration status %q\n", result.Status)
			return 1
		}
		if !*poll {
			return finish()
		}
		idle++
		fmt.Fprintf(stdout, "idle iteration %d: caught_up\n", idle)
		if *idleIterations > 0 && idle >= *idleIterations {
			return finish()
		}
		time.Sleep(*interval)
	}
}

type cdcOrchestratorApplySpec struct {
	Root                      string
	SourceClusterID           string
	ProjectID                 string
	ExecutorBinary            string
	SourceConnectionStringEnv string
	TargetConnectionStringEnv string
	CheckpointStatus          string
	CommandTimeout            time.Duration
	CommandRetries            int
	RetryBackoff              time.Duration
	Resume                    bool
}

type cdcOrchestratorApplyStatus struct {
	Applied        bool
	AppliedChanges int
}

func runCDCOrchestratorApplyApproved(spec cdcOrchestratorApplySpec, stdout, stderr io.Writer) (cdcOrchestratorApplyStatus, error) {
	_, err := gitops.PrepareWorkerExecutor(spec.Root, spec.SourceClusterID, spec.ProjectID, "cdc", gitops.WorkerExecutorPrepareSpec{
		Binary:                    spec.ExecutorBinary,
		SourceConnectionStringEnv: spec.SourceConnectionStringEnv,
		TargetConnectionStringEnv: spec.TargetConnectionStringEnv,
	})
	if err != nil {
		if isCDCOrchestratorApplyNotReadyError(err) {
			fmt.Fprintf(stdout, "approved cdc apply not ready: %v\n", err)
			return cdcOrchestratorApplyStatus{}, nil
		}
		return cdcOrchestratorApplyStatus{}, err
	}
	applyStatus, err := gitops.CheckCDCPlanApplyStatus(spec.Root, spec.SourceClusterID, spec.ProjectID)
	if err != nil {
		return cdcOrchestratorApplyStatus{}, err
	}
	if !applyStatus.Needed {
		fmt.Fprintf(stdout, "approved cdc apply skipped: %s\n", applyStatus.Reason)
		return cdcOrchestratorApplyStatus{}, nil
	}
	args := []string{
		"worker-executor",
		"--root", spec.Root,
		"--source-cluster-id", spec.SourceClusterID,
		"--project-id", spec.ProjectID,
		"--stage", "cdc",
		"--executor-binary", spec.ExecutorBinary,
		"--source-connection-string-env", spec.SourceConnectionStringEnv,
		"--target-connection-string-env", spec.TargetConnectionStringEnv,
		"--execute",
	}
	if spec.CommandTimeout > 0 {
		args = append(args, "--command-timeout", spec.CommandTimeout.String())
	}
	if spec.CommandRetries > 0 {
		args = append(args, "--command-retries", strconv.Itoa(spec.CommandRetries))
	}
	if spec.RetryBackoff != time.Second {
		args = append(args, "--retry-backoff", spec.RetryBackoff.String())
	}
	if spec.Resume {
		args = append(args, "--resume")
	}
	if code := runWorkerExecutor(args[1:], stdout, stderr); code != 0 {
		return cdcOrchestratorApplyStatus{}, fmt.Errorf("approved cdc apply failed")
	}
	result, err := gitops.AdvanceCDCCheckpointFromExecutorEvidence(spec.Root, spec.SourceClusterID, spec.ProjectID, gitops.CDCCheckpointAdvanceSpec{
		Status: spec.CheckpointStatus,
	})
	if err != nil {
		return cdcOrchestratorApplyStatus{}, err
	}
	fmt.Fprintln(stdout, "approved cdc apply completed")
	fmt.Fprintf(stdout, "checkpoint status: %s\n", result.Status)
	fmt.Fprintf(stdout, "checkpoint updated tables: %d\n", result.UpdatedTables)
	fmt.Fprintf(stdout, "checkpoint applied changes: %d\n", result.AppliedChanges)
	fmt.Fprintf(stdout, "wrote %s\n", result.CheckpointFile)
	return cdcOrchestratorApplyStatus{Applied: true, AppliedChanges: result.AppliedChanges}, nil
}

func isCDCOrchestratorApplyNotReadyError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "cdc approval is not approved") ||
		strings.Contains(message, "cdc approval payload hash mismatch") ||
		strings.Contains(message, "cdc plan status is ") ||
		strings.Contains(message, "read approval:")
}

type cdcOrchestratorLSNBounds struct {
	MaxLSN  string
	MinLSNs map[string]string
}

func cdcOrchestratorProbeLSNBounds(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv, maxLSNOverride string, skipRetentionCheck bool) (cdcOrchestratorLSNBounds, error) {
	maxLSN, err := cdcOrchestratorMaxLSN(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv, maxLSNOverride)
	if err != nil {
		return cdcOrchestratorLSNBounds{}, err
	}
	minLSNs := map[string]string{}
	if !skipRetentionCheck {
		minLSNs, err = cdcOrchestratorMinLSNs(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv)
		if err != nil {
			return cdcOrchestratorLSNBounds{}, err
		}
	}
	return cdcOrchestratorLSNBounds{
		MaxLSN:  maxLSN,
		MinLSNs: minLSNs,
	}, nil
}

func cdcOrchestratorMaxLSN(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv, maxLSNOverride string) (string, error) {
	if maxLSN := strings.TrimSpace(maxLSNOverride); maxLSN != "" {
		return maxLSN, nil
	}
	binary := strings.TrimSpace(executorBinary)
	if binary == "" {
		binary = "sqlserver2tidb-executor"
	}
	args := []string{
		"cdc-lsn",
		"--execute",
		"--root", ".",
		"--source-cluster-id", strings.TrimSpace(sourceClusterID),
		"--project-id", strings.TrimSpace(projectID),
	}
	if strings.TrimSpace(sourceConnectionStringEnv) != "" {
		args = append(args, "--source-connection-string-env", strings.TrimSpace(sourceConnectionStringEnv))
	}
	cmd := newWorkerExecutorCommand(context.Background(), binary, args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cdc-lsn probe failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	maxLSN, err := parseCDCMaxLSNOutput(string(output))
	if err != nil {
		return "", err
	}
	return maxLSN, nil
}

func cdcOrchestratorMinLSNs(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv string) (map[string]string, error) {
	sourceObjects, err := gitops.ListCDCTrackedSourceObjects(root, sourceClusterID, projectID)
	if err != nil {
		return nil, err
	}
	minLSNs := make(map[string]string, len(sourceObjects))
	for _, sourceObject := range sourceObjects {
		minLSN, err := cdcOrchestratorMinLSN(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv, sourceObject)
		if err != nil {
			return nil, err
		}
		minLSNs[sourceObject] = minLSN
	}
	return minLSNs, nil
}

func cdcOrchestratorMinLSN(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv, sourceObject string) (string, error) {
	binary := strings.TrimSpace(executorBinary)
	if binary == "" {
		binary = "sqlserver2tidb-executor"
	}
	args := []string{
		"cdc-lsn",
		"--execute",
		"--root", ".",
		"--source-cluster-id", strings.TrimSpace(sourceClusterID),
		"--project-id", strings.TrimSpace(projectID),
		"--source-object", strings.TrimSpace(sourceObject),
	}
	if strings.TrimSpace(sourceConnectionStringEnv) != "" {
		args = append(args, "--source-connection-string-env", strings.TrimSpace(sourceConnectionStringEnv))
	}
	cmd := newWorkerExecutorCommand(context.Background(), binary, args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cdc-lsn min probe failed for %s: %w: %s", sourceObject, err, strings.TrimSpace(string(output)))
	}
	minLSN, err := parseCDCMinLSNOutput(string(output))
	if err != nil {
		return "", fmt.Errorf("cdc-lsn min probe failed for %s: %w", sourceObject, err)
	}
	return minLSN, nil
}

func parseCDCMaxLSNOutput(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "max_lsn:") {
			maxLSN := strings.TrimSpace(strings.TrimPrefix(line, "max_lsn:"))
			if maxLSN == "" {
				return "", fmt.Errorf("cdc-lsn output max_lsn is empty")
			}
			return maxLSN, nil
		}
	}
	return "", fmt.Errorf("cdc-lsn output did not include max_lsn")
}

func parseCDCMinLSNOutput(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "min_lsn:") {
			minLSN := strings.TrimSpace(strings.TrimPrefix(line, "min_lsn:"))
			if minLSN == "" {
				return "", fmt.Errorf("cdc-lsn output min_lsn is empty")
			}
			return minLSN, nil
		}
	}
	return "", fmt.Errorf("cdc-lsn output did not include min_lsn")
}

func runCDCHealth(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cdc-health", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	maxLSN := fs.String("max-lsn", "", "current SQL Server CDC max LSN")
	maxCheckpointAge := fs.Duration("max-checkpoint-age", 0, "maximum allowed CDC checkpoint age; 0 disables age checking")
	nowRaw := fs.String("now", "", "current time override as RFC3339, for deterministic checks")
	jsonOutput := fs.Bool("json", false, "emit JSON health report")
	metricsFile := fs.String("metrics-file", "", "optional path to write JSON health metrics")
	historyFile := fs.String("history-file", "", "optional JSONL file to append CDC health reports")
	failOnWarning := fs.Bool("fail-on-warning", false, "return non-zero when health status is warning")
	probeLSN := fs.Bool("probe-lsn", false, "probe SQL Server CDC max/min LSNs through the executor before evaluating health")
	executorBinary := fs.String("executor-binary", "sqlserver2tidb-executor", "executor binary used when --probe-lsn is set")
	sourceConnectionStringEnv := fs.String("source-connection-string-env", "SQLSERVER2TIDB_SOURCE_CONNECTION_STRING", "environment variable containing the SQL Server CDC connection string")
	feishuWebhookEnv := fs.String("feishu-webhook-env", "SQLSERVER2TIDB_FEISHU_WEBHOOK", "environment variable containing the Feishu custom bot webhook URL; empty value disables Feishu alerts")
	feishuSecretEnv := fs.String("feishu-secret-env", "SQLSERVER2TIDB_FEISHU_SECRET", "environment variable containing the optional Feishu custom bot signing secret")
	feishuAlertMinSeverity := fs.String("feishu-alert-min-severity", "critical", "minimum CDC health status that sends Feishu alerts: ok, warning, critical, or none")
	slackWebhookEnv := fs.String("slack-webhook-env", "SQLSERVER2TIDB_SLACK_WEBHOOK", "environment variable containing the Slack incoming webhook URL; empty value disables Slack alerts")
	slackAlertMinSeverity := fs.String("slack-alert-min-severity", "critical", "minimum CDC health status that sends Slack alerts: ok, warning, critical, or none")
	var minLSNs cdcHealthMinLSNFlags
	fs.Var(&minLSNs, "min-lsn", "per-table SQL Server CDC min LSN as source.object=0x...")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *maxCheckpointAge < 0 {
		fmt.Fprintln(stderr, "cdc-health --max-checkpoint-age must be non-negative")
		return 2
	}
	now := time.Now().UTC()
	if strings.TrimSpace(*nowRaw) != "" {
		parsedNow, err := time.Parse(time.RFC3339, strings.TrimSpace(*nowRaw))
		if err != nil {
			fmt.Fprintf(stderr, "cdc-health --now must be RFC3339: %v\n", err)
			return 2
		}
		now = parsedNow.UTC()
	}
	feishuAlertSeverity, err := parseCDCHealthAlertSeverity(*feishuAlertMinSeverity, "--feishu-alert-min-severity")
	if err != nil {
		fmt.Fprintf(stderr, "cdc-health: %v\n", err)
		return 2
	}
	slackAlertSeverity, err := parseCDCHealthAlertSeverity(*slackAlertMinSeverity, "--slack-alert-min-severity")
	if err != nil {
		fmt.Fprintf(stderr, "cdc-health: %v\n", err)
		return 2
	}
	minLSNMap := minLSNs.Map()
	if *probeLSN {
		bounds, err := cdcOrchestratorProbeLSNBounds(*root, *sourceClusterID, *projectID, *executorBinary, *sourceConnectionStringEnv, *maxLSN, false)
		if err != nil {
			fmt.Fprintf(stderr, "cdc-health: %v\n", err)
			return 1
		}
		*maxLSN = bounds.MaxLSN
		for sourceObject, minLSN := range bounds.MinLSNs {
			minLSNMap[sourceObject] = minLSN
		}
	}
	report, err := gitops.EvaluateCDCHealth(*root, *sourceClusterID, *projectID, gitops.CDCHealthSpec{
		MaxLSN:           *maxLSN,
		MinLSNs:          minLSNMap,
		MaxCheckpointAge: *maxCheckpointAge,
		Now:              now,
	})
	if err != nil {
		fmt.Fprintf(stderr, "cdc-health: %v\n", err)
		return 1
	}
	reportJSON, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "cdc-health: marshal report: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*metricsFile) != "" {
		metricsPath := strings.TrimSpace(*metricsFile)
		if !filepath.IsAbs(metricsPath) {
			metricsPath = filepath.Join(*root, filepath.FromSlash(metricsPath))
		}
		if err := os.MkdirAll(filepath.Dir(metricsPath), 0o755); err != nil {
			fmt.Fprintf(stderr, "cdc-health: create metrics directory: %v\n", err)
			return 1
		}
		if err := os.WriteFile(metricsPath, append(reportJSON, '\n'), 0o644); err != nil {
			fmt.Fprintf(stderr, "cdc-health: write metrics file: %v\n", err)
			return 1
		}
	}
	if strings.TrimSpace(*historyFile) != "" {
		historyPath, err := appendCDCHealthHistory(*root, *historyFile, report)
		if err != nil {
			fmt.Fprintf(stderr, "cdc-health: append history: %v\n", err)
			return 1
		}
		if !*jsonOutput {
			fmt.Fprintf(stdout, "cdc health history appended: %s\n", historyPath)
		}
	}
	feishuWebhook := strings.TrimSpace(os.Getenv(strings.TrimSpace(*feishuWebhookEnv)))
	if feishuWebhook != "" && shouldSendCDCHealthAlert(report.Status, feishuAlertSeverity) {
		if err := sendFeishuCDCHealthAlert(feishuWebhook, os.Getenv(strings.TrimSpace(*feishuSecretEnv)), now, report); err != nil {
			fmt.Fprintf(stderr, "cdc-health: send Feishu alert: %v\n", err)
			return 1
		}
		if !*jsonOutput {
			fmt.Fprintln(stdout, "Feishu CDC health alert sent")
		}
	}
	slackWebhook := strings.TrimSpace(os.Getenv(strings.TrimSpace(*slackWebhookEnv)))
	if slackWebhook != "" && shouldSendCDCHealthAlert(report.Status, slackAlertSeverity) {
		if err := sendSlackCDCHealthAlert(slackWebhook, report); err != nil {
			fmt.Fprintf(stderr, "cdc-health: send Slack alert: %v\n", err)
			return 1
		}
		if !*jsonOutput {
			fmt.Fprintln(stdout, "Slack CDC health alert sent")
		}
	}
	if *jsonOutput {
		fmt.Fprintln(stdout, string(reportJSON))
	} else {
		renderCDCHealthText(stdout, report)
	}
	if report.Status == "critical" || (*failOnWarning && report.Status == "warning") {
		return 1
	}
	return 0
}

func appendCDCHealthHistory(root, path string, report gitops.CDCHealthReport) (string, error) {
	historyPath := strings.TrimSpace(path)
	if historyPath == "" {
		return "", fmt.Errorf("history file is required")
	}
	if !filepath.IsAbs(historyPath) {
		historyPath = filepath.Join(root, filepath.FromSlash(historyPath))
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("marshal history report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(historyPath), 0o755); err != nil {
		return "", fmt.Errorf("create history directory: %w", err)
	}
	file, err := os.OpenFile(historyPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", fmt.Errorf("open history file: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(reportJSON, '\n')); err != nil {
		return "", fmt.Errorf("write history entry: %w", err)
	}
	return filepath.ToSlash(historyPath), nil
}

type cdcHealthAlertSeverity int

const (
	cdcHealthAlertNone cdcHealthAlertSeverity = iota
	cdcHealthAlertOK
	cdcHealthAlertWarning
	cdcHealthAlertCritical
)

func parseCDCHealthAlertSeverity(value, flagName string) (cdcHealthAlertSeverity, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "critical":
		return cdcHealthAlertCritical, nil
	case "warning":
		return cdcHealthAlertWarning, nil
	case "ok", "always":
		return cdcHealthAlertOK, nil
	case "none", "disabled":
		return cdcHealthAlertNone, nil
	default:
		return cdcHealthAlertNone, fmt.Errorf("invalid %s %q", flagName, value)
	}
}

func shouldSendCDCHealthAlert(status string, min cdcHealthAlertSeverity) bool {
	if min == cdcHealthAlertNone {
		return false
	}
	return cdcHealthStatusRank(status) >= int(min)
}

func cdcHealthStatusRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "critical":
		return int(cdcHealthAlertCritical)
	case "warning":
		return int(cdcHealthAlertWarning)
	case "ok":
		return int(cdcHealthAlertOK)
	default:
		return 0
	}
}

func sendFeishuCDCHealthAlert(webhookURL, secret string, now time.Time, report gitops.CDCHealthReport) error {
	payload := map[string]any{
		"msg_type": "text",
		"content": map[string]string{
			"text": renderCDCHealthAlertText(report),
		},
	}
	if strings.TrimSpace(secret) != "" {
		timestamp := strconv.FormatInt(now.Unix(), 10)
		payload["timestamp"] = timestamp
		payload["sign"] = signFeishuCustomBotRequest(timestamp, strings.TrimSpace(secret))
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimSpace(webhookURL), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if err := checkFeishuWebhookResponse(respBody); err != nil {
		return err
	}
	return nil
}

func sendSlackCDCHealthAlert(webhookURL string, report gitops.CDCHealthReport) error {
	payload := map[string]string{
		"text": renderCDCHealthAlertText(report),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimSpace(webhookURL), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func signFeishuCustomBotRequest(timestamp, secret string) string {
	stringToSign := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func checkFeishuWebhookResponse(body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var response struct {
		Code          *int   `json:"code"`
		Msg           string `json:"msg"`
		StatusCode    *int   `json:"StatusCode"`
		StatusMessage string `json:"StatusMessage"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil
	}
	if response.Code != nil && *response.Code != 0 {
		return fmt.Errorf("webhook returned code %d: %s", *response.Code, response.Msg)
	}
	if response.StatusCode != nil && *response.StatusCode != 0 {
		return fmt.Errorf("webhook returned StatusCode %d: %s", *response.StatusCode, response.StatusMessage)
	}
	return nil
}

func renderCDCHealthAlertText(report gitops.CDCHealthReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sqlserver2tidb CDC health %s\n", report.Status)
	fmt.Fprintf(&b, "source_cluster_id: %s\n", report.SourceClusterID)
	fmt.Fprintf(&b, "project_id: %s\n", report.ProjectID)
	fmt.Fprintf(&b, "generated_at: %s\n", report.GeneratedAt)
	if report.MaxLSN != "" {
		fmt.Fprintf(&b, "max_lsn: %s\n", report.MaxLSN)
	}
	fmt.Fprintf(&b, "tracked_tables: %d, lagging_tables: %d, expired_tables: %d\n", report.TrackedTables, report.LaggingTables, report.ExpiredTables)
	if len(report.Alerts) == 0 {
		b.WriteString("alerts: none")
		return b.String()
	}
	b.WriteString("alerts:\n")
	for _, alert := range report.Alerts {
		if alert.SourceObject != "" {
			fmt.Fprintf(&b, "- %s %s %s: %s\n", alert.Severity, alert.Code, alert.SourceObject, alert.Message)
		} else {
			fmt.Fprintf(&b, "- %s %s: %s\n", alert.Severity, alert.Code, alert.Message)
		}
	}
	return strings.TrimSpace(b.String())
}

type cdcHealthMinLSNFlags map[string]string

func (flags *cdcHealthMinLSNFlags) String() string {
	if flags == nil || len(*flags) == 0 {
		return ""
	}
	parts := make([]string, 0, len(*flags))
	for sourceObject, minLSN := range *flags {
		parts = append(parts, sourceObject+"="+minLSN)
	}
	return strings.Join(parts, ",")
}

func (flags *cdcHealthMinLSNFlags) Set(value string) error {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return fmt.Errorf("min-lsn value is required")
	}
	index := strings.LastIndex(raw, "=")
	if index <= 0 || index == len(raw)-1 {
		return fmt.Errorf("min-lsn must use source.object=0x... format")
	}
	sourceObject := strings.TrimSpace(raw[:index])
	minLSN := strings.TrimSpace(raw[index+1:])
	if sourceObject == "" || minLSN == "" {
		return fmt.Errorf("min-lsn must use source.object=0x... format")
	}
	if *flags == nil {
		*flags = cdcHealthMinLSNFlags{}
	}
	(*flags)[sourceObject] = minLSN
	return nil
}

func (flags cdcHealthMinLSNFlags) Map() map[string]string {
	result := make(map[string]string, len(flags))
	for sourceObject, minLSN := range flags {
		result[sourceObject] = minLSN
	}
	return result
}

func renderCDCHealthText(stdout io.Writer, report gitops.CDCHealthReport) {
	fmt.Fprintf(stdout, "cdc health: %s\n", report.Status)
	fmt.Fprintf(stdout, "source cluster: %s\n", report.SourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", report.ProjectID)
	fmt.Fprintf(stdout, "checkpoint status: %s\n", report.CheckpointStatus)
	if report.CheckpointUpdatedAt != "" {
		fmt.Fprintf(stdout, "checkpoint updated_at: %s\n", report.CheckpointUpdatedAt)
	}
	if report.CheckpointAgeSeconds != nil {
		fmt.Fprintf(stdout, "checkpoint age seconds: %d\n", *report.CheckpointAgeSeconds)
	}
	if report.MaxLSN != "" {
		fmt.Fprintf(stdout, "max_lsn: %s\n", report.MaxLSN)
	}
	fmt.Fprintf(stdout, "tracked tables: %d\n", report.TrackedTables)
	fmt.Fprintf(stdout, "lagging tables: %d\n", report.LaggingTables)
	fmt.Fprintf(stdout, "expired tables: %d\n", report.ExpiredTables)
	if len(report.Alerts) > 0 {
		fmt.Fprintln(stdout, "alerts:")
		for _, alert := range report.Alerts {
			if alert.SourceObject != "" {
				fmt.Fprintf(stdout, "- %s %s %s: %s\n", alert.Severity, alert.Code, alert.SourceObject, alert.Message)
			} else {
				fmt.Fprintf(stdout, "- %s %s: %s\n", alert.Severity, alert.Code, alert.Message)
			}
		}
	}
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

func runSyncGitHubPRApproval(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync-github-pr-approval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "PR stage with an approval file: schema, export, import, cdc, validation, or cutover")
	prNumber := fs.Int("pr", 0, "GitHub pull request number")
	repo := fs.String("repo", "", "optional GitHub repository in owner/name form")
	ghBinary := fs.String("gh-binary", "gh", "GitHub CLI binary used to read PR status")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *prNumber <= 0 {
		fmt.Fprintln(stderr, "sync-github-pr-approval --pr must be positive")
		return 2
	}
	prStatus, err := readGitHubPRStatus(*root, *ghBinary, *repo, *prNumber)
	if err != nil {
		fmt.Fprintf(stderr, "sync GitHub PR approval: %v\n", err)
		return 1
	}
	inferred := inferGitHubPRMetadata(prStatus)
	effectiveSourceClusterID := firstNonEmpty(*sourceClusterID, inferred.SourceClusterID)
	effectiveProjectID := firstNonEmpty(*projectID, inferred.ProjectID)
	effectiveStage := firstNonEmpty(*stage, inferred.Stage)
	result, err := gitops.SyncGitHubPRApproval(*root, gitops.GitHubPRApprovalSyncSpec{
		SourceClusterID: effectiveSourceClusterID,
		ProjectID:       effectiveProjectID,
		Stage:           effectiveStage,
		PRNumber:        *prNumber,
		PRState:         prStatus.State,
		ReviewDecision:  prStatus.ReviewDecision,
		ChecksStatus:    deriveGitHubPRChecksStatus(prStatus.StatusCheckRollup),
		MergedAt:        prStatus.MergedAt,
		ApprovedBy:      githubPRApprovers(prStatus.LatestReviews),
		ChangedFiles:    githubPRChangedFiles(prStatus.Files),
	})
	if err != nil {
		fmt.Fprintf(stderr, "sync GitHub PR approval: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "GitHub PR approval synced for %s\n", result.ApprovalStage)
	fmt.Fprintf(stdout, "PR: #%d\n", result.PRNumber)
	fmt.Fprintf(stdout, "approval file: %s\n", result.ApprovalFile)
	fmt.Fprintf(stdout, "payload hash: %s\n", result.PayloadHash)
	fmt.Fprintf(stdout, "approved by: %s\n", strings.Join(result.ApprovedBy, ", "))
	return 0
}

type githubPRViewStatus struct {
	Title             string                `json:"title"`
	Body              string                `json:"body"`
	State             string                `json:"state"`
	ReviewDecision    string                `json:"reviewDecision"`
	MergedAt          string                `json:"mergedAt"`
	LatestReviews     []githubPRReview      `json:"latestReviews"`
	StatusCheckRollup []githubPRStatusCheck `json:"statusCheckRollup"`
	Files             []githubPRFile        `json:"files"`
}

type githubPRReview struct {
	State  string `json:"state"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
}

type githubPRStatusCheck struct {
	State      string `json:"state"`
	Conclusion string `json:"conclusion"`
}

type githubPRFile struct {
	Path string `json:"path"`
}

func readGitHubPRStatus(root, ghBinary, repo string, prNumber int) (githubPRViewStatus, error) {
	binary := strings.TrimSpace(ghBinary)
	if binary == "" {
		binary = "gh"
	}
	args := []string{
		"pr", "view", strconv.Itoa(prNumber),
		"--json", "title,body,state,reviewDecision,mergedAt,latestReviews,statusCheckRollup,files",
	}
	if strings.TrimSpace(repo) != "" {
		args = append(args, "--repo", strings.TrimSpace(repo))
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return githubPRViewStatus{}, fmt.Errorf("gh pr view failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var status githubPRViewStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return githubPRViewStatus{}, fmt.Errorf("parse gh pr view JSON: %w", err)
	}
	return status, nil
}

type inferredGitHubPRMetadata struct {
	SourceClusterID string
	ProjectID       string
	Stage           string
}

func inferGitHubPRMetadata(status githubPRViewStatus) inferredGitHubPRMetadata {
	inferred := inferredGitHubPRMetadata{}
	if stage, projectID, ok := parseGitHubPRTitle(status.Title); ok {
		inferred.Stage = stage
		inferred.ProjectID = projectID
	}
	for _, line := range strings.Split(status.Body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "- Stage:"):
			inferred.Stage = firstNonEmpty(extractBacktickValue(trimmed), inferred.Stage)
		case strings.HasPrefix(trimmed, "- Source cluster:"):
			inferred.SourceClusterID = firstNonEmpty(extractBacktickValue(trimmed), inferred.SourceClusterID)
		case strings.HasPrefix(trimmed, "- Project:"):
			value := extractBacktickValue(trimmed)
			if value != "" && value != "cluster-level" {
				inferred.ProjectID = firstNonEmpty(value, inferred.ProjectID)
			}
		}
	}
	return inferred
}

func parseGitHubPRTitle(title string) (string, string, bool) {
	trimmed := strings.TrimSpace(title)
	if !strings.HasPrefix(trimmed, "[") {
		return "", "", false
	}
	end := strings.Index(trimmed, "]")
	if end <= 1 || end+1 >= len(trimmed) {
		return "", "", false
	}
	stage := strings.TrimSpace(trimmed[1:end])
	projectID := strings.TrimSpace(trimmed[end+1:])
	if stage == "" || projectID == "" {
		return "", "", false
	}
	return stage, projectID, true
}

func extractBacktickValue(line string) string {
	start := strings.Index(line, "`")
	if start < 0 {
		return ""
	}
	end := strings.Index(line[start+1:], "`")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(line[start+1 : start+1+end])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func deriveGitHubPRChecksStatus(checks []githubPRStatusCheck) string {
	for _, check := range checks {
		conclusion := strings.ToUpper(strings.TrimSpace(check.Conclusion))
		switch conclusion {
		case "SUCCESS", "SKIPPED", "NEUTRAL":
			continue
		case "FAILURE", "FAILED", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED":
			return "FAILED"
		}
		state := strings.ToUpper(strings.TrimSpace(check.State))
		switch state {
		case "", "SUCCESS":
			continue
		case "FAILURE", "FAILED", "ERROR":
			return "FAILED"
		default:
			return "PENDING"
		}
	}
	return "PASSED"
}

func githubPRApprovers(reviews []githubPRReview) []string {
	seen := map[string]bool{}
	approvers := make([]string, 0, len(reviews))
	for _, review := range reviews {
		if strings.ToUpper(strings.TrimSpace(review.State)) != "APPROVED" {
			continue
		}
		login := strings.TrimSpace(review.Author.Login)
		if login == "" || seen[login] {
			continue
		}
		seen[login] = true
		approvers = append(approvers, login)
	}
	return approvers
}

func githubPRChangedFiles(files []githubPRFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path != "" {
			out = append(out, path)
		}
	}
	return out
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
	stage := fs.String("stage", "", "executor evidence PR stage: ddl, export, import, cdc-enable, cdc, validation")
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
	stage := fs.String("stage", "", "executor evidence PR stage: ddl, export, import, cdc-enable, cdc, validation")
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

func runWorkerCutover(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-cutover", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.RunCutoverWorker(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "worker cutover: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cutover worker completed for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", result.Status)
	fmt.Fprintf(stdout, "gates: %d\n", result.Items)
	fmt.Fprintf(stdout, "payload hash: %s\n", result.PayloadHash)
	fmt.Fprintf(stdout, "wrote %s\n", result.StateFile)
	fmt.Fprintf(stdout, "wrote %s\n", result.EvidenceFile)
	fmt.Fprintf(stdout, "wrote %s\n", "evidence/post-cutover-report.md")
	return 0
}

func runWorkerExecutor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-executor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "executor stage: ddl, export, import, cdc-enable, cdc, or validation")
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
			dataRows, dataBytes, errDataMetrics := workerExecutorDataMetrics(spec.Stage, string(output))
			if errParse == nil {
				errParse = errDataMetrics
			}
			dataSHA256, errDataSHA256 := workerExecutorDataSHA256(spec.Stage, string(output))
			if errParse == nil {
				errParse = errDataSHA256
			}
			if errParse == nil {
				errParse = workerExecutorRequiredDataAuditError(spec.Stage, args, dataRows, dataBytes, dataSHA256)
			}
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
				DataRows:          dataRows,
				DataBytes:         dataBytes,
				DataSHA256:        dataSHA256,
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
	if workerExecutorCommandRequiresDataAudit(stage, expectedArgs) && !workerExecutorCommandHasDataAudit(command) {
		return false
	}
	return true
}

func workerExecutorCommandRequiresDataAudit(stage string, args []string) bool {
	switch stage {
	case "export":
		return true
	case "import":
		switch workerExecutorImportEngine(args) {
		case "sql-insert":
			return true
		case "tidb-import-into":
			return workerExecutorImportSourceNeedsLocalAudit(workerExecutorArgValue(args, "--source-uri"))
		case "tidb-lightning":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func workerExecutorImportEngine(args []string) string {
	engine := "sql-insert"
	if value := workerExecutorArgValue(args, "--engine"); value != "" {
		engine = value
	}
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "", "sql-insert":
		return "sql-insert"
	case "tidb-import-into", "import-into":
		return "tidb-import-into"
	case "tidb-lightning", "lightning":
		return "tidb-lightning"
	default:
		return strings.ToLower(strings.TrimSpace(engine))
	}
}

func workerExecutorArgValue(args []string, flagName string) string {
	for i, arg := range args {
		if arg == flagName && i+1 < len(args) {
			return strings.TrimSpace(args[i+1])
		}
		if strings.HasPrefix(arg, flagName+"=") {
			return strings.TrimSpace(strings.TrimPrefix(arg, flagName+"="))
		}
	}
	return ""
}

func workerExecutorImportSourceNeedsLocalAudit(sourceURI string) bool {
	sourceURI = strings.TrimSpace(sourceURI)
	if sourceURI == "" {
		return true
	}
	parsed, err := url.Parse(sourceURI)
	if err != nil {
		return true
	}
	switch parsed.Scheme {
	case "", "file", "s3", "gs":
		return true
	default:
		return true
	}
}

func workerExecutorCommandHasDataAudit(command workerExecutorRunCommandEvidence) bool {
	if command.DataRows == nil || *command.DataRows < 0 {
		return false
	}
	if command.DataBytes == nil || *command.DataBytes < 0 {
		return false
	}
	return isWorkerExecutorSHA256(strings.TrimSpace(command.DataSHA256))
}

func workerExecutorRequiredDataAuditError(stage string, args []string, dataRows, dataBytes *int64, dataSHA256 string) error {
	if !workerExecutorCommandRequiresDataAudit(stage, args) {
		return nil
	}
	if dataRows != nil && dataBytes != nil && isWorkerExecutorSHA256(strings.TrimSpace(dataSHA256)) {
		return nil
	}
	switch stage {
	case "export":
		return fmt.Errorf("export executor output must include exported rows: N, output bytes: N, and output sha256: sha256:<digest>")
	case "import":
		return fmt.Errorf("import executor output must include imported rows: N, input bytes: N, and input sha256: sha256:<digest>")
	default:
		return fmt.Errorf("%s executor output must include data_rows, data_bytes, and data_sha256", stage)
	}
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
	DataRows          *int64                                    `json:"data_rows,omitempty"`
	DataBytes         *int64                                    `json:"data_bytes,omitempty"`
	DataSHA256        string                                    `json:"data_sha256,omitempty"`
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

func workerExecutorDataMetrics(stage, output string) (*int64, *int64, error) {
	switch stage {
	case "export":
		return parseWorkerExecutorDataMetrics(stage, output, "exported rows:", "output bytes:")
	case "import":
		return parseWorkerExecutorDataMetrics(stage, output, "imported rows:", "input bytes:")
	default:
		return nil, nil, nil
	}
}

func parseWorkerExecutorDataMetrics(stage, output, rowsPrefix, bytesPrefix string) (*int64, *int64, error) {
	var dataRows *int64
	var dataBytes *int64
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, rowsPrefix) {
			value, err := parseWorkerExecutorNonNegativeInt64Metric(line, rowsPrefix)
			if err != nil {
				return nil, nil, fmt.Errorf("%s executor output metric %q must contain a non-negative integer", stage, rowsPrefix)
			}
			dataRows = &value
			continue
		}
		if strings.HasPrefix(line, bytesPrefix) {
			value, err := parseWorkerExecutorNonNegativeInt64Metric(line, bytesPrefix)
			if err != nil {
				return nil, nil, fmt.Errorf("%s executor output metric %q must contain a non-negative integer", stage, bytesPrefix)
			}
			dataBytes = &value
		}
	}
	if (dataRows == nil) != (dataBytes == nil) {
		return nil, nil, fmt.Errorf("%s executor output must include both %s N and %s N when data metrics are present", stage, rowsPrefix, bytesPrefix)
	}
	return dataRows, dataBytes, nil
}

func parseWorkerExecutorNonNegativeInt64Metric(line, prefix string) (int64, error) {
	value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid non-negative integer")
	}
	return parsed, nil
}

func workerExecutorDataSHA256(stage, output string) (string, error) {
	switch stage {
	case "export":
		return parseWorkerExecutorDataSHA256(stage, output, "output sha256:")
	case "import":
		return parseWorkerExecutorDataSHA256(stage, output, "input sha256:")
	default:
		return "", nil
	}
}

func parseWorkerExecutorDataSHA256(stage, output, prefix string) (string, error) {
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if !isWorkerExecutorSHA256(value) {
			return "", fmt.Errorf("%s executor output metric %q must contain sha256:<64 hex chars>", stage, prefix)
		}
		return value, nil
	}
	return "", nil
}

func isWorkerExecutorSHA256(value string) bool {
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
  sqlserver2tidb generate-data-plans --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --object-uri-prefix https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full --compression gzip
  sqlserver2tidb generate-cdc-plan --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb prepare-cdc-range --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --to-lsn 0x00000027000001f40003
  sqlserver2tidb prepare-cdc-iteration --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --max-lsn 0x00000027000001f40004 --pr-draft
  sqlserver2tidb cdc-orchestrator --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --apply-approved --poll --pr-draft
  sqlserver2tidb cdc-health --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --probe-lsn --max-checkpoint-age 15m --metrics-file artifacts/cdc-health.json --history-file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/cdc-health-history.jsonl
  sqlserver2tidb generate-validation-plan --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb generate-pr-draft --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage schema
  sqlserver2tidb create-pr --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage schema
  sqlserver2tidb sync-github-pr-approval --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export --pr 42 --repo BornChanger/sqlserver2tidb
  sqlserver2tidb create-worker-state-pr --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export
  sqlserver2tidb generate-executor-evidence-pr-draft --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage ddl
  sqlserver2tidb create-executor-evidence-pr --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage ddl
  sqlserver2tidb compute-payload-hash --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export
  sqlserver2tidb worker-export --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-import --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-cdc --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-validate --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-cutover --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-executor --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export
  sqlserver2tidb worker-executor --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage import --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING --import-batch-size 1000
  sqlserver2tidb worker-executor --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage cdc-enable --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING
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
