package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/BornChanger/sqlserver2tidb/internal/gitops"
	sqlservercatalog "github.com/BornChanger/sqlserver2tidb/internal/sqlserver"
)

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "init-repo":
		return runInitRepo(args[1:], stdout, stderr)
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
	case "generate-pr-draft":
		return runGeneratePRDraft(args[1:], stdout, stderr)
	case "create-pr":
		return runCreatePR(args[1:], stdout, stderr)
	case "compute-payload-hash":
		return runComputePayloadHash(args[1:], stdout, stderr)
	case "worker-validate":
		return runWorkerValidate(args[1:], stdout, stderr)
	case "create-cluster":
		return runCreateCluster(args[1:], stdout, stderr)
	case "create-project":
		return runCreateProject(args[1:], stdout, stderr)
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
	objectURIPrefix := fs.String("object-uri-prefix", "", "object storage URI prefix for exported full-load files")
	chunkSizeRows := fs.Int64("chunk-size-rows", 1000000, "estimated rows per export chunk")
	exportFormat := fs.String("export-format", "parquet", "export file format")
	importEngine := fs.String("import-engine", "import-into", "TiDB import engine")
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

func runComputePayloadHash(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compute-payload-hash", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "stage to hash; currently supports validation")
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
  sqlserver2tidb init-repo --root .
  sqlserver2tidb validate-repo --root .
  sqlserver2tidb discover-sqlserver --root . --source-cluster-id prod-sqlserver-a --dry-run
  sqlserver2tidb discover-sqlserver --root . --source-cluster-id prod-sqlserver-a --connection-string-env SQLSERVER2TIDB_SQLSERVER_DSN
  sqlserver2tidb analyze-compatibility --root . --source-cluster-id prod-sqlserver-a
  sqlserver2tidb generate-schema-draft --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb generate-data-plans --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --object-uri-prefix s3://bucket/prefix
  sqlserver2tidb generate-pr-draft --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage schema
  sqlserver2tidb create-pr --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage schema
  sqlserver2tidb compute-payload-hash --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage validation
  sqlserver2tidb worker-validate --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb create-cluster --cluster-id prod-sqlserver-a --display-name "prod SQL Server A" --listener sqlserver-a.internal --secret-ref vault://...
  sqlserver2tidb create-project --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --source-database sales --source-schema dbo --target-name tidb-prod-a --target-database app --target-secret-ref vault://...
`)
}
