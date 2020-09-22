package breaker

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type state int

const (
	stateClosed state = iota + 1
	stateHalfClosed
	stateOpen
)

// ErrCircuitIsOpen is returned when the circuit is open.
var ErrCircuitIsOpen = errors.New("circuit is open")

// ErrResponseConsideredError is used when a request did not result in an error,
// but the response is considered an error to the circuit breaker (see isErr field on the breaker).
type ErrResponseConsideredError struct {
	respCode int
}

// Error satisfies the error interface.
func (e ErrResponseConsideredError) Error() string {
	return fmt.Sprintf("response (status code %d) considered error to circuit breaker", e.respCode)
}

// Breaker is a circuit breaker that trips (opens) after having received only errors for x time.
type Breaker struct {
	// roundTripper is the RoundTripper / transport that actually does the round trip.
	roundTripper http.RoundTripper

	// timeUntilTrip defines after how long of receiving errors only to trip the breaker.
	timeUntilTrip time.Duration

	// timeUntilStartClosing defines after how long of being open the transition back to closed should start.
	timeUntilStartClosing time.Duration

	// hasRequestInTransit is true if there is a request in transit while being half closed.
	hasRequestInTransit bool

	// isErr is a function that determines if the response should be considered an error to the circuit breaker.
	// This determines which requests should trip the breaker if too many of them happen consecutively for
	// a too long period of time. Requests that return an error will always count as an erroneous request and are not
	// passed to this function.
	isErr func(resp *http.Response) bool

	mux          *sync.Mutex
	state        state
	hasErr       bool
	firstErrTime time.Time
	lastErrTime  time.Time
}

// NewBreaker returns a new breaker.
// For more information on parameters, see the Breaker struct.
func NewBreaker(
	roundTripper http.RoundTripper,
	timeUntilTrip time.Duration,
	timeUntilStartClosing time.Duration,
	isErr func(resp *http.Response) bool,
) *Breaker {
	return &Breaker{
		state:                 stateClosed,
		roundTripper:          roundTripper,
		timeUntilTrip:         timeUntilTrip,
		timeUntilStartClosing: timeUntilStartClosing,
		isErr:                 isErr,
		mux:                   &sync.Mutex{},
	}
}

// RoundTrip satisfies the http.RoundTripper interface.
func (t *Breaker) RoundTrip(req *http.Request) (*http.Response, error) {
	// Determine what to do.
	// Use a lock so that if the current round trip would trip the breaker,
	// the next round trip will see that and can act accordingly.
	t.mux.Lock()

	switch t.state {
	case stateOpen:
		if t.timeSinceLastErr() > t.timeUntilStartClosing {
			t.closeHalf()
		}

		t.mux.Unlock()
		return nil, ErrCircuitIsOpen
	case stateHalfClosed:
		// While the circuit is half closed, there may only be one request
		// in transit to see if we can close the circuit completely.
		if t.hasRequestInTransit {
			t.mux.Unlock()
			return nil, ErrCircuitIsOpen
		}

		t.hasRequestInTransit = true
	}

	t.mux.Unlock()

	resp, err := t.roundTripper.RoundTrip(req)

	t.mux.Lock()
	defer t.mux.Unlock()

	t.hasRequestInTransit = false

	if err != nil || t.isErr(resp) {
		if t.hasErr {
			t.lastErrTime = time.Now()

			// A previous execution of this block (within the mutex) may have opened the circuit.
			if t.state == stateOpen {
				return nil, ErrCircuitIsOpen
			}

			if t.timeSinceFirstErr() > t.timeUntilTrip {
				t.open()
				return nil, ErrCircuitIsOpen
			}
		} else {
			t.hasErr = true
			t.firstErrTime = time.Now()
		}

		if err != nil {
			return nil, err
		}

		return nil, ErrResponseConsideredError{resp.StatusCode}
	}

	// Reset and close the breaker.
	t.hasErr = false
	t.close()

	return resp, err
}

func (t *Breaker) open() {
	t.state = stateOpen
}

func (t *Breaker) closeHalf() {
	t.state = stateHalfClosed
}

func (t *Breaker) close() {
	t.state = stateClosed
}

func (t *Breaker) timeSinceFirstErr() time.Duration {
	return time.Now().Sub(t.firstErrTime)
}

func (t *Breaker) timeSinceLastErr() time.Duration {
	return time.Now().Sub(t.lastErrTime)
}
