package lightrag_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

func TestQueryMode_Valid(t *testing.T) {
	require.True(t, lightrag.QueryModeHybrid.Valid())
	require.True(t, lightrag.QueryModeLocal.Valid())
	require.True(t, lightrag.QueryModeGlobal.Valid())
	require.True(t, lightrag.QueryModeNaive.Valid())
	require.False(t, lightrag.QueryMode("bogus").Valid())
}

func TestClient_InterfaceMethods(t *testing.T) {
	// Compile-time check: any implementation must satisfy this method set.
	var _ lightrag.Client = (*stubClient)(nil)
}

func TestHTTPClient_ImplementsClient(t *testing.T) {
	var _ lightrag.Client = (*lightrag.HTTPClient)(nil)
}

type stubClient struct{ lightrag.Client }
