package v1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"
)

// TestCRDCoversSharedSchema is the Go ⇄ JSON-Schema parity bridge (CLI §4.4,
// v2 §13/§16). The shared JSON Schema (karo-runtime/schema/agentteam.schema.json,
// generated from the pydantic models) is the source of truth for the AgentTeam
// `spec` body. This test fails if the generated CRD's spec schema does not carry
// every property the shared schema declares — i.e. if a `karo export` field would
// be pruned by the apiserver on `kubectl apply`. It is the check that was missing
// in M0 and let the Go types silently drift into a lossy subset.
func TestCRDCoversSharedSchema(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..")
	schemaPath := filepath.Join(repoRoot, "karo-runtime", "schema", "agentteam.schema.json")
	crdPath := filepath.Join("..", "..", "config", "crd", "karo.dev_agentteams.yaml")

	schema := loadJSON(t, schemaPath)
	crd := loadYAML(t, crdPath)

	// Resolve the shared schema's AgentTeamSpec property set (it is referenced via
	// $defs from the top-level spec property).
	defs, _ := schema["$defs"].(map[string]any)
	specDef, ok := defs["AgentTeamSpec"].(map[string]any)
	if !ok {
		t.Fatalf("shared schema missing $defs.AgentTeamSpec")
	}
	wantSpec := propNames(specDef)

	// Pull the CRD's spec property set.
	crdSpec := crdSpecSchema(t, crd)
	gotSpec := propNames(crdSpec)

	for _, p := range wantSpec {
		// runtime: is a v2-only addition that lives in the CRD, not the shared
		// schema, so it is allowed to be a superset there.
		if !contains(gotSpec, p) {
			t.Errorf("CRD spec is missing shared-schema property %q — `karo export` would prune it on apply", p)
		}
	}

	// Spot-check the coordination sub-object, which previously dropped
	// mailbox/taskLayer (the exact F1 drift).
	wantCoord := refProps(t, schema, specDef, "coordination")
	gotCoord := subProps(crdSpec, "coordination")
	for _, p := range wantCoord {
		if !contains(gotCoord, p) {
			t.Errorf("CRD coordination is missing shared-schema property %q", p)
		}
	}
}

func loadJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

func loadYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

func propNames(obj map[string]any) []string {
	props, _ := obj["properties"].(map[string]any)
	out := make([]string, 0, len(props))
	for k := range props {
		out = append(out, k)
	}
	return out
}

// refProps returns the property names of a sub-object reached via $ref from a
// parent definition (e.g. AgentTeamSpec.coordination -> $defs.Coordination).
func refProps(t *testing.T, schema, parent map[string]any, field string) []string {
	t.Helper()
	props, _ := parent["properties"].(map[string]any)
	fp, _ := props[field].(map[string]any)
	ref, _ := fp["$ref"].(string)
	if ref == "" {
		// Some fields inline an allOf/$ref combo.
		if all, ok := fp["allOf"].([]any); ok && len(all) > 0 {
			if first, ok := all[0].(map[string]any); ok {
				ref, _ = first["$ref"].(string)
			}
		}
	}
	if ref == "" {
		t.Fatalf("field %q is not a $ref in the shared schema", field)
	}
	name := filepath.Base(ref) // "#/$defs/Coordination" -> "Coordination"
	defs, _ := schema["$defs"].(map[string]any)
	def, _ := defs[name].(map[string]any)
	return propNames(def)
}

func crdSpecSchema(t *testing.T, crd map[string]any) map[string]any {
	t.Helper()
	spec, _ := crd["spec"].(map[string]any)
	versions, _ := spec["versions"].([]any)
	if len(versions) == 0 {
		t.Fatalf("CRD has no versions")
	}
	v0, _ := versions[0].(map[string]any)
	sch, _ := v0["schema"].(map[string]any)
	openapi, _ := sch["openAPIV3Schema"].(map[string]any)
	props, _ := openapi["properties"].(map[string]any)
	specSchema, _ := props["spec"].(map[string]any)
	return specSchema
}

func subProps(obj map[string]any, field string) []string {
	props, _ := obj["properties"].(map[string]any)
	fp, _ := props[field].(map[string]any)
	return propNames(fp)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
