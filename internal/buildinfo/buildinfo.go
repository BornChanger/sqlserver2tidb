package buildinfo

import "fmt"

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func Format(binaryName string) string {
	return fmt.Sprintf("%s version %s\ncommit: %s\nbuilt: %s\n", binaryName, Version, Commit, BuildDate)
}
