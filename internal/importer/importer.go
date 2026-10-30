package importer

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Options struct {
	Kind      string
	BatchSize int
	Fresh     bool
	Logger    *log.Logger
}

type Stats struct {
	Tables    map[string]int64
	Skipped   []string
	TotalRows int64
}

func Import(ctx context.Context, sqlite *sql.DB, postgres *pgxpool.Pool, opts Options) (Stats, error) {
	specs, err := Tables(opts.Kind)
	if err != nil {
		return Stats{}, err
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 50000
	}
	stats := Stats{Tables: map[string]int64{}}
	for _, spec := range specs {
		if !opts.Fresh {
			var completed bool
			err := postgres.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM import_state WHERE table_name = $1)`, spec.Name).Scan(&completed)
			if err != nil {
				return stats, err
			}
			if completed {
				stats.Skipped = append(stats.Skipped, spec.Name)
				if opts.Logger != nil {
					opts.Logger.Printf("%s: already complete, skipping", spec.Name)
				}
				continue
			}
		}
		if _, err := postgres.Exec(ctx, `TRUNCATE TABLE `+pgx.Identifier{spec.Name}.Sanitize()); err != nil {
			return stats, fmt.Errorf("truncate %s: %w", spec.Name, err)
		}
		n, err := importTable(ctx, sqlite, postgres, spec, opts)
		stats.Tables[spec.Name] = n
		stats.TotalRows += n
		if err != nil {
			return stats, err
		}
		if _, err := postgres.Exec(ctx, `
			INSERT INTO import_state (table_name, rows, completed_at)
			VALUES ($1, $2, now())
			ON CONFLICT (table_name) DO UPDATE SET rows = EXCLUDED.rows, completed_at = now()`,
			spec.Name, n); err != nil {
			return stats, fmt.Errorf("mark %s complete: %w", spec.Name, err)
		}
	}
	return stats, nil
}

func importTable(ctx context.Context, sqlite *sql.DB, postgres *pgxpool.Pool, spec TableSpec, opts Options) (int64, error) {
	started := time.Now()
	if opts.Logger != nil {
		opts.Logger.Printf("%s: starting", spec.Name)
	}
	rows, err := sqlite.QueryContext(ctx, spec.Query)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", spec.Name, err)
	}
	defer rows.Close()

	var count int64
	var nextProgress int64 = 1000000
	batch := make([][]any, 0, opts.BatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		copied, err := postgres.CopyFrom(ctx, pgx.Identifier{spec.Name}, spec.Columns, pgx.CopyFromRows(batch))
		if err != nil {
			return fmt.Errorf("copy %s at row %d: %w", spec.Name, count, err)
		}
		if copied != int64(len(batch)) {
			return fmt.Errorf("copy %s: copied %d of %d rows", spec.Name, copied, len(batch))
		}
		batch = batch[:0]
		return nil
	}

	for rows.Next() {
		values := make([]any, len(spec.Columns))
		pointers := make([]any, len(spec.Columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return count, fmt.Errorf("scan %s: %w", spec.Name, err)
		}
		row := make([]any, len(values))
		for i, value := range values {
			if b, ok := value.([]byte); ok {
				value = string(b)
			}
			row[i] = value
		}
		batch = append(batch, row)
		count++
		if len(batch) == opts.BatchSize {
			if err := flush(); err != nil {
				return count, err
			}
		}
		if opts.Logger != nil && count >= nextProgress {
			elapsed := time.Since(started).Seconds()
			opts.Logger.Printf("%s: %d rows (%.0f rows/s)", spec.Name, count, float64(count)/max(elapsed, 0.001))
			nextProgress += 1000000
		}
	}
	if err := rows.Err(); err != nil {
		return count, fmt.Errorf("read %s: %w", spec.Name, err)
	}
	if err := flush(); err != nil {
		return count, err
	}
	if opts.Logger != nil {
		opts.Logger.Printf("%s: complete, %d rows in %s", spec.Name, count, time.Since(started).Round(time.Second))
	}
	return count, nil
}
