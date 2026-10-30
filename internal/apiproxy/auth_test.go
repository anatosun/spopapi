package apiproxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBearerAuth(t *testing.T) {
	called := 0
	handler := BearerAuth("test-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))
	tests := []struct {
		name, header string
		wantStatus   int
		wantCalls    int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "wrong", header: "Bearer wrong", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", header: "Basic test-token", wantStatus: http.StatusUnauthorized},
		{name: "valid", header: "Bearer test-token", wantStatus: http.StatusNoContent, wantCalls: 1},
		{name: "scheme is case insensitive", header: "bearer test-token", wantStatus: http.StatusNoContent, wantCalls: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/tracks/abc", nil)
			req.Header.Set("Authorization", test.header)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != test.wantStatus || called != test.wantCalls {
				t.Fatalf("status=%d calls=%d, want status=%d calls=%d", rec.Code, called, test.wantStatus, test.wantCalls)
			}
		})
	}
}
