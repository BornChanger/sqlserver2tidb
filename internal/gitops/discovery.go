package gitops

import (
	"fmt"
	"os"
	"path/filepath"
)

type SQLServerDiscoveryDryRunPlan struct {
	SourceClusterID string
	Mode            string
	WritesFiles     bool
	TargetFiles     []string
	CatalogQueries  []string
}

func BuildSQLServerDiscoveryDryRunPlan(root, sourceClusterID string) (SQLServerDiscoveryDryRunPlan, error) {
	if !idPattern.MatchString(sourceClusterID) {
		return SQLServerDiscoveryDryRunPlan{}, fmt.Errorf("invalid source cluster id %q", sourceClusterID)
	}

	clusterDir := filepath.Join(root, "clusters", sourceClusterID)
	if info, err := os.Stat(clusterDir); err != nil || !info.IsDir() {
		return SQLServerDiscoveryDryRunPlan{}, fmt.Errorf("source cluster %q does not exist", sourceClusterID)
	}
	clusterFile := filepath.Join(clusterDir, "cluster.yaml")
	if info, err := os.Stat(clusterFile); err != nil || info.IsDir() {
		return SQLServerDiscoveryDryRunPlan{}, fmt.Errorf("source cluster %q is missing cluster.yaml", sourceClusterID)
	}

	inventoryBase := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "inventory"))
	return SQLServerDiscoveryDryRunPlan{
		SourceClusterID: sourceClusterID,
		Mode:            "dry-run",
		WritesFiles:     false,
		TargetFiles: []string{
			filepath.ToSlash(filepath.Join(inventoryBase, "inventory.json")),
			filepath.ToSlash(filepath.Join(inventoryBase, "compatibility-report.md")),
			filepath.ToSlash(filepath.Join(inventoryBase, "schema-issues.yaml")),
			filepath.ToSlash(filepath.Join(inventoryBase, "source-ddl")),
		},
		CatalogQueries: []string{
			"databases: sys.databases",
			"schemas: sys.schemas",
			"tables: sys.tables + sys.schemas + sys.partitions",
			"columns: sys.columns + sys.types",
			"indexes: sys.indexes + sys.index_columns + sys.columns",
			"constraints: sys.key_constraints + sys.foreign_keys + sys.check_constraints + sys.default_constraints",
			"routines: sys.objects + sys.sql_modules",
			"cdc: cdc.change_tables + sys.databases",
		},
	}, nil
}
