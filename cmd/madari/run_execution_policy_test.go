package main

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ankitvg/madari/internal/launch"
	"github.com/ankitvg/madari/internal/registry"
)

func TestRunExecutionPolicyUsesStrictestRingAndOnlyAllowsShorterCLI(t *testing.T) {
	store := newTestStore(t)
	installFakeCodex(t, 0)
	allowed := []string{"read"}
	if err := store.Save(registry.Manifest{
		Name: "docs", Transport: registry.TransportHTTP, URL: "https://example.com/mcp",
		Enabled: true, Clients: []string{"codex"}, Access: &registry.AccessProfile{AllowedTools: &allowed},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	for name, duration := range map[string]string{"longer": "20m", "shorter": "10m"} {
		if err := store.SaveRing(registry.Ring{
			Name: name, Members: []string{"docs"},
			Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired, Execution: testExecutionPolicy(duration)},
		}); err != nil {
			t.Fatalf("save ring %s: %v", name, err)
		}
	}

	result := runCmd(store, "run", "codex", "--ring", "longer", "--ring", "shorter", "--max-duration", "5m", "--dry-run", "--json", "--", "inspect")
	if result.code != 0 {
		t.Fatalf("shorter CLI duration failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if !plan.Ready || plan.Execution.MaxDuration != "5m0s" || !plan.Execution.Declared || !plan.Execution.Required {
		t.Fatalf("unexpected execution policy: %#v", plan.Execution)
	}
	if plan.Execution.AmbientEnv != launch.AmbientEnvDeny || plan.Execution.Sandbox != launch.SandboxReadOnly ||
		plan.Execution.CredentialExposure != launch.CredentialExposureRunProcess || plan.Execution.StdioConfinement != "not-applicable" {
		t.Fatalf("execution controls mismatch: %#v", plan.Execution)
	}

	tooLong := runCmd(store, "run", "codex", "--ring", "shorter", "--max-duration", "11m", "--dry-run", "--json", "--", "inspect")
	if tooLong.code == 0 || !strings.Contains(tooLong.stdout, "exceeds the selected ring maximum 10m0s") {
		t.Fatalf("longer CLI duration did not fail closed: stdout=%s stderr=%s", tooLong.stdout, tooLong.stderr)
	}
}

func TestRunRequiredExecutionSandboxBlocksLocalStdioBeforeExecution(t *testing.T) {
	store := newTestStore(t)
	logPath := installFakeCodex(t, 0)
	allowed := []string{"read"}
	if err := store.Save(registry.Manifest{
		Name: "local", Command: mustCurrentExecutable(t), Enabled: true, Clients: []string{"codex"},
		Access: &registry.AccessProfile{AllowedTools: &allowed},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name: "required", Members: []string{"local"},
		Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired, Execution: testExecutionPolicy("10m")},
	}); err != nil {
		t.Fatalf("save ring: %v", err)
	}

	result := runCmd(store, "run", "codex", "--ring", "required", "--dry-run", "--json", "--", "inspect")
	if result.code == 0 || !strings.Contains(result.stdout, "cannot confine local stdio MCP server filesystem or network access") {
		t.Fatalf("required stdio isolation did not block: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if plan.Ready || plan.Execution.StdioConfinement != "unverified" {
		t.Fatalf("blocked stdio plan was not truthful: %#v", plan)
	}
	for _, control := range plan.Authority.Effective {
		if strings.HasPrefix(control.Control, "stdio-") && control.Classification != launch.ClassificationBlocked {
			t.Fatalf("required stdio authority was not blocked: %#v", control)
		}
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Codex execution started despite required degradation: %v", err)
	}
}

func TestRunAdvisoryExecutionReportsStdioConfinementUnverified(t *testing.T) {
	store := newTestStore(t)
	installFakeCodex(t, 0)
	if err := store.Save(registry.Manifest{
		Name: "local", Command: mustCurrentExecutable(t), Enabled: true, Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name: "advisory", Members: []string{"local"},
		Policy: &registry.RingPolicy{Execution: testExecutionPolicy("10m")},
	}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	result := runCmd(store, "run", "codex", "--ring", "advisory", "--dry-run", "--json", "--", "inspect")
	if result.code != 0 {
		t.Fatalf("advisory stdio policy should remain runnable: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if !plan.Ready || plan.Execution.StdioConfinement != "unverified" {
		t.Fatalf("advisory stdio execution mismatch: %#v", plan)
	}
	want := []string{"stdio-filesystem-confinement", "stdio-network-confinement"}
	var degraded []string
	for _, control := range plan.Authority.Effective {
		if strings.HasPrefix(control.Control, "stdio-") && control.EnforcedBy == launch.EnforcedByNone &&
			control.Verification == launch.VerificationUnverified && control.Classification == launch.ClassificationDegraded {
			degraded = append(degraded, control.Control)
		}
	}
	if !reflect.DeepEqual(degraded, want) {
		t.Fatalf("stdio degradation explanation mismatch: %#v", plan.Authority)
	}
}

func TestRunTimeoutTerminatesFakeCodexProcessTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Codex process-tree fixture is Unix-specific; Windows Job Object coverage lives in internal/proctree")
	}
	store := newTestStore(t)
	childPIDPath := installHangingFakeCodex(t)
	if err := store.Save(registry.Manifest{
		Name: "docs", Transport: registry.TransportHTTP, URL: "https://example.com/mcp",
		Enabled: true, Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name: "bounded", Members: []string{"docs"},
		Policy: &registry.RingPolicy{Execution: testExecutionPolicy("100ms")},
	}); err != nil {
		t.Fatalf("save ring: %v", err)
	}

	result := runCmd(store, "run", "codex", "--ring", "bounded", "--", "wait")
	if result.code == 0 || !strings.Contains(result.stdout+result.stderr, "context deadline exceeded") {
		t.Fatalf("bounded run did not time out: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	payload, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatalf("read fake Codex child pid: %v", err)
	}
	pid := strings.TrimSpace(string(payload))
	deadline := time.Now().Add(2 * time.Second)
	for processExists(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processExists(pid) {
		t.Fatalf("fake Codex child process %s survived timeout tree termination", pid)
	}
}

func processExists(pid string) bool {
	if pid == "" {
		return false
	}
	return exec.Command("/bin/kill", "-0", pid).Run() == nil
}

func testExecutionPolicy(duration string) *registry.ExecutionPolicy {
	return &registry.ExecutionPolicy{
		AmbientEnv: registry.ExecutionAmbientEnvDeny, Sandbox: registry.ExecutionSandboxReadOnly,
		MaxDuration: duration, CredentialExposure: registry.ExecutionCredentialExposureRunProcess,
	}
}
