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
		Policy: &RingPolicy{Enforcement: PolicyEnforcementRequired},
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
	if !ringPoliciesEqual(out.Policy, in.Policy) {
		t.Fatalf("expected policy to survive roundtrip, got: %#v", out.Policy)
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

func TestParseAndMarshalRequiredRingPolicy(t *testing.T) {
	payload := `name = "bounded"
members = ["tools"]

[policy]
enforcement = "required"
`
	ring, err := ParseRing([]byte(payload))
	if err != nil {
		t.Fatalf("parse policy ring: %v", err)
	}
	if !ring.RequiresPolicyEnforcement() || ring.Policy == nil || !ring.Policy.Required() {
		t.Fatalf("expected required policy, got: %#v", ring.Policy)
	}
	encoded, err := MarshalRing(ring)
	if err != nil {
		t.Fatalf("marshal policy ring: %v", err)
	}
	if string(encoded) != payload {
		t.Fatalf("policy ring output drift:\nwant:\n%s\ngot:\n%s", payload, encoded)
	}
}

func TestParseAndMarshalExecutionPolicyWithoutEnforcement(t *testing.T) {
	payload := `name = "bounded"
members = ["tools"]

[policy.execution]
ambient_env = "deny"
sandbox = "read-only"
max_duration = "15m"
credential_exposure = "run-process"
`
	ring, err := ParseRing([]byte(payload))
	if err != nil {
		t.Fatalf("parse execution policy ring: %v", err)
	}
	if ring.Policy == nil || ring.Policy.Execution == nil {
		t.Fatalf("expected execution policy, got: %#v", ring.Policy)
	}
	if ring.Policy.Enforcement != "" || ring.Policy.Required() {
		t.Fatalf("standalone execution policy should be advisory: %#v", ring.Policy)
	}
	wantExecution := ExecutionPolicy{
		AmbientEnv:         ExecutionAmbientEnvDeny,
		Sandbox:            ExecutionSandboxReadOnly,
		MaxDuration:        "15m",
		CredentialExposure: ExecutionCredentialExposureRunProcess,
	}
	if *ring.Policy.Execution != wantExecution {
		t.Fatalf("unexpected execution policy: %#v", ring.Policy.Execution)
	}

	encoded, err := MarshalRing(ring)
	if err != nil {
		t.Fatalf("marshal execution policy ring: %v", err)
	}
	if string(encoded) != payload {
		t.Fatalf("execution policy output drift:\nwant:\n%s\ngot:\n%s", payload, encoded)
	}
}

func TestParseAndMarshalRequiredExecutionPolicy(t *testing.T) {
	payload := `name = "bounded"
members = ["tools"]

[policy]
enforcement = "required"

[policy.execution]
ambient_env = "deny"
sandbox = "read-only"
max_duration = "1h30m"
credential_exposure = "run-process"
`
	ring, err := ParseRing([]byte(payload))
	if err != nil {
		t.Fatalf("parse required execution policy ring: %v", err)
	}
	if !ring.RequiresPolicyEnforcement() || ring.Policy.Execution == nil {
		t.Fatalf("expected required execution policy, got: %#v", ring.Policy)
	}
	encoded, err := MarshalRing(ring)
	if err != nil {
		t.Fatalf("marshal required execution policy ring: %v", err)
	}
	if string(encoded) != payload {
		t.Fatalf("required execution policy output drift:\nwant:\n%s\ngot:\n%s", payload, encoded)
	}
}

func TestParseExecutionPolicyAllowsParentPolicyAfterExecution(t *testing.T) {
	payload := `name = "bounded"
members = ["tools"]

[policy.execution]
ambient_env = "deny"
sandbox = "read-only"
max_duration = "15m"
credential_exposure = "run-process"

[policy]
enforcement = "required"
`
	ring, err := ParseRing([]byte(payload))
	if err != nil {
		t.Fatalf("parse execution-first policy: %v", err)
	}
	if !ring.RequiresPolicyEnforcement() || ring.Policy.Execution == nil {
		t.Fatalf("execution-first policy lost fields: %#v", ring.Policy)
	}
}

func TestParseRingRejectsInvalidExecutionPolicy(t *testing.T) {
	base := `name = "bounded"
members = ["tools"]

[policy.execution]
`
	validFields := `ambient_env = "deny"
sandbox = "read-only"
max_duration = "15m"
credential_exposure = "run-process"
`
	tests := []struct {
		name    string
		fields  string
		expects string
	}{
		{name: "empty", fields: "", expects: `ambient_env must be "deny"`},
		{name: "partial", fields: `ambient_env = "deny"` + "\n", expects: `sandbox must be "read-only"`},
		{name: "ambient env", fields: strings.Replace(validFields, `ambient_env = "deny"`, `ambient_env = "inherit"`, 1), expects: `ambient_env must be "deny"`},
		{name: "sandbox", fields: strings.Replace(validFields, `sandbox = "read-only"`, `sandbox = "workspace-write"`, 1), expects: `sandbox must be "read-only"`},
		{name: "invalid duration", fields: strings.Replace(validFields, `max_duration = "15m"`, `max_duration = "forever"`, 1), expects: "max_duration must be a Go duration"},
		{name: "zero duration", fields: strings.Replace(validFields, `max_duration = "15m"`, `max_duration = "0s"`, 1), expects: "max_duration must be positive"},
		{name: "negative duration", fields: strings.Replace(validFields, `max_duration = "15m"`, `max_duration = "-1s"`, 1), expects: "max_duration must be positive"},
		{name: "padded duration", fields: strings.Replace(validFields, `max_duration = "15m"`, `max_duration = " 15m "`, 1), expects: "max_duration must not have leading or trailing whitespace"},
		{name: "credential exposure", fields: strings.Replace(validFields, `credential_exposure = "run-process"`, `credential_exposure = "brokered"`, 1), expects: `credential_exposure must be "run-process"`},
		{name: "unknown key", fields: validFields + `unknown = "value"` + "\n", expects: `unknown key "unknown" in [policy.execution]`},
		{name: "duplicate key", fields: validFields + `sandbox = "read-only"` + "\n", expects: `duplicate key "sandbox" in [policy.execution]`},
		{name: "non-string", fields: strings.Replace(validFields, `max_duration = "15m"`, `max_duration = 15`, 1), expects: "invalid execution policy max_duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRing([]byte(base + tt.fields))
			if err == nil || !strings.Contains(err.Error(), tt.expects) {
				t.Fatalf("expected error containing %q, got: %v", tt.expects, err)
			}
		})
	}

	duplicateSection := base + validFields + "\n[policy.execution]\n"
	if _, err := ParseRing([]byte(duplicateSection)); err == nil || !strings.Contains(err.Error(), `duplicate section "policy.execution"`) {
		t.Fatalf("expected duplicate execution section error, got: %v", err)
	}
}

