package apiproxy

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func BearerAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") ||
			subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="spopapi"`)
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]any{
					"status":  http.StatusUnauthorized,
					"message": "Missing or invalid proxy token",
				},
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}
