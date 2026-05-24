package httpapi_test

import (
	"context"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

// stubService is a zero-value MemoryService stub for use in tests.
type stubService struct {
	createErr error
	getMem    httpapi.Memory
	getErr    error
}

func (s *stubService) CreateMemory(_ context.Context, m httpapi.Memory) (httpapi.Memory, error) {
	if s.createErr != nil {
		return httpapi.Memory{}, s.createErr
	}
	m.ID = "mem_stub"
	return m, nil
}

func (s *stubService) GetMemory(_ context.Context, _ string) (httpapi.Memory, error) {
	return s.getMem, s.getErr
}

func (s *stubService) DeleteMemory(_ context.Context, _ string) error { return nil }

func (s *stubService) Query(_ context.Context, _ httpapi.Query) (httpapi.QueryResult, error) {
	return httpapi.QueryResult{}, nil
}

func (s *stubService) Describe(_ context.Context, _ httpapi.Query) (httpapi.DescribeResult, error) {
	return httpapi.DescribeResult{}, nil
}

func (s *stubService) GetEntity(_ context.Context, _ string) (httpapi.Entity, error) {
	return httpapi.Entity{}, nil
}

func (s *stubService) SearchEntities(_ context.Context, _ string) ([]httpapi.Entity, error) {
	return nil, nil
}

func (s *stubService) PatchEntity(_ context.Context, _ string, _ httpapi.Entity) (httpapi.Entity, error) {
	return httpapi.Entity{}, nil
}

func (s *stubService) ListEdges(_ context.Context) ([]httpapi.Edge, error) { return nil, nil }

func (s *stubService) CreateEdge(_ context.Context, e httpapi.Edge) (httpapi.Edge, error) {
	e.ID = "edge_stub"
	return e, nil
}

func (s *stubService) DeleteEdge(_ context.Context, _ string) error { return nil }