func TestMarshalRingOrdersContractBeforePolicy(t *testing.T) {
	ring := Ring{
		Name:     "bounded",
		Members:  []string{"tools"},
		Contract: &RingContract{Summary: "Use bounded tools"},
		Policy:   &RingPolicy{Enforcement: PolicyEnforcementRequired},
	}
	encoded, err := MarshalRing(ring)
	if err != nil {
		t.Fatalf("marshal contract and policy: %v", err)
	}
	want := `name = "bounded"
members = ["tools"]

[contract]
summary = "Use bounded tools"

[policy]
enforcement = "required"
`
	if string(encoded) != want {
		t.Fatalf("contract/policy order drift:\nwant:\n%s\ngot:\n%s", want, encoded)
	}
}

func TestParseRingRejectsInvalidPolicy(t *testing.T) {
	base := "name = \"bounded\"\nmembers = [\"tools\"]\n"
	tests := []struct {
		name    string
		extra   string
		expects string
	}{
		{name: "missing enforcement", extra: "\n[policy]\n", expects: `policy enforcement must be "required"`},
		{name: "invalid enforcement", extra: "\n[policy]\nenforcement = \"best-effort\"\n", expects: `policy enforcement must be "required"`},
		{name: "padded enforcement", extra: "\n[policy]\nenforcement = \" required \"\n", expects: `policy enforcement must be "required"`},
		{name: "unknown key", extra: "\n[policy]\nunknown = \"value\"\n", expects: `unknown key "unknown" in [policy]`},
		{name: "duplicate key", extra: "\n[policy]\nenforcement = \"required\"\nenforcement = \"required\"\n", expects: `duplicate key "enforcement"`},
		{name: "duplicate section", extra: "\n[policy]\nenforcement = \"required\"\n[policy]\n", expects: `duplicate section "policy"`},
		{name: "unknown execution key", extra: "\n[policy.execution]\nmode = \"sandbox\"\n", expects: `unknown key "mode" in [policy.execution]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRing([]byte(base + tt.extra))
			if err == nil || !strings.Contains(err.Error(), tt.expects) {
				t.Fatalf("expected error containing %q, got: %v", tt.expects, err)
			}
		})
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

func TestParseAndMarshalRingContractFile(t *testing.T) {
	payload := `summary = "Observe production behavior"
good_for = ["logs", "traces"]
not_for = ["deployments", "schema changes"]
required_context = ["project", "region", "time window"]
optional_context = ["request id", "trace id"]
expected_outputs = ["findings", "evidence", "next check"]
`

	contract, err := ParseRingContract([]byte(payload))
	if err != nil {
		t.Fatalf("parse contract failed: %v", err)
	}
	if !reflect.DeepEqual(contract.GoodFor, []string{"logs", "traces"}) {
		t.Fatalf("expected contract array order preserved, got: %#v", contract.GoodFor)
	}

	encoded, err := MarshalRingContract(contract)
	if err != nil {
		t.Fatalf("marshal contract failed: %v", err)
	}
	if string(encoded) != payload {
		t.Fatalf("expected deterministic contract file:\n%s\ngot:\n%s", payload, encoded)
	}
}

func TestParseRingContractRejectsUnknownKeysAndSections(t *testing.T) {
	if _, err := ParseRingContract([]byte(`unknown = "value"`)); err == nil || !strings.Contains(err.Error(), `unknown key "unknown" in contract file`) {
		t.Fatalf("expected unknown contract file key error, got: %v", err)
	}
	if _, err := ParseRingContract([]byte("[contract]\nsummary = \"value\"\n")); err == nil || !strings.Contains(err.Error(), "unknown section") {
		t.Fatalf("expected contract file section error, got: %v", err)
	}
	if _, err := ParseRingContract([]byte("# empty\n")); err == nil || !strings.Contains(err.Error(), "contract file has no fields") {
		t.Fatalf("expected empty contract file error, got: %v", err)
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
		{
			name:    "invalid policy enforcement",
			mutate:  func(r *Ring) { r.Policy = &RingPolicy{Enforcement: "best-effort"} },
			expects: "policy enforcement must be",
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
