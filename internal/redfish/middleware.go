package redfish

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// unauthenticated Redfish URIs per DSP0266 §9.2: the service root and these
// resources MUST be accessible without auth so clients (e.g. gofish) can
// bootstrap the service before authenticating. (trailingSlashMiddleware runs
// first, so paths arrive here without a trailing slash.)
var redfishPublicPaths = map[string]bool{
	"/redfish":              true,
	"/redfish/v1":           true,
	"/redfish/v1/odata":     true,
	"/redfish/v1/$metadata": true,
}

func (s *Server) basicAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redfishPublicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(s.user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(s.pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Redfish"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) trailingSlashMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			r.URL.Path = strings.TrimSuffix(r.URL.Path, "/")
		}
		next.ServeHTTP(w, r)
	})
}
