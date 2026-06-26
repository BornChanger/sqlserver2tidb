package executor

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "export":
		return runExport(args[1:], stdout, stderr)
	case "import":
		return runImport(args[1:], stdout, stderr)
	case "cdc":
		return runCDC(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown executor command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	chunkID := fs.String("chunk-id", "", "export chunk id")
	sourceObject := fs.String("source-object", "", "source object")
	targetObject := fs.String("target-object", "", "target object")
	outputURI := fs.String("output-uri", "", "export output URI")
	predicate := fs.String("predicate", "", "source split predicate")
	execute := fs.Bool("execute", false, "perform the export; not implemented in this adapter")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *execute {
		fmt.Fprintln(stderr, "executor export: --execute is not implemented yet")
		return 1
	}
	if err := requireFields("executor export",
		field{"source cluster id", *sourceClusterID},
		field{"project id", *projectID},
		field{"chunk id", *chunkID},
		field{"source object", *sourceObject},
		field{"target object", *targetObject},
		field{"output uri", *outputURI},
	); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	fmt.Fprintln(stdout, "executor export dry run")
	fmt.Fprintf(stdout, "root: %s\n", *root)
	fmt.Fprintf(stdout, "source cluster: %s\n", *sourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", *projectID)
	fmt.Fprintf(stdout, "chunk id: %s\n", *chunkID)
	fmt.Fprintf(stdout, "source object: %s\n", *sourceObject)
	fmt.Fprintf(stdout, "target object: %s\n", *targetObject)
	fmt.Fprintf(stdout, "output uri: %s\n", *outputURI)
	if strings.TrimSpace(*predicate) != "" {
		fmt.Fprintf(stdout, "predicate: %s\n", *predicate)
	}
	fmt.Fprintln(stdout, "No SQL Server connection will be opened.")
	fmt.Fprintln(stdout, "No object storage write will be attempted.")
	return 0
}

func runImport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	jobID := fs.String("job-id", "", "import job id")
	targetObject := fs.String("target-object", "", "target object")
	sourceURI := fs.String("source-uri", "", "import source URI")
	dependsOnExportChunk := fs.String("depends-on-export-chunk", "", "upstream export chunk id")
	execute := fs.Bool("execute", false, "perform the import; not implemented in this adapter")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *execute {
		fmt.Fprintln(stderr, "executor import: --execute is not implemented yet")
		return 1
	}
	if err := requireFields("executor import",
		field{"source cluster id", *sourceClusterID},
		field{"project id", *projectID},
		field{"job id", *jobID},
		field{"target object", *targetObject},
		field{"source uri", *sourceURI},
	); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	fmt.Fprintln(stdout, "executor import dry run")
	fmt.Fprintf(stdout, "root: %s\n", *root)
	fmt.Fprintf(stdout, "source cluster: %s\n", *sourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", *projectID)
	fmt.Fprintf(stdout, "job id: %s\n", *jobID)
	fmt.Fprintf(stdout, "target object: %s\n", *targetObject)
	fmt.Fprintf(stdout, "source uri: %s\n", *sourceURI)
	if strings.TrimSpace(*dependsOnExportChunk) != "" {
		fmt.Fprintf(stdout, "depends on export chunk: %s\n", *dependsOnExportChunk)
	}
	fmt.Fprintln(stdout, "No TiDB connection will be opened.")
	return 0
}

func runCDC(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cdc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	sourceObject := fs.String("source-object", "", "source object")
	targetObject := fs.String("target-object", "", "target object")
	applyBatchSize := fs.Int("apply-batch-size", 0, "apply batch size")
	execute := fs.Bool("execute", false, "perform CDC apply; not implemented in this adapter")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *execute {
		fmt.Fprintln(stderr, "executor cdc: --execute is not implemented yet")
		return 1
	}
	if err := requireFields("executor cdc",
		field{"source cluster id", *sourceClusterID},
		field{"project id", *projectID},
		field{"source object", *sourceObject},
		field{"target object", *targetObject},
	); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *applyBatchSize <= 0 {
		fmt.Fprintln(stderr, "executor cdc: apply batch size must be positive")
		return 1
	}

	fmt.Fprintln(stdout, "executor cdc dry run")
	fmt.Fprintf(stdout, "root: %s\n", *root)
	fmt.Fprintf(stdout, "source cluster: %s\n", *sourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", *projectID)
	fmt.Fprintf(stdout, "source object: %s\n", *sourceObject)
	fmt.Fprintf(stdout, "target object: %s\n", *targetObject)
	fmt.Fprintf(stdout, "apply batch size: %s\n", strconv.Itoa(*applyBatchSize))
	fmt.Fprintln(stdout, "No CDC reader or TiDB apply worker will be started.")
	return 0
}

type field struct {
	name  string
	value string
}

func requireFields(prefix string, fields ...field) error {
	for _, f := range fields {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("%s: %s is required", prefix, f.name)
		}
	}
	return nil
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `sqlserver2tidb-executor executes reviewed migration work items.

Usage:
  sqlserver2tidb-executor export --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --chunk-id dbo.orders.000001 --source-object sales.dbo.orders --target-object app.orders --output-uri s3://bucket/path.parquet
  sqlserver2tidb-executor import --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --job-id import-dbo.orders.000001 --target-object app.orders --source-uri s3://bucket/path.parquet
  sqlserver2tidb-executor cdc --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --source-object sales.dbo.orders --target-object app.orders --apply-batch-size 1000
`)
}
