package executionreceipt

import (
	"strings"
	"testing"
	"time"
)

func TestValidateAcceptsEveryCoherentOutcome(t *testing.T) {
	tests := []struct {
		name    string
		receipt Receipt
	}{
		{name: "success", receipt: validReceipt()},
		{name: "planning blocked", receipt: blockedReceipt()},
		{name: "preparation failure", receipt: receiptForFailure(ReasonPreparationFailed, false, nil)},
		{name: "start failure", receipt: receiptForFailure(ReasonProcessStartFailed, false, nil)},
		{name: "nonzero exit", receipt: receiptForFailure(ReasonProcessFailed, true, &Exit{Code: pointer(17)})},
		{name: "signal exit", receipt: receiptForFailure(ReasonProcessFailed, true, &Exit{Signal: pointer("SIGTERM")})},
		{name: "containment failure after success", receipt: receiptForFailure(ReasonContainmentFailed, true, &Exit{Code: pointer(0)})},
		{name: "timeout", receipt: receiptForTermination(OutcomeTimeout, ReasonTimeout, TerminationTimeout, true)},
		{name: "cancelled after start", receipt: receiptForTermination(OutcomeCancelled, ReasonCancelled, TerminationCancelled, true)},
		{name: "cancelled before start", receipt: receiptForTermination(OutcomeCancelled, ReasonCancelled, TerminationCancelled, false)},
		{name: "one nanosecond timeout", receipt: receiptWithTimeout(1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.receipt.Validate(); err != nil {
				t.Fatalf("valid receipt rejected: %v", err)
			}
		})
	}
}

func TestValidateRejectsInvalidBaseFieldsAndOrdering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Receipt)
		want   string
	}{
		{name: "attestation claim", mutate: func(r *Receipt) { r.Evidence.CryptographicAttestation = true }, want: "must be false"},
		{name: "wrong producer", mutate: func(r *Receipt) { r.Producer.Name = "other" }, want: "producer.name"},
		{name: "unsafe producer version", mutate: func(r *Receipt) { r.Producer.Version = " secret" }, want: "producer.version"},
		{name: "wrong target", mutate: func(r *Receipt) { r.Target = "other" }, want: "target must be"},
		{name: "finished before started", mutate: func(r *Receipt) { r.FinishedAt = r.StartedAt.Add(-time.Millisecond) }, want: "before started_at"},
		{name: "duration mismatch", mutate: func(r *Receipt) { r.DurationMS++ }, want: "must equal"},
		{name: "nil rings", mutate: func(r *Receipt) { r.Rings = nil }, want: "rings must be a JSON array"},
		{name: "unsorted servers", mutate: func(r *Receipt) { r.Servers[0], r.Servers[1] = r.Servers[1], r.Servers[0] }, want: "servers must be sorted"},
		{name: "duplicate server", mutate: func(r *Receipt) { r.Servers[1].Name = r.Servers[0].Name }, want: "duplicate name"},
		{name: "invalid component name", mutate: func(r *Receipt) { r.Rings[0].Name = "Research Secret" }, want: "rings[0].name is invalid"},
		{name: "uppercase digest", mutate: func(r *Receipt) { *r.Servers[0].SHA256 = "sha256:" + strings.Repeat("A", 64) }, want: "64 lowercase hex"},
		{name: "nil execution component hash", mutate: func(r *Receipt) { r.Skills[0].SHA256 = nil }, want: "execution skills[0].sha256"},
		{name: "bad launch digest", mutate: func(r *Receipt) { r.Artifact.LaunchDigest = "abc" }, want: "artifact.launch_digest"},
		{name: "client mismatch", mutate: func(r *Receipt) { r.Client.Name = "other" }, want: "client.name"},
		{name: "zero timeout", mutate: func(r *Receipt) { *r.EffectiveTimeoutNS = 0 }, want: "must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := validReceipt()
			tt.mutate(&receipt)
			if err := receipt.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got: %v", tt.want, err)
			}
		})
	}
}

func TestValidateRejectsInvalidAuthority(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Receipt)
		want   string
	}{
		{name: "nil requested", mutate: func(r *Receipt) { r.Authority.Requested = nil }, want: "authority.requested must be a JSON array"},
		{name: "unsorted", mutate: func(r *Receipt) {
			r.Authority.Requested[0], r.Authority.Requested[1] = r.Authority.Requested[1], r.Authority.Requested[0]
		}, want: "sorted by control"},
		{name: "duplicate control", mutate: func(r *Receipt) { r.Authority.Requested[1].Control = r.Authority.Requested[0].Control }, want: "duplicate control"},
		{name: "unknown control", mutate: func(r *Receipt) { r.Authority.Requested[1].Control = "zzz-control" }, want: "control is unsupported"},
		{name: "unknown enforcer", mutate: func(r *Receipt) { r.Authority.Requested[0].EnforcedBy = "daemon" }, want: "enforced_by is unsupported"},
		{name: "unknown verification", mutate: func(r *Receipt) { r.Authority.Requested[0].Verification = "assumed" }, want: "verification is unsupported"},
		{name: "unknown classification", mutate: func(r *Receipt) { r.Authority.Requested[0].Classification = "maybe" }, want: "classification is unsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := validReceipt()
			tt.mutate(&receipt)
			if err := receipt.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got: %v", tt.want, err)
			}
		})
	}
}

