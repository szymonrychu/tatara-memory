package fake_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
)

func TestFake_ImplementsClient(t *testing.T) {
	var _ lightrag.Client = fake.New()
}

func TestFake_InsertAndGetDocument(t *testing.T) {
	f := fake.New()
	resp, err := f.InsertDocument(context.Background(), lightrag.InsertRequest{
		Documents: []lightrag.Document{{Content: "hello"}},
	})
	require.NoError(t, err)
	require.Len(t, resp.IDs, 1)

	doc, err := f.GetDocument(context.Background(), resp.IDs[0])
	require.NoError(t, err)
	require.Equal(t, "hello", doc.Content)
}

func TestFake_DeleteDocument(t *testing.T) {
	f := fake.New()
	resp, _ := f.InsertDocument(context.Background(), lightrag.InsertRequest{
		Documents: []lightrag.Document{{Content: "x"}},
	})
	require.NoError(t, f.DeleteDocument(context.Background(), resp.IDs[0]))
	_, err := f.GetDocument(context.Background(), resp.IDs[0])
	require.Error(t, err)
}
