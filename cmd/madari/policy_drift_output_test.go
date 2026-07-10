package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/ankitvg/madari/internal/doctor"
)

func TestDriftJSONReportsPolicyStale(t *testing.T) {
	payload := driftToJSON([]doctor.DriftReport{{
		Target:      "codex",
		Status:      doctor.StatusWarning,
		Stale:       []string{"docs.api", "issues"},
		PolicyStale: []string{"docs.api"},
	}})
	if len(payload) != 1 {
		t.Fatalf("expected one drift payload, got: %#v", payload)
	}
	data, err := json.Marshal(payload[0])
	if err != nil {
		t.Fatalf("marshal drift JSON: %v", err)
	}
	if !bytes.Contains(data, []byte(`"policy_stale":["docs.api"]`)) {
		t.Fatalf("expected policy_stale JSON field, got: %s", data)
	}
}

func TestPrintPolicyDriftText(t *testing.T) {
	var out bytes.Buffer
	printPolicyDriftDetail(
		&out,
		"codex (user scope)",
		"madari sync codex --scope user",
		doctor.StatusWarning,
		[]string{"docs.api", "issues"},
	)
	want := "codex (user scope) policy drift: [warn] stale=docs.api,issues (fix: madari sync codex --scope user)\n"
	if got := out.String(); got != want {
		t.Fatalf("doctor policy drift output mismatch:\nwant: %q\n got: %q", want, got)
	}

	out.Reset()
	printPolicyDriftSummary(&out, "codex-user", "madari sync codex --scope user", []string{"docs.api", "issues"})
	want = "codex-user-policy-drift: stale=2 (fix: madari sync codex --scope user)\n"
	if got := out.String(); got != want {
		t.Fatalf("status policy drift output mismatch:\nwant: %q\n got: %q", want, got)
	}
}

func TestPrintPolicyDriftTextOmitsEmptySubset(t *testing.T) {
	var out bytes.Buffer
	printPolicyDriftDetail(&out, "codex", "madari sync codex", doctor.StatusReady, nil)
	printPolicyDriftSummary(&out, "codex", "madari sync codex", []string{})
	if got := out.String(); got != "" {
		t.Fatalf("expected legacy text output to remain unchanged, got: %q", got)
	}
}