func TestValidateRejectsInvalidForwardedEnvironment(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Receipt)
		want   string
	}{
		{name: "nil recipients", mutate: func(r *Receipt) { r.ForwardedEnvironment = nil }, want: "must be a JSON array"},
		{name: "missing codex recipient", mutate: func(r *Receipt) { r.ForwardedEnvironment = r.ForwardedEnvironment[1:] }, want: "exactly one codex-process"},
		{name: "duplicate codex recipient", mutate: func(r *Receipt) {
			r.ForwardedEnvironment = append([]EnvironmentForwarding{r.ForwardedEnvironment[0]}, r.ForwardedEnvironment...)
		}, want: "duplicate recipient"},
		{name: "unsorted recipients", mutate: func(r *Receipt) {
			r.ForwardedEnvironment[0], r.ForwardedEnvironment[1] = r.ForwardedEnvironment[1], r.ForwardedEnvironment[0]
		}, want: "sorted by recipient"},
		{name: "unknown recipient", mutate: func(r *Receipt) { r.ForwardedEnvironment[2].Recipient.Kind = "zzz" }, want: "kind is unsupported"},
		{name: "wrong codex name", mutate: func(r *Receipt) { r.ForwardedEnvironment[0].Recipient.Name = "docs" }, want: "codex-process recipient name"},
		{name: "invented stdio server", mutate: func(r *Receipt) { r.ForwardedEnvironment[2].Recipient.Name = "invented" }, want: "not a selected server"},
		{name: "invented remote server", mutate: func(r *Receipt) { r.ForwardedEnvironment[1].Recipient.Name = "invented" }, want: "not a selected server"},
		{name: "nil keys", mutate: func(r *Receipt) { r.ForwardedEnvironment[0].Keys = nil }, want: "keys must be a JSON array"},
		{name: "unsorted keys", mutate: func(r *Receipt) { r.ForwardedEnvironment[0].Keys = []string{"DOCS_TOKEN", "CLOUDSQL_TOKEN"} }, want: "keys must be sorted"},
		{name: "duplicate key", mutate: func(r *Receipt) { r.ForwardedEnvironment[0].Keys = []string{"DOCS_TOKEN", "DOCS_TOKEN"} }, want: "duplicate"},
		{name: "invalid key", mutate: func(r *Receipt) { r.ForwardedEnvironment[0].Keys = []string{"TOKEN=value"} }, want: "keys[0] is invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := validReceipt()
			tt.mutate(&receipt)
			if err := receipt.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got: %v", tt.want, err)
			}
		})
	}
}

func TestValidateRejectsIncoherentOutcomeEvidence(t *testing.T) {
	tests := []struct {
		name    string
		receipt func() Receipt
		mutate  func(*Receipt)
		want    string
	}{
		{name: "planning success", receipt: blockedReceipt, mutate: func(r *Receipt) { r.Outcome = OutcomeSuccess }, want: "planning phase requires outcome"},
		{name: "planning wrong reason", receipt: blockedReceipt, mutate: func(r *Receipt) { r.ReasonCode = ReasonNone }, want: "invalid reason_code"},
		{name: "planning artifact", receipt: blockedReceipt, mutate: func(r *Receipt) { r.Artifact = validReceipt().Artifact }, want: "artifact null"},
		{name: "planning client", receipt: blockedReceipt, mutate: func(r *Receipt) { r.Client = validReceipt().Client }, want: "client null"},
		{name: "planning forwarding", receipt: blockedReceipt, mutate: func(r *Receipt) { r.ForwardedEnvironment = validReceipt().ForwardedEnvironment }, want: "must not report forwarded"},
		{name: "execution artifact null", receipt: validReceipt, mutate: func(r *Receipt) { r.Artifact = nil }, want: "requires artifact"},
		{name: "execution client null", receipt: validReceipt, mutate: func(r *Receipt) { r.Client = nil }, want: "requires client"},
		{name: "execution timeout null", receipt: validReceipt, mutate: func(r *Receipt) { r.EffectiveTimeoutNS = nil }, want: "requires effective_timeout_ns"},
		{name: "success wrong reason", receipt: validReceipt, mutate: func(r *Receipt) { r.ReasonCode = ReasonProcessFailed }, want: "success outcome requires"},
		{name: "success not started", receipt: validReceipt, mutate: func(r *Receipt) { r.ProcessStarted = false; r.Exit = nil }, want: "process_started true"},
		{name: "success nonzero", receipt: validReceipt, mutate: func(r *Receipt) { *r.Exit.Code = 1 }, want: "exit code 0"},
		{name: "preparation started", receipt: func() Receipt { return receiptForFailure(ReasonPreparationFailed, false, nil) }, mutate: func(r *Receipt) { r.ProcessStarted = true }, want: "requires process_started false"},
		{name: "process failure not started", receipt: func() Receipt { return receiptForFailure(ReasonProcessFailed, true, &Exit{Code: pointer(17)}) }, mutate: func(r *Receipt) { r.ProcessStarted = false; r.Exit = nil }, want: "requires process_started true"},
		{name: "process failure exit zero", receipt: func() Receipt { return receiptForFailure(ReasonProcessFailed, true, &Exit{Code: pointer(17)}) }, mutate: func(r *Receipt) { *r.Exit.Code = 0 }, want: "cannot report exit code 0"},
		{name: "containment failure nonzero", receipt: func() Receipt { return receiptForFailure(ReasonContainmentFailed, true, &Exit{Code: pointer(0)}) }, mutate: func(r *Receipt) { *r.Exit.Code = 1 }, want: "requires exit code 0"},
		{name: "timeout not started", receipt: func() Receipt { return receiptForTermination(OutcomeTimeout, ReasonTimeout, TerminationTimeout, true) }, mutate: func(r *Receipt) { r.ProcessStarted = false; r.Exit = nil }, want: "requires process_started true"},
		{name: "timeout missing termination", receipt: func() Receipt { return receiptForTermination(OutcomeTimeout, ReasonTimeout, TerminationTimeout, true) }, mutate: func(r *Receipt) { r.Termination = nil }, want: "requires timeout termination"},
		{name: "cancelled after start missing termination", receipt: func() Receipt {
			return receiptForTermination(OutcomeCancelled, ReasonCancelled, TerminationCancelled, true)
		}, mutate: func(r *Receipt) { r.Termination = nil }, want: "requires cancellation termination"},
		{name: "cancelled before start has termination", receipt: func() Receipt {
			return receiptForTermination(OutcomeCancelled, ReasonCancelled, TerminationCancelled, false)
		}, mutate: func(r *Receipt) {
			r.Termination = &Termination{Reason: TerminationCancelled, TreeTermination: TreeTerminationCompleted}
		}, want: "requires termination null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := tt.receipt()
			tt.mutate(&receipt)
			if err := receipt.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got: %v", tt.want, err)
			}
		})
	}
}

