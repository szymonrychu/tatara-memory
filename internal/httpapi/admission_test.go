package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

// blockingHandler parks every request until release is closed, so a test can
// hold admission slots open for as long as it needs. Each entry is announced
// on entered.
func blockingHandler(release <-chan struct{}, entered chan<- struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if entered != nil {
			entered <- struct{}{}
		}
		<-release
		w.WriteHeader(http.StatusOK)
	})
}

func newTestLimiter(t *testing.T, cfg AdmissionConfig) *AdmissionLimiter {
	t.Helper()
	if cfg.Class == "" {
		cfg.Class = "test"
	}
	cfg.Metrics = NewAdmissionMetrics(prometheus.NewRegistry(), cfg.Class)
	return NewAdmissionLimiter(cfg)
}

// A saturated class must SHED with 429 + Retry-After, not 503. 503 tells the
// caller the service is unavailable (a server fault, and what pages an
// operator); shed load is neither - it is retryable backpressure, which is
// exactly what 429 means. tatara-memory#80.
func TestAdmissionLimiter_ShedsWith429AndRetryAfter(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	lim := newTestLimiter(t, AdmissionConfig{
		MaxInFlight: 1,
		Wait:        20 * time.Millisecond,
		RetryAfter:  5 * time.Second,
	})
	h := lim.Middleware(blockingHandler(release, entered))

	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/memories:bulk", nil))
	<-entered // the single slot is now held

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/memories:bulk", nil))
	close(release)

	require.Equal(t, http.StatusTooManyRequests, w.Code,
		"a request shed by admission control must be 429 (retryable backpressure), never 503")
	ra := w.Header().Get("Retry-After")
	require.NotEmpty(t, ra, "shed responses must carry Retry-After so clients back off instead of hot-retrying")
	secs, err := strconv.Atoi(ra)
	require.NoError(t, err, "Retry-After must be delta-seconds")
	require.GreaterOrEqual(t, secs, 5)

	var env errorEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	require.NotEmpty(t, env.Error)
}

// The whole point of admission control: concurrency is bounded, so
// /code-graph:bulk transactions can never take more DB pool connections than
// the budget allows and starve /memories:bulk. tatara-memory#82.
func TestAdmissionLimiter_BoundsConcurrency(t *testing.T) {
	const limit = 2
	const callers = 12

	var cur, peak atomic.Int64
	lim := newTestLimiter(t, AdmissionConfig{
		MaxInFlight: limit,
		Wait:        5 * time.Second,
		RetryAfter:  time.Second,
	})
	h := lim.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := cur.Add(1)
		for {
			m := peak.Load()
			if n <= m || peak.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		cur.Add(-1)
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/code-graph:bulk", nil))
		}()
	}
	wg.Wait()

	require.LessOrEqual(t, peak.Load(), int64(limit),
		"admission control must never let more than MaxInFlight handlers run at once")
	require.Positive(t, peak.Load())
}

// A short saturation spike must be absorbed by the wait budget, not shed:
// shedding a request that could have been served in milliseconds would turn
// normal burstiness into client-visible errors.
func TestAdmissionLimiter_AdmitsWhenSlotFreesWithinWait(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	lim := newTestLimiter(t, AdmissionConfig{
		MaxInFlight: 1,
		Wait:        5 * time.Second,
		RetryAfter:  time.Second,
	})
	h := lim.Middleware(blockingHandler(release, entered))

	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/memories:bulk", nil))
	<-entered

	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/memories:bulk", nil))
		done <- w.Code
	}()

	time.Sleep(20 * time.Millisecond)
	close(release)

	select {
	case code := <-done:
		require.Equal(t, http.StatusOK, code, "a slot freeing within the wait budget must admit, not shed")
	case <-time.After(10 * time.Second):
		t.Fatal("queued request never admitted after the slot was freed")
	}
}

// MaxInFlight 0 disables the limiter entirely (the repo's "0 disables"
// convention for every other bound), so a deployment can turn it off and every
// caller that does not opt in behaves exactly as before.
func TestAdmissionLimiter_DisabledWhenZero(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 4)
	lim := newTestLimiter(t, AdmissionConfig{MaxInFlight: 0, Wait: time.Millisecond})
	h := lim.Middleware(blockingHandler(release, entered))

	for i := 0; i < 4; i++ {
		go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/memories:bulk", nil))
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("a disabled limiter must not bound concurrency")
		}
	}
	close(release)
}

