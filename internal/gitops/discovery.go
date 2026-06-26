package gitops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type SQLServerDiscoveryDryRunPlan struct {
	SourceClusterID string
	Mode            string
	WritesFiles     bool
	TargetFiles     []string
	CatalogQueries  []string
}

type SQLServerCatalogReader interface {
	DiscoverSQLServerCatalog(ctx context.Context) (SQLServerCatalogSnapshot, error)
}

type SQLServerCatalogSnapshot struct {
	Inventory  SQLServerInventory
	SourceDDLs map[string]string
}

type SQLServerDiscoveryResult struct {
	SourceClusterID string
	InventoryFile   string
	Databases       int
	Tables          int
	Columns         int
	SourceDDLs      int
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

func ExecuteSQLServerDiscovery(ctx context.Context, root, sourceClusterID string, reader SQLServerCatalogReader) (SQLServerDiscoveryResult, error) {
	if reader == nil {
		return SQLServerDiscoveryResult{}, fmt.Errorf("catalog reader is required")
	}
	if !idPattern.MatchString(sourceClusterID) {
		return SQLServerDiscoveryResult{}, fmt.Errorf("invalid source cluster id %q", sourceClusterID)
	}
	clusterDir := filepath.Join(root, "clusters", sourceClusterID)
	if info, err := os.Stat(clusterDir); err != nil || !info.IsDir() {
		return SQLServerDiscoveryResult{}, fmt.Errorf("source cluster %q does not exist", sourceClusterID)
	}
	if info, err := os.Stat(filepath.Join(clusterDir, "cluster.yaml")); err != nil || info.IsDir() {
		return SQLServerDiscoveryResult{}, fmt.Errorf("source cluster %q is missing cluster.yaml", sourceClusterID)
	}

	snapshot, err := reader.DiscoverSQLServerCatalog(ctx)
	if err != nil {
		return SQLServerDiscoveryResult{}, fmt.Errorf("discover SQL Server catalog: %w", err)
	}
	if strings.TrimSpace(snapshot.Inventory.Status) == "" {
		snapshot.Inventory.Status = "discovered"
	}

	inventoryRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "inventory", "inventory.json"))
	inventoryPath := filepath.Join(root, filepath.FromSlash(inventoryRel))
	data, err := json.MarshalIndent(snapshot.Inventory, "", "  ")
	if err != nil {
		return SQLServerDiscoveryResult{}, fmt.Errorf("marshal inventory: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(inventoryPath, data, 0o644); err != nil {
		return SQLServerDiscoveryResult{}, fmt.Errorf("write inventory: %w", err)
	}

	sourceDDLDir := filepath.Join(root, "clusters", sourceClusterID, "inventory", "source-ddl")
	if err := os.MkdirAll(sourceDDLDir, 0o755); err != nil {
		return SQLServerDiscoveryResult{}, fmt.Errorf("create source ddl dir: %w", err)
	}
	for name, ddl := range snapshot.SourceDDLs {
		path := filepath.Join(sourceDDLDir, safeSQLFileName(name)+".sql")
		if err := os.WriteFile(path, []byte(ensureTrailingNewline(ddl)), 0o644); err != nil {
			return SQLServerDiscoveryResult{}, fmt.Errorf("write source ddl %s: %w", name, err)
		}
	}

	result := SQLServerDiscoveryResult{
		SourceClusterID: sourceClusterID,
		InventoryFile:   inventoryRel,
		SourceDDLs:      len(snapshot.SourceDDLs),
	}
	for _, database := range snapshot.Inventory.Databases {
		result.Databases++
		for _, schema := range database.Schemas {
			result.Tables += len(schema.Tables)
			for _, table := range schema.Tables {
				result.Columns += len(table.Columns)
			}
		}
	}
	return result, nil
}

var unsafeSQLFileNameChars = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func safeSQLFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "object"
	}
	return unsafeSQLFileNameChars.ReplaceAllString(name, "_")
}

func ensureTrailingNewline(value string) string {
	if strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}
