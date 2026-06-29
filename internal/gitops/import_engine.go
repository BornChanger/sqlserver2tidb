package gitops

import (
	"fmt"
	"strings"
)

const (
	importEngineSQLInsert      = "sql-insert"
	importEngineTiDBImportInto = "tidb-import-into"
	importEngineImportInto     = "import-into"
	importEngineTiDBLightning  = "tidb-lightning"
	importEngineLightning      = "lightning"
)

func normalizeImportEngine(engine string) string {
	engine = strings.ToLower(strings.TrimSpace(engine))
	switch engine {
	case "":
		return importEngineSQLInsert
	case importEngineImportInto:
		return importEngineTiDBImportInto
	case importEngineLightning:
		return importEngineTiDBLightning
	default:
		return engine
	}
}

func validateSupportedImportEngine(engine string) error {
	engine = normalizeImportEngine(engine)
	switch engine {
	case importEngineSQLInsert, importEngineTiDBImportInto, importEngineTiDBLightning:
		return nil
	default:
		return fmt.Errorf("import engine %s is not supported by sqlserver2tidb-executor; supported engines: %s, %s, %s", engine, importEngineSQLInsert, importEngineTiDBImportInto, importEngineTiDBLightning)
	}
}
