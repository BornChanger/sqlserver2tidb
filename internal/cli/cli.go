package cli

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/BornChanger/sqlserver2tidb/internal/gitops"
)

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "init-repo":
		return runInitRepo(args[1:], stdout, stderr)
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
  sqlserver2tidb create-cluster --cluster-id prod-sqlserver-a --display-name "prod SQL Server A" --listener sqlserver-a.internal --secret-ref vault://...
  sqlserver2tidb create-project --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --source-database sales --source-schema dbo --target-name tidb-prod-a --target-database app --target-secret-ref vault://...
`)
}
