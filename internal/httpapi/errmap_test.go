package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

func TestMapServiceError_CodeGraph(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"entity not found", codegraph.ErrEntityNotFound, http.StatusNotFound},
		{"invalid scope", codegraph.ErrInvalidScope, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			mapServiceError(w, r, c.err)
			require.Equal(t, c.want, w.Code)
		})
	}
}
