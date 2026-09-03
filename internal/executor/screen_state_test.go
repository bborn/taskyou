package executor

import "testing"

// The two fixtures below are real captures from host mona during task #5289,
// which did no work at all across its first run: it spawned, painted the
// onboarding dialog, was never answered, and parked two minutes later as "Task
// needs review" — a message that named neither the dialog nor the fact that not
// one turn had run. The second capture is the same session after the dialog was
// answered by hand, failing its first turn on a provider 529.
const onboardingDialogCapture = `  HOW TO FINISH THIS TASK (read this — it is different from usual)

  You are running on a different machine from the one that scheduled you, so the
  taskyou_* MCP tools are NOT available here.

────────────────────────────────────────────────────────────────────────────────
  Teach auto mode about your environment?

  Auto mode works better when it knows your environment. Takes about a minute.

  ❯ 1. Yes
    2. Not now
    3. Don't show again

  Enter to confirm · Esc to cancel
────────────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents                      /rc`

const overloadedCapture = `      .ty/signal done "<one line saying what you did>"

● API Error: 529 Overloaded. This is a server-side issue, usually
  temporary — try again in a moment.

✻ Crunched for 4m 12s
────────────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents                      /rc`

const workingCapture = `● Reading internal/executor/executor.go

✻ Crunched for 1m 4s
────────────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt · ← for agents   /rc`

const retryLoopCapture = `✻ API error · Retrying in 8s · attempt 2/10
                                                              ● high · /effort
────────────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt · ← for agents   /rc`

func TestDetectBlockingPrompt(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty content", "", false},
		{"real onboarding dialog from task 5289", onboardingDialogCapture, true},
		{"generic modal footer", "Something?\n\n  Enter to confirm · Esc to cancel", true},
		{"folder trust prompt", "Do you trust the files in this folder?\n1. Yes\n2. No", true},
		{"working agent", workingCapture, false},
		{"agent that hit a 529", overloadedCapture, false},
		{"ordinary task output", "Editing main.go\nRunning tests...\nAll tests passed.", false},
		// This file, and the PR that adds it, must not trip the detector when a
		// task happens to be editing them.
		{"a diff mentioning the patterns", "+\t{\"do you trust the files in this folder\", \"...\"},", false},
		{"prose quoting the modal footer", "The dialog shows Enter to confirm · Esc to cancel at the bottom.", false},
		{"grep output quoting the footer", "screen_state.go:24:\t{\"enter to confirm · esc to cancel\", \"...\"},", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, got := DetectBlockingPrompt(tt.content)
			if got != tt.want {
				t.Errorf("DetectBlockingPrompt() = %v, want %v (reason=%q)", got, tt.want, reason)
			}
			if got && reason == "" {
				t.Error("DetectBlockingPrompt() matched but gave no reason")
			}
		})
	}
}

func TestDetectBusy(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty content", "", false},
		{"turn in flight", workingCapture, true},
		{"provider retry loop", retryLoopCapture, true},
		{"finished agent at its prompt", overloadedCapture, false},
		// A dialog can be raised mid-turn, leaving the footer's "esc to
		// interrupt" on screen. Nothing is progressing behind it, and calling it
		// busy would keep the task processing forever instead of asking for the
		// keystroke that unsticks it.
		{"dialog outranks a stale busy marker",
			onboardingDialogCapture + "\n · esc to interrupt · ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectBusy(tt.content); got != tt.want {
				t.Errorf("DetectBusy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectStallNotice(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty content", "", false},
		{"real 529 from task 5289", overloadedCapture, true},
		{"healthy agent", workingCapture, false},
		{"ordinary task output", "All tests passed.", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, got := DetectStallNotice(tt.content)
			if got != tt.want {
				t.Errorf("DetectStallNotice() = %v, want %v (reason=%q)", got, tt.want, reason)
			}
			if got && reason == "" {
				t.Error("DetectStallNotice() matched but gave no reason")
			}
		})
	}
}

// TestIdleTrackerResetKeepsRetryLoopAlive is the point of the whole change:
// a provider retry loop repaints an identical frame, so without the busy check
// the tracker reaches its threshold and parks an agent that is still trying.
func TestIdleTrackerResetKeepsRetryLoopAlive(t *testing.T) {
	idle := idleTracker{threshold: 3}
	sum := paneSum(retryLoopCapture)

	for i := 0; i < 10; i++ {
		if DetectBusy(retryLoopCapture) {
			idle.reset()
			continue
		}
		if idle.record(sum, true) {
			t.Fatalf("parked a retrying agent after %d identical captures", i+1)
		}
	}

	// And the guard must not make the tracker unable to park anything: the same
	// screen without the busy marker still reaches the threshold.
	stillSum := paneSum(overloadedCapture)
	var parked bool
	for i := 0; i < 3; i++ {
		if DetectBusy(overloadedCapture) {
			idle.reset()
			continue
		}
		parked = idle.record(stillSum, true)
	}
	if !parked {
		t.Error("a genuinely still screen never parked; the busy guard disabled idle detection")
	}
}
