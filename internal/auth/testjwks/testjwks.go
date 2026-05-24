package testjwks

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

// Server is an in-process OIDC-compatible test server backed by an RSA key pair.
type Server struct {
	t      *testing.T
	srv    *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	issuer string
}

// NewServer creates a new test JWKS server and registers cleanup on t.
func NewServer(t *testing.T) *Server {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	s := &Server{t: t, key: key, kid: "test-kid-1"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":   s.issuer,
			"jwks_uri": s.issuer + "/jwks.json",
		})
	})
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": s.kid,
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
			}},
		})
	})

	s.srv = httptest.NewServer(mux)
	s.issuer = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}

// Issuer returns the base URL of the test server (acts as OIDC issuer).
func (s *Server) Issuer() string { return s.issuer }

// JWKSURL returns the JWKS endpoint URL.
func (s *Server) JWKSURL() string { return s.issuer + "/jwks.json" }

// Claims holds parameters for signing a test token.
type Claims struct {
	Issuer    string
	Audience  []string
	Subject   string
	NotBefore time.Time
	IssuedAt  time.Time
	ExpiresAt time.Time
	Extra     map[string]any
}

// SignToken signs a JWT with the server's RSA key.
func (s *Server) SignToken(t *testing.T, c Claims) string {
	t.Helper()
	now := time.Now()
	if c.IssuedAt.IsZero() {
		c.IssuedAt = now
	}
	if c.NotBefore.IsZero() {
		c.NotBefore = now
	}
	if c.ExpiresAt.IsZero() {
		c.ExpiresAt = now.Add(time.Hour)
	}

	claims := jwt.MapClaims{
		"iss": c.Issuer,
		"aud": c.Audience,
		"sub": c.Subject,
		"iat": c.IssuedAt.Unix(),
		"nbf": c.NotBefore.Unix(),
		"exp": c.ExpiresAt.Unix(),
	}
	for k, v := range c.Extra {
		claims[k] = v
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = s.kid
	signed, err := tok.SignedString(s.key)
	require.NoError(t, err)
	return signed
}

// SignTokenWithKey signs a JWT with a foreign RSA key (for bad-signature tests).
func (s *Server) SignTokenWithKey(t *testing.T, key *rsa.PrivateKey, c Claims) string {
	t.Helper()
	tmp := *s
	tmp.key = key
	return tmp.SignToken(t, c)
}
