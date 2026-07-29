package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/szymonrychu/tatara-memory/internal/memory"

	"github.com/stretchr/testify/require"
)

// The 429-vs-503 contract (tatara-memory#80). Backpressure - we are saturated,
// or an upstream told us it is busy - is a RETRYABLE condition the caller
// caused by pushing too hard, and belongs on 429 with Retry-After. 503 is
// reserved for genuine unavailability: the upstream is actually failing or
// unreachable, which is a server fault worth paging on. Reporting shed load as
// 503 is what made every concurrent-ingest burst look like an outage on the
// 5xx-ratio alert.
func TestMapServiceError_BackpressureIs429_UnavailabilityIs503(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		want       int
		retryAfter bool
	}{
		{
			name:       "upstream busy is backpressure",
			err:        fmt.Errorf("%w: delete returned status=%q", memory.ErrBusy, "busy"),
			want:       http.StatusTooManyRequests,
			retryAfter: true,
		},
		{
			name:       "our own deadline firing under load is backpressure",
			err:        fmt.Errorf("ingest: create job: begin tx: %w", context.DeadlineExceeded),
			want:       http.StatusTooManyRequests,
			retryAfter: true,
		},
		{
			name:       "genuine upstream unavailability stays 503",
			err:        fmt.Errorf("%w: lightrag 502", memory.ErrTransient),
			want:       http.StatusServiceUnavailable,
			retryAfter: true,
		},
		{
			name: "client disconnect stays 499",
			err:  context.Canceled,
			want: 499,
		},
		{
			name: "unclassified upstream failure stays 502",
			err:  fmt.Errorf("%w: bad payload", memory.ErrUpstream),
			want: http.StatusBadGateway,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/memories:bulk", nil)
			mapServiceError(w, r, c.err)
			require.Equal(t, c.want, w.Code)
			if c.retryAfter {
				ra := w.Header().Get("Retry-After")
				require.NotEmpty(t, ra, "every retryable status must tell the caller when to come back")
				secs, err := strconv.Atoi(ra)
				require.NoError(t, err)
				require.Positive(t, secs)
			}
		})
	}
}
