//go:build e2e

// Package e2e holds Kind-based end-to-end tests (PRD-KARO-v2.md §15).
//
// Run with: go test -tags e2e ./test/e2e/...
// Requires a running Kind cluster with the CRDs installed and the controller
// deployed (make install && make deploy).
//
// The headline e2e (v2 §15): apply a 3-agent team, drive an objective, kill all
// pods mid-run, assert resume to completion; plus scale-to-zero and the
// paused-agent exemption.
package e2e

import "testing"

func TestPlaceholder(t *testing.T) {
	t.Skip("e2e requires a Kind cluster; see PRD-KARO-v2.md §15 for the suite")
}
