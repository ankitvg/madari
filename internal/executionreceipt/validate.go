package executionreceipt

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var (
	componentNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
	envKeyPattern        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	signalPattern        = regexp.MustCompile(`^(?:SIG[A-Z0-9]+|signal-[0-9]+)$`)
	digestPattern        = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Validate enforces the complete V1 receipt contract.
func (r Receipt) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported execution receipt schema version %d (supported: %d)", r.SchemaVersion, SchemaVersion)
	}
	if r.Evidence.Kind != EvidenceKindSelfReported {
		return fmt.Errorf("evidence.kind must be %q", EvidenceKindSelfReported)
	}
	if r.Evidence.CryptographicAttestation {
		return fmt.Errorf("evidence.cryptographic_attestation must be false")
	}
	if err := validateRunID(r.RunID); err != nil {
		return err
	}
	if r.Producer.Name != ProducerNameMadari {
		return fmt.Errorf("producer.name must be %q", ProducerNameMadari)
	}
	if err := validateBoundedText("producer.version", r.Producer.Version, 128); err != nil {
		return err
	}
	if r.Target != TargetCodex {
		return fmt.Errorf("target must be %q", TargetCodex)
	}
	if err := validateTimestamp("started_at", r.StartedAt); err != nil {
		return err
	}
	if err := validateTimestamp("finished_at", r.FinishedAt); err != nil {
		return err
	}
	if r.FinishedAt.Before(r.StartedAt) {
		return fmt.Errorf("finished_at must not be before started_at")
	}
	if r.DurationMS < 0 {
		return fmt.Errorf("duration_ms must not be negative")
	}
	if elapsed := r.FinishedAt.Sub(r.StartedAt).Milliseconds(); r.DurationMS != elapsed {
		return fmt.Errorf("duration_ms must equal finished_at minus started_at in whole milliseconds (%d)", elapsed)
	}
	if err := validateComponents("rings", r.Rings); err != nil {
		return err
	}
	if err := validateComponents("servers", r.Servers); err != nil {
		return err
	}
	if err := validateComponents("skills", r.Skills); err != nil {
		return err
	}
	if err := validateAuthority(r.Authority); err != nil {
		return err
	}
	if err := validateForwardedEnvironment(r.ForwardedEnvironment); err != nil {
		return err
	}
	if r.EffectiveTimeoutNS != nil && *r.EffectiveTimeoutNS <= 0 {
		return fmt.Errorf("effective_timeout_ns must be positive when present")
	}
	if r.Artifact != nil {
		if err := validateDigest("artifact.launch_digest", r.Artifact.LaunchDigest); err != nil {
			return err
		}
		if err := validateDigest("artifact.policy_digest", r.Artifact.PolicyDigest); err != nil {
			return err
		}
	}
	if r.Client != nil {
		if r.Client.Name != r.Target {
			return fmt.Errorf("client.name must match target %q", r.Target)
		}
		if err := validateBoundedText("client.version", r.Client.Version, 128); err != nil {
			return err
		}
	}
	if err := validateExit(r.Exit); err != nil {
		return err
	}
	if err := validateTermination(r.Termination); err != nil {
		return err
	}

	switch r.Phase {
	case PhasePlanning:
		return r.validatePlanningOutcome()
	case PhaseExecution:
		return r.validateExecutionOutcome()
	default:
		return fmt.Errorf("unsupported phase %q", r.Phase)
	}
}

func (r Receipt) validatePlanningOutcome() error {
	if r.Outcome != OutcomeBlocked {
		return fmt.Errorf("planning phase requires outcome %q", OutcomeBlocked)
	}
	switch r.ReasonCode {
	case ReasonInvalidInvocation, ReasonLaunchNotReady, ReasonPolicyBlocked, ReasonArtifactCompilationFailed:
	default:
		return fmt.Errorf("planning blocked outcome has invalid reason_code %q", r.ReasonCode)
	}
	if r.Artifact != nil {
		return fmt.Errorf("planning blocked receipt must have artifact null")
	}
	if r.Client != nil {
		return fmt.Errorf("planning blocked receipt must have client null")
	}
	if r.EffectiveTimeoutNS != nil {
		return fmt.Errorf("planning blocked receipt must have effective_timeout_ns null")
	}
	if r.ProcessStarted {
		return fmt.Errorf("planning blocked receipt must have process_started false")
	}
	if r.Termination != nil {
		return fmt.Errorf("planning blocked receipt must have termination null")
	}
	if r.Exit != nil {
		return fmt.Errorf("planning blocked receipt must have exit null")
	}
	if len(r.ForwardedEnvironment) != 0 {
		return fmt.Errorf("planning blocked receipt must not report forwarded environment")
	}
	return nil
}

