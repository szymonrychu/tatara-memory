package httpapi_test

import (
	"context"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// stubService is a zero-value MemoryService stub for use in tests.
type stubService struct {
	createErr error
	getMem    memory.Memory
	getErr    error
}

func (s *stubService) CreateMemory(_ context.Context, m memory.Memory) (memory.Memory, error) {
	if s.createErr != nil {
		return memory.Memory{}, s.createErr
	}
	m.ID = "mem_stub"
	return m, nil
}

func (s *stubService) GetMemory(_ context.Context, _ string) (memory.Memory, error) {
	return s.getMem, s.getErr
}

func (s *stubService) DeleteMemory(_ context.Context, _ string) error { return nil }

func (s *stubService) DeleteMemoriesBySource(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

func (s *stubService) DeleteMemoriesBySources(_ context.Context, _ string, _ []string) (int, error) {
	return 0, nil
}

func (s *stubService) Query(_ context.Context, _ memory.Query) (memory.QueryResult, error) {
	return memory.QueryResult{}, nil
}

func (s *stubService) QueryData(_ context.Context, _ memory.Query) (memory.QueryResult, error) {
	return memory.QueryResult{}, nil
}

func (s *stubService) Describe(_ context.Context, _ memory.Query) (memory.DescribeResult, error) {
	return memory.DescribeResult{}, nil
}

func (s *stubService) GetEntity(_ context.Context, _ string) (memory.Entity, error) {
	return memory.Entity{}, nil
}

func (s *stubService) SearchEntities(_ context.Context, _ string) ([]memory.Entity, error) {
	return nil, nil
}

func (s *stubService) PatchEntity(_ context.Context, _ string, _ memory.Entity) (memory.Entity, error) {
	return memory.Entity{}, nil
}

func (s *stubService) ListEdges(_ context.Context) ([]memory.Edge, error) { return nil, nil }

func (s *stubService) CreateEdge(_ context.Context, e memory.Edge) (memory.Edge, error) {
	e.ID = "edge_stub"
	return e, nil
}

func (s *stubService) DeleteEdge(_ context.Context, _ string) error { return nil }
