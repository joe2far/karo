package controller

// Common phase constants used across controllers
const (
	PhaseActive    = "Active"
	PhaseDegraded  = "Degraded"
	PhaseError     = "Error"
	PhasePending   = "Pending"
	PhaseReady     = "Ready"
	PhaseRunning   = "Running"
	PhaseSucceeded = "Succeeded"
	PhaseFailed    = "Failed"
)

// Sandbox mode constants
const (
	SandboxModeRestricted = "restricted"
	SandboxModeOpen       = "open"
	SandboxModeNone       = "none"
)