func (r Receipt) validateExecutionOutcome() error {
	if r.Artifact == nil {
		return fmt.Errorf("execution phase requires artifact evidence")
	}
	if r.Client == nil {
		return fmt.Errorf("execution phase requires client evidence")
	}
	if r.EffectiveTimeoutNS == nil {
		return fmt.Errorf("execution phase requires effective_timeout_ns")
	}
	if err := requireComponentHashes("rings", r.Rings); err != nil {
		return err
	}
	if err := requireComponentHashes("servers", r.Servers); err != nil {
		return err
	}
	if err := requireComponentHashes("skills", r.Skills); err != nil {
		return err
	}
	if err := validateExecutionEnvironmentRecipients(r.ForwardedEnvironment, r.Servers); err != nil {
		return err
	}

	switch r.Outcome {
	case OutcomeSuccess:
		if r.ReasonCode != ReasonNone {
			return fmt.Errorf("success outcome requires reason_code %q", ReasonNone)
		}
		if !r.ProcessStarted {
			return fmt.Errorf("success outcome requires process_started true")
		}
		if r.Termination != nil {
			return fmt.Errorf("success outcome requires termination null")
		}
		if r.Exit == nil || r.Exit.Code == nil || *r.Exit.Code != 0 || r.Exit.Signal != nil {
			return fmt.Errorf("success outcome requires exit code 0 and no signal")
		}
	case OutcomeFailure:
		if err := r.validateFailureOutcome(); err != nil {
			return err
		}
	case OutcomeTimeout:
		if r.ReasonCode != ReasonTimeout {
			return fmt.Errorf("timeout outcome requires reason_code %q", ReasonTimeout)
		}
		if r.Termination == nil || r.Termination.Reason != TerminationTimeout {
			return fmt.Errorf("timeout outcome requires timeout termination evidence")
		}
		if !r.ProcessStarted {
			return fmt.Errorf("timeout outcome requires process_started true")
		}
		if err := validateNonSuccessExit(r.Exit); err != nil {
			return err
		}
	case OutcomeCancelled:
		if r.ReasonCode != ReasonCancelled {
			return fmt.Errorf("cancelled outcome requires reason_code %q", ReasonCancelled)
		}
		if r.ProcessStarted {
			if r.Termination == nil || r.Termination.Reason != TerminationCancelled {
				return fmt.Errorf("cancelled outcome after process start requires cancellation termination evidence")
			}
		} else if r.Termination != nil {
			return fmt.Errorf("cancelled outcome before process start requires termination null")
		}
		if err := validateNonSuccessExit(r.Exit); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported execution outcome %q", r.Outcome)
	}
	if !r.ProcessStarted && r.Exit != nil {
		return fmt.Errorf("exit must be null when process_started is false")
	}
	return nil
}

func (r Receipt) validateFailureOutcome() error {
	switch r.ReasonCode {
	case ReasonPreparationFailed, ReasonProcessStartFailed:
		if r.ProcessStarted {
			return fmt.Errorf("reason_code %q requires process_started false", r.ReasonCode)
		}
		if r.Exit != nil {
			return fmt.Errorf("reason_code %q requires exit null", r.ReasonCode)
		}
	case ReasonProcessFailed:
		if !r.ProcessStarted {
			return fmt.Errorf("reason_code %q requires process_started true", r.ReasonCode)
		}
		if err := validateNonSuccessExit(r.Exit); err != nil {
			return err
		}
	case ReasonContainmentFailed:
		if !r.ProcessStarted {
			return fmt.Errorf("reason_code %q requires process_started true", r.ReasonCode)
		}
		if r.Exit == nil || r.Exit.Code == nil || *r.Exit.Code != 0 || r.Exit.Signal != nil {
			return fmt.Errorf("reason_code %q requires exit code 0 and no signal", r.ReasonCode)
		}
	default:
		return fmt.Errorf("failure outcome has invalid reason_code %q", r.ReasonCode)
	}
	if r.Termination != nil {
		return fmt.Errorf("failure outcome requires termination null")
	}
	return nil
}

