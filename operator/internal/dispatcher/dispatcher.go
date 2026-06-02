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
	// TODO(v2-M2): claim ready tasks atomically (FOR UPDATE SKIP LOCKED),
	// request pods for zero-scaled agents, route mailbox streams, emit events.
	<-ctx.Done()
	return ctx.Err()
}
