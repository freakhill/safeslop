package creds

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

const (
	leaseMinimumUsableLifetime = 10 * time.Minute
	leaseInitialRetry          = 5 * time.Second
	leaseMaximumRetry          = 5 * time.Minute
)

type LeaseState string

const (
	LeaseHealthy  LeaseState = "healthy"
	LeaseRenewing LeaseState = "renewing"
	LeaseDegraded LeaseState = "degraded"
	LeaseExpired  LeaseState = "expired"
)

// LeaseTimer and LeaseClock keep the renewal loop deterministic in tests. The manager has no
// listener and accepts no requests: it is solely host-side lifecycle machinery.
type LeaseTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type LeaseClock interface {
	Now() time.Time
	NewTimer(time.Duration) LeaseTimer
}

type realLeaseClock struct{}

type realLeaseTimer struct{ timer *time.Timer }

func (realLeaseClock) Now() time.Time { return time.Now() }
func (realLeaseClock) NewTimer(d time.Duration) LeaseTimer {
	return realLeaseTimer{timer: time.NewTimer(d)}
}
func (t realLeaseTimer) C() <-chan time.Time { return t.timer.C }
func (t realLeaseTimer) Stop() bool          { return t.timer.Stop() }

// LeaseConfig carries only expiry metadata and a host callback. Renew must keep all credential
// material in host memory; its result is an expiry, never a credential value.
type LeaseConfig struct {
	Clock     LeaseClock
	ExpiresAt time.Time
	Horizon   *time.Time
	Renew     func(context.Context) (time.Time, error)
	// Jitter is injectable for deterministic tests. Production defaults to a bounded 0–20% delay.
	Jitter func(time.Duration) time.Duration
	// OnChange receives only value-free renewal metadata. It is host lifecycle bookkeeping, not a
	// listener: callbacks must not expose a credential service to the sandbox.
	OnChange func(LeaseSnapshot)
}

// LeaseSnapshot is safe for session/status persistence: it contains no token, reference, path, or
// provider response. Reason is a coarse class, deliberately not an error string.
type LeaseSnapshot struct {
	State            LeaseState
	Reason           string
	CurrentExpiresAt time.Time
	Horizon          *time.Time
	NextRetry        time.Duration
	Attempts         int
}

// Lease renews a host-staged credential until cancellation, expiry, or the optional policy horizon.
type Lease struct {
	clock    LeaseClock
	renew    func(context.Context) (time.Time, error)
	jitter   func(time.Duration) time.Duration
	onChange func(LeaseSnapshot)

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once

	mu       sync.RWMutex
	timer    LeaseTimer
	snapshot LeaseSnapshot
}

func StartLease(config LeaseConfig) (*Lease, error) {
	if config.ExpiresAt.IsZero() {
		return nil, fmt.Errorf("credential lease requires a current expiry")
	}
	if config.Renew == nil {
		return nil, fmt.Errorf("credential lease requires a host renewal callback")
	}
	if config.Clock == nil {
		config.Clock = realLeaseClock{}
	}
	if config.Jitter == nil {
		config.Jitter = defaultLeaseJitter
	}
	ctx, cancel := context.WithCancel(context.Background())
	var horizon *time.Time
	if config.Horizon != nil {
		copy := config.Horizon.UTC()
		horizon = &copy
	}
	lease := &Lease{
		clock: config.Clock, renew: config.Renew, jitter: config.Jitter, onChange: config.OnChange,
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
		snapshot: LeaseSnapshot{State: LeaseHealthy, CurrentExpiresAt: config.ExpiresAt.UTC(), Horizon: horizon},
	}
	go lease.run()
	lease.notify()
	return lease, nil
}

func (l *Lease) run() {
	defer close(l.done)
	for {
		delay, terminal := l.nextDelay()
		if terminal {
			return
		}
		timer := l.clock.NewTimer(delay)
		l.mu.Lock()
		l.timer = timer
		l.mu.Unlock()
		select {
		case <-l.ctx.Done():
			return
		case <-timer.C():
		}
		if l.expiredAt(l.clock.Now()) {
			return
		}
		l.setRenewing()
		expiresAt, err := l.renew(l.ctx)
		if err != nil || expiresAt.Sub(l.clock.Now()) < leaseMinimumUsableLifetime {
			l.setRenewalFailure()
			continue
		}
		l.setRenewalSuccess(expiresAt)
	}
}