func validateTimestamp(field string, value interface {
	IsZero() bool
	Zone() (string, int)
}) error {
	if value.IsZero() {
		return fmt.Errorf("%s is required", field)
	}
	_, offset := value.Zone()
	if offset != 0 {
		return fmt.Errorf("%s must be UTC", field)
	}
	return nil
}

func validateComponents(field string, components []Component) error {
	if components == nil {
		return fmt.Errorf("%s must be a JSON array, not null", field)
	}
	if !sort.SliceIsSorted(components, func(i, j int) bool { return components[i].Name < components[j].Name }) {
		return fmt.Errorf("%s must be sorted by name", field)
	}
	for i, component := range components {
		if !componentNamePattern.MatchString(component.Name) {
			return fmt.Errorf("%s[%d].name is invalid", field, i)
		}
		if i > 0 && components[i-1].Name == component.Name {
			return fmt.Errorf("%s contains duplicate name %q", field, component.Name)
		}
		if component.SHA256 != nil {
			if err := validateDigest(fmt.Sprintf("%s[%d].sha256", field, i), *component.SHA256); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireComponentHashes(field string, components []Component) error {
	for i, component := range components {
		if component.SHA256 == nil {
			return fmt.Errorf("execution %s[%d].sha256 must not be null", field, i)
		}
	}
	return nil
}

func validateAuthority(authority Authority) error {
	if err := validateAuthorityRecords("authority.requested", authority.Requested); err != nil {
		return err
	}
	return validateAuthorityRecords("authority.effective", authority.Effective)
}

func validateAuthorityRecords(field string, records []AuthorityRecord) error {
	if records == nil {
		return fmt.Errorf("%s must be a JSON array, not null", field)
	}
	if !sort.SliceIsSorted(records, func(i, j int) bool { return records[i].Control < records[j].Control }) {
		return fmt.Errorf("%s must be sorted by control", field)
	}
	for i, record := range records {
		if !validAuthorityControl(record.Control) {
			return fmt.Errorf("%s[%d].control is unsupported: %q", field, i, record.Control)
		}
		if i > 0 && records[i-1].Control == record.Control {
			return fmt.Errorf("%s contains duplicate control %q", field, record.Control)
		}
		if !validEnforcedBy(record.EnforcedBy) {
			return fmt.Errorf("%s[%d].enforced_by is unsupported: %q", field, i, record.EnforcedBy)
		}
		if !validVerification(record.Verification) {
			return fmt.Errorf("%s[%d].verification is unsupported: %q", field, i, record.Verification)
		}
		if !validClassification(record.Classification) {
			return fmt.Errorf("%s[%d].classification is unsupported: %q", field, i, record.Classification)
		}
	}
	return nil
}

func validateForwardedEnvironment(forwarded []EnvironmentForwarding) error {
	if forwarded == nil {
		return fmt.Errorf("forwarded_environment must be a JSON array, not null")
	}
	keyFor := func(entry EnvironmentForwarding) string {
		return string(entry.Recipient.Kind) + "\x00" + entry.Recipient.Name
	}
	if !sort.SliceIsSorted(forwarded, func(i, j int) bool { return keyFor(forwarded[i]) < keyFor(forwarded[j]) }) {
		return fmt.Errorf("forwarded_environment must be sorted by recipient kind and name")
	}
	for i, entry := range forwarded {
		if !validRecipientKind(entry.Recipient.Kind) {
			return fmt.Errorf("forwarded_environment[%d].recipient.kind is unsupported: %q", i, entry.Recipient.Kind)
		}
		if !componentNamePattern.MatchString(entry.Recipient.Name) {
			return fmt.Errorf("forwarded_environment[%d].recipient.name is invalid", i)
		}
		if entry.Recipient.Kind == RecipientCodexProcess && entry.Recipient.Name != TargetCodex {
			return fmt.Errorf("codex-process recipient name must be %q", TargetCodex)
		}
		if i > 0 && keyFor(forwarded[i-1]) == keyFor(entry) {
			return fmt.Errorf("forwarded_environment contains duplicate recipient %q", entry.Recipient.Name)
		}
		if entry.Keys == nil {
			return fmt.Errorf("forwarded_environment[%d].keys must be a JSON array, not null", i)
		}
		if !sort.StringsAreSorted(entry.Keys) {
			return fmt.Errorf("forwarded_environment[%d].keys must be sorted", i)
		}
		for j, key := range entry.Keys {
			if !envKeyPattern.MatchString(key) {
				return fmt.Errorf("forwarded_environment[%d].keys[%d] is invalid", i, j)
			}
			if j > 0 && entry.Keys[j-1] == key {
				return fmt.Errorf("forwarded_environment[%d].keys contains duplicate %q", i, key)
			}
		}
	}
	return nil
}

func validateExecutionEnvironmentRecipients(forwarded []EnvironmentForwarding, servers []Component) error {
	serverNames := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		serverNames[server.Name] = struct{}{}
	}
	codexRecipients := 0
	for i, entry := range forwarded {
		switch entry.Recipient.Kind {
		case RecipientCodexProcess:
			codexRecipients++
		case RecipientStdioServer, RecipientRemoteAuth:
			if _, ok := serverNames[entry.Recipient.Name]; !ok {
				return fmt.Errorf("forwarded_environment[%d] recipient %q is not a selected server", i, entry.Recipient.Name)
			}
		}
	}
	if codexRecipients != 1 {
		return fmt.Errorf("execution receipt requires exactly one codex-process forwarded_environment recipient")
	}
	return nil
}

func validateExit(exit *Exit) error {
	if exit == nil {
		return nil
	}
	if (exit.Code == nil) == (exit.Signal == nil) {
		return fmt.Errorf("exit must contain exactly one of code or signal")
	}
	if exit.Code != nil && *exit.Code < 0 {
		return fmt.Errorf("exit.code must not be negative")
	}
	if exit.Signal != nil && !signalPattern.MatchString(*exit.Signal) {
		return fmt.Errorf("exit.signal is invalid")
	}
	return nil
}

func validateNonSuccessExit(exit *Exit) error {
	if exit != nil && exit.Code != nil && *exit.Code == 0 {
		return fmt.Errorf("non-success outcome cannot report exit code 0")
	}
	return nil
}

func validateTermination(termination *Termination) error {
	if termination == nil {
		return nil
	}
	if termination.Reason != TerminationTimeout && termination.Reason != TerminationCancelled {
		return fmt.Errorf("termination.reason is unsupported: %q", termination.Reason)
	}
	if termination.TreeTermination != TreeTerminationCompleted && termination.TreeTermination != TreeTerminationIncomplete {
		return fmt.Errorf("termination.tree_termination is unsupported: %q", termination.TreeTermination)
	}
	return nil
}

func validateDigest(field, value string) error {
	if !digestPattern.MatchString(value) {
		return fmt.Errorf("%s must match sha256:<64 lowercase hex>", field)
	}
	return nil
}

func validateBoundedText(field, value string, maxBytes int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must not have leading or trailing whitespace", field)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", field, maxBytes)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain control characters", field)
		}
	}
	return nil
}

