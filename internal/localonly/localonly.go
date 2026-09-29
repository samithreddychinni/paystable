// Package localonly limits handlers to local operators.
package localonly

import (
	"net"
	"net/http"
)

// Wrap permits loopback requests and explicitly trusted addresses.
func Wrap(allowed []net.IP, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || (!ip.IsLoopback() && !contains(allowed, ip)) {
			http.Error(w, `{"error":"dashboard is available on localhost only"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func contains(allowed []net.IP, candidate net.IP) bool {
	for _, ip := range allowed {
		if ip.Equal(candidate) {
			return true
		}
	}
	return false
}
