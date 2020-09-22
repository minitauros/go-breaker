package breaker

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func Test_Breaker_RoundTrip(t *testing.T) {
	Convey("Breaker.RoundTrip()", t, func() {
		b := &Breaker{
			roundTripper:          http.DefaultTransport,
			timeUntilTrip:         10 * time.Millisecond,
			timeUntilStartClosing: 10 * time.Millisecond,
			isErr: func(resp *http.Response) bool {
				return false
			},
			mux:   &sync.Mutex{},
			state: stateClosed,
		}

		// Set up a testserver. We will use this test server to ensure that no request is actually sent if it
		// should not be sent.
		var reqReceivedCount int
		var respStatus = http.StatusOK
		var sleepDur time.Duration
		testServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if sleepDur > 0 {
				time.Sleep(sleepDur)
			}
			rw.WriteHeader(respStatus)
			_, err := rw.Write([]byte(`{}`))
			fatal(err)
			reqReceivedCount++
		}))
		defer testServer.Close()

		req, err := http.NewRequest(http.MethodGet, testServer.URL, nil)
		fatal(err)

		Convey("If state is open", func() {
			b.state = stateOpen

			Convey("If must become half closed", func() {
				b.lastErrTime = time.Now().Add(-2 * b.timeUntilTrip)

				Convey("Closes half and returns err", func() {
					resp, err := b.RoundTrip(req)

					So(resp, ShouldBeNil)
					So(err, ShouldNotBeNil)
					So(err, ShouldHaveSameTypeAs, ErrCircuitIsOpen)
					So(reqReceivedCount, ShouldEqual, 0)
					So(b.state, ShouldEqual, stateHalfClosed)
				})
			})

			Convey("If must not become half closed", func() {
				b.lastErrTime = time.Now()

				Convey("Returns err", func() {
					resp, err := b.RoundTrip(req)

					So(resp, ShouldBeNil)
					So(err, ShouldNotBeNil)
					So(err, ShouldHaveSameTypeAs, ErrCircuitIsOpen)
					So(reqReceivedCount, ShouldEqual, 0)
					So(b.state, ShouldEqual, stateOpen)
				})
			})
		})

		Convey("If state is half closed", func() {
			b.state = stateHalfClosed

			Convey("If there are no request in transit", func() {
				b.hasRequestInTransit = false

				Convey("If the req succeeds, returns a response and closes the circuit", func() {
					resp, err := b.RoundTrip(req)

					So(err, ShouldBeNil)
					So(resp, ShouldNotBeNil)
					So(resp.StatusCode, ShouldEqual, http.StatusOK)
					So(reqReceivedCount, ShouldEqual, 1)
					So(b.hasRequestInTransit, ShouldBeFalse)
					So(b.hasErr, ShouldBeFalse)
					So(b.state, ShouldEqual, stateClosed)
				})
			})

			Convey("If there is a request in transit", func() {
				b.hasRequestInTransit = true

				Convey("Returns err", func() {
					resp, err := b.RoundTrip(req)

					So(resp, ShouldBeNil)
					So(err, ShouldNotBeNil)
					So(err, ShouldHaveSameTypeAs, ErrCircuitIsOpen)
					So(reqReceivedCount, ShouldEqual, 0)
					So(b.state, ShouldEqual, stateHalfClosed)
				})
			})
		})

		Convey("If state is closed", func() {
			b.state = stateClosed

			Convey("If request is incorrect", func() {
				req.URL = nil // Will cause an error.

				Convey("If an error has already occured before", func() {
					b.hasErr = true

					Convey("If the previous simultaneous request resulted in opening the circuit, returns err", func() {
						// To simulate two simultaneous requests, we will make the test server sleep
						// for each request, so that both routines will enter into the post-request block
						// at the same time, while only one at a time can get access.

						sleepDur = 5 * time.Millisecond

						ch1 := make(chan struct{})
						ch2 := make(chan struct{})

						var resp1, resp2 *http.Response
						var err1, err2 error

						go func() {
							resp1, err1 = b.RoundTrip(&http.Request{})
							close(ch1)
						}()

						go func() {
							resp2, err2 = b.RoundTrip(&http.Request{})
							close(ch2)
						}()

						var resp *http.Response
						var err error

						select {
						case <-ch1:
							resp = resp1
							err = err1
						case <-ch2:
							resp = resp2
							err = err2
						}

						So(resp, ShouldBeNil)
						So(err, ShouldNotBeNil)
						So(err, ShouldHaveSameTypeAs, ErrCircuitIsOpen)
						So(reqReceivedCount, ShouldEqual, 0)
						So(b.state, ShouldEqual, stateOpen)
					})

					Convey("If this request must open the circuit", func() {
						b.firstErrTime = time.Now().Add(-2 * b.timeUntilTrip)

						Convey("Opens the circuit and returns err", func() {
							resp, err := b.RoundTrip(req)

							So(resp, ShouldBeNil)
							So(err, ShouldNotBeNil)
							So(err, ShouldHaveSameTypeAs, ErrCircuitIsOpen)
							So(reqReceivedCount, ShouldEqual, 0)
							So(b.state, ShouldEqual, stateOpen)
						})
					})
				})

				Convey("If no error has occured before", func() {
					b.hasErr = false

					Convey("Returns err", func() {
						resp, err := b.RoundTrip(req)

						So(resp, ShouldBeNil)
						So(err, ShouldNotBeNil)
						So(err, ShouldHaveSameTypeAs, ErrCircuitIsOpen)
						So(reqReceivedCount, ShouldEqual, 0)
						So(b.state, ShouldEqual, stateClosed)
					})
				})
			})

			Convey("If response is considered an error", func() {
				// Make the test server respond with an internal server error status.
				respStatus = http.StatusInternalServerError

				b.isErr = func(resp *http.Response) bool {
					return resp.StatusCode == http.StatusInternalServerError
				}

				Convey("If an error has already occured before", func() {
					b.hasErr = true

					Convey("If the previous simultaneous request resulted in opening the circuit, returns err", func() {
						// To simulate two simultaneous requests, we will make the test server sleep
						// for each request, so that both routines will enter into the post-request block
						// at the same time, while only one at a time can get access.

						sleepDur = 5 * time.Millisecond

						ch1 := make(chan struct{})
						ch2 := make(chan struct{})

						var resp1, resp2 *http.Response
						var err1, err2 error

						go func() {
							resp1, err1 = b.RoundTrip(req)
							close(ch1)
						}()

						go func() {
							resp2, err2 = b.RoundTrip(req)
							close(ch2)
						}()

						var resp *http.Response
						var err error

						select {
						case <-ch1:
							resp = resp1
							err = err1
						case <-ch2:
							resp = resp2
							err = err2
						}

						So(resp, ShouldBeNil)
						So(err, ShouldNotBeNil)
						So(err, ShouldHaveSameTypeAs, ErrCircuitIsOpen)
						So(reqReceivedCount, ShouldBeGreaterThan, 0)
						So(b.state, ShouldEqual, stateOpen)
					})

					Convey("If this request must open the circuit", func() {
						b.firstErrTime = time.Now().Add(-2 * b.timeUntilTrip)

						Convey("Opens the circuit and returns err", func() {
							resp, err := b.RoundTrip(req)

							So(resp, ShouldBeNil)
							So(err, ShouldNotBeNil)
							So(err, ShouldHaveSameTypeAs, ErrCircuitIsOpen)
							So(reqReceivedCount, ShouldEqual, 1)
							So(b.state, ShouldEqual, stateOpen)
						})
					})
				})

				Convey("If no error has occured before", func() {
					b.hasErr = false

					Convey("Returns err", func() {
						resp, err := b.RoundTrip(req)

						So(resp, ShouldBeNil)
						So(err, ShouldNotBeNil)
						So(err, ShouldHaveSameTypeAs, ErrResponseConsideredError{})
						So(err.(ErrResponseConsideredError).respCode, ShouldEqual, http.StatusInternalServerError)
						So(reqReceivedCount, ShouldEqual, 1)
						So(b.state, ShouldEqual, stateClosed)
					})
				})
			})

			Convey("If the request succeeds without errors", func() {
				Convey("Returns response and resets the circuit", func() {
					resp, err := b.RoundTrip(req)

					So(err, ShouldBeNil)
					So(resp, ShouldNotBeNil)
					So(resp.StatusCode, ShouldEqual, http.StatusOK)
					So(reqReceivedCount, ShouldEqual, 1)
					So(b.hasRequestInTransit, ShouldBeFalse)
					So(b.hasErr, ShouldBeFalse)
					So(b.state, ShouldEqual, stateClosed)
				})
			})
		})
	})
}