func validAuthorityControl(value AuthorityControl) bool {
	switch value {
	case ControlMCPAccess, ControlMCPToolFiltering, ControlOAuthScopes, ControlToolApprovals,
		ControlInstructions, ControlAmbientEnvironment, ControlClientSandbox, ControlMaxDuration,
		ControlCredentialExposure, ControlStdioFilesystemConfinement, ControlStdioNetworkConfinement:
		return true
	default:
		return false
	}
}

func validEnforcedBy(value EnforcedBy) bool {
	switch value {
	case EnforcedByProvider, EnforcedByClient, EnforcedByProcess, EnforcedByAdvisory, EnforcedByNone:
		return true
	default:
		return false
	}
}

func validVerification(value Verification) bool {
	switch value {
	case VerificationObserved, VerificationConfigured, VerificationUnverified:
		return true
	default:
		return false
	}
}

func validClassification(value Classification) bool {
	switch value {
	case ClassificationExact, ClassificationAdvisory, ClassificationDegraded, ClassificationBlocked, ClassificationNone:
		return true
	default:
		return false
	}
}

func validRecipientKind(value RecipientKind) bool {
	switch value {
	case RecipientCodexProcess, RecipientStdioServer, RecipientRemoteAuth:
		return true
	default:
		return false
	}
}
