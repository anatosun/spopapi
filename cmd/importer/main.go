package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"spopapi/internal/importer"
	"spopapi/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dump := flag.String("dump", "", "explicit dump path, overrides INGEST_DIR")
	kind := flag.String("kind", "core", "dump kind: core or audio-features")
	ingestDir := flag.String("ingest-dir", envOr("INGEST_DIR", ""), "directory containing the standard dump files")
	dsn := flag.String("db", envOr("DATABASE_URL", "postgres://spopapi:spopapi@localhost:5432/spopapi"), "postgres DSN")
	tempDir := flag.String("temp-dir", "", "decompression directory, defaults to INGEST_DIR or the dump directory")
	batch := flag.Int("batch", 50000, "rows per Postgres COPY operation")
	fresh := flag.Bool("fresh", false, "reimport tables even if import_state marks them complete")
	flag.Parse()

	dumpPath, err := importer.DumpPath(*kind, *dump, *ingestDir)
	if err != nil {
		return err
	}
	if *tempDir == "" {
		if *ingestDir != "" {
			*tempDir = *ingestDir
		} else {
			*tempDir = filepath.Dir(dumpPath)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.New(ctx, *dsn)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer st.Close()

	start := time.Now()
	db, cleanup, err := importer.OpenSQLite(ctx, dumpPath, *tempDir)
	if err != nil {
		return fmt.Errorf("open dump: %w", err)
	}
	defer cleanup()
	log.Printf("SQLite ready after %s", time.Since(start).Round(time.Second))

	stats, err := importer.Import(ctx, db, st.Pool(), importer.Options{
		Kind:      *kind,
		BatchSize: *batch,
		Fresh:     *fresh,
		Logger:    log.Default(),
	})
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}
	log.Printf("import complete: %d rows in %s", stats.TotalRows, time.Since(start).Round(time.Second))
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