// nextDelay selects either two-thirds of the observed current lifetime, a retry, or the nearest
// terminal deadline. A horizon never invalidates an already-issued credential; it only ends future
// safeslop renewal attempts.
func (l *Lease) nextDelay() (time.Duration, bool) {
	l.mu.RLock()
	snapshot := l.snapshot
	l.mu.RUnlock()
	now := l.clock.Now()
	if l.expiredAt(now) {
		return 0, true
	}
	var delay time.Duration
	if snapshot.State == LeaseDegraded && snapshot.NextRetry > 0 {
		delay = snapshot.NextRetry
	} else {
		delay = snapshot.CurrentExpiresAt.Sub(now) * 2 / 3
	}
	if delay < 0 {
		delay = 0
	}
	if deadline := l.deadline(snapshot); !deadline.IsZero() && now.Add(delay).After(deadline) {
		delay = deadline.Sub(now)
	}
	return delay, false
}

func (l *Lease) deadline(snapshot LeaseSnapshot) time.Time {
	deadline := snapshot.CurrentExpiresAt
	if snapshot.Horizon != nil && snapshot.Horizon.Before(deadline) {
		deadline = *snapshot.Horizon
	}
	return deadline
}

func (l *Lease) expiredAt(now time.Time) bool {
	l.mu.Lock()
	expired := false
	if l.snapshot.Horizon != nil && !now.Before(*l.snapshot.Horizon) {
		l.snapshot.State = LeaseExpired
		l.snapshot.Reason = "horizon_reached"
		l.snapshot.NextRetry = 0
		expired = true
	} else if !now.Before(l.snapshot.CurrentExpiresAt) {
		l.snapshot.State = LeaseExpired
		l.snapshot.Reason = "token_expired"
		l.snapshot.NextRetry = 0
		expired = true
	}
	l.mu.Unlock()
	if expired {
		l.notify()
	}
	return expired
}

func (l *Lease) setRenewing() {
	l.mu.Lock()
	l.snapshot.State = LeaseRenewing
	l.snapshot.Reason = ""
	l.mu.Unlock()
	l.notify()
}

func (l *Lease) setRenewalFailure() {
	l.mu.Lock()
	l.snapshot.State = LeaseDegraded
	l.snapshot.Reason = "renewal_failed"
	l.snapshot.Attempts++
	base := leaseRetryDelay(l.snapshot.Attempts)
	l.snapshot.NextRetry = boundedLeaseJitter(base, l.jitter(base))
	l.mu.Unlock()
	l.notify()
}

func (l *Lease) setRenewalSuccess(expiresAt time.Time) {
	l.mu.Lock()
	l.snapshot.State = LeaseHealthy
	l.snapshot.Reason = ""
	l.snapshot.CurrentExpiresAt = expiresAt.UTC()
	l.snapshot.NextRetry = 0
	l.snapshot.Attempts = 0
	l.mu.Unlock()
	l.notify()
}

func leaseRetryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return leaseInitialRetry
	}
	delay := leaseInitialRetry
	for i := 1; i < attempt && delay < leaseMaximumRetry; i++ {
		delay *= 2
		if delay > leaseMaximumRetry {
			return leaseMaximumRetry
		}
	}
	return delay
}

func defaultLeaseJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	return delay + time.Duration(rand.Int63n(int64(delay)/5+1))
}

func boundedLeaseJitter(base, candidate time.Duration) time.Duration {
	if candidate < base {
		return base
	}
	maximum := base + base/5
	if maximum > leaseMaximumRetry {
		maximum = leaseMaximumRetry
	}
	if candidate > maximum {
		return maximum
	}
	return candidate
}

func (l *Lease) notify() {
	if l.onChange != nil {
		l.onChange(l.Snapshot())
	}
}

func (l *Lease) Snapshot() LeaseSnapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := l.snapshot
	if out.Horizon != nil {
		copy := *out.Horizon
		out.Horizon = &copy
	}
	return out
}

// Stop cancels the host renewal callback, stops the pending timer, and waits for the loop. It is
// idempotent so every run teardown path can call it safely.
func (l *Lease) Stop() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.cancel()
		l.mu.RLock()
		timer := l.timer
		l.mu.RUnlock()
		if timer != nil {
			_ = timer.Stop()
		}
		<-l.done
		// Cancellation can race renewal between a fired timer and arming the
		// next one. The loop has now exited, so stop the final published timer
		// as well; otherwise that late timer survives teardown until its deadline.
		l.mu.RLock()
		timer = l.timer
		l.mu.RUnlock()
		if timer != nil {
			_ = timer.Stop()
		}
	})
}
