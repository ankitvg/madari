package executionreceipt

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMarshalV1GoldenAndParseRoundTrip(t *testing.T) {
	receipt := validReceipt()
	payload, err := Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}

	want := `{
  "schema_version": 1,
  "evidence": {
    "kind": "self-reported",
    "cryptographic_attestation": false
  },
  "run_id": "00000000-0000-4000-8000-000000000000",
  "producer": {
    "name": "madari",
    "version": "v0.3.0"
  },
  "target": "codex",
  "started_at": "2026-07-10T12:00:00.123456789Z",
  "finished_at": "2026-07-10T12:00:01.357456789Z",
  "duration_ms": 1234,
  "phase": "execution",
  "outcome": "success",
  "reason_code": "none",
  "artifact": {
    "launch_digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "policy_digest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  },
  "client": {
    "name": "codex",
    "version": "0.139.0"
  },
  "rings": [
    {
      "name": "research",
      "sha256": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
    }
  ],
  "servers": [
    {
      "name": "cloud-sql",
      "sha256": "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
    },
    {
      "name": "docs",
      "sha256": "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
    }
  ],
  "skills": [
    {
      "name": "release",
      "sha256": "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
    }
  ],
  "authority": {
    "requested": [
      {
        "control": "mcp-tool-filtering",
        "enforced_by": "client",
        "verification": "configured",
        "classification": "exact"
      },
      {
        "control": "oauth-scopes",
        "enforced_by": "provider",
        "verification": "unverified",
        "classification": "advisory"
      }
    ],
    "effective": [
      {
        "control": "mcp-tool-filtering",
        "enforced_by": "client",
        "verification": "configured",
        "classification": "exact"
      },
      {
        "control": "oauth-scopes",
        "enforced_by": "provider",
        "verification": "unverified",
        "classification": "advisory"
      }
    ]
  },
  "forwarded_environment": [
    {
      "recipient": {
        "kind": "codex-process",
        "name": "codex"
      },
      "keys": [
        "CLOUDSQL_TOKEN",
        "DOCS_TOKEN"
      ]
    },
    {
      "recipient": {
        "kind": "remote-auth",
        "name": "cloud-sql"
      },
      "keys": [
        "CLOUDSQL_TOKEN"
      ]
    },
    {
      "recipient": {
        "kind": "stdio-server",
        "name": "docs"
      },
      "keys": [
        "DOCS_TOKEN"
      ]
    }
  ],
  "effective_timeout_ns": 900000000000,
  "process_started": true,
  "termination": null,
  "exit": {
    "code": 0,
    "signal": null
  }
}
`
	if string(payload) != want {
		t.Fatalf("receipt JSON drift:\nwant:\n%s\ngot:\n%s", want, payload)
	}

	parsed, err := Parse(payload)
	if err != nil {
		t.Fatalf("parse receipt: %v", err)
	}
	if !reflect.DeepEqual(parsed, receipt) {
		t.Fatalf("round trip mismatch:\nwant: %#v\ngot:  %#v", receipt, parsed)
	}
}

func TestParseRejectsUnknownMissingAndTrailingFields(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]any)
		trailer string
		want    string
	}{
		{
			name: "unknown top-level",
			mutate: func(root map[string]any) {
				root["secret"] = "value"
			},
			want: `unknown field "secret"`,
		},
		{
			name: "unknown nested",
			mutate: func(root map[string]any) {
				root["evidence"].(map[string]any)["signature"] = "nope"
			},
			want: `unknown field "signature"`,
		},
		{
			name: "missing required false boolean",
			mutate: func(root map[string]any) {
				delete(root["evidence"].(map[string]any), "cryptographic_attestation")
			},
			want: `evidence is missing required field "cryptographic_attestation"`,
		},
		{
			name: "missing nullable component hash",
			mutate: func(root map[string]any) {
				delete(root["servers"].([]any)[0].(map[string]any), "sha256")
			},
			want: `servers[0] is missing required field "sha256"`,
		},
		{
			name: "missing nullable exit signal",
			mutate: func(root map[string]any) {
				delete(root["exit"].(map[string]any), "signal")
			},
			want: `exit is missing required field "signal"`,
		},
		{
			name: "missing process started",
			mutate: func(root map[string]any) {
				delete(root, "process_started")
			},
			want: `receipt is missing required field "process_started"`,
		},
		{
			name: "unsupported version",
			mutate: func(root map[string]any) {
				root["schema_version"] = float64(2)
			},
			want: "unsupported execution receipt schema version 2",
		},
		{
			name: "null array",
			mutate: func(root map[string]any) {
				root["skills"] = nil
			},
			want: `receipt field "skills" must not be null`,
		},
		{
			name:    "trailing document",
			mutate:  func(map[string]any) {},
			trailer: `{}`,
			want:    "trailing data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := receiptJSONMap(t, validReceipt())
			tt.mutate(root)
			payload, err := json.Marshal(root)
			if err != nil {
				t.Fatalf("marshal mutated receipt: %v", err)
			}
			payload = append(payload, tt.trailer...)
			_, err = Parse(payload)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got: %v", tt.want, err)
			}
		})
	}
}

