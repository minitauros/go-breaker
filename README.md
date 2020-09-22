Package `breaker` provides an implementation of the [circuit breaker pattern](https://martinfowler.com/bliki/CircuitBreaker.html) in the form of a Golang `RoundTripper`.

The circuit breaker is time based. That means that it looks at for how long consecutively only errors are detected. If a given treshold is exceeded, the circuit will trip/open, preventing more requests from being made. The circuit breaker is allowed to close again and let requests through after a specified time.

## Example usage

In the following example, the circuit breaker trips if requests return only errors for 5 seconds straight. After 30 seconds it will let one request through to check if the circuit can be closed (so that it can let all requests through again). If that request returns no error, the circuit closes and requests are allowed through as normal.

```go
httpClient.Transport = breaker.NewBreaker(
    // The transport/tripper that actually does the round trip.
    http.DefaultTransport,
    
    // After how much time of having consecutive errors should the breaker trip?
    5*time.Second,
    
    // After how long of being open should the circuit transition to half closed?
    30*time.Second,
    
    // Function that determines if the response should be considered an error to the circuit breaker.
    // This determines which requests should trip the breaker if too many of them happen consecutively for a too long 
    // period of time. Requests that return an error will always count as an erroneous request and are not passed to 
    // this function.
    func(resp *http.Response) bool {
       return resp.StatusCode >= http.StatusInternalServerError
    },
)
```