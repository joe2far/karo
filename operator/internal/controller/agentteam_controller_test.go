package controller

import (
	"strings"
	"testing"

	karov1 "github.com/joe2far/karo/operator/api/v1"
)

func team(agents ...karov1.Agent) *karov1.AgentTeam {
	return &karov1.AgentTeam{Spec: karov1.AgentTeamSpec{Agents: agents}}
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

func TestScaleToZeroYieldsNoActiveAgents(t *testing.T) {
	yes := true
	at := team(karov1.Agent{Name: "a", Harness: "sdk"})
	at.Spec.Runtime = &karov1.RuntimeSpec{ScaleToZero: &yes}
	if n := countActiveAgents(at); n != 0 {
		t.Fatalf("scale-to-zero should yield 0 active agents, got %d", n)
	}
}
