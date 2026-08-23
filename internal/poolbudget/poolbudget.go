// Package poolbudget holds the one arithmetic relationship that keeps the
// shared Postgres pool from being oversubscribed, as an artifact that can be
// tested instead of a comment that has to be believed.
//
// Every term below can hold one pooled connection simultaneously: each admitted
// bulk request for its whole transaction (tatara-memory#82), each ingest worker,
// and each in-flight analytics recompute for the whole of its write transaction
// (tatara-memory#89). If those alone can take every connection, then reads,
// /readyz and job creation starve exactly as they did before admission control
// existed, and the limiter is not protecting anything.
//
// The rule: the reserved floor must be strictly less than the pool. A bulk
// budget of 0 means that class is UNBOUNDED, so it contributes no fixed term -
// but it can only ever add to the floor, never subtract from it, so the
// remaining terms are still checked. This is the property
// charts/tatara-memory/values.yaml states as "REFUSES TO START"; before
// tatara-memory#114 a single 0 skipped the whole check and let a configuration
// that reserved the entire pool start cleanly.
package poolbudget

import (
	"fmt"
	"strings"
)

// Budget is the set of concurrency limits sized against one connection pool.
// The two bulk budgets are 0 when that class is unbounded; the other three are
// always positive on a validated config.
type Budget struct {
	MemoriesBulk  int
	CodeGraphBulk int
	WorkerPool    int
	Analytics     int
	MaxOpenConns  int
}

// Check reports whether the reserved floor fits in the pool. The error
// enumerates every term with its value, and names any unbounded class, so that
// the arithmetic in the message is the arithmetic the reader can verify.
func (b Budget) Check() error {
	var (
		terms     []string
		unbounded []string
		reserved  int
	)
	add := func(name string, n int) {
		terms = append(terms, fmt.Sprintf("%s(%d)", name, n))
		reserved += n
	}
	for _, c := range []struct {
		name string
		n    int
	}{
		{"memories-bulk-max-in-flight", b.MemoriesBulk},
		{"code-graph-bulk-max-in-flight", b.CodeGraphBulk},
	} {
		if c.n > 0 {
			add(c.name, c.n)
			continue
		}
		unbounded = append(unbounded, c.name)
	}
	add("worker-pool-size", b.WorkerPool)
	add("analytics-max-concurrency", b.Analytics)

	if reserved < b.MaxOpenConns {
		return nil
	}
	sum := strings.Join(terms, " + ")
	if len(unbounded) == 0 {
		return fmt.Errorf(
			"admission budgets oversubscribe the DB pool: %s = %d must be < db-max-open-conns(%d)",
			sum, reserved, b.MaxOpenConns)
	}
	verb := "is"
	if len(unbounded) > 1 {
		verb = "are"
	}
	return fmt.Errorf(
		"admission budgets oversubscribe the DB pool: %s %s unbounded (0), and the reserved floor that remains, %s = %d, must be < db-max-open-conns(%d): an unbounded class can only add to that floor",
		strings.Join(unbounded, " and "), verb, sum, reserved, b.MaxOpenConns)
}