func TestParseRejectsInvalidUUIDTimestampAndOutcome(t *testing.T) {
	tests := []struct {
		name   string
		field  string
		value  any
		wanted string
	}{
		{name: "uuid version", field: "run_id", value: "00000000-0000-5000-8000-000000000000", wanted: "UUID v4"},
		{name: "uuid variant", field: "run_id", value: "00000000-0000-4000-7000-000000000000", wanted: "UUID v4"},
		{name: "uuid uppercase", field: "run_id", value: "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", wanted: "canonical lowercase UUID v4"},
		{name: "malformed timestamp", field: "started_at", value: "not-a-timestamp", wanted: "cannot parse"},
		{name: "non UTC timestamp", field: "started_at", value: "2026-07-10T08:00:00.123456789-04:00", wanted: "started_at must be UTC"},
		{name: "unknown outcome", field: "outcome", value: "maybe", wanted: "unsupported execution outcome"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := receiptJSONMap(t, validReceipt())
			root[tt.field] = tt.value
			payload, err := json.Marshal(root)
			if err != nil {
				t.Fatalf("marshal mutated receipt: %v", err)
			}
			_, err = Parse(payload)
			if err == nil || !strings.Contains(err.Error(), tt.wanted) {
				t.Fatalf("expected error containing %q, got: %v", tt.wanted, err)
			}
		})
	}
}

