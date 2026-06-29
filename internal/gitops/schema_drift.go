package gitops

import (
	"fmt"
	"path/filepath"
	"strings"
)

func requireCDCPlanMatchesInventory(root, sourceClusterID string, plan cdcPlanSummary) error {
	inventory, err := readSQLServerInventory(filepath.Join(root, "clusters", sourceClusterID, "inventory", "inventory.json"))
	if err != nil {
		return err
	}
	for _, tracked := range plan.Tables {
		table, ok := findInventoryTableBySourceObject(inventory, tracked.SourceObject)
		if !ok {
			return fmt.Errorf("cdc tracked table %s source schema drift: source table is missing from inventory", tracked.SourceObject)
		}
		inventoryColumns := chooseCDCCapturedColumns(table)
		if !equalNormalizedStringSlices(tracked.Columns, inventoryColumns) {
			return fmt.Errorf("cdc tracked table %s source schema drift: plan columns %v do not match inventory columns %v", tracked.SourceObject, tracked.Columns, inventoryColumns)
		}
		if !keyColumnsMatchInventoryKey(tracked.KeyColumns, table.Indexes) {
			return fmt.Errorf("cdc tracked table %s source schema drift: plan key_columns %v do not match current primary key or non-filtered unique index", tracked.SourceObject, tracked.KeyColumns)
		}
	}
	return nil
}

func findInventoryTableBySourceObject(inventory SQLServerInventory, sourceObject string) (SQLServerTable, bool) {
	parts := strings.Split(strings.TrimSpace(sourceObject), ".")
	if len(parts) != 3 {
		return SQLServerTable{}, false
	}
	sourceDatabase, sourceSchema, sourceTable := parts[0], parts[1], parts[2]
	for _, database := range inventory.Databases {
		if !strings.EqualFold(database.Name, sourceDatabase) {
			continue
		}
		for _, schema := range database.Schemas {
			if !strings.EqualFold(schema.Name, sourceSchema) {
				continue
			}
			for _, table := range schema.Tables {
				if strings.EqualFold(table.Name, sourceTable) {
					return table, true
				}
			}
		}
	}
	return SQLServerTable{}, false
}

func keyColumnsMatchInventoryKey(planKeyColumns []string, indexes []SQLServerIndex) bool {
	for _, index := range indexes {
		if !index.PrimaryKey || index.Filtered || len(index.Columns) == 0 {
			continue
		}
		if equalNormalizedStringSlices(planKeyColumns, index.Columns) {
			return true
		}
	}
	for _, index := range indexes {
		if !index.Unique || index.Filtered || len(index.Columns) == 0 {
			continue
		}
		if equalNormalizedStringSlices(planKeyColumns, index.Columns) {
			return true
		}
	}
	return false
}

func equalNormalizedStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !strings.EqualFold(strings.TrimSpace(left[i]), strings.TrimSpace(right[i])) {
			return false
		}
	}
	return true
}
