package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/szymonrychu/tatara-memory/internal/poolbudget"
)

// The chart's values.yaml carries the same five numbers as this binary's flag
// defaults, with the reasoning from tatara-memory#82/#87/#89 attached, and it
// states the pool relationship as a production guarantee ("REFUSES TO START ...
// Defaults: 4 + 2 + 4 + 2 = 12 < 20"). Two hand-maintained copies of one
// contract with nothing comparing them is what tatara-memory#114 is about.
//
// Scope, deliberately narrow: this pins FIVE keys. The chart ships 26 env keys
// and the operator's ConfigMap sets 6 of them, so the other 15 remain
// unreachable on the deployed path and nothing here compares them.
func TestChartValuesMatchTheBudgetDefaults(t *testing.T) {
	raw, err := os.ReadFile("../../charts/tatara-memory/values.yaml")
	require.NoError(t, err)

	var values struct {
		WorkerPoolSize           int `yaml:"workerPoolSize"`
		MemoriesBulkMaxInFlight  int `yaml:"memoriesBulkMaxInFlight"`
		CodeGraphBulkMaxInFlight int `yaml:"codeGraphBulkMaxInFlight"`
		AnalyticsMaxConcurrency  int `yaml:"analyticsMaxConcurrency"`
		DBMaxOpenConns           int `yaml:"dbMaxOpenConns"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &values))

	os.Clearenv()
	def, err := loadConfig([]string{})
	require.NoError(t, err)

	for _, tc := range []struct {
		key         string
		chart, flag int
	}{
		{"workerPoolSize", values.WorkerPoolSize, def.WorkerPoolSize},
		{"memoriesBulkMaxInFlight", values.MemoriesBulkMaxInFlight, def.MemoriesBulkMaxInFlight},
		{"codeGraphBulkMaxInFlight", values.CodeGraphBulkMaxInFlight, def.CodeGraphBulkMaxInFlight},
		{"analyticsMaxConcurrency", values.AnalyticsMaxConcurrency, def.AnalyticsMaxConcurrency},
		{"dbMaxOpenConns", values.DBMaxOpenConns, def.DBMaxOpenConns},
	} {
		require.Equalf(t, tc.flag, tc.chart,
			"charts/tatara-memory/values.yaml %s is %d but this binary defaults to %d; the chart and the app are two copies of one contract",
			tc.key, tc.chart, tc.flag)
	}

	// The chart's documented arithmetic, asserted rather than written down.
	require.NoError(t, poolbudget.Budget{
		MemoriesBulk:  values.MemoriesBulkMaxInFlight,
		CodeGraphBulk: values.CodeGraphBulkMaxInFlight,
		WorkerPool:    values.WorkerPoolSize,
		Analytics:     values.AnalyticsMaxConcurrency,
		MaxOpenConns:  values.DBMaxOpenConns,
	}.Check(), "the chart's own defaults would refuse to start")
}
