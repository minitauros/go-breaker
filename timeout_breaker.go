package breaker

import (
	"errors"
	"net/http"
	"sync"
	"time"
)

type timeoutBreakerState int

const (
	timeoutBreakerStateClosed timeoutBreakerState = iota
	timeoutBreakerStateHalfOpen
	timeoutBreakerStateOpen
)

// CircuitIsOpenErr is returned when the circuit is open.
var CircuitIsOpenErr = errors.New("circuit is open")

// TimeoutBreaker is a circuit breaker that opens after having received only errors for x time.
type TimeoutBreaker struct {
	// tripper is the round tripper / transport that actually does the round trip.
	tripper http.RoundTripper

	// timeUntilOpen defines after how long of receiving errors only to open the circuit.
	timeUntilOpen time.Duration

	// timeUntilHalfClosed defines after how long of being open the transition back to closed should start.
	timeUntilHalfClosed time.Duration

	// hasRequestInTransit is true if there is a request in transit while being half closed.
	hasRequestInTransit bool

	// isErr is a function that determines if the response should be considered an error to the circuit breaker.
	isErr func(resp *http.Response) bool

	mux          *sync.Mutex
	state        timeoutBreakerState
	hasErr       bool
	firstErrTime time.Time
	lastErrTime  time.Time
}

// NewTimeoutBreaker returns a new timeout breaker.
// For more information on parameters, see the TimeoutBreaker struct.
func NewTimeoutBreaker(
	tripper http.RoundTripper,
	timeUntilOpen time.Duration,
	timeUntilClose time.Duration,
	isErr func(resp *http.Response) bool,
) *TimeoutBreaker {
	return &TimeoutBreaker{
		state:               timeoutBreakerStateClosed,
		tripper:             tripper,
		timeUntilOpen:       timeUntilOpen,
		timeUntilHalfClosed: timeUntilClose,
		isErr:               isErr,
		mux:                 &sync.Mutex{},
	}
}

// RoundTrip satisfies the http.RoundTripper interface.
func (t *TimeoutBreaker) RoundTrip(req *http.Request) (*http.Response, error) {
	// Determine what to do.
	// Use a lock so that if the current trip would open the circuit,
	// the next trip will see that.
	t.mux.Lock()

	switch t.state {
	case timeoutBreakerStateOpen:
		if t.timeSinceLastErr() > t.timeUntilHalfClosed {
			t.closeHalf()
		}

		t.mux.Unlock()
		return nil, CircuitIsOpenErr
	case timeoutBreakerStateHalfOpen:
		// While the circuit is half closed, there may only be one request
		// in transit to see if we can close the circuit completely.
		if t.hasRequestInTransit {
			t.mux.Unlock()
			return nil, CircuitIsOpenErr
		}

		t.hasRequestInTransit = true
	}

	t.mux.Unlock()

	resp, err := t.tripper.RoundTrip(req)

	t.mux.Lock()
	defer t.mux.Unlock()

	if err != nil || t.isErr(resp) {
		if t.hasErr {
			t.lastErrTime = time.Now()

			// A previous execution of this block (within the mutex) may have opened the circuit.
			if t.state == timeoutBreakerStateOpen {
				return nil, CircuitIsOpenErr
			}

			if t.timeSinceFirstErr() > t.timeUntilOpen {
				t.open()
				return nil, CircuitIsOpenErr
			}
		} else {
			t.hasErr = true
			t.firstErrTime = time.Now()
		}

		return resp, err
	}

	// Reset and close the breaker.
	t.hasRequestInTransit = false
	t.hasErr = false
	t.close()

	return resp, err
}

func (t *TimeoutBreaker) open() {
	t.state = timeoutBreakerStateOpen
}

func (t *TimeoutBreaker) closeHalf() {
	t.state = timeoutBreakerStateHalfOpen
}

func (t *TimeoutBreaker) close() {
	t.state = timeoutBreakerStateClosed
}

func (t *TimeoutBreaker) timeSinceFirstErr() time.Duration {
	return time.Now().Sub(t.firstErrTime)
}

func (t *TimeoutBreaker) timeSinceLastErr() time.Duration {
	return time.Now().Sub(t.lastErrTime)
}
