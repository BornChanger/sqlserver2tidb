package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type sourceSchemaDriftContext struct {
	Inventory    SQLServerInventory
	ReviewedDiff *schemaDiffDocument
}

func loadSourceSchemaDriftContext(root, sourceClusterID, projectDir string) (sourceSchemaDriftContext, error) {
	inventory, err := readSQLServerInventory(filepath.Join(root, "clusters", sourceClusterID, "inventory", "inventory.json"))
	if err != nil {
		return sourceSchemaDriftContext{}, err
	}
	ctx := sourceSchemaDriftContext{Inventory: inventory}
	diffPath := filepath.Join(projectDir, "schema", "schema-diff.json")
	info, err := os.Stat(diffPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ctx, nil
		}
		return sourceSchemaDriftContext{}, fmt.Errorf("stat schema-diff.json: %w", err)
	}
	if info.IsDir() {
		return ctx, nil
	}
	diff, err := readSchemaDiffForValidation(diffPath)
	if err != nil {
		return sourceSchemaDriftContext{}, err
	}
	if diff.Status == "reviewed" {
		ctx.ReviewedDiff = &diff
	}
	return ctx, nil
}

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

func requireExportPlanMatchesInventory(root, sourceClusterID, projectDir string, chunks []dataExportChunkState) error {
	ctx, err := loadSourceSchemaDriftContext(root, sourceClusterID, projectDir)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{})
	for _, chunk := range chunks {
		sourceObject := strings.TrimSpace(chunk.SourceObject)
		if _, ok := seen[strings.ToLower(sourceObject)]; ok {
			continue
		}
		if _, err := ensureSourceObjectMatchesInventory(ctx, sourceObject, "export chunk "+chunk.ID); err != nil {
			return err
		}
		seen[strings.ToLower(sourceObject)] = struct{}{}
	}
	return nil
}

