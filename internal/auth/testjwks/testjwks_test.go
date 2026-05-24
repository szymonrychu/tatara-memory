package testjwks_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/auth/testjwks"
)

func TestServer_SignsValidToken(t *testing.T) {
	srv := testjwks.NewServer(t)
	token := srv.SignToken(t, testjwks.Claims{
		Issuer:   srv.Issuer(),
		Audience: []string{"tatara-memory"},
		Subject:  "user-1",
		Extra:    map[string]any{"preferred_username": "szymon"},
	})
	require.NotEmpty(t, token)
	// JWT has 3 dot-separated parts
	parts := 0
	for _, c := range token {
		if c == '.' {
			parts++
		}
	}
	require.Equal(t, 2, parts)
}

func TestServer_ServesJWKS(t *testing.T) {
	srv := testjwks.NewServer(t)
	resp, err := http.Get(srv.JWKSURL())
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var jwks struct {
		Keys []map[string]any `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(body, &jwks))
	require.Len(t, jwks.Keys, 1)
	require.Equal(t, "RSA", jwks.Keys[0]["kty"])
	require.NotEmpty(t, jwks.Keys[0]["n"])
	require.NotEmpty(t, jwks.Keys[0]["e"])
	require.NotEmpty(t, jwks.Keys[0]["kid"])
}
