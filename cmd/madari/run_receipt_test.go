package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ankitvg/madari/internal/executionreceipt"
	"github.com/ankitvg/madari/internal/proctree"
	"github.com/ankitvg/madari/internal/registry"
)

func TestRunReceiptHelpAndFlagValidation(t *testing.T) {
	help := runCmd(newTestStore(t), "run", "--help")
	if help.code != 0 || !strings.Contains(help.stdout, "--receipt <path>") {
		t.Fatalf("run help omitted receipt flag: code=%d stdout=%s stderr=%s", help.code, help.stdout, help.stderr)
	}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing value",
			args: []string{"run", "codex", "--receipt"},
			want: "flag needs an argument",
		},
		{
			name: "empty path",
			args: []string{"run", "codex", "--ring", "missing", "--receipt=", "--", "inspect"},
			want: "invalid --receipt: receipt path is required",
		},
		{
			name: "dry run",
			args: []string{"run", "codex", "--ring", "missing", "--receipt", "receipt.json", "--dry-run", "--", "inspect"},
			want: "--receipt cannot be used with --dry-run",
		},
		{
			name: "non codex",
			args: []string{"run", "claude-code", "--ring", "missing", "--receipt", "receipt.json", "--", "inspect"},
			want: "--receipt is only supported for codex",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := runCmd(newTestStore(t), test.args...)
			if result.code == 0 || !strings.Contains(result.stderr, test.want) {
				t.Fatalf("receipt validation mismatch: code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
			}
		})
	}
}

func TestRunReceiptResolvesRelativeAndTildePathsBeforeExecution(t *testing.T) {
	t.Run("relative", func(t *testing.T) {
		working := t.TempDir()
		chdirForTest(t, working)
		store := setupSuccessfulReceiptRun(t)
		result := runCmd(store, "run", "codex", "--ring", "docs", "--receipt", "nested/../receipts/run.json", "--", "inspect")
		if result.code != 0 {
			t.Fatalf("relative receipt run failed: stdout=%s stderr=%s", result.stdout, result.stderr)
		}
		path := filepath.Join(working, "receipts", "run.json")
		readRunReceipt(t, path)
	})

	t.Run("tilde", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		store := setupSuccessfulReceiptRun(t)
		result := runCmd(store, "run", "codex", "--ring", "docs", "--receipt", "~/receipts/run.json", "--", "inspect")
		if result.code != 0 {
			t.Fatalf("tilde receipt run failed: stdout=%s stderr=%s", result.stdout, result.stderr)
		}
		readRunReceipt(t, filepath.Join(home, "receipts", "run.json"))
	})
}

func TestRunReceiptRecordsSuccessfulExecutionWithoutChangingOutput(t *testing.T) {
	store := setupSuccessfulReceiptRun(t)
	path := filepath.Join(t.TempDir(), "success.json")
	result := runCmd(store, "run", "codex", "--ring", "docs", "--receipt", path, "--", "inspect")
	if result.code != 0 {
		t.Fatalf("successful receipt run failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	if runtime.GOOS != "windows" {
		if !strings.Contains(result.stdout, "codex stdout") || !strings.Contains(result.stderr, "codex stderr") {
			t.Fatalf("stdout/stderr passthrough changed: stdout=%s stderr=%s", result.stdout, result.stderr)
		}
	}
	if strings.Contains(strings.ToLower(result.stdout), "receipt") || strings.Contains(strings.ToLower(result.stderr), "receipt") {
		t.Fatalf("successful run printed a receipt message: stdout=%s stderr=%s", result.stdout, result.stderr)
	}

	receipt, _ := readRunReceipt(t, path)
	if receipt.Phase != executionreceipt.PhaseExecution || receipt.Outcome != executionreceipt.OutcomeSuccess ||
		receipt.ReasonCode != executionreceipt.ReasonNone || !receipt.ProcessStarted {
		t.Fatalf("unexpected success receipt: %#v", receipt)
	}
	if receipt.Artifact == nil || receipt.Client == nil || receipt.Client.Name != "codex" ||
		receipt.Client.Version != "0.139.0" || receipt.EffectiveTimeoutNS == nil || *receipt.EffectiveTimeoutNS != int64(15*time.Minute) {
		t.Fatalf("missing launch evidence: %#v", receipt)
	}
	if receipt.Exit == nil || receipt.Exit.Code == nil || *receipt.Exit.Code != 0 || receipt.Exit.Signal != nil {
		t.Fatalf("unexpected success exit: %#v", receipt.Exit)
	}
	if receipt.Producer.Name != "madari" || receipt.Producer.Version != version {
		t.Fatalf("unexpected producer: %#v", receipt.Producer)
	}
	if !strings.HasPrefix(receipt.Artifact.LaunchDigest, "sha256:") || !strings.HasPrefix(receipt.Artifact.PolicyDigest, "sha256:") {
		t.Fatalf("digests are not algorithm-qualified: %#v", receipt.Artifact)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat receipt: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("receipt is not owner-only: mode=%v", info.Mode().Perm())
		}
	}
}

