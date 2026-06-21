package registry

import (
	"strings"
	"testing"
)

func TestParseAndMarshalSkillRoundTrip(t *testing.T) {
	in := Skill{
		Name:        "release-helper",
		Description: "Release workflow instructions",
	}

	encoded, err := MarshalSkill(in)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	expected := "name = \"release-helper\"\ndescription = \"Release workflow instructions\"\n"
	if string(encoded) != expected {
		t.Fatalf("expected deterministic output:\n%s\ngot:\n%s", expected, encoded)
	}

	out, err := ParseSkill(encoded)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if out != in {
		t.Fatalf("roundtrip mismatch: %#v", out)
	}
}

func TestParseSkillRejectsUnknownKey(t *testing.T) {
	manifest := `
name = "release"
content = "inline"
`
	_, err := ParseSkill([]byte(manifest))
	if err == nil {
		t.Fatalf("expected parse error for unknown key")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSkillRejectsSections(t *testing.T) {
	manifest := `
name = "release"

[env]
KEY = "value"
`
	_, err := ParseSkill([]byte(manifest))
	if err == nil {
		t.Fatalf("expected parse error for section")
	}
	if !strings.Contains(err.Error(), "no sections") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSkillValidate(t *testing.T) {
	tests := []struct {
		name    string
		skill   Skill
		expects string
	}{
		{
			name:    "empty name",
			skill:   Skill{},
			expects: "name is required",
		},
		{
			name:    "invalid name",
			skill:   Skill{Name: "Bad Name"},
			expects: "name must match",
		},
		{
			name:    "valid dotted name",
			skill:   Skill{Name: "release.patch"},
			expects: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.skill.Validate()
			if tt.expects == "" {
				if err != nil {
					t.Fatalf("expected valid skill, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.expects) {
				t.Fatalf("expected error containing %q, got: %v", tt.expects, err)
			}
		})
	}
}

func TestValidateSkillContentRejectsEmpty(t *testing.T) {
	if err := validateSkillContent([]byte("  \n\t")); err == nil {
		t.Fatalf("expected empty content error")
	}
	if err := validateSkillContent([]byte("# Release\n")); err != nil {
		t.Fatalf("expected non-empty content to pass: %v", err)
	}
}
