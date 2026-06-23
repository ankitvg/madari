package registry

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseAndMarshalRingRoundTrip(t *testing.T) {
	in := Ring{
		Name:        "research",
		Members:     []string{"stewreads", "arxiv"},
		Skills:      []string{"release", "review"},
		Description: "Research helpers",
		Contract: &RingContract{
			Summary:         "Investigate research context",
			GoodFor:         []string{"source collection", "evidence review"},
			NotFor:          []string{"deployments", "database mutation"},
			RequiredContext: []string{"question", "time window"},
			OptionalContext: []string{"artifact id", "request id"},
			ExpectedOutputs: []string{"findings summary", "recommended next check"},
		},
	}

	encoded, err := MarshalRing(in)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	out, err := ParseRing(encoded)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if out.Name != "research" || out.Description != "Research helpers" {
		t.Fatalf("roundtrip mismatch: %#v", out)
	}
	if !reflect.DeepEqual(out.Members, []string{"arxiv", "stewreads"}) {
		t.Fatalf("expected sorted members to survive roundtrip, got: %#v", out.Members)
	}
	if !reflect.DeepEqual(out.Skills, []string{"release", "review"}) {
		t.Fatalf("expected sorted skills to survive roundtrip, got: %#v", out.Skills)
	}
	if !ringContractsEqual(out.Contract, in.Contract) {
		t.Fatalf("expected contract to survive roundtrip, got: %#v", out.Contract)
	}
}

func TestMarshalRingIsDeterministic(t *testing.T) {
	a, err := MarshalRing(Ring{Name: "research", Members: []string{"beta", "alpha"}, Skills: []string{"release", "audit"}})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	b, err := MarshalRing(Ring{Name: "research", Members: []string{"alpha", "beta"}, Skills: []string{"audit", "release"}})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("expected member order not to affect output:\n%s\nvs\n%s", a, b)
	}

	expected := "name = \"research\"\nmembers = [\"alpha\", \"beta\"]\nskills = [\"audit\", \"release\"]\n"
	if string(a) != expected {
		t.Fatalf("expected deterministic output:\n%s\ngot:\n%s", expected, a)
	}
}

func TestMarshalRingPreservesContractOrder(t *testing.T) {
	ring := Ring{
		Name:    "observe",
		Members: []string{"logs"},
		Contract: &RingContract{
			Summary:         "Observe production behavior",
			GoodFor:         []string{"logs", "traces"},
			NotFor:          []string{"deployments", "schema changes"},
			RequiredContext: []string{"project", "region", "time window"},
			OptionalContext: []string{"request id", "trace id"},
			ExpectedOutputs: []string{"findings", "evidence", "next check"},
		},
	}

	encoded, err := MarshalRing(ring)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	expected := `name = "observe"
members = ["logs"]

[contract]
summary = "Observe production behavior"
good_for = ["logs", "traces"]
not_for = ["deployments", "schema changes"]
required_context = ["project", "region", "time window"]
optional_context = ["request id", "trace id"]
expected_outputs = ["findings", "evidence", "next check"]
`
	if string(encoded) != expected {
		t.Fatalf("expected contract order to be preserved:\n%s\ngot:\n%s", expected, encoded)
	}
}

func TestMarshalRingOmitsEmptyContract(t *testing.T) {
	encoded, err := MarshalRing(Ring{Name: "observe", Members: []string{"logs"}, Contract: &RingContract{}})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	expected := "name = \"observe\"\nmembers = [\"logs\"]\n"
	if string(encoded) != expected {
		t.Fatalf("expected empty contract to be omitted:\n%s\ngot:\n%s", expected, encoded)
	}
}

func TestMarshalRingAllowsSkillOnlyRing(t *testing.T) {
	encoded, err := MarshalRing(Ring{Name: "workflow", Skills: []string{"release"}})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	expected := "name = \"workflow\"\nskills = [\"release\"]\n"
	if string(encoded) != expected {
		t.Fatalf("expected skill-only ring output:\n%s\ngot:\n%s", expected, encoded)
	}
}

func TestParseRingRejectsUnknownKey(t *testing.T) {
	manifest := `
name = "research"
members = ["stewreads"]
command = "/bin/echo"
`
	_, err := ParseRing([]byte(manifest))
	if err == nil {
		t.Fatalf("expected parse error for unknown key")
	}
	if !strings.Contains(err.Error(), "unknown top-level key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRingRejectsUnknownSections(t *testing.T) {
	manifest := `
name = "research"
members = ["stewreads"]

[env]
KEY = "value"
`
	_, err := ParseRing([]byte(manifest))
	if err == nil {
		t.Fatalf("expected parse error for section")
	}
	if !strings.Contains(err.Error(), "unknown section") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRingRejectsUnknownContractKey(t *testing.T) {
	manifest := `
name = "research"
members = ["stewreads"]

[contract]
unknown = "value"
`
	_, err := ParseRing([]byte(manifest))
	if err == nil {
		t.Fatalf("expected parse error for unknown contract key")
	}
	if !strings.Contains(err.Error(), `unknown key "unknown" in [contract]`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRingValidate(t *testing.T) {
	base := func() Ring {
		return Ring{Name: "research", Members: []string{"stewreads"}}
	}

	tests := []struct {
		name    string
		mutate  func(*Ring)
		expects string
	}{
		{
			name:    "empty name",
			mutate:  func(r *Ring) { r.Name = "" },
			expects: "name is required",
		},
		{
			name:    "invalid name",
			mutate:  func(r *Ring) { r.Name = "Bad Name" },
			expects: "name must match",
		},
		{
			name:    "no members",
			mutate:  func(r *Ring) { r.Members = nil },
			expects: "at least one member or skill is required",
		},
		{
			name:    "duplicate members",
			mutate:  func(r *Ring) { r.Members = []string{"stewreads", "stewreads"} },
			expects: "duplicate member",
		},
		{
			name:    "invalid member name",
			mutate:  func(r *Ring) { r.Members = []string{"Not Valid"} },
			expects: "invalid member",
		},
		{
			name:    "duplicate skills",
			mutate:  func(r *Ring) { r.Members = nil; r.Skills = []string{"release", "release"} },
			expects: "duplicate skill",
		},
		{
			name:    "invalid skill name",
			mutate:  func(r *Ring) { r.Members = nil; r.Skills = []string{"Not Valid"} },
			expects: "invalid skill",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ring := base()
			tt.mutate(&ring)
			err := ring.Validate()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.expects) {
				t.Fatalf("expected error containing %q, got: %v", tt.expects, err)
			}
		})
	}
}

func TestRingHasMember(t *testing.T) {
	ring := Ring{Name: "research", Members: []string{"stewreads", "arxiv"}, Skills: []string{"release"}}
	if !ring.HasMember("arxiv") {
		t.Fatalf("expected arxiv membership")
	}
	if ring.HasMember("other") {
		t.Fatalf("did not expect other membership")
	}
	if !ring.HasSkill("release") {
		t.Fatalf("expected release skill membership")
	}
	if ring.HasSkill("other") {
		t.Fatalf("did not expect other skill membership")
	}
}
