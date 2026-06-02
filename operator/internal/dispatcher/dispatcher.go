// Package dispatcher is the event-driven decoupling layer (PRD-KARO-v2.md §4.1).
//
// It pumps ready tasks to agents, routes mailbox messages, and emits lifecycle
// events. It is stateless — all durable state lives in the coordination stores
// (Redis/Postgres) — so it can be scaled and restarted freely.
//
// This is a scaffold for the v2-M1/M2 milestones; the task pump and mailbox
// routing run against the shared karo-runtime store Protocols.
package dispatcher

import "context"

// Dispatcher pumps ready tasks to agent pods and routes mailbox messages.
type Dispatcher struct {
	// TasksDSN / MailboxDSN / MemoryDSN are injected from runtime.backends
	// secretRefs (v2 §4.1 bootstrap contract).
	TasksDSN   string
	MailboxDSN string
	MemoryDSN  string
}

// Run starts the dispatch loop until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) error {
	// Scale-from-zero (waking the owner + lead on claimable work) now lives in the
	// AgentTeamReconciler, which watches AgentTask projections and sets per-agent
	// replicas (v2 §5.1); atomic task claiming is done pod-side against Postgres
	// (FOR UPDATE SKIP LOCKED). What remains for this component: keeping the
	// AgentTask projection in sync with the Postgres tasks store (so the reconciler
	// sees real state) and mailbox-stream routing / event emission.
	<-ctx.Done()
	return ctx.Err()
}
