package ui

import (
	"testing"
	"time"
)

// The breaker is the backstop for the shape of the 5040 loop: repeated executor
// launches for one task with no pane ever adopted.

func TestSpawnBreakerStopsARunawayTask(t *testing.T) {
	b := newSpawnBreaker(3, 2*time.Minute)
	now := time.Now()

	for i := 0; i < 3; i++ {
		if !b.allow(5040, now.Add(time.Duration(i)*10*time.Second)) {
			t.Fatalf("launch %d refused; the first 3 must be allowed", i+1)
		}
	}
	if b.allow(5040, now.Add(30*time.Second)) {
		t.Error("4th launch inside the window was allowed; the loop is not bounded")
	}
	// The observed loop ran ~180 times; none past the limit may get through.
	for i := 0; i < 20; i++ {
		if b.allow(5040, now.Add(time.Duration(40+i)*time.Second)) {
			t.Fatalf("launch %d past the limit was allowed", i+5)
		}
	}
}

func TestSpawnBreakerIsPerTask(t *testing.T) {
	b := newSpawnBreaker(1, time.Minute)
	now := time.Now()

	if !b.allow(5040, now) {
		t.Fatal("first launch for 5040 refused")
	}
	if b.allow(5040, now) {
		t.Error("second launch for 5040 allowed")
	}
	if !b.allow(5044, now) {
		t.Error("a different task was blocked by 5040's history")
	}
}

func TestSpawnBreakerForgetsAfterTheWindow(t *testing.T) {
	b := newSpawnBreaker(2, time.Minute)
	now := time.Now()

	b.allow(5040, now)
	b.allow(5040, now)
	if b.allow(5040, now.Add(30*time.Second)) {
		t.Fatal("refusal expected while the burst is still inside the window")
	}
	// A refused launch must not extend the window, or a user who fixed the
	// cause stays locked out.
	if !b.allow(5040, now.Add(2*time.Minute)) {
		t.Error("launch after the window elapsed was still refused")
	}
}

func TestSpawnBreakerResetsOnAdopt(t *testing.T) {
	b := newSpawnBreaker(2, time.Minute)
	now := time.Now()

	b.allow(5040, now)
	b.allow(5040, now)
	b.adopted(5040)
	if !b.allow(5040, now) {
		t.Error("a healthy view that adopted a pane still trips the breaker")
	}
}

func TestSpawnBreakerResetsOnRecovery(t *testing.T) {
	b := newSpawnBreaker(1, time.Minute)
	now := time.Now()

	b.allow(5040, now)
	if b.allow(5040, now) {
		t.Fatal("second launch allowed before recovery")
	}
	b.reset(5040)
	if !b.allow(5040, now) {
		t.Error("explicit recovery did not clear the breaker")
	}
}