func TestParseRejectsNullForRequiredScalars(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{name: "schema version", mutate: func(root map[string]any) { root["schema_version"] = nil }, want: `receipt field "schema_version" must not be null`},
		{name: "attestation boolean", mutate: func(root map[string]any) { root["evidence"].(map[string]any)["cryptographic_attestation"] = nil }, want: `evidence field "cryptographic_attestation" must not be null`},
		{name: "duration", mutate: func(root map[string]any) { root["duration_ms"] = nil }, want: `receipt field "duration_ms" must not be null`},
		{name: "process started", mutate: func(root map[string]any) { root["process_started"] = nil }, want: `receipt field "process_started" must not be null`},
		{name: "artifact digest", mutate: func(root map[string]any) { root["artifact"].(map[string]any)["launch_digest"] = nil }, want: `artifact field "launch_digest" must not be null`},
		{name: "client version", mutate: func(root map[string]any) { root["client"].(map[string]any)["version"] = nil }, want: `client field "version" must not be null`},
		{name: "component name", mutate: func(root map[string]any) { root["servers"].([]any)[0].(map[string]any)["name"] = nil }, want: `servers[0] field "name" must not be null`},
		{name: "authority classification", mutate: func(root map[string]any) {
			root["authority"].(map[string]any)["requested"].([]any)[0].(map[string]any)["classification"] = nil
		}, want: `authority.requested[0] field "classification" must not be null`},
		{name: "recipient kind", mutate: func(root map[string]any) {
			root["forwarded_environment"].([]any)[0].(map[string]any)["recipient"].(map[string]any)["kind"] = nil
		}, want: `forwarded_environment[0].recipient field "kind" must not be null`},
		{name: "forwarded keys", mutate: func(root map[string]any) { root["forwarded_environment"].([]any)[0].(map[string]any)["keys"] = nil }, want: `forwarded_environment[0] field "keys" must not be null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := receiptJSONMap(t, validReceipt())
			tt.mutate(root)
			payload, err := json.Marshal(root)
			if err != nil {
				t.Fatalf("marshal mutated receipt: %v", err)
			}
			_, err = Parse(payload)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got: %v", tt.want, err)
			}
		})
	}

	root := receiptJSONMap(t, receiptForTermination(OutcomeTimeout, ReasonTimeout, TerminationTimeout, true))
	root["termination"].(map[string]any)["reason"] = nil
	payload, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal null termination receipt: %v", err)
	}
	if _, err := Parse(payload); err == nil || !strings.Contains(err.Error(), `termination field "reason" must not be null`) {
		t.Fatalf("expected null termination reason rejection, got: %v", err)
	}
}

func TestNewRunIDProducesUUIDV4(t *testing.T) {
	id, err := newRunID(bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatalf("generate deterministic run ID: %v", err)
	}
	if id != "00000000-0000-4000-8000-000000000000" {
		t.Fatalf("unexpected deterministic UUID: %s", id)
	}
	if err := validateRunID(id); err != nil {
		t.Fatalf("validate deterministic UUID: %v", err)
	}

	for i := 0; i < 32; i++ {
		id, err := NewRunID()
		if err != nil {
			t.Fatalf("generate run ID %d: %v", i, err)
		}
		if err := validateRunID(id); err != nil {
			t.Fatalf("validate generated run ID %q: %v", id, err)
		}
	}

	_, err = newRunID(errorReader{})
	if err == nil || !strings.Contains(err.Error(), "generate execution receipt run ID") {
		t.Fatalf("expected random source error, got: %v", err)
	}
}

func TestWriteAtomicallyReplacesWithOwnerOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "receipt.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("make receipt directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("old permissive content"), 0o644); err != nil {
		t.Fatalf("write old receipt: %v", err)
	}
	if err := Write(path, validReceipt()); err != nil {
		t.Fatalf("write receipt: %v", err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	if _, err := Parse(payload); err != nil {
		t.Fatalf("parse written receipt: %v", err)
	}
	if strings.Contains(string(payload), "old permissive content") {
		t.Fatalf("old receipt content survived replacement")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat receipt: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("receipt mode = %04o, want 0600", got)
		}
	}
	temps, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".madari-*.tmp"))
	if err != nil {
		t.Fatalf("glob temporary receipts: %v", err)
	}
	if len(temps) != 0 {
		t.Fatalf("temporary receipt files remain: %v", temps)
	}
}

func TestWriteValidatesBeforeReplacingExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	original := []byte("keep me")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("write original: %v", err)
	}
	receipt := validReceipt()
	receipt.Outcome = OutcomeFailure
	if err := Write(path, receipt); err == nil {
		t.Fatalf("expected invalid receipt error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("invalid receipt replaced original: %q", got)
	}
}

func validReceipt() Receipt {
	started := time.Date(2026, 7, 10, 12, 0, 0, 123456789, time.UTC)
	finished := started.Add(1234 * time.Millisecond)
	return Receipt{
		SchemaVersion: SchemaVersion,
		Evidence: Evidence{
			Kind:                     EvidenceKindSelfReported,
			CryptographicAttestation: false,
		},
		RunID:      "00000000-0000-4000-8000-000000000000",
		Producer:   Producer{Name: ProducerNameMadari, Version: "v0.3.0"},
		Target:     TargetCodex,
		StartedAt:  started,
		FinishedAt: finished,
		DurationMS: 1234,
		Phase:      PhaseExecution,
		Outcome:    OutcomeSuccess,
		ReasonCode: ReasonNone,
		Artifact: &Artifact{
			LaunchDigest: testDigest('a'),
			PolicyDigest: testDigest('b'),
		},
		Client: &Client{Name: TargetCodex, Version: "0.139.0"},
		Rings:  []Component{{Name: "research", SHA256: pointer(testDigest('c'))}},
		Servers: []Component{
			{Name: "cloud-sql", SHA256: pointer(testDigest('f'))},
			{Name: "docs", SHA256: pointer(testDigest('d'))},
		},
		Skills: []Component{{Name: "release", SHA256: pointer(testDigest('e'))}},
		Authority: Authority{
			Requested: []AuthorityRecord{
				{Control: ControlMCPToolFiltering, EnforcedBy: EnforcedByClient, Verification: VerificationConfigured, Classification: ClassificationExact},
				{Control: ControlOAuthScopes, EnforcedBy: EnforcedByProvider, Verification: VerificationUnverified, Classification: ClassificationAdvisory},
			},
			Effective: []AuthorityRecord{
				{Control: ControlMCPToolFiltering, EnforcedBy: EnforcedByClient, Verification: VerificationConfigured, Classification: ClassificationExact},
				{Control: ControlOAuthScopes, EnforcedBy: EnforcedByProvider, Verification: VerificationUnverified, Classification: ClassificationAdvisory},
			},
		},
		ForwardedEnvironment: []EnvironmentForwarding{
			{Recipient: Recipient{Kind: RecipientCodexProcess, Name: TargetCodex}, Keys: []string{"CLOUDSQL_TOKEN", "DOCS_TOKEN"}},
			{Recipient: Recipient{Kind: RecipientRemoteAuth, Name: "cloud-sql"}, Keys: []string{"CLOUDSQL_TOKEN"}},
			{Recipient: Recipient{Kind: RecipientStdioServer, Name: "docs"}, Keys: []string{"DOCS_TOKEN"}},
		},
		EffectiveTimeoutNS: pointer(int64(15 * time.Minute)),
		ProcessStarted:     true,
		Termination:        nil,
		Exit:               &Exit{Code: pointer(0), Signal: nil},
	}
}

func blockedReceipt() Receipt {
	receipt := validReceipt()
	receipt.Phase = PhasePlanning
	receipt.Outcome = OutcomeBlocked
	receipt.ReasonCode = ReasonPolicyBlocked
	receipt.Artifact = nil
	receipt.Client = nil
	receipt.Servers[0].SHA256 = nil
	receipt.ForwardedEnvironment = []EnvironmentForwarding{}
	receipt.EffectiveTimeoutNS = nil
	receipt.ProcessStarted = false
	receipt.Termination = nil
	receipt.Exit = nil
	return receipt
}

func receiptJSONMap(t *testing.T, receipt Receipt) map[string]any {
	t.Helper()
	payload, err := Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal receipt fixture: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(payload, &root); err != nil {
		t.Fatalf("decode receipt fixture: %v", err)
	}
	return root
}

func testDigest(fill byte) string {
	return "sha256:" + strings.Repeat(string(fill), 64)
}

func pointer[T any](value T) *T {
	return &value
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("random source failed")
}
