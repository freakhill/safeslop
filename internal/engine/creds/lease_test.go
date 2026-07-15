package creds

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeLeaseTimer struct {
	mu      sync.Mutex
	ch      chan time.Time
	stopped bool
	fired   bool
}

func (t *fakeLeaseTimer) C() <-chan time.Time { return t.ch }
func (t *fakeLeaseTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return false
	}
	t.stopped = true
	return true
}
func (t *fakeLeaseTimer) fireable() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.fired = true
	return true
}
func (t *fakeLeaseTimer) isStopped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped
}

type fakeLeaseClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeLeaseTimer
}

func (c *fakeLeaseClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeLeaseClock) NewTimer(time.Duration) LeaseTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeLeaseTimer{ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	return t
}
func (c *fakeLeaseClock) Fire(d time.Duration) {
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		for _, candidate := range c.timers {
			if candidate.fireable() {
				c.now = c.now.Add(d)
				t := candidate
				c.mu.Unlock()
				t.ch <- c.now
				return
			}
		}
		c.mu.Unlock()
		if time.Now().After(deadline) {
			panic("lease did not arm a timer")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLeaseRenewsAtTwoThirdsAndStopsWithoutLeak(t *testing.T) {
	clock := &fakeLeaseClock{now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)}
	var calls atomic.Int32
	lease, err := StartLease(LeaseConfig{
		Clock: clock, ExpiresAt: clock.Now().Add(time.Hour),
		Renew: func(context.Context) (time.Time, error) {
			calls.Add(1)
			return clock.Now().Add(time.Hour), nil
		},
		Jitter: func(d time.Duration) time.Duration { return d },
	})
	if err != nil {
		t.Fatal(err)
	}
	clock.Fire(40 * time.Minute)
	waitLease(t, func() bool { return calls.Load() == 1 })
	if got := lease.Snapshot(); got.State != LeaseHealthy || got.Attempts != 0 || !got.CurrentExpiresAt.Equal(clock.Now().Add(time.Hour)) {
		t.Fatalf("renewal snapshot = %+v", got)
	}
	lease.Stop()
	if !clock.timers[len(clock.timers)-1].isStopped() {
		t.Fatal("Stop must stop the pending timer")
	}
}

func TestLeaseBacksOffUntilSuccessAndResets(t *testing.T) {
	clock := &fakeLeaseClock{now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)}
	var calls atomic.Int32
	lease, err := StartLease(LeaseConfig{
		Clock: clock, ExpiresAt: clock.Now().Add(time.Hour),
		Renew: func(context.Context) (time.Time, error) {
			if calls.Add(1) == 1 {
				return time.Time{}, errors.New("transport failure")
			}
			return clock.Now().Add(time.Hour), nil
		},
		Jitter: func(d time.Duration) time.Duration { return d },
	})
	if err != nil {
		t.Fatal(err)
	}
	clock.Fire(40 * time.Minute)
	waitLease(t, func() bool { return lease.Snapshot().State == LeaseDegraded })
	if got := lease.Snapshot(); got.NextRetry != 5*time.Second || got.Attempts != 1 {
		t.Fatalf("first failed renewal = %+v", got)
	}
	clock.Fire(5 * time.Second)
	waitLease(t, func() bool { return calls.Load() == 2 && lease.Snapshot().State == LeaseHealthy })
	if got := lease.Snapshot(); got.Attempts != 0 || got.NextRetry != 0 {
		t.Fatalf("success must reset retry state: %+v", got)
	}
	lease.Stop()
}

func TestLeaseExpiresAtHorizonWithoutAnotherMint(t *testing.T) {
	clock := &fakeLeaseClock{now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)}
	var calls atomic.Int32
	horizon := clock.Now().Add(30 * time.Minute)
	lease, err := StartLease(LeaseConfig{
		Clock: clock, ExpiresAt: clock.Now().Add(time.Hour), Horizon: &horizon,
		Renew: func(context.Context) (time.Time, error) { calls.Add(1); return clock.Now().Add(time.Hour), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	clock.Fire(30 * time.Minute)
	waitLease(t, func() bool { return lease.Snapshot().State == LeaseExpired })
	if calls.Load() != 0 {
		t.Fatalf("horizon must prevent future minting; calls=%d", calls.Load())
	}
	lease.Stop()
}

func TestLeaseRetryBackoffAndJitterAreBounded(t *testing.T) {
	if got := leaseRetryDelay(1); got != 5*time.Second {
		t.Fatalf("first retry = %s", got)
	}
	if got := leaseRetryDelay(20); got != 5*time.Minute {
		t.Fatalf("retry cap = %s", got)
	}
	if got := boundedLeaseJitter(5*time.Second, 30*time.Second); got != 6*time.Second {
		t.Fatalf("jitter must cap at +20%%: %s", got)
	}
	if got := boundedLeaseJitter(5*time.Second, time.Second); got != 5*time.Second {
		t.Fatalf("jitter must not shrink retry: %s", got)
	}
}

func TestLeaseRejectsShortRenewalLifetime(t *testing.T) {
	clock := &fakeLeaseClock{now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)}
	lease, err := StartLease(LeaseConfig{
		Clock: clock, ExpiresAt: clock.Now().Add(time.Hour),
		Renew:  func(context.Context) (time.Time, error) { return clock.Now().Add(9 * time.Minute), nil },
		Jitter: func(d time.Duration) time.Duration { return d },
	})
	if err != nil {
		t.Fatal(err)
	}
	clock.Fire(40 * time.Minute)
	waitLease(t, func() bool { return lease.Snapshot().State == LeaseDegraded })
	if got := lease.Snapshot(); got.NextRetry != 5*time.Second {
		t.Fatalf("short replacement must use retry state: %+v", got)
	}
	lease.Stop()
}

func waitLease(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("lease state did not converge")
}
