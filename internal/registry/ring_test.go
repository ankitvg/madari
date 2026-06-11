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
		Description: "Research helpers",
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
}

func TestMarshalRingIsDeterministic(t *testing.T) {
	a, err := MarshalRing(Ring{Name: "research", Members: []string{"beta", "alpha"}})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	b, err := MarshalRing(Ring{Name: "research", Members: []string{"alpha", "beta"}})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("expected member order not to affect output:\n%s\nvs\n%s", a, b)
	}

	expected := "name = \"research\"\nmembers = [\"alpha\", \"beta\"]\n"
	if string(a) != expected {
		t.Fatalf("expected deterministic output:\n%s\ngot:\n%s", expected, a)
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
	if !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRingRejectsSections(t *testing.T) {
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
	if !strings.Contains(err.Error(), "no sections") {
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
			expects: "at least one member is required",
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
	ring := Ring{Name: "research", Members: []string{"stewreads", "arxiv"}}
	if !ring.HasMember("arxiv") {
		t.Fatalf("expected arxiv membership")
	}
	if ring.HasMember("other") {
		t.Fatalf("did not expect other membership")
	}
}