func requireImportPlanMatchesInventory(root, sourceClusterID, projectDir, engine string, jobs []dataImportJobState) error {
	ctx, err := loadSourceSchemaDriftContext(root, sourceClusterID, projectDir)
	if err != nil {
		return err
	}
	chunkSources, err := readExportChunkSourceObjectMap(filepath.Join(projectDir, "plan", "export-plan.yaml"))
	if err != nil {
		return err
	}
	for _, job := range jobs {
		sourceObject := strings.TrimSpace(chunkSources[job.DependsOnExportChunk])
		if sourceObject == "" {
			continue
		}
		table, err := ensureSourceObjectMatchesInventory(ctx, sourceObject, "import job "+job.ID)
		if err != nil {
			return err
		}
		if normalizeImportEngine(engine) == importEngineTiDBImportInto && len(job.Fields) > 0 {
			if err := requireImportFieldsMatchInventory(job, table); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireValidationPlanMatchesInventory(root, sourceClusterID, projectDir string, checks []validationPlanCheck) error {
	ctx, err := loadSourceSchemaDriftContext(root, sourceClusterID, projectDir)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{})
	for _, check := range checks {
		switch check.Type {
		case "row_count", "row-count":
			sourceObject := strings.TrimSpace(check.SourceObject)
			if sourceObject == "" || validateSQLServerSourceObjectName("validation row_count check "+check.ID+" source_object", sourceObject) != nil {
				continue
			}
			if _, err := ensureSourceObjectMatchesInventory(ctx, sourceObject, "validation check "+check.ID); err != nil {
				return err
			}
			seen[strings.ToLower(sourceObject)] = struct{}{}
		case "checksum", "sampled_hash", "bucketed_count", "business_sql":
			for _, sourceObject := range extractValidationSourceObjects(check.SourceSQL) {
				normalized := strings.ToLower(sourceObject)
				if _, ok := seen[normalized]; ok {
					continue
				}
				if _, err := ensureSourceObjectMatchesInventory(ctx, sourceObject, "validation check "+check.ID); err != nil {
					return err
				}
				seen[normalized] = struct{}{}
			}
		}
	}
	return nil
}

func ensureSourceObjectMatchesInventory(ctx sourceSchemaDriftContext, sourceObject, label string) (SQLServerTable, error) {
	sourceObject = strings.TrimSpace(sourceObject)
	if sourceObject == "" || validateSQLServerSourceObjectName(label+" source_object", sourceObject) != nil {
		return SQLServerTable{}, nil
	}
	table, ok := findInventoryTableBySourceObject(ctx.Inventory, sourceObject)
	if !ok {
		return SQLServerTable{}, fmt.Errorf("%s source schema drift: source table %s is missing from inventory", label, sourceObject)
	}
	if ctx.ReviewedDiff != nil {
		diffTable, ok := findSchemaDiffTableBySourceObject(*ctx.ReviewedDiff, sourceObject)
		if !ok {
			return SQLServerTable{}, fmt.Errorf("%s source schema drift: source table %s is missing from reviewed schema diff", label, sourceObject)
		}
		if !schemaDiffColumnsMatchInventory(diffTable.Columns, table.Columns) {
			return SQLServerTable{}, fmt.Errorf("%s source schema drift: reviewed columns %v do not match inventory columns %v for %s", label, schemaDiffSourceColumns(diffTable.Columns), inventorySourceColumns(table.Columns), sourceObject)
		}
	}
	return table, nil
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

func findSchemaDiffTableBySourceObject(diff schemaDiffDocument, sourceObject string) (schemaTableDiff, bool) {
	for _, table := range diff.Tables {
		if strings.EqualFold(strings.TrimSpace(table.SourceObject), strings.TrimSpace(sourceObject)) {
			return table, true
		}
	}
	return schemaTableDiff{}, false
}

func readExportChunkSourceObjectMap(path string) (map[string]string, error) {
	chunks, err := readExportPlanChunks(path)
	if err != nil {
		return nil, err
	}
	sources := make(map[string]string, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk.ID) == "" || strings.TrimSpace(chunk.SourceObject) == "" {
			continue
		}
		sources[chunk.ID] = chunk.SourceObject
	}
	return sources, nil
}

func requireImportFieldsMatchInventory(job dataImportJobState, table SQLServerTable) error {
	var planFields []string
	for _, field := range job.Fields {
		field = strings.TrimSpace(field)
		if field == "" || strings.HasPrefix(field, "@") {
			continue
		}
		planFields = append(planFields, field)
	}
	inventoryFields := inventorySourceColumns(table.Columns)
	if !equalNormalizedStringSlices(planFields, inventoryFields) {
		return fmt.Errorf("import job %s source schema drift: fields %v do not match inventory columns %v", job.ID, planFields, inventoryFields)
	}
	return nil
}

func schemaDiffColumnsMatchInventory(diffColumns []schemaColumnDiff, inventoryColumns []SQLServerColumn) bool {
	if len(diffColumns) != len(inventoryColumns) {
		return false
	}
	for i := range diffColumns {
		if !strings.EqualFold(strings.TrimSpace(diffColumns[i].SourceColumn), strings.TrimSpace(inventoryColumns[i].Name)) {
			return false
		}
		if !strings.EqualFold(strings.TrimSpace(diffColumns[i].SourceType), strings.TrimSpace(inventoryColumns[i].Type)) {
			return false
		}
	}
	return true
}

func schemaDiffSourceColumns(columns []schemaColumnDiff) []string {
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		name := strings.TrimSpace(column.SourceColumn)
		if strings.TrimSpace(column.SourceType) != "" {
			name += ":" + strings.TrimSpace(column.SourceType)
		}
		names = append(names, name)
	}
	return names
}

func inventorySourceColumns(columns []SQLServerColumn) []string {
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		name := strings.TrimSpace(column.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names
}

func extractValidationSourceObjects(sql string) []string {
	fields := strings.Fields(sql)
	var objects []string
	for i := 0; i < len(fields)-1; i++ {
		keyword := strings.ToLower(strings.Trim(fields[i], " \t\r\n(),;"))
		if keyword != "from" && keyword != "join" {
			continue
		}
		object := normalizeSQLObjectToken(fields[i+1])
		if strings.Count(object, ".") != 2 {
			continue
		}
		if validateSQLServerSourceObjectName("validation source_sql source object", object) == nil {
			objects = append(objects, object)
		}
	}
	return objects
}

func normalizeSQLObjectToken(token string) string {
	token = strings.TrimSpace(token)
	token = strings.Trim(token, " \t\r\n(),;")
	token = strings.TrimPrefix(token, "(")
	token = strings.TrimSuffix(token, ")")
	token = strings.ReplaceAll(token, "[", "")
	token = strings.ReplaceAll(token, "]", "")
	token = strings.ReplaceAll(token, "`", "")
	token = strings.ReplaceAll(token, `"`, "")
	return strings.Trim(token, " \t\r\n,;")
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
