// Package executionreceipt defines Madari's independently versioned execution
// receipt contract. Receipts are self-reported evidence, not cryptographic
// attestation.
package executionreceipt

import "time"

// SchemaVersion is the only execution receipt schema version emitted or read.
const SchemaVersion = 1

const (
	EvidenceKindSelfReported = "self-reported"
	ProducerNameMadari       = "madari"
	TargetCodex              = "codex"
)

type Phase string

const (
	PhasePlanning  Phase = "planning"
	PhaseExecution Phase = "execution"
)

type Outcome string

const (
	OutcomeBlocked   Outcome = "blocked"
	OutcomeSuccess   Outcome = "success"
	OutcomeFailure   Outcome = "failure"
	OutcomeTimeout   Outcome = "timeout"
	OutcomeCancelled Outcome = "cancelled"
)

type ReasonCode string

const (
	ReasonNone                      ReasonCode = "none"
	ReasonInvalidInvocation         ReasonCode = "invalid-invocation"
	ReasonLaunchNotReady            ReasonCode = "launch-not-ready"
	ReasonPolicyBlocked             ReasonCode = "policy-blocked"
	ReasonArtifactCompilationFailed ReasonCode = "artifact-compilation-failed"
	ReasonPreparationFailed         ReasonCode = "preparation-failed"
	ReasonProcessStartFailed        ReasonCode = "process-start-failed"
	ReasonProcessFailed             ReasonCode = "process-failed"
	ReasonContainmentFailed         ReasonCode = "containment-failed"
	ReasonTimeout                   ReasonCode = "timeout"
	ReasonCancelled                 ReasonCode = "cancelled"
)

type AuthorityControl string

const (
	ControlMCPAccess                  AuthorityControl = "mcp-access"
	ControlMCPToolFiltering           AuthorityControl = "mcp-tool-filtering"
	ControlOAuthScopes                AuthorityControl = "oauth-scopes"
	ControlToolApprovals              AuthorityControl = "tool-approvals"
	ControlInstructions               AuthorityControl = "instructions"
	ControlAmbientEnvironment         AuthorityControl = "ambient-environment"
	ControlClientSandbox              AuthorityControl = "client-sandbox"
	ControlMaxDuration                AuthorityControl = "max-duration"
	ControlCredentialExposure         AuthorityControl = "credential-exposure"
	ControlStdioFilesystemConfinement AuthorityControl = "stdio-filesystem-confinement"
	ControlStdioNetworkConfinement    AuthorityControl = "stdio-network-confinement"
)

type EnforcedBy string

const (
	EnforcedByProvider EnforcedBy = "provider"
	EnforcedByClient   EnforcedBy = "client"
	EnforcedByProcess  EnforcedBy = "process"
	EnforcedByAdvisory EnforcedBy = "advisory"
	EnforcedByNone     EnforcedBy = "none"
)

type Verification string

const (
	VerificationObserved   Verification = "observed"
	VerificationConfigured Verification = "configured"
	VerificationUnverified Verification = "unverified"
)

type Classification string

const (
	ClassificationExact    Classification = "exact"
	ClassificationAdvisory Classification = "advisory"
	ClassificationDegraded Classification = "degraded"
	ClassificationBlocked  Classification = "blocked"
	ClassificationNone     Classification = "none"
)

type RecipientKind string

const (
	RecipientCodexProcess RecipientKind = "codex-process"
	RecipientStdioServer  RecipientKind = "stdio-server"
	RecipientRemoteAuth   RecipientKind = "remote-auth"
)

type TerminationReason string

const (
	TerminationTimeout   TerminationReason = "timeout"
	TerminationCancelled TerminationReason = "cancelled"
)

type TreeTermination string

const (
	TreeTerminationCompleted  TreeTermination = "completed"
	TreeTerminationIncomplete TreeTermination = "incomplete"
)

// Receipt is the complete V1 wire contract. No field is optional on the wire;
// fields that may be unavailable are represented by explicit JSON null values.
type Receipt struct {
	SchemaVersion        int                     `json:"schema_version"`
	Evidence             Evidence                `json:"evidence"`
	RunID                string                  `json:"run_id"`
	Producer             Producer                `json:"producer"`
	Target               string                  `json:"target"`
	StartedAt            time.Time               `json:"started_at"`
	FinishedAt           time.Time               `json:"finished_at"`
	DurationMS           int64                   `json:"duration_ms"`
	Phase                Phase                   `json:"phase"`
	Outcome              Outcome                 `json:"outcome"`
	ReasonCode           ReasonCode              `json:"reason_code"`
	Artifact             *Artifact               `json:"artifact"`
	Client               *Client                 `json:"client"`
	Rings                []Component             `json:"rings"`
	Servers              []Component             `json:"servers"`
	Skills               []Component             `json:"skills"`
	Authority            Authority               `json:"authority"`
	ForwardedEnvironment []EnvironmentForwarding `json:"forwarded_environment"`
	EffectiveTimeoutNS   *int64                  `json:"effective_timeout_ns"`
	ProcessStarted       bool                    `json:"process_started"`
	Termination          *Termination            `json:"termination"`
	Exit                 *Exit                   `json:"exit"`
}

type Evidence struct {
	Kind                     string `json:"kind"`
	CryptographicAttestation bool   `json:"cryptographic_attestation"`
}

type Producer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Artifact struct {
	LaunchDigest string `json:"launch_digest"`
	PolicyDigest string `json:"policy_digest"`
}

type Client struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Component struct {
	Name   string  `json:"name"`
	SHA256 *string `json:"sha256"`
}

type Authority struct {
	Requested []AuthorityRecord `json:"requested"`
	Effective []AuthorityRecord `json:"effective"`
}

type AuthorityRecord struct {
	Control        AuthorityControl `json:"control"`
	EnforcedBy     EnforcedBy       `json:"enforced_by"`
	Verification   Verification     `json:"verification"`
	Classification Classification   `json:"classification"`
}

type EnvironmentForwarding struct {
	Recipient Recipient `json:"recipient"`
	Keys      []string  `json:"keys"`
}

type Recipient struct {
	Kind RecipientKind `json:"kind"`
	Name string        `json:"name"`
}

type Termination struct {
	Reason          TerminationReason `json:"reason"`
	TreeTermination TreeTermination   `json:"tree_termination"`
}

type Exit struct {
	Code   *int    `json:"code"`
	Signal *string `json:"signal"`
}
