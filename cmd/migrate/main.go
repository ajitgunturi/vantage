// cmd/migrate is a one-shot migration runner for the vantage pipeline.
//
// It reads VANTAGE_DB_DSN from the environment, calls pkg/db.Migrate to
// apply all pending forward migrations against that database, then exits.
// The exit code is 0 on success and 1 on any error.
//
// This binary is used by:
//   - scripts/smoke/phase02-postgres.sh (manual verification)
//   - Phase 5 Kubernetes init-job (apply schema before services start)
//
// DSN security: the DSN is read from the environment and is never logged.
// Error output carries context ("db: ...") but not the DSN value (ASVS V8).
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/ajitg/vantage/pkg/db"
)

func main() {
	if err := run(); err != nil {
		log.Printf("migrate: %v", err)
		os.Exit(1)
	}
	fmt.Println("migrate: schema up to date")
}

func run() error {
	cfg, err := db.FromEnv()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if err := db.Migrate(ctx, cfg.DSN); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	return nil
}
