package apiproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

// StoredResponse is an API response held durably in Postgres.
type StoredResponse struct {
	Status      int
	ContentType string
	Body        []byte
}

// Store is the Postgres-backed durable layer. The dump importer seeds it;
// every upstream fetch that was not already stored gets persisted too, so
// the store deepens over time.
type Store interface {
	GetResponse(ctx context.Context, key string) (StoredResponse, bool, error)
	PutResponse(ctx context.Context, key string, r StoredResponse, source string) error
	Counts(ctx context.Context) (tracks, responses int64, err error)
}

// CatalogStore reconstructs dump-backed responses from relational tables.
type CatalogStore interface {
	GetCatalogResponse(ctx context.Context, path string, q url.Values) (StoredResponse, bool, error)
}

// Upstream fetches from the real Spotify API.
type Upstream interface {
	Get(ctx context.Context, path string, q url.Values) (UpstreamResponse, error)
}

// Proxy is a cache-aside reverse proxy for the Spotify Web API:
// Redis -> Postgres -> Spotify, persisting anything new to Postgres.
type Proxy struct {
	RDB   *redis.Client
	Store Store // optional durable layer; without it the proxy is Redis-only
	Up    Upstream
	TTLs  TTLConfig

	SF singleflight.Group
}

func cacheKey(path string, q url.Values) string {
	if len(q) == 0 {
		return "GET:" + path + "?"
	}
	return "GET:" + path + "?" + q.Encode()
}

// encodeCached prefixes the body with its HTTP status: "status\nbody".
func encodeCached(status int, body []byte) []byte {
	head := strconv.Itoa(status)
	out := make([]byte, 0, len(head)+1+len(body))
	out = append(out, head...)
	out = append(out, '\n')
	return append(out, body...)
}

func decodeCached(val []byte) (int, []byte) {
	i := bytes.IndexByte(val, '\n')
	if i < 0 {
		return http.StatusOK, val
	}
	status, err := strconv.Atoi(string(val[:i]))
	if err != nil {
		return http.StatusOK, val
	}
	return status, val[i+1:]
}

type fetchResult struct {
	status      int
	contentType string
	body        []byte
	xcache      string
}

func (p *Proxy) Handle(w http.ResponseWriter, r *http.Request) {
	sub := "/v1/" + r.PathValue("path")
	q := r.URL.Query()
	key := cacheKey(sub, q)

	if val, err := p.RDB.Get(r.Context(), key).Bytes(); err == nil {
		status, body := decodeCached(val)
		p.RDB.Incr(r.Context(), "proxy.hits")
		writeResp(w, status, "application/json", body, "HIT")
		return
	} else if !errors.Is(err, redis.Nil) {
		// Redis error: fall through to Postgres / upstream.
	}

	v, err, _ := p.SF.Do(key, func() (any, error) {
		return p.fetch(r.Context(), key, sub, q)
	})
	if err != nil {
		http.Error(w, `{"error":"upstream unavailable"}`, http.StatusBadGateway)
		return
	}
	res := v.(fetchResult)
	writeResp(w, res.status, res.contentType, res.body, res.xcache)
}

func (p *Proxy) fetch(ctx context.Context, key, path string, q url.Values) (fetchResult, error) {
	// Re-check after singleflight admission: another request may have filled it.
	if val, err := p.RDB.Get(ctx, key).Bytes(); err == nil {
		status, body := decodeCached(val)
		p.RDB.Incr(ctx, "proxy.hits")
		return fetchResult{status: status, contentType: "application/json", body: body, xcache: "HIT"}, nil
	}

	// Durable layer: anything previously imported or fetched is here.
	if p.Store != nil {
		if sr, found, err := p.Store.GetResponse(ctx, key); err == nil && found {
			p.RDB.Set(ctx, key, encodeCached(sr.Status, sr.Body), p.ttlFor(path, sr.Status))
			p.RDB.Incr(ctx, "proxy.hits")
			return fetchResult{status: sr.Status, contentType: contentTypeOrJSON(sr.ContentType), body: sr.Body, xcache: "HIT"}, nil
		}

		// Reconstruct dump-backed objects before spending a Spotify API call.
		if catalog, ok := p.Store.(CatalogStore); ok {
			if sr, found, err := catalog.GetCatalogResponse(ctx, path, q); err == nil && found {
				_ = p.Store.PutResponse(ctx, key, sr, "dump")
				p.RDB.Set(ctx, key, encodeCached(sr.Status, sr.Body), p.ttlFor(path, sr.Status))
				p.RDB.Incr(ctx, "proxy.hits")
				return fetchResult{status: sr.Status, contentType: contentTypeOrJSON(sr.ContentType), body: sr.Body, xcache: "HIT"}, nil
			}
		}
	}

	resp, err := p.Up.Get(ctx, path, q)
	if err != nil {
		return fetchResult{}, err
	}
	ct := contentTypeOrJSON(resp.ContentType)
	sr := StoredResponse{Status: resp.Status, ContentType: ct, Body: resp.Body}
	if p.Store != nil {
		// Persist anything we didn't already have; re-fetches refresh it.
		if err := p.Store.PutResponse(ctx, key, sr, "upstream"); err != nil {
			// Persistence is best-effort on the read path; serve anyway.
			_ = err
		}
	}
	p.RDB.Set(ctx, key, encodeCached(resp.Status, resp.Body), p.ttlFor(path, resp.Status))
	p.RDB.Incr(ctx, "proxy.misses")
	return fetchResult{status: resp.Status, contentType: ct, body: resp.Body, xcache: "MISS"}, nil
}

func (p *Proxy) ttlFor(path string, status int) time.Duration {
	if status == http.StatusNotFound {
		return p.TTLs.Negative
	}
	return p.TTLs.For(path)
}

func contentTypeOrJSON(ct string) string {
	if ct == "" {
		return "application/json"
	}
	return ct
}

func writeResp(w http.ResponseWriter, status int, contentType string, body []byte, xcache string) {
	w.Header().Set("Content-Type", contentType)
	if xcache != "" {
		w.Header().Set("X-Cache", xcache)
	}
	w.WriteHeader(status)
	w.Write(body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, _ := json.Marshal(v)
	writeResp(w, status, "application/json", body, "")
}

// Stats reports hit/miss counters plus Postgres row counts.
func (p *Proxy) Stats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{}
	if n, err := p.RDB.Get(ctx, "proxy.hits").Int64(); err == nil {
		out["hits"] = n
	}
	if n, err := p.RDB.Get(ctx, "proxy.misses").Int64(); err == nil {
		out["misses"] = n
	}
	if n, err := p.RDB.DBSize(ctx).Result(); err == nil {
		out["redis_keys"] = n
	}
	if p.Store != nil {
		if tracks, responses, err := p.Store.Counts(ctx); err == nil {
			out["tracks_seeded"] = tracks
			out["responses_stored"] = responses
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// Health pings Redis and, if configured, Postgres.
func (p *Proxy) Health(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := map[string]string{"redis": "ok", "postgres": "disabled"}
	if err := p.RDB.Ping(ctx).Err(); err != nil {
		status["redis"] = err.Error()
	}
	if p.Store != nil {
		if _, _, err := p.Store.Counts(ctx); err != nil {
			status["postgres"] = err.Error()
		} else {
			status["postgres"] = "ok"
		}
	}
	writeJSON(w, http.StatusOK, status)
}
