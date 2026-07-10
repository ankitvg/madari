package main

import (
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/registry"
)

func TestRingCreateExecutionPolicyShowAndListJSON(t *testing.T) {
	store := newTestStore(t)
	if result := runCmd(store, "add", "docs", "--command", mustCurrentExecutable(t), "--client", "codex", "--allow-tool", "read"); result.code != 0 {
		t.Fatalf("server setup failed: %s", result.stderr)
	}
	result := runCmd(
		store,
		"ring", "create", "bounded",
		"--member", "docs",
		"--ambient-env", "deny",
		"--sandbox", "read-only",
		"--max-duration", "15m",
		"--credential-exposure", "run-process",
	)
	if result.code != 0 {
		t.Fatalf("execution-policy ring create failed: %s", result.stderr)
	}

	ring, err := store.GetRing("bounded")
	if err != nil {
		t.Fatalf("load execution-policy ring: %v", err)
	}
	if ring.Policy == nil || ring.Policy.Execution == nil || ring.Policy.Enforcement != "" {
		t.Fatalf("unexpected stored execution policy: %#v", ring.Policy)
	}
	wantExecution := registry.ExecutionPolicy{
		AmbientEnv:         registry.ExecutionAmbientEnvDeny,
		Sandbox:            registry.ExecutionSandboxReadOnly,
		MaxDuration:        "15m",
		CredentialExposure: registry.ExecutionCredentialExposureRunProcess,
	}
	if *ring.Policy.Execution != wantExecution {
		t.Fatalf("stored execution policy mismatch: %#v", ring.Policy.Execution)
	}
	required := runCmd(
		store,
		"ring", "create", "required-bounded",
		"--member", "docs",
		"--enforcement", "required",
		"--ambient-env", "deny",
		"--sandbox", "read-only",
		"--max-duration", "10m",
		"--credential-exposure", "run-process",
	)
	if required.code != 0 {
		t.Fatalf("required execution-policy ring create failed: %s", required.stderr)
	}
	requiredRing, err := store.GetRing("required-bounded")
	if err != nil || !requiredRing.RequiresPolicyEnforcement() || requiredRing.Policy.Execution == nil || requiredRing.Policy.Execution.MaxDuration != "10m" {
		t.Fatalf("required execution policy not stored: ring=%#v err=%v", requiredRing, err)
	}

	show := runCmd(store, "ring", "show", "bounded")
	if show.code != 0 {
		t.Fatalf("ring show failed: %s", show.stderr)
	}
	for _, want := range []string{
		"policy:\n  execution:",
		"    ambient_env: deny",
		"    sandbox: read-only",
		"    max_duration: 15m",
		"    credential_exposure: run-process",
	} {
		if !strings.Contains(show.stdout, want) {
			t.Fatalf("ring show missing %q:\n%s", want, show.stdout)
		}
	}
	if strings.Contains(show.stdout, "enforcement:") {
		t.Fatalf("advisory execution policy should omit enforcement:\n%s", show.stdout)
	}

	for _, command := range [][]string{
		{"ring", "show", "bounded", "--json"},
		{"ring", "list", "--json"},
	} {
		output := runCmd(store, command...)
		if output.code != 0 {
			t.Fatalf("%v failed: %s", command, output.stderr)
		}
		payload := decodeJSONObject(t, output.stdout)
		var ringJSON map[string]any
		if command[1] == "show" {
			ringJSON = payload["ring"].(map[string]any)
		} else {
			ringJSON = payload["rings"].([]any)[0].(map[string]any)
		}
		policy := ringJSON["policy"].(map[string]any)
		if _, exists := policy["enforcement"]; exists {
			t.Fatalf("advisory execution JSON should omit enforcement: %#v", policy)
		}
		execution := policy["execution"].(map[string]any)
		assertJSONKeys(t, execution, "ambient_env", "sandbox", "max_duration", "credential_exposure")
		if execution["ambient_env"] != "deny" || execution["sandbox"] != "read-only" || execution["max_duration"] != "15m" || execution["credential_exposure"] != "run-process" {
			t.Fatalf("unexpected execution policy JSON: %#v", execution)
		}
	}
}

func TestRingCreateRejectsPartialAndInvalidExecutionPolicy(t *testing.T) {
	store := newTestStore(t)
	if result := runCmd(store, "add", "docs", "--command", mustCurrentExecutable(t), "--client", "codex"); result.code != 0 {
		t.Fatalf("server setup failed: %s", result.stderr)
	}
	valid := []string{
		"ring", "create", "bounded", "--member", "docs",
		"--ambient-env", "deny",
		"--sandbox", "read-only",
		"--max-duration", "15m",
		"--credential-exposure", "run-process",
	}
	tests := []struct {
		name    string
		args    []string
		expects string
	}{
		{
			name:    "partial",
			args:    []string{"ring", "create", "partial", "--member", "docs", "--ambient-env", "deny"},
			expects: `sandbox must be "read-only"`,
		},
		{
			name:    "ambient env",
			args:    replaceCLIArg(valid, "bounded", "bad-ambient", "deny", "inherit"),
			expects: `ambient_env must be "deny"`,
		},
		{
			name:    "sandbox",
			args:    replaceCLIArg(valid, "bounded", "bad-sandbox", "read-only", "workspace-write"),
			expects: `sandbox must be "read-only"`,
		},
		{
			name:    "duration",
			args:    replaceCLIArg(valid, "bounded", "bad-duration", "15m", "0s"),
			expects: "max_duration must be positive",
		},
		{
			name:    "credential exposure",
			args:    replaceCLIArg(valid, "bounded", "bad-exposure", "run-process", "brokered"),
			expects: `credential_exposure must be "run-process"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runCmd(store, tt.args...)
			if result.code == 0 || !strings.Contains(result.stderr, tt.expects) {
				t.Fatalf("expected error containing %q: code=%d stdout=%s stderr=%s", tt.expects, result.code, result.stdout, result.stderr)
			}
		})
	}
}

func TestRingCreateHelpIncludesExecutionPolicy(t *testing.T) {
	result := runCmd(newTestStore(t), "ring", "create", "--help")
	if result.code != 0 {
		t.Fatalf("ring create help failed: %s", result.stderr)
	}
	for _, want := range []string{"--ambient-env deny", "--sandbox read-only", "--max-duration <duration>", "--credential-exposure run-process", "all four execution options together"} {
		if !strings.Contains(result.stdout, want) {
			t.Fatalf("ring create help missing %q:\n%s", want, result.stdout)
		}
	}
}

func replaceCLIArg(args []string, oldName, newName, oldValue, newValue string) []string {
	out := append([]string(nil), args...)
	for i, value := range out {
		switch value {
		case oldName:
			out[i] = newName
		case oldValue:
			out[i] = newValue
		}
	}
	return out
}
