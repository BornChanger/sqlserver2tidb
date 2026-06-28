package gitops

import (
	"fmt"
	"strings"
)

const (
	compressionNone = "none"
	compressionGzip = "gzip"
)

func normalizeCompression(compression string) string {
	compression = strings.ToLower(strings.TrimSpace(compression))
	if compression == "" {
		return compressionNone
	}
	return compression
}

func validateSupportedCompression(compression string) error {
	compression = normalizeCompression(compression)
	switch compression {
	case compressionNone, compressionGzip:
		return nil
	default:
		return fmt.Errorf("compression %s is not supported; supported compression: %s, %s", compression, compressionNone, compressionGzip)
	}
}

func validateCompressionForImportEngine(compression, importEngine string) error {
	compression = normalizeCompression(compression)
	if err := validateSupportedCompression(compression); err != nil {
		return err
	}
	if compression != compressionNone && normalizeImportEngine(importEngine) == importEngineTiDBImportInto {
		return fmt.Errorf("compression %s is only supported with %s import", compression, importEngineSQLInsert)
	}
	return nil
}

func compressedExportFileExtension(format, compression string) string {
	extension := strings.ToLower(strings.TrimSpace(format))
	if extension == "" {
		extension = "csv"
	}
	if normalizeCompression(compression) == compressionGzip {
		return extension + ".gz"
	}
	return extension
}
