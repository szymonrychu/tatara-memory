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
	ListOlderThan(ctx context.Context, maxAge time.Duration) ([]string, error)
}

// Reaper periodically removes tombstones once lightrag confirms the document
// has been deleted, plus a 24h forced sweep that re-verifies each aged
// tombstone upstream before deleting (skipping still-present docs).
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
			Help: "Tombstone operations by type (reaped, forced, force_skipped_still_present, created, check_error)",
		},
		[]string{"op"},
	)
	reg.MustRegister(m)
	// Pre-initialize all label values so Gather always returns the family.
	for _, op := range []string{"reaped", "forced", "force_skipped_still_present", "created", "check_error"} {
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
		resp, err := r.lightrag.TrackStatus(ctx, id)
		if err != nil {
			var he *lightrag.HTTPError
			if errors.As(err, &he) && he.Status == http.StatusNotFound {
				r.reapConfirmed(ctx, id)
			} else {
				r.metric.WithLabelValues("check_error").Inc()
				r.logger.Warn("tombstone check error", "track_id", id, "err", err)
			}
		} else if resp == nil || len(resp.Documents) == 0 {
			// HTTP 200 with empty (or nil) Documents: upstream has no record of this
			// doc, treat as confirmed gone (mirrors GetMemory's own ErrNotFound path).
			r.reapConfirmed(ctx, id)
		}
		// else: err==nil and docs present -> still being deleted, leave tombstone
	}
	r.forceTick(ctx)
}

// forceTick is the 24h forced-reap path. Unlike the fast path, it calls
// TrackStatus per candidate id and only deletes tombstones whose upstream doc
// is confirmed gone (404 or empty-docs). Tombstones whose doc is still present
// are skipped and counted as force_skipped_still_present so the resurrection is
// observable without silently un-deleting content.
func (r *Reaper) forceTick(ctx context.Context) {
	aged, err := r.store.ListOlderThan(ctx, r.maxAge)
	if err != nil {
		r.logger.Error("tombstone list older", "err", err)
		return
	}
	for _, id := range aged {
		if err := ctx.Err(); err != nil {
			return
		}
		resp, err := r.lightrag.TrackStatus(ctx, id)
		if err != nil {
			var he *lightrag.HTTPError
			if errors.As(err, &he) && he.Status == http.StatusNotFound {
				// confirmed gone via 404
				r.reapConfirmedForced(ctx, id)
			} else {
				// transient / unknown: keep tombstone, log WARN
				r.metric.WithLabelValues("check_error").Inc()
				r.logger.Warn("tombstone force-check error", "track_id", id, "err", err)
			}
		} else if resp == nil || len(resp.Documents) == 0 {
			// 200 + empty: confirmed gone
			r.reapConfirmedForced(ctx, id)
		} else {
			// doc still present: skip, emit observable metric + WARN
			r.metric.WithLabelValues("force_skipped_still_present").Inc()
			r.logger.Warn("tombstone force-reap skipped: doc still present",
				"track_id", id)
		}
	}
}

func (r *Reaper) reapConfirmed(ctx context.Context, id string) {
	if derr := r.store.Delete(ctx, id); derr == nil {
		r.metric.WithLabelValues("reaped").Inc()
		r.logger.Info("tombstone reaped", "track_id", id)
	} else {
		r.logger.Error("tombstone reap delete", "track_id", id, "err", derr)
	}
}

func (r *Reaper) reapConfirmedForced(ctx context.Context, id string) {
	if derr := r.store.Delete(ctx, id); derr == nil {
		r.metric.WithLabelValues("forced").Inc()
		r.logger.Info("tombstone forced reap confirmed", "track_id", id)
	} else {
		r.logger.Error("tombstone forced reap delete", "track_id", id, "err", derr)
	}
}