func TestValidateRejectsInvalidExitAndTermination(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Receipt)
		want   string
	}{
		{name: "empty exit", mutate: func(r *Receipt) { r.Outcome = OutcomeFailure; r.ReasonCode = ReasonProcessFailed; r.Exit = &Exit{} }, want: "exactly one"},
		{name: "code and signal", mutate: func(r *Receipt) {
			r.Outcome = OutcomeFailure
			r.ReasonCode = ReasonProcessFailed
			r.Exit.Signal = pointer("SIGTERM")
		}, want: "exactly one"},
		{name: "negative exit", mutate: func(r *Receipt) { r.Outcome = OutcomeFailure; r.ReasonCode = ReasonProcessFailed; *r.Exit.Code = -1 }, want: "must not be negative"},
		{name: "untyped signal", mutate: func(r *Receipt) {
			r.Outcome = OutcomeFailure
			r.ReasonCode = ReasonProcessFailed
			r.Exit = &Exit{Signal: pointer("killed by signal")}
		}, want: "exit.signal is invalid"},
		{name: "unknown termination reason", mutate: func(r *Receipt) {
			r.Outcome = OutcomeTimeout
			r.ReasonCode = ReasonTimeout
			r.Exit = &Exit{Signal: pointer("SIGKILL")}
			r.Termination = &Termination{Reason: "expired", TreeTermination: TreeTerminationCompleted}
		}, want: "termination.reason is unsupported"},
		{name: "unknown tree status", mutate: func(r *Receipt) {
			r.Outcome = OutcomeTimeout
			r.ReasonCode = ReasonTimeout
			r.Exit = &Exit{Signal: pointer("SIGKILL")}
			r.Termination = &Termination{Reason: TerminationTimeout, TreeTermination: "maybe"}
		}, want: "tree_termination is unsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := validReceipt()
			tt.mutate(&receipt)
			if err := receipt.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got: %v", tt.want, err)
			}
		})
	}
}

func receiptForFailure(reason ReasonCode, started bool, exit *Exit) Receipt {
	receipt := validReceipt()
	receipt.Outcome = OutcomeFailure
	receipt.ReasonCode = reason
	receipt.ProcessStarted = started
	receipt.Exit = exit
	return receipt
}

func receiptForTermination(outcome Outcome, reason ReasonCode, terminationReason TerminationReason, started bool) Receipt {
	receipt := validReceipt()
	receipt.Outcome = outcome
	receipt.ReasonCode = reason
	receipt.ProcessStarted = started
	if started {
		receipt.Termination = &Termination{Reason: terminationReason, TreeTermination: TreeTerminationCompleted}
		receipt.Exit = &Exit{Signal: pointer("SIGKILL")}
	} else {
		receipt.Termination = nil
		receipt.Exit = nil
	}
	return receipt
}

func receiptWithTimeout(timeout time.Duration) Receipt {
	receipt := validReceipt()
	receipt.EffectiveTimeoutNS = pointer(int64(timeout))
	return receipt
}