func TestRunReceiptRecordsNonzeroClientFailure(t *testing.T) {
	store := setupReceiptRemoteRun(t, 17)
	path := filepath.Join(t.TempDir(), "failure.json")
	result := runCmd(store, "run", "codex", "--ring", "docs", "--receipt", path, "--", "inspect")
	if result.code == 0 {
		t.Fatalf("nonzero Codex exit succeeded: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	receipt, _ := readRunReceipt(t, path)
	if receipt.Outcome != executionreceipt.OutcomeFailure || receipt.ReasonCode != executionreceipt.ReasonProcessFailed || !receipt.ProcessStarted {
		t.Fatalf("unexpected failure receipt: %#v", receipt)
	}
	if receipt.Exit == nil || receipt.Exit.Code == nil || *receipt.Exit.Code != 17 || receipt.Exit.Signal != nil {
		t.Fatalf("unexpected failure exit: %#v", receipt.Exit)
	}
}

func TestRunReceiptRecordsRealProcessTreeTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix hanging fixture records a descendant PID; Windows Job Object lifecycle is covered in internal/proctree")
	}
	withCodexAdminSkillRoots(t, nil)
	t.Setenv("CODEX_HOME", t.TempDir())
	store := newTestStore(t)
	childPIDPath := installHangingFakeCodex(t)
	if err := store.Save(registry.Manifest{
		Name: "docs", Transport: registry.TransportHTTP, URL: "https://example.com/mcp", Enabled: true, Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name: "docs", Members: []string{"docs"}, Policy: &registry.RingPolicy{Execution: testExecutionPolicy("1s")},
	}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	path := filepath.Join(t.TempDir(), "timeout.json")
	result := runCmd(store, "run", "codex", "--ring", "docs", "--receipt", path, "--", "wait")
	if result.code == 0 {
		t.Fatalf("timed run succeeded: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	receipt, _ := readRunReceipt(t, path)
	if receipt.Outcome != executionreceipt.OutcomeTimeout || receipt.ReasonCode != executionreceipt.ReasonTimeout ||
		!receipt.ProcessStarted || receipt.EffectiveTimeoutNS == nil || *receipt.EffectiveTimeoutNS != int64(time.Second) {
		t.Fatalf("unexpected timeout receipt: %#v", receipt)
	}
	if receipt.Termination == nil || receipt.Termination.Reason != executionreceipt.TerminationTimeout {
		t.Fatalf("missing timeout termination evidence: %#v", receipt.Termination)
	}
	payload, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatalf("read hanging child pid: %v", err)
	}
	pid := strings.TrimSpace(string(payload))
	deadline := time.Now().Add(2 * time.Second)
	for processExists(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processExists(pid) {
		t.Fatalf("timed out Codex child %s survived", pid)
	}
}

func TestRunReceiptRecordsBlockedPlanWithoutStartingCodex(t *testing.T) {
	withCodexAdminSkillRoots(t, nil)
	t.Setenv("CODEX_HOME", t.TempDir())
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
	path := filepath.Join(t.TempDir(), "blocked.json")
	result := runCmd(store, "run", "codex", "--ring", "required", "--receipt", path, "--", "inspect")
	if result.code == 0 {
		t.Fatalf("blocked plan succeeded: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	receipt, _ := readRunReceipt(t, path)
	if receipt.Phase != executionreceipt.PhasePlanning || receipt.Outcome != executionreceipt.OutcomeBlocked ||
		receipt.ReasonCode != executionreceipt.ReasonLaunchNotReady || receipt.ProcessStarted {
		t.Fatalf("unexpected blocked receipt: %#v", receipt)
	}
	if receipt.Artifact != nil || receipt.Client != nil || receipt.EffectiveTimeoutNS != nil || receipt.Exit != nil || receipt.Termination != nil {
		t.Fatalf("blocked receipt claimed execution evidence: %#v", receipt)
	}
	if len(receipt.ForwardedEnvironment) != 0 {
		t.Fatalf("blocked receipt claimed forwarding: %#v", receipt.ForwardedEnvironment)
	}
	if len(receipt.Rings) != 1 || receipt.Rings[0].Name != "required" || receipt.Rings[0].SHA256 != nil ||
		len(receipt.Servers) != 1 || receipt.Servers[0].Name != "local" || receipt.Servers[0].SHA256 != nil {
		t.Fatalf("blocked component evidence mismatch: rings=%#v servers=%#v", receipt.Rings, receipt.Servers)
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Codex executed despite blocked plan: %v", err)
	}
}

func TestRunReceiptBlockedMissingRingRetainsRequestedRingName(t *testing.T) {
	withCodexAdminSkillRoots(t, nil)
	t.Setenv("CODEX_HOME", t.TempDir())
	installFakeCodex(t, 0)
	path := filepath.Join(t.TempDir(), "missing-ring.json")
	result := runCmd(newTestStore(t), "run", "codex", "--ring", "missing", "--receipt", path, "--", "inspect")
	if result.code == 0 {
		t.Fatalf("missing ring plan succeeded: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	receipt, _ := readRunReceipt(t, path)
	if len(receipt.Rings) != 1 || receipt.Rings[0].Name != "missing" || receipt.Rings[0].SHA256 != nil {
		t.Fatalf("missing ring evidence mismatch: %#v", receipt.Rings)
	}
}

func TestRunReceiptRecordsPreparationAndStartFailures(t *testing.T) {
	tests := []struct {
		name   string
		result proctree.Result
		reason executionreceipt.ReasonCode
	}{
		{name: "preparation", result: proctree.Result{}, reason: executionreceipt.ReasonPreparationFailed},
		{name: "start", result: proctree.Result{Outcome: proctree.OutcomeStartFailed}, reason: executionreceipt.ReasonProcessStartFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := setupSuccessfulReceiptRun(t)
			failureSentinel := "EXECUTOR_FAILURE_SENTINEL_" + strings.ToUpper(test.name)
			withCodexRunExecutor(t, func(context.Context, cliApp, runLaunchPlan) (proctree.Result, error) {
				return test.result, errors.New(failureSentinel)
			})
			path := filepath.Join(t.TempDir(), test.name+".json")
			result := runCmd(store, "run", "codex", "--ring", "docs", "--receipt", path, "--", "inspect")
			if result.code == 0 || !strings.Contains(result.stderr, failureSentinel) {
				t.Fatalf("expected executor failure: code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
			}
			receipt, raw := readRunReceipt(t, path)
			if receipt.Outcome != executionreceipt.OutcomeFailure || receipt.ReasonCode != test.reason || receipt.ProcessStarted || receipt.Exit != nil {
				t.Fatalf("unexpected %s receipt: %#v", test.name, receipt)
			}
			if bytes.Contains(raw, []byte(failureSentinel)) {
				t.Fatalf("raw executor error leaked into receipt: %s", raw)
			}
		})
	}
}

func TestRunReceiptTypedLifecycleMapping(t *testing.T) {
	cancelled := receiptResultFromProcess(proctree.Result{
		Outcome: proctree.OutcomeCancelled, ProcessStarted: true,
		Termination: &proctree.Termination{Reason: proctree.TerminationReasonCancelled, TreeTermination: proctree.TreeTerminationCompleted},
		Exit:        &proctree.Exit{Code: -1, Signal: "SIGTERM"},
	})
	if cancelled.outcome != executionreceipt.OutcomeCancelled || cancelled.reason != executionreceipt.ReasonCancelled ||
		cancelled.termination == nil || cancelled.termination.Reason != executionreceipt.TerminationCancelled ||
		cancelled.exit == nil || cancelled.exit.Signal == nil || *cancelled.exit.Signal != "SIGTERM" || cancelled.exit.Code != nil {
		t.Fatalf("cancellation mapping mismatch: %#v", cancelled)
	}

	preStart := receiptResultFromProcess(proctree.Result{Outcome: proctree.OutcomeCancelled})
	if preStart.processStarted || preStart.termination != nil || preStart.exit != nil {
		t.Fatalf("pre-start cancellation fabricated termination evidence: %#v", preStart)
	}

	containment := receiptResultFromProcess(proctree.Result{
		Outcome: proctree.OutcomeFailure, ProcessStarted: true, Exit: &proctree.Exit{Code: 0},
	})
	if containment.reason != executionreceipt.ReasonContainmentFailed {
		t.Fatalf("containment failure mapping mismatch: %#v", containment)
	}
}

func TestRunReceiptRecordsHandledCancellation(t *testing.T) {
	store := setupSuccessfulReceiptRun(t)
	withCodexRunExecutor(t, func(context.Context, cliApp, runLaunchPlan) (proctree.Result, error) {
		return proctree.Result{
			Outcome: proctree.OutcomeCancelled, ProcessStarted: true,
			Termination: &proctree.Termination{Reason: proctree.TerminationReasonCancelled, TreeTermination: proctree.TreeTerminationCompleted},
			Exit:        &proctree.Exit{Code: -1, Signal: "SIGTERM"},
		}, context.Canceled
	})
	path := filepath.Join(t.TempDir(), "cancelled.json")
	result := runCmd(store, "run", "codex", "--ring", "docs", "--receipt", path, "--", "inspect")
	if result.code == 0 || !strings.Contains(result.stderr, context.Canceled.Error()) {
		t.Fatalf("handled cancellation did not fail command: code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	receipt, raw := readRunReceipt(t, path)
	if receipt.Outcome != executionreceipt.OutcomeCancelled || receipt.ReasonCode != executionreceipt.ReasonCancelled ||
		!receipt.ProcessStarted || receipt.Termination == nil || receipt.Termination.Reason != executionreceipt.TerminationCancelled ||
		receipt.Exit == nil || receipt.Exit.Signal == nil || *receipt.Exit.Signal != "SIGTERM" {
		t.Fatalf("unexpected cancellation receipt: %#v", receipt)
	}
	if bytes.Contains(raw, []byte(context.Canceled.Error())) {
		t.Fatalf("raw cancellation error leaked into receipt: %s", raw)
	}
}

func TestRunReceiptWriteFailureDoesNotMaskExecutionFailure(t *testing.T) {
	store := setupReceiptRemoteRun(t, 17)
	path := t.TempDir() // Renaming the atomic receipt file over a directory must fail.
	result := runCmd(store, "run", "codex", "--ring", "docs", "--receipt", path, "--", "inspect")
	if result.code == 0 || !strings.Contains(result.stderr, "exit status 17") || !strings.Contains(result.stderr, "write run receipt") {
		t.Fatalf("joined failure lost execution or write error: code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
}

func TestRunReceiptIsWrittenWhenPlanConstructionReturnsError(t *testing.T) {
	withCodexAdminSkillRoots(t, nil)
	t.Setenv("CODEX_HOME", t.TempDir())
	installFakeCodex(t, 0)
	serversPath := filepath.Join(t.TempDir(), "servers-as-file")
	if err := os.WriteFile(serversPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write invalid store path: %v", err)
	}
	store := registry.NewStore(serversPath)
	path := filepath.Join(t.TempDir(), "planning-error.json")
	result := runCmd(store, "run", "codex", "--ring", "missing", "--receipt", path, "--", "inspect")
	if result.code == 0 || !strings.Contains(result.stderr, "read servers directory") {
		t.Fatalf("expected plan construction error: code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	receipt, _ := readRunReceipt(t, path)
	if receipt.Phase != executionreceipt.PhasePlanning || receipt.Outcome != executionreceipt.OutcomeBlocked ||
		receipt.ReasonCode != executionreceipt.ReasonPlanningFailed || receipt.ProcessStarted {
		t.Fatalf("unexpected planning error receipt: %#v", receipt)
	}
}

func TestRunReceiptRawBytesExcludeSensitiveValuesAndIncludeForwardingNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("comprehensive fake Codex output fixture is Unix shell-specific")
	}
	withCodexAdminSkillRoots(t, nil)
	store := newTestStore(t)
	installFakeCodex(t, 0)

	ambient := map[string]string{
		"AWS_SECRET_ACCESS_KEY":          "AMBIENT_AWS_SENTINEL_VALUE",
		"GOOGLE_APPLICATION_CREDENTIALS": "AMBIENT_GCP_SENTINEL_VALUE",
		"GITHUB_TOKEN":                   "AMBIENT_GITHUB_SENTINEL_VALUE",
		"SSH_AUTH_SOCK":                  "AMBIENT_SSH_SENTINEL_VALUE",
		"MADARI_UNDECLARED_SENTINEL":     "AMBIENT_ARBITRARY_SENTINEL_VALUE",
	}
	for key, value := range ambient {
		t.Setenv(key, value)
	}
	t.Setenv("REQUIRED_TOKEN", "REQUIRED_TOKEN_SENTINEL_VALUE")
	t.Setenv("SECRET_TOKEN", "SECRET_TOKEN_SENTINEL_VALUE")
	t.Setenv("REMOTE_TOKEN", "REMOTE_TOKEN_SENTINEL_VALUE")
	authHome := filepath.Join(t.TempDir(), "AUTH_PATH_SENTINEL_VALUE")
	if err := os.MkdirAll(authHome, 0o700); err != nil {
		t.Fatalf("create auth home: %v", err)
	}
	writeTextFile(t, authHome, "auth.json", "AUTH_CONTENT_SENTINEL_VALUE\n")
	t.Setenv("CODEX_HOME", authHome)

	if err := store.Save(registry.Manifest{
		Name: "local-helper", Command: mustCurrentExecutable(t), Args: []string{"--token=ARG_SENTINEL_VALUE"},
		Env:         map[string]string{"STATIC_KEY": "STATIC_ENV_SENTINEL_VALUE"},
		RequiredEnv: registry.RequiredEnv{Keys: []string{"REQUIRED_TOKEN"}},
		SecretEnv:   registry.SecretEnv{Keys: []string{"SECRET_TOKEN"}},
		Enabled:     true, Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save stdio manifest: %v", err)
	}
	if err := store.Save(registry.Manifest{
		Name: "remote-docs", Transport: registry.TransportHTTP, URL: "https://example.com/mcp",
		BearerTokenEnvVar: "REMOTE_TOKEN", Headers: map[string]string{"x-static": "HEADER_SENTINEL_VALUE"},
		Enabled: true, Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save remote manifest: %v", err)
	}
	skillDir := filepath.Join(t.TempDir(), "release")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}
	writeTextFile(t, skillDir, "SKILL.md", "---\nname: release\ndescription: Release workflow\n---\n\nSKILL_CONTENT_SENTINEL_VALUE\n")
	skill, err := registry.NewSkillPackageFromDir(skillDir)
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	if err := store.SaveSkillPackage(skill); err != nil {
		t.Fatalf("save skill: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name: "bounded", Members: []string{"local-helper", "remote-docs"}, Skills: []string{"release"},
	}); err != nil {
		t.Fatalf("save ring: %v", err)
	}

	path := filepath.Join(t.TempDir(), "sentinels.json")
	result := runCmd(store, "run", "codex", "--ring", "bounded", "--receipt", path, "--", "PROMPT_SENTINEL_VALUE")
	if result.code != 0 {
		t.Fatalf("sentinel run failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	receipt, raw := readRunReceipt(t, path)
	for _, sentinel := range []string{
		"AMBIENT_AWS_SENTINEL_VALUE", "AMBIENT_GCP_SENTINEL_VALUE", "AMBIENT_GITHUB_SENTINEL_VALUE",
		"AMBIENT_SSH_SENTINEL_VALUE", "AMBIENT_ARBITRARY_SENTINEL_VALUE", "REQUIRED_TOKEN_SENTINEL_VALUE",
		"SECRET_TOKEN_SENTINEL_VALUE", "REMOTE_TOKEN_SENTINEL_VALUE", "STATIC_ENV_SENTINEL_VALUE",
		"HEADER_SENTINEL_VALUE", "AUTH_CONTENT_SENTINEL_VALUE", "AUTH_PATH_SENTINEL_VALUE",
		"PROMPT_SENTINEL_VALUE", "SKILL_CONTENT_SENTINEL_VALUE", "ARG_SENTINEL_VALUE",
		"codex stdout", "codex stderr",
	} {
		if bytes.Contains(raw, []byte(sentinel)) {
			t.Fatalf("sensitive value %q leaked into raw receipt:\n%s", sentinel, raw)
		}
	}
	for _, key := range []string{"REMOTE_TOKEN", "REQUIRED_TOKEN", "SECRET_TOKEN", "STATIC_KEY"} {
		if !bytes.Contains(raw, []byte(`"`+key+`"`)) {
			t.Fatalf("declared forwarding key %q missing from receipt:\n%s", key, raw)
		}
	}
	for key := range ambient {
		if bytes.Contains(raw, []byte(`"`+key+`"`)) {
			t.Fatalf("undeclared ambient key %q appeared in forwarding evidence:\n%s", key, raw)
		}
	}
	assertReceiptForwarding(t, receipt, executionreceipt.RecipientCodexProcess, "codex", []string{"REMOTE_TOKEN", "REQUIRED_TOKEN", "SECRET_TOKEN"})
	assertReceiptForwarding(t, receipt, executionreceipt.RecipientStdioServer, "local-helper", []string{"REQUIRED_TOKEN", "SECRET_TOKEN", "STATIC_KEY"})
	assertReceiptForwarding(t, receipt, executionreceipt.RecipientRemoteAuth, "remote-docs", []string{"REMOTE_TOKEN"})
}

func TestRunReceiptEvidenceStaysFrozenAfterRegistryAndEnvironmentMutation(t *testing.T) {
	store := setupSuccessfulReceiptRun(t)
	t.Setenv("FROZEN_TOKEN", "before-value")
	manifest, err := store.Get("docs")
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	manifest.BearerTokenEnvVar = "FROZEN_TOKEN"
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save bearer manifest: %v", err)
	}
	app := cliApp{store: store, stdout: io.Discard, stderr: io.Discard}
	plan, err := app.buildRunPlan("codex", []string{"docs"}, "inspect")
	if err != nil || !plan.Ready || plan.Artifact == nil {
		t.Fatalf("build frozen plan: ready=%t artifact=%v err=%v errors=%v", plan.Ready, plan.Artifact != nil, err, plan.Errors)
	}
	before := sanitizeRunPlanForReceipt(plan)

	manifest.BearerTokenEnvVar = "MUTATED_TOKEN"
	if err := store.Save(manifest); err != nil {
		t.Fatalf("mutate registry: %v", err)
	}
	t.Setenv("FROZEN_TOKEN", "after-value")
	t.Setenv("MUTATED_TOKEN", "mutated-value")
	after := sanitizeRunPlanForReceipt(plan)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("registry/environment mutation changed frozen evidence:\nbefore=%#v\nafter=%#v", before, after)
	}
	if got := before.forwardedEnvironment; len(got) == 0 {
		t.Fatal("frozen summary omitted forwarding evidence")
	}
}

func setupSuccessfulReceiptRun(t *testing.T) *registry.Store {
	t.Helper()
	return setupReceiptRemoteRun(t, 0)
}

func setupReceiptRemoteRun(t *testing.T, exitCode int) *registry.Store {
	t.Helper()
	withCodexAdminSkillRoots(t, nil)
	t.Setenv("CODEX_HOME", t.TempDir())
	store := newTestStore(t)
	installFakeCodex(t, exitCode)
	if err := store.Save(registry.Manifest{
		Name: "docs", Transport: registry.TransportHTTP, URL: "https://example.com/mcp", Enabled: true, Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save receipt manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{Name: "docs", Members: []string{"docs"}}); err != nil {
		t.Fatalf("save receipt ring: %v", err)
	}
	return store
}

func readRunReceipt(t *testing.T, path string) (executionreceipt.Receipt, []byte) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read receipt %s: %v", path, err)
	}
	receipt, err := executionreceipt.Parse(payload)
	if err != nil {
		t.Fatalf("parse receipt %s: %v\n%s", path, err, payload)
	}
	return receipt, payload
}

func withCodexRunExecutor(t *testing.T, executor runExecutor) {
	t.Helper()
	for i := range runTargets {
		if runTargets[i].target != "codex" {
			continue
		}
		previous := runTargets[i].executor
		runTargets[i].executor = executor
		t.Cleanup(func() { runTargets[i].executor = previous })
		return
	}
	t.Fatal("Codex run target not found")
}

func assertReceiptForwarding(t *testing.T, receipt executionreceipt.Receipt, kind executionreceipt.RecipientKind, name string, keys []string) {
	t.Helper()
	for _, entry := range receipt.ForwardedEnvironment {
		if entry.Recipient.Kind == kind && entry.Recipient.Name == name {
			if !reflect.DeepEqual(entry.Keys, keys) {
				t.Fatalf("forwarding keys for %s/%s: got=%v want=%v", kind, name, entry.Keys, keys)
			}
			return
		}
	}
	t.Fatalf("forwarding recipient %s/%s missing: %#v", kind, name, receipt.ForwardedEnvironment)
}
