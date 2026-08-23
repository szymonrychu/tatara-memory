package poolbudget_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/poolbudget"
)

func TestBudgetCheck(t *testing.T) {
	for _, tc := range []struct {
		name    string
		budget  poolbudget.Budget
		wantErr []string // substrings; empty means the budget must be accepted
	}{
		{
			name:   "chart defaults fit",
			budget: poolbudget.Budget{MemoriesBulk: 4, CodeGraphBulk: 2, WorkerPool: 4, Analytics: 2, MaxOpenConns: 20},
		},
		{
			name:   "exactly equal is not strictly less",
			budget: poolbudget.Budget{MemoriesBulk: 4, CodeGraphBulk: 2, WorkerPool: 4, Analytics: 2, MaxOpenConns: 12},
			wantErr: []string{
				"oversubscribe the DB pool",
				"memories-bulk-max-in-flight(4) + code-graph-bulk-max-in-flight(2) + worker-pool-size(4) + analytics-max-concurrency(2) = 12 must be < db-max-open-conns(12)",
			},
		},
		{
			name:   "one unbounded class still checks the floor",
			budget: poolbudget.Budget{MemoriesBulk: 0, CodeGraphBulk: 2, WorkerPool: 16, Analytics: 2, MaxOpenConns: 20},
			wantErr: []string{
				"memories-bulk-max-in-flight is unbounded",
				"code-graph-bulk-max-in-flight(2) + worker-pool-size(16) + analytics-max-concurrency(2) = 20",
				"db-max-open-conns(20)",
			},
		},
		{
			name:   "the other unbounded class is named too",
			budget: poolbudget.Budget{MemoriesBulk: 4, CodeGraphBulk: 0, WorkerPool: 16, Analytics: 2, MaxOpenConns: 20},
			wantErr: []string{
				"code-graph-bulk-max-in-flight is unbounded",
				"memories-bulk-max-in-flight(4) + worker-pool-size(16) + analytics-max-concurrency(2) = 22",
			},
		},
		{
			name:   "both unbounded leaves workers and analytics",
			budget: poolbudget.Budget{MemoriesBulk: 0, CodeGraphBulk: 0, WorkerPool: 4, Analytics: 2, MaxOpenConns: 6},
			wantErr: []string{
				"memories-bulk-max-in-flight and code-graph-bulk-max-in-flight are unbounded",
				"worker-pool-size(4) + analytics-max-concurrency(2) = 6",
			},
		},
		{
			name:   "both unbounded fits when the floor fits",
			budget: poolbudget.Budget{MemoriesBulk: 0, CodeGraphBulk: 0, WorkerPool: 4, Analytics: 2, MaxOpenConns: 7},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.budget.Check()
			if len(tc.wantErr) == 0 {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, want := range tc.wantErr {
				require.Contains(t, err.Error(), want)
			}
		})
	}
}