func Test_Breaker(t *testing.T) {
	Convey("Breaker", t, func() {
		b := &Breaker{
			roundTripper:          http.DefaultTransport,
			timeUntilTrip:         5 * time.Millisecond,
			timeUntilStartClosing: 5 * time.Millisecond,
			isErr: func(resp *http.Response) bool {
				return resp.StatusCode == http.StatusInternalServerError
			},
			mux:   &sync.Mutex{},
			state: stateClosed,
		}

		client := &http.Client{
			Transport: b,
		}

		respStatus := http.StatusOK
		testServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			rw.WriteHeader(respStatus)
			_, err := rw.Write([]byte(`{}`))
			fatal(err)
		}))
		defer testServer.Close()

		Convey("Scenario", func() {
			var resp *http.Response
			var err error

			// Make test server return error.
			respStatus = http.StatusInternalServerError

			// First req results in err.
			resp, err = client.Get(testServer.URL)
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, ErrResponseConsideredError{respCode: http.StatusInternalServerError}.Error())
			So(b.state, ShouldEqual, stateClosed)

			// Second one as well, but does not yet open circuit, because within timeUntilTrip limit.
			resp, err = client.Get(testServer.URL)
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, ErrResponseConsideredError{respCode: http.StatusInternalServerError}.Error())
			So(b.state, ShouldEqual, stateClosed)

			// Sleep to force the circuit to trip.
			time.Sleep(b.timeUntilTrip)

			// Results in circuit opening because after timeUntilTrip.
			resp, err = client.Get(testServer.URL)
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, ErrCircuitIsOpen.Error())
			So(b.state, ShouldEqual, stateOpen)

			time.Sleep(b.timeUntilStartClosing)

			// Req should make the circuit close half, because we slept until
			// the circuit was allowed to start closing again.
			resp, err = client.Get(testServer.URL)
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, ErrCircuitIsOpen.Error())
			So(b.state, ShouldEqual, stateHalfClosed)

			// Since this req also returns an error, the circuit will open again
			// and wait until it may start closing again.
			resp, err = client.Get(testServer.URL)
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, ErrCircuitIsOpen.Error())
			So(b.state, ShouldEqual, stateOpen)

			time.Sleep(b.timeUntilStartClosing / 2)

			// Since we slept not long enough to allow the circuit to start closing again,
			// next req should just keep the circuit open.
			resp, err = client.Get(testServer.URL)
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, ErrCircuitIsOpen.Error())
			So(b.state, ShouldEqual, stateOpen)

			// Sleep until we may close.
			time.Sleep(b.timeUntilStartClosing)

			// Next req should close the circuit half, because we are
			// allowed to start closing after the previous sleep.
			resp, err = client.Get(testServer.URL)
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, ErrCircuitIsOpen.Error())
			So(b.state, ShouldEqual, stateHalfClosed)

			// Next req returns success and should close the circuit.
			respStatus = http.StatusOK
			resp, err = client.Get(testServer.URL)
			So(err, ShouldBeNil)
			So(resp, ShouldNotBeNil)
			So(resp.StatusCode, ShouldEqual, http.StatusOK)
			So(b.state, ShouldEqual, stateClosed)
		})
	})
}

func fatal(err error) {
	if err != nil {
		panic(err)
	}
}
