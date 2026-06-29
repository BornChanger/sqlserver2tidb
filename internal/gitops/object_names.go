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

func validateSQLServerCDCSysname(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) > 128 {
		return fmt.Errorf("%s %q exceeds SQL Server sysname length 128", field, value)
	}
	for index, r := range value {
		if index == 0 {
			if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_') {
				return fmt.Errorf("%s %q must start with a letter or underscore", field, value)
			}
			continue
		}
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return fmt.Errorf("%s %q contains unsupported character %q", field, value, r)
		}
	}
	return nil
}
