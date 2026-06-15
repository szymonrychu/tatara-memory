package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

type ctxKey struct{}

// ClaimsFromContext retrieves validated claims from the request context.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(*Claims)
	return c, ok
}

const wwwAuthenticate = `Bearer realm="tatara-memory"`

// authAttempts holds the auth_attempts_total counter. It is constructed by
// newAuthAttempts and kept as a package-private struct so the nil-registry path
// stays a no-op without nil checks at every call site.
type authAttempts struct {
	total *prometheus.CounterVec
}

func newAuthAttempts(reg prometheus.Registerer) *authAttempts {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_attempts_total",
		Help: "Count of auth middleware decisions by result.",
	}, []string{"result"})
	if reg != nil {
		reg.MustRegister(c)
	}
	for _, r := range []string{"success", "missing_token", "invalid_scheme", "invalid_token"} {
		c.WithLabelValues(r)
	}
	return &authAttempts{total: c}
}

func (a *authAttempts) inc(result string) { a.total.WithLabelValues(result).Inc() }

// Middleware returns a chi-compatible middleware that verifies the Bearer token
// and injects parsed Claims into the request context.
// Uses the package-global slog for rejection logs; call MiddlewareWithMetrics to
// also count auth outcomes in Prometheus.
func Middleware(v *Verifier) func(http.Handler) http.Handler {
	return middleware(v, newAuthAttempts(nil))
}

// MiddlewareWithMetrics is Middleware plus an auth_attempts_total counter registered
// in reg. Use this in production; Middleware is kept for test helpers that do not
// supply a registry.
func MiddlewareWithMetrics(v *Verifier, reg prometheus.Registerer) func(http.Handler) http.Handler {
	return middleware(v, newAuthAttempts(reg))
}

func middleware(v *Verifier, attempts *authAttempts) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, reason := bearerToken(r)
			if raw == "" {
				attempts.inc(reason)
				slog.WarnContext(r.Context(), "auth: rejected", "reason", reason)
				w.Header().Set("WWW-Authenticate", wwwAuthenticate)
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			claims, err := v.Verify(r.Context(), raw)
			if err != nil {
				attempts.inc("invalid_token")
				slog.WarnContext(r.Context(), "auth: rejected", "reason", "invalid_token")
				w.Header().Set("WWW-Authenticate", wwwAuthenticate)
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			attempts.inc("success")
			ctx := context.WithValue(r.Context(), ctxKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken extracts the token from the Authorization header.
// Returns the token (empty on failure) and a rejection reason string.
func bearerToken(r *http.Request) (string, string) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", "missing_token"
	}
	const prefix = "Bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", "invalid_scheme"
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if tok == "" {
		return "", "missing_token"
	}
	return tok, ""
}
