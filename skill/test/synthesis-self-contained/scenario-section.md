I'm refactoring a rate limiter and want a second opinion on whether the new
version is actually better. I'll give you the current code, then the proposed
rewrite, then my concern.

Current implementation — a mutex-guarded token bucket:

```go
type Limiter struct {
    mu       sync.Mutex
    tokens   float64
    last     time.Time
    rate     float64 // tokens per second
    capacity float64
}

func (l *Limiter) Allow() bool {
    l.mu.Lock()
    defer l.mu.Unlock()
    now := time.Now()
    l.tokens += now.Sub(l.last).Seconds() * l.rate
    if l.tokens > l.capacity {
        l.tokens = l.capacity
    }
    l.last = now
    if l.tokens >= 1 {
        l.tokens--
        return true
    }
    return false
}
```

Proposed rewrite — lock-free using atomics and a nanosecond accumulator:

```go
type Limiter struct {
    state    atomic.Int64 // packed: last-refill-nanos in high bits, milli-tokens in low
    rate     int64
    capacity int64
}

func (l *Limiter) Allow() bool {
    for {
        old := l.state.Load()
        // unpack, refill based on elapsed nanos, try to subtract one token,
        // repack, CAS; retry on contention
        new, ok := l.refillAndTake(old)
        if !ok {
            return false
        }
        if l.state.CompareAndSwap(old, new) {
            return true
        }
    }
}
```

My concern: under high contention the CAS loop in the rewrite might spin enough
that it's actually *slower* than just taking the mutex, and the packed-int64
accumulator loses precision compared to the float64.

Two questions:
1. At what contention level (roughly) does a CAS-retry loop like this start to
   lose to a plain mutex on modern hardware?
2. Is the precision loss from packing tokens into an int64 a real correctness
   problem, or just cosmetic?

Ask me whatever you need before answering.
