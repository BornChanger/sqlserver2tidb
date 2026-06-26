package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/BornChanger/sqlserver2tidb/internal/gitops"
	_ "github.com/microsoft/go-mssqldb"
)

type CatalogReader struct {
	db *sql.DB
}

func NewCatalogReader(connectionString string) (*CatalogReader, error) {
	if strings.TrimSpace(connectionString) == "" {
		return nil, fmt.Errorf("connection string is required")
	}
	db, err := sql.Open("sqlserver", connectionString)
	if err != nil {
		return nil, err
	}
	return &CatalogReader{db: db}, nil
}

func (reader *CatalogReader) Close() error {
	if reader == nil || reader.db == nil {
		return nil
	}
	return reader.db.Close()
}

func (reader *CatalogReader) DiscoverSQLServerCatalog(ctx context.Context) (gitops.SQLServerCatalogSnapshot, error) {
	databaseName, err := reader.currentDatabase(ctx)
	if err != nil {
		return gitops.SQLServerCatalogSnapshot{}, err
	}

	acc := newInventoryAccumulator(databaseName)
	if err := reader.loadTables(ctx, acc); err != nil {
		return gitops.SQLServerCatalogSnapshot{}, err
	}
	if err := reader.loadColumns(ctx, acc); err != nil {
		return gitops.SQLServerCatalogSnapshot{}, err
	}
	if err := reader.loadIndexes(ctx, acc); err != nil {
		return gitops.SQLServerCatalogSnapshot{}, err
	}
	if err := reader.loadTriggers(ctx, acc); err != nil {
		return gitops.SQLServerCatalogSnapshot{}, err
	}
	if err := reader.loadRoutines(ctx, acc); err != nil {
		return gitops.SQLServerCatalogSnapshot{}, err
	}

	return gitops.SQLServerCatalogSnapshot{
		Inventory: acc.inventory(),
	}, nil
}

func (reader *CatalogReader) currentDatabase(ctx context.Context) (string, error) {
	var name string
	if err := reader.db.QueryRowContext(ctx, "SELECT DB_NAME()").Scan(&name); err != nil {
		return "", fmt.Errorf("query current database: %w", err)
	}
	return name, nil
}

func (reader *CatalogReader) loadTables(ctx context.Context, acc *inventoryAccumulator) error {
	rows, err := reader.db.QueryContext(ctx, `
SELECT s.name AS schema_name, t.name AS table_name, COALESCE(SUM(p.rows), 0) AS row_count
FROM sys.tables AS t
JOIN sys.schemas AS s ON s.schema_id = t.schema_id
LEFT JOIN sys.partitions AS p ON p.object_id = t.object_id AND p.index_id IN (0, 1)
WHERE t.is_ms_shipped = 0
GROUP BY s.name, t.name
ORDER BY s.name, t.name`)
	if err != nil {
		return fmt.Errorf("query tables: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schemaName, tableName string
		var rowCount int64
		if err := rows.Scan(&schemaName, &tableName, &rowCount); err != nil {
			return fmt.Errorf("scan table: %w", err)
		}
		table := acc.ensureTable(schemaName, tableName)
		table.RowCount = rowCount
	}
	return rows.Err()
}

func (reader *CatalogReader) loadColumns(ctx context.Context, acc *inventoryAccumulator) error {
	rows, err := reader.db.QueryContext(ctx, `
SELECT s.name AS schema_name, t.name AS table_name, c.name AS column_name, typ.name AS type_name,
       c.is_identity, c.is_computed
FROM sys.columns AS c
JOIN sys.tables AS t ON t.object_id = c.object_id
JOIN sys.schemas AS s ON s.schema_id = t.schema_id
JOIN sys.types AS typ ON typ.user_type_id = c.user_type_id
WHERE t.is_ms_shipped = 0
ORDER BY s.name, t.name, c.column_id`)
	if err != nil {
		return fmt.Errorf("query columns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schemaName, tableName, columnName, typeName string
		var identity, computed bool
		if err := rows.Scan(&schemaName, &tableName, &columnName, &typeName, &identity, &computed); err != nil {
			return fmt.Errorf("scan column: %w", err)
		}
		table := acc.ensureTable(schemaName, tableName)
		table.Columns = append(table.Columns, gitops.SQLServerColumn{
			Name:     columnName,
			Type:     typeName,
			Identity: identity,
			Computed: computed,
		})
	}
	return rows.Err()
}

func (reader *CatalogReader) loadIndexes(ctx context.Context, acc *inventoryAccumulator) error {
	rows, err := reader.db.QueryContext(ctx, `
SELECT s.name AS schema_name, t.name AS table_name, i.name AS index_name, i.has_filter,
       c.name AS column_name, ic.is_included_column
FROM sys.indexes AS i
JOIN sys.tables AS t ON t.object_id = i.object_id
JOIN sys.schemas AS s ON s.schema_id = t.schema_id
LEFT JOIN sys.index_columns AS ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
LEFT JOIN sys.columns AS c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
WHERE t.is_ms_shipped = 0 AND i.name IS NOT NULL AND i.is_hypothetical = 0
ORDER BY s.name, t.name, i.name, ic.key_ordinal, ic.index_column_id`)
	if err != nil {
		return fmt.Errorf("query indexes: %w", err)
	}
	defer rows.Close()

	indexes := map[string]*gitops.SQLServerIndex{}
	for rows.Next() {
		var schemaName, tableName, indexName string
		var hasFilter bool
		var columnName sql.NullString
		var included bool
		if err := rows.Scan(&schemaName, &tableName, &indexName, &hasFilter, &columnName, &included); err != nil {
			return fmt.Errorf("scan index: %w", err)
		}
		table := acc.ensureTable(schemaName, tableName)
		key := schemaName + "." + tableName + "." + indexName
		index := indexes[key]
		if index == nil {
			table.Indexes = append(table.Indexes, gitops.SQLServerIndex{Name: indexName, Filtered: hasFilter})
			index = &table.Indexes[len(table.Indexes)-1]
			indexes[key] = index
		}
		if included && columnName.Valid {
			index.IncludedColumns = append(index.IncludedColumns, columnName.String)
		}
	}
	return rows.Err()
}

func (reader *CatalogReader) loadTriggers(ctx context.Context, acc *inventoryAccumulator) error {
	rows, err := reader.db.QueryContext(ctx, `
SELECT s.name AS schema_name, t.name AS table_name, tr.name AS trigger_name
FROM sys.triggers AS tr
JOIN sys.tables AS t ON t.object_id = tr.parent_id
JOIN sys.schemas AS s ON s.schema_id = t.schema_id
WHERE t.is_ms_shipped = 0
ORDER BY s.name, t.name, tr.name`)
	if err != nil {
		return fmt.Errorf("query triggers: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schemaName, tableName, triggerName string
		if err := rows.Scan(&schemaName, &tableName, &triggerName); err != nil {
			return fmt.Errorf("scan trigger: %w", err)
		}
		table := acc.ensureTable(schemaName, tableName)
		table.Triggers = append(table.Triggers, gitops.SQLServerDBObject{Name: triggerName})
	}
	return rows.Err()
}

func (reader *CatalogReader) loadRoutines(ctx context.Context, acc *inventoryAccumulator) error {
	rows, err := reader.db.QueryContext(ctx, `
SELECT s.name AS schema_name, o.name AS routine_name, o.type_desc
FROM sys.objects AS o
JOIN sys.schemas AS s ON s.schema_id = o.schema_id
WHERE o.type IN ('P', 'FN', 'IF', 'TF') AND o.is_ms_shipped = 0
ORDER BY s.name, o.name`)
	if err != nil {
		return fmt.Errorf("query routines: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schemaName, routineName, routineType string
		if err := rows.Scan(&schemaName, &routineName, &routineType); err != nil {
			return fmt.Errorf("scan routine: %w", err)
		}
		schema := acc.ensureSchema(schemaName)
		schema.routines = append(schema.routines, gitops.SQLServerRoutine{Name: routineName, Type: routineType})
	}
	return rows.Err()
}

type inventoryAccumulator struct {
	databaseName string
	schemas      map[string]*schemaAccumulator
}

type schemaAccumulator struct {
	name     string
	tables   map[string]*gitops.SQLServerTable
	routines []gitops.SQLServerRoutine
}

func newInventoryAccumulator(databaseName string) *inventoryAccumulator {
	return &inventoryAccumulator{
		databaseName: databaseName,
		schemas:      map[string]*schemaAccumulator{},
	}
}

func (acc *inventoryAccumulator) ensureSchema(schemaName string) *schemaAccumulator {
	schema := acc.schemas[schemaName]
	if schema == nil {
		schema = &schemaAccumulator{name: schemaName, tables: map[string]*gitops.SQLServerTable{}}
		acc.schemas[schemaName] = schema
	}
	return schema
}

func (acc *inventoryAccumulator) ensureTable(schemaName, tableName string) *gitops.SQLServerTable {
	schema := acc.ensureSchema(schemaName)
	table := schema.tables[tableName]
	if table == nil {
		table = &gitops.SQLServerTable{Name: tableName}
		schema.tables[tableName] = table
	}
	return table
}

func (acc *inventoryAccumulator) inventory() gitops.SQLServerInventory {
	schemaNames := make([]string, 0, len(acc.schemas))
	for schemaName := range acc.schemas {
		schemaNames = append(schemaNames, schemaName)
	}
	sort.Strings(schemaNames)

	database := gitops.SQLServerDatabase{Name: acc.databaseName}
	for _, schemaName := range schemaNames {
		schemaAcc := acc.schemas[schemaName]
		schema := gitops.SQLServerSchema{Name: schemaAcc.name, Routines: schemaAcc.routines}
		tableNames := make([]string, 0, len(schemaAcc.tables))
		for tableName := range schemaAcc.tables {
			tableNames = append(tableNames, tableName)
		}
		sort.Strings(tableNames)
		for _, tableName := range tableNames {
			schema.Tables = append(schema.Tables, *schemaAcc.tables[tableName])
		}
		database.Schemas = append(database.Schemas, schema)
	}

	return gitops.SQLServerInventory{
		Status:    "discovered",
		Databases: []gitops.SQLServerDatabase{database},
	}
}
