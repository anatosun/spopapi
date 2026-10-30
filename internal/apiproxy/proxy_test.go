package apiproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type fakeStore struct {
	m    map[string]StoredResponse
	puts int
}

func (f *fakeStore) GetResponse(_ context.Context, key string) (StoredResponse, bool, error) {
	r, ok := f.m[key]
	return r, ok, nil
}

func (f *fakeStore) PutResponse(_ context.Context, key string, r StoredResponse, _ string) error {
	if f.m == nil {
		f.m = map[string]StoredResponse{}
	}
	f.m[key] = r
	f.puts++
	return nil
}

func (f *fakeStore) Counts(context.Context) (int64, int64, error) {
	return int64(len(f.m)), int64(len(f.m)), nil
}

func newTestProxy(t *testing.T) (*Proxy, *int64) {
	t.Helper()
	mr := miniredis.RunT(t)
	var calls int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
		case "/v1/tracks/abc":
			atomic.AddInt64(&calls, 1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"abc","name":"Fake Track"}`)
		case "/v1/tracks/nope":
			atomic.AddInt64(&calls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"not found"}`)
		default:
			atomic.AddInt64(&calls, 1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true}`)
		}
	}))
	t.Cleanup(upstream.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	p := &Proxy{
		RDB: rdb,
		Up: &SpotifyClient{
			ClientID: "id", ClientSecret: "secret",
			APIBase: upstream.URL, TokenURL: upstream.URL + "/api/token",
			HTTP: upstream.Client(),
		},
		TTLs: DefaultTTLs(),
	}
	return p, &calls
}

func muxOf(p *Proxy) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/{path...}", p.Handle)
	return mux
}

func do(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMissThenHit(t *testing.T) {
	p, calls := newTestProxy(t)
	mux := muxOf(p)

	r1 := do(t, mux, "/v1/tracks/abc")
	if r1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first request: X-Cache=%q, want MISS", r1.Header().Get("X-Cache"))
	}
	r2 := do(t, mux, "/v1/tracks/abc")
	if r2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second request: X-Cache=%q, want HIT", r2.Header().Get("X-Cache"))
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Fatalf("upstream called %d times, want 1", got)
	}
}

func TestSeededRedisEntryNeedsNoUpstream(t *testing.T) {
	p, calls := newTestProxy(t)
	// The importer writes values in exactly this shape: "status\nbody".
	p.RDB.Set(context.Background(), "GET:/v1/tracks/seeded?", "200\n"+`{"id":"seeded","name":"From dump"}`, 0)

	r := do(t, muxOf(p), "/v1/tracks/seeded")
	if r.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("X-Cache=%q, want HIT", r.Header().Get("X-Cache"))
	}
	if got := atomic.LoadInt64(calls); got != 0 {
		t.Fatalf("upstream called %d times, want 0", got)
	}
}

func TestPostgresFallbackBackfillsRedis(t *testing.T) {
	p, calls := newTestProxy(t)
	st := &fakeStore{m: map[string]StoredResponse{
		"GET:/v1/tracks/frompg?": {Status: 200, ContentType: "application/json", Body: []byte(`{"id":"frompg"}`)},
	}}
	p.Store = st

	r := do(t, muxOf(p), "/v1/tracks/frompg")
	if r.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("X-Cache=%q, want HIT", r.Header().Get("X-Cache"))
	}
	if got := atomic.LoadInt64(calls); got != 0 {
		t.Fatalf("upstream called %d times, want 0", got)
	}
	// Redis should have been backfilled.
	if _, err := p.RDB.Get(context.Background(), "GET:/v1/tracks/frompg?").Result(); err != nil {
		t.Fatalf("redis backfill missing: %v", err)
	}
}

func TestUpstreamResponsePersistedToPostgres(t *testing.T) {
	p, _ := newTestProxy(t)
	st := &fakeStore{m: map[string]StoredResponse{}}
	p.Store = st

	r := do(t, muxOf(p), "/v1/tracks/abc")
	if r.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("X-Cache=%q, want MISS", r.Header().Get("X-Cache"))
	}
	if st.puts != 1 {
		t.Fatalf("PutResponse calls=%d, want 1", st.puts)
	}
	sr, ok := st.m["GET:/v1/tracks/abc?"]
	if !ok || sr.Status != 200 || string(sr.Body) != `{"id":"abc","name":"Fake Track"}` {
		t.Fatalf("stored response wrong: %+v ok=%v", sr, ok)
	}
}

func TestNegativeCaching(t *testing.T) {
	p, calls := newTestProxy(t)
	mux := muxOf(p)

	r1 := do(t, mux, "/v1/tracks/nope")
	if r1.Code != http.StatusNotFound || r1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first 404: code=%d X-Cache=%q", r1.Code, r1.Header().Get("X-Cache"))
	}
	r2 := do(t, mux, "/v1/tracks/nope")
	if r2.Code != http.StatusNotFound || r2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("cached 404: code=%d X-Cache=%q", r2.Code, r2.Header().Get("X-Cache"))
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Fatalf("upstream called %d times, want 1", got)
	}
}
