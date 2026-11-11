package host

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// openingHolds is a HoldService whose Open waits for a signal, so a test can
// cancel the request while the open is in flight.
type openingHolds struct {
	mutableHolds
	started chan struct{}
	proceed chan struct{}

	mu       sync.Mutex
	openErr  error
	released []string
}

func (o *openingHolds) Open(ctx context.Context, _ OpenHoldRequest) (OpenedHold, error) {
	close(o.started)
	<-o.proceed
	o.mu.Lock()
	defer o.mu.Unlock()
	o.openErr = ctx.Err()
	return OpenedHold{HoldStatus: HoldStatus{ID: "hold-1"}, Lease: "lease"}, nil
}

func (o *openingHolds) Release(_ context.Context, id string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.released = append(o.released, id)
	return nil
}

func TestCancelledOpenRunsToTheEndAndThenReleasesTheHold(t *testing.T) {
	holds := &openingHolds{started: make(chan struct{}), proceed: make(chan struct{})}
	handler := newHandler("test", time.Unix(1000, 0), Services{Holds: holds}, &gate{})

	reqCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/holds", strings.NewReader(`{"endpointId":"ep/AAAAAAAAAAAAAAAAAAAAAA","kinds":["motion"]}`)).WithContext(reqCtx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}()

	<-holds.started
	cancel() // the CLI was interrupted after start-session was dispatched
	close(holds.proceed)
	<-done

	holds.mu.Lock()
	defer holds.mu.Unlock()
	require.NoError(t, holds.openErr, "the open must not see the caller's cancellation")
	require.Equal(t, []string{"hold-1"}, holds.released, "a hold nobody received a lease for is released, sending end-session")
}
