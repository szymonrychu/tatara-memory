package memory

import "context"

// TickForTest exposes the unexported tick method to package memory_test.
func TickForTest(r *Reaper, ctx context.Context) {
	r.tick(ctx)
}
