package controller

import (
	"strings"
	"testing"

	karov1 "github.com/joe2far/karo/operator/api/v1"
)

// team builds a minimal *valid* lead-and-teammates team: a coordination lead is
// required by the cross-field rules (CLI §4.3), so the helper sets one pointing
// at the first agent.
func team(agents ...karov1.Agent) *karov1.AgentTeam {
	lead := ""
	if len(agents) > 0 {
		lead = agents[0].Name
	}
	return &karov1.AgentTeam{Spec: karov1.AgentTeamSpec{
		Agents:       agents,
		Coordination: karov1.Coordination{Pattern: "lead-and-teammates", Lead: lead},
	}}
}

func TestValidateTeamRejectsUnknownAPIVersion(t *testing.T) {
	at := team(karov1.Agent{Name: "a", Harness: "sdk"})
	at.APIVersion = "karo.dev/v2"
	if reason := validateTeam(at); reason == "" || !strings.Contains(reason, "apiVersion") {
		t.Fatalf("expected apiVersion rejection, got %q", reason)
	}
}

func TestValidateTeamRequiresAgents(t *testing.T) {
	if reason := validateTeam(team()); reason == "" {
		t.Fatal("expected error for empty agents")
	}
}

func TestValidateTeamRejectsLocalOnlyHarness(t *testing.T) {
	at := team(karov1.Agent{Name: "a", Harness: "cursor"})
	if reason := validateTeam(at); !strings.Contains(reason, "local-only") {
		t.Fatalf("expected local-only rejection, got %q", reason)
	}
}

func TestValidateTeamAcceptsSDK(t *testing.T) {
	at := team(karov1.Agent{Name: "a", Harness: "sdk"})
	at.APIVersion = "karo.dev/v1"
	if reason := validateTeam(at); reason != "" {
		t.Fatalf("expected valid team, got %q", reason)
	}
}

// F4: cross-field rules mirrored from karo-runtime so the operator agrees with
// `karo validate` (CLI §4.3).
func TestValidateTeamRequiresLeadForLeadAndTeammates(t *testing.T) {
	at := &karov1.AgentTeam{Spec: karov1.AgentTeamSpec{
		Agents:       []karov1.Agent{{Name: "a", Harness: "sdk"}},
		Coordination: karov1.Coordination{Pattern: "lead-and-teammates"},
	}}
	if reason := validateTeam(at); !strings.Contains(reason, "lead") {
		t.Fatalf("expected lead-required rejection, got %q", reason)
	}
}

func TestValidateTeamRequiresStagesForPipeline(t *testing.T) {
	at := &karov1.AgentTeam{Spec: karov1.AgentTeamSpec{
		Agents:       []karov1.Agent{{Name: "a", Harness: "sdk"}},
		Coordination: karov1.Coordination{Pattern: "pipeline"},
	}}
	if reason := validateTeam(at); !strings.Contains(reason, "pipeline.stages") {
		t.Fatalf("expected pipeline.stages rejection, got %q", reason)
	}
}

func TestValidateTeamRejectsPromptAutonomous(t *testing.T) {
	at := team(karov1.Agent{
		Name: "a", Harness: "sdk", PermissionMode: "prompt",
		Interaction: &karov1.Interaction{Autonomy: "autonomous"},
	})
	at.APIVersion = "karo.dev/v1"
	if reason := validateTeam(at); !strings.Contains(reason, "prompt") {
		t.Fatalf("expected prompt+autonomous rejection, got %q", reason)
	}
}

func TestScaleToZeroYieldsNoActiveAgents(t *testing.T) {
	yes := true
	at := team(karov1.Agent{Name: "a", Harness: "sdk"})
	at.Spec.Runtime = &karov1.RuntimeSpec{ScaleToZero: &yes}
	if n := countActiveAgents(at); n != 0 {
		t.Fatalf("scale-to-zero should yield 0 active agents, got %d", n)
	}
}

// F6: the M0 acceptance bar — a team with no provisioned agents is Pending,
// not Running.
func TestPhaseForPendingThenRunning(t *testing.T) {
	if p := phaseFor(0); p != "Pending" {
		t.Fatalf("expected Pending with 0 active agents, got %q", p)
	}
	if p := phaseFor(3); p != "Running" {
		t.Fatalf("expected Running with active agents, got %q", p)
	}
}
