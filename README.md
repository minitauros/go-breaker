Package `breaker` provides an implementation of the [circuit breaker pattern](https://martinfowler.com/bliki/CircuitBreaker.html) in the form of a Golang `RoundTripper`.

## Example usage

```go
http.DefaultClient.Transport = breaker.NewTimeoutBreaker(
    // The transport/tripper that actually does the round trip.
    // Could for example be another roundtripper, like rehttp (for retrying requests).
    http.DefaultTransport,
    
    // After how much time of having consecutive errors the circuit opens.
    5*time.Second,
    
    // After how long of being open the circuit will try to transition back to closed.
    30*time.Second,
    
    // Function that determines if the response should be considered an error to the circuit breaker.
    func(resp *http.Response) bool {
       return resp.StatusCode >= http.StatusInternalServerError
    },
)
```