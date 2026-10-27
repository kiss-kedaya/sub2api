package service

import "time"

// Adapted from ranxi2001/sub2api AdvanceConcurrency, LGPL-3.0.
type AutomationOutcome struct {
	Reset     bool
	ID        string
	AccountID int64
	GroupID   int64
	Model     string
	StartedAt time.Time
	Success   bool
}
type AutomationRampState struct {
	Revision    string    `json:"revision"`
	Concurrency int       `json:"concurrency"`
	Successes   int       `json:"successes"`
	PausedUntil time.Time `json:"paused_until"`
}

func AdvanceAutomationRamp(state AutomationRampState, current int, c OpsAutomationConfig, event AutomationOutcome, now time.Time) (AutomationRampState, int) {
	if event.Reset || state.Revision != c.Revision || state.Concurrency != current {
		state = AutomationRampState{Revision: c.Revision, Concurrency: current, PausedUntil: now.Add(time.Duration(c.Ramp.CooldownSeconds) * time.Second)}
	}
	if !event.Success {
		state.Successes = 0
		state.PausedUntil = now.Add(time.Duration(c.Ramp.CooldownSeconds) * time.Second)
		return state, current
	}
	if current <= 0 || current >= c.Ramp.Maximum || event.StartedAt.Before(state.PausedUntil) || event.StartedAt.Before(c.UpdatedAt) {
		return state, current
	}
	state.Successes++
	if state.Successes >= c.Ramp.SuccessesPerStep {
		current = min(current+c.Ramp.Step, c.Ramp.Maximum)
		state.Concurrency = current
		state.Successes = 0
		state.PausedUntil = now.Add(time.Duration(c.Ramp.CooldownSeconds) * time.Second)
	}
	return state, current
}
