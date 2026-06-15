package memory

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

// TombstoneReapBatchSize is the maximum number of tombstones processed per tick
// via the confirm (TrackStatus 404) path. A 24h forced-TTL backstop handles any
// backlog beyond this limit. 1000 is chosen to keep each tick well under 5 min
// even at the upstream's slowest observed response time (~200ms/id).
const TombstoneReapBatchSize = 1000

// trackStatuser is the subset of the lightrag client used by the reaper.
// Defining it here keeps the package boundary narrow and the reaper testable
// with a fake.
type trackStatuser interface {
	TrackStatus(ctx context.Context, trackID string) (*lightrag.TrackStatusResponse, error)
}

// reapStore is the minimal store interface the Reaper needs. *TombstoneStore
// satisfies it; tests may inject a fake.
type reapStore interface {
	List(ctx context.Context, limit int) ([]string, error)
	Delete(ctx context.Context, id string) error
	ReapOlderThan(ctx context.Context, maxAge time.Duration) (int64, error)
}

// Reaper periodically removes tombstones once lightrag confirms the document
// has been deleted, plus an unconditional 24h TTL fallback.
type Reaper struct {
	store    reapStore
	lightrag trackStatuser
	logger   *slog.Logger
	interval time.Duration
	maxAge   time.Duration
	metric   *prometheus.CounterVec
}

// NewReaper constructs a Reaper. Registers tatara_memory_tombstone_total{op}
// against the given registerer.
func NewReaper(store *TombstoneStore, lr trackStatuser, logger *slog.Logger, reg prometheus.Registerer) *Reaper {
	return newReaper(store, lr, logger, reg)
}

func newReaper(store reapStore, lr trackStatuser, logger *slog.Logger, reg prometheus.Registerer) *Reaper {
	m := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tatara_memory_tombstone_total",
			Help: "Tombstone operations by type (reaped, forced, created, check_error)",
		},
		[]string{"op"},
	)
	reg.MustRegister(m)
	// Pre-initialize all label values so Gather always returns the family.
	for _, op := range []string{"reaped", "forced", "created", "check_error"} {
		m.WithLabelValues(op)
	}
	return &Reaper{
		store:    store,
		lightrag: lr,
		logger:   logger,
		interval: 5 * time.Minute,
		maxAge:   24 * time.Hour,
		metric:   m,
	}
}

// IncCreated increments tatara_memory_tombstone_total{op="created"}.
// Called by TombstoneStore.Mark via SetMarkCounter.
func (r *Reaper) IncCreated() {
	r.metric.WithLabelValues("created").Inc()
}

// Run blocks until ctx is canceled, ticking every interval.
func (r *Reaper) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.tick(ctx)
		}
	}
}

func (r *Reaper) tick(ctx context.Context) {
	ids, err := r.store.List(ctx, TombstoneReapBatchSize)
	if err != nil {
		r.logger.Error("tombstone list", "err", err)
		return
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return // context cancelled; abort early to avoid serial timeouts
		}
		_, err := r.lightrag.TrackStatus(ctx, id)
		if err != nil {
			var he *lightrag.HTTPError
			if errors.As(err, &he) && he.Status == http.StatusNotFound {
				if derr := r.store.Delete(ctx, id); derr == nil {
					r.metric.WithLabelValues("reaped").Inc()
					r.logger.Info("tombstone reaped", "track_id", id)
				} else {
					r.logger.Error("tombstone reap delete", "track_id", id, "err", derr)
				}
			} else {
				r.metric.WithLabelValues("check_error").Inc()
				r.logger.Warn("tombstone check error", "track_id", id, "err", err)
			}
		}
	}
	forced, err := r.store.ReapOlderThan(ctx, r.maxAge)
	if err != nil {
		r.logger.Error("tombstone reap forced", "err", err)
		return
	}
	if forced > 0 {
		r.metric.WithLabelValues("forced").Add(float64(forced))
		r.logger.Info("tombstone forced reap", "count", forced)
	}
}