// A caller that disconnects while queued is not a server error and not shed
// load: it keeps the existing 499 contract (kept off the 5xx dashboards) and
// must never be counted as a shed.
func TestAdmissionLimiter_ClientCancelWhileQueuedIs499(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{}, 1)
	lim := newTestLimiter(t, AdmissionConfig{
		MaxInFlight: 1,
		Wait:        30 * time.Second,
		RetryAfter:  time.Second,
	})
	h := lim.Middleware(blockingHandler(release, entered))

	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/memories:bulk", nil))
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/memories:bulk", nil).WithContext(ctx)

	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		done <- w.Code
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case code := <-done:
		require.Equal(t, 499, code, "a client that disconnects while queued is 499, not 429 and not 5xx")
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled request never returned")
	}

	require.Zero(t, counterValue(t, lim.metrics.total, "test", admissionShed),
		"a client cancellation must not be counted as shed load")
}

// Shed load must be VISIBLE: hard rule 13 (metrics for everything that counts,
// times out, or can fail). Without these, a shedding service looks identical to
// a healthy one on the dashboards.
func TestAdmissionLimiter_ShedIsObservable(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	lim := newTestLimiter(t, AdmissionConfig{
		Class:       "memories_bulk",
		MaxInFlight: 1,
		Wait:        10 * time.Millisecond,
		RetryAfter:  time.Second,
	})
	h := lim.Middleware(blockingHandler(release, entered))

	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/memories:bulk", nil))
	<-entered

	require.Equal(t, 1.0, gaugeValue(t, lim.metrics.inFlight, "memories_bulk"),
		"in-flight gauge must track the admitted request")

	for i := 0; i < 3; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/memories:bulk", nil))
	}
	close(release)

	require.Equal(t, 3.0, counterValue(t, lim.metrics.total, "memories_bulk", admissionShed))
	require.Equal(t, 0.0, gaugeValue(t, lim.metrics.waiting, "memories_bulk"),
		"the waiting gauge must drain back to zero after every shed")
	require.Equal(t, uint64(3), histogramCount(t, lim.metrics.wait, "memories_bulk", admissionShed),
		"every shed must observe how long it waited before being rejected")
}

// The three read helpers below pull one label combination out of a *Vec via the
// dto protobuf, instead of prometheus/client_golang's testutil - testutil pulls
// an extra module into go.mod that nothing else in this repo needs, and it
// cannot read histograms at all.
func writeMetric(t *testing.T, m prometheus.Metric) *dto.Metric {
	t.Helper()
	var pb dto.Metric
	require.NoError(t, m.Write(&pb))
	return &pb
}

func gaugeValue(t *testing.T, vec *prometheus.GaugeVec, labels ...string) float64 {
	t.Helper()
	g, err := vec.GetMetricWithLabelValues(labels...)
	require.NoError(t, err)
	return writeMetric(t, g).GetGauge().GetValue()
}

func counterValue(t *testing.T, vec *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	c, err := vec.GetMetricWithLabelValues(labels...)
	require.NoError(t, err)
	return writeMetric(t, c).GetCounter().GetValue()
}

func histogramCount(t *testing.T, vec *prometheus.HistogramVec, labels ...string) uint64 {
	t.Helper()
	o, err := vec.GetMetricWithLabelValues(labels...)
	require.NoError(t, err)
	m, ok := o.(prometheus.Metric)
	require.True(t, ok)
	return writeMetric(t, m).GetHistogram().GetSampleCount()
}

// Retry-After is spread over [base, base+base/2] so a shed wave (a mass
// concurrent re-ingest, tatara-memory#87) does not re-arrive in lockstep and
// re-saturate the service at exactly the same instant.
func TestAdmissionLimiter_RetryAfterIsJittered(t *testing.T) {
	lim := newTestLimiter(t, AdmissionConfig{MaxInFlight: 1, RetryAfter: 10 * time.Second})
	seen := map[int]bool{}
	for i := 0; i < 40; i++ {
		v := lim.retryAfterSeconds()
		require.GreaterOrEqual(t, v, 10)
		require.LessOrEqual(t, v, 15)
		seen[v] = true
	}
	require.Greater(t, len(seen), 1, "Retry-After must not be a single constant for every shed response")
}

// A sub-second RetryAfter must still advertise a legal delta-seconds value:
// "Retry-After: 0" invites an immediate hot retry, which is the opposite of
// backpressure.
func TestAdmissionLimiter_RetryAfterNeverZero(t *testing.T) {
	lim := newTestLimiter(t, AdmissionConfig{MaxInFlight: 1, RetryAfter: 100 * time.Millisecond})
	require.GreaterOrEqual(t, lim.retryAfterSeconds(), 1)
}
