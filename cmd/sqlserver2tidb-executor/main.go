package main

import (
	"os"

	"github.com/BornChanger/sqlserver2tidb/internal/executor"
)

func main() {
	os.Exit(executor.Run(os.Args[1:], os.Stdout, os.Stderr))
}
