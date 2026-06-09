package codegraph_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

// fakeOpenAI returns a minimal chat completions response with the given content.
func fakeOpenAI(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": content}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func labelerFor(t *testing.T, srv *httptest.Server) *codegraph.OpenAILabeler {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", srv.URL)
	t.Setenv("SEMANTIC_MODEL", "gpt-test")
	l := codegraph.NewOpenAILabelerFromEnv()
	require.NotNil(t, l)
	return l
}

func TestOpenAILabeler_Label_Trimming(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", "Auth", "Auth"},
		{"trailing newline", "Auth\n", "Auth"},
		{"leading and trailing spaces", "  Auth  ", "Auth"},
		{"quoted", `"Auth"`, "Auth"},
		{"quoted with whitespace", `"Auth"\n`, `Auth"\n`}, // TrimPrefix strips leading quote; trailing \n is not a real newline
		{"quoted and trimmed", `  "Auth"  `, "Auth"},
		{"quoted with real newline", "\"Auth\"\n", "Auth"},
		{"mixed spaces and quotes", "  \"Payment Processing\"  \n", "Payment Processing"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeOpenAI(t, tc.raw)
			defer srv.Close()
			l := labelerFor(t, srv)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			got, err := l.Label(ctx, []string{"PaymentHandler", "Checkout"})
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestOpenAILabeler_Label_EmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{"choices": []any{}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	l := labelerFor(t, srv)
	_, err := l.Label(context.Background(), []string{"foo"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty choices")
}
