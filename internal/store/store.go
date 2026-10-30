package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spopapi/internal/apiproxy"
)

//go:embed schema.sql
var schemaSQL string

type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	s := &Store{pool: pool}
	if err := EnsureSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, schemaSQL)
	return err
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) GetResponse(ctx context.Context, key string) (apiproxy.StoredResponse, bool, error) {
	var r apiproxy.StoredResponse
	var ct *string
	err := s.pool.QueryRow(ctx,
		`SELECT status, content_type, body FROM responses WHERE key = $1`, key).
		Scan(&r.Status, &ct, &r.Body)
	if errors.Is(err, pgx.ErrNoRows) {
		return apiproxy.StoredResponse{}, false, nil
	}
	if err != nil {
		return apiproxy.StoredResponse{}, false, err
	}
	r.ContentType = "application/json"
	if ct != nil && *ct != "" {
		r.ContentType = *ct
	}
	return r, true, nil
}

func (s *Store) PutResponse(ctx context.Context, key string, r apiproxy.StoredResponse, source string) error {
	ct := r.ContentType
	if ct == "" {
		ct = "application/json"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO responses (key, status, content_type, body, source)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (key) DO UPDATE SET
		  status       = EXCLUDED.status,
		  content_type = EXCLUDED.content_type,
		  body         = EXCLUDED.body,
		  fetched_at   = now(),
		  source       = EXCLUDED.source`,
		key, r.Status, ct, r.Body, source)
	return err
}

func (s *Store) Counts(ctx context.Context) (tracks, responses int64, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM catalog_tracks), (SELECT count(*) FROM responses)`).
		Scan(&tracks, &responses)
	return tracks, responses, err
}
