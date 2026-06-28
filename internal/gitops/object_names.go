package gitops

import (
	"fmt"
	"strings"
)

func validateSQLServerSourceObjectName(field, sourceObject string) error {
	parts := strings.Split(strings.TrimSpace(sourceObject), ".")
	if len(parts) != 2 && len(parts) != 3 {
		return fmt.Errorf("%s must be schema.table or database.schema.table", field)
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("%s contains an empty identifier", field)
		}
	}
	return nil
}

func validateTiDBTargetObjectName(field, targetObject string) error {
	parts := strings.Split(strings.TrimSpace(targetObject), ".")
	if len(parts) != 1 && len(parts) != 2 {
		return fmt.Errorf("%s must be table or database.table", field)
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("%s contains an empty identifier", field)
		}
	}
	return nil
}
