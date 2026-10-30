package apiproxy

import (
	"context"
	"net/url"
	"sync/atomic"
	"testing"
)

type fakeCatalogStore struct {
	fakeStore
	response StoredResponse
	found    bool
	calls    int64
}

func (f *fakeCatalogStore) GetCatalogResponse(context.Context, string, url.Values) (StoredResponse, bool, error) {
	atomic.AddInt64(&f.calls, 1)
	return f.response, f.found, nil
}

func TestCatalogFallbackAvoidsUpstreamAndPersists(t *testing.T) {
	p, upstreamCalls := newTestProxy(t)
	st := &fakeCatalogStore{
		fakeStore: fakeStore{m: map[string]StoredResponse{}},
		response:  StoredResponse{Status: 200, ContentType: "application/json", Body: []byte(`{"id":"catalog"}`)},
		found:     true,
	}
	p.Store = st

	r := do(t, muxOf(p), "/v1/tracks/catalog")
	if r.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("X-Cache=%q, want HIT", r.Header().Get("X-Cache"))
	}
	if got := atomic.LoadInt64(upstreamCalls); got != 0 {
		t.Fatalf("upstream called %d times, want 0", got)
	}
	if got := atomic.LoadInt64(&st.calls); got != 1 {
		t.Fatalf("catalog called %d times, want 1", got)
	}
	sr, ok := st.m["GET:/v1/tracks/catalog?"]
	if !ok || string(sr.Body) != `{"id":"catalog"}` {
		t.Fatalf("assembled response not persisted: %+v", st.m)
	}
	if _, err := p.RDB.Get(context.Background(), "GET:/v1/tracks/catalog?").Result(); err != nil {
		t.Fatalf("redis backfill missing: %v", err)
	}
}
