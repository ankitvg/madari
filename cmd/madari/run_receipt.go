package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ankitvg/madari/internal/executionreceipt"
	"github.com/ankitvg/madari/internal/launch"
	"github.com/ankitvg/madari/internal/proctree"
	"github.com/ankitvg/madari/internal/registry"
)

var (
	newExecutionReceiptRunID = executionreceipt.NewRunID
	writeExecutionReceipt    = executionreceipt.Write
	executionReceiptNow      = func() time.Time { return time.Now().UTC() }
)

type runReceiptInvocation struct {
	path            string
	runID           string
	producerVersion string
	startedAt       time.Time
}

type runReceiptMetadata struct {
	runID           string
	producerVersion string
	startedAt       time.Time
	finishedAt      time.Time
}

// runReceiptPlanSummary is the only planning input accepted by receipt
// construction. It contains names, receipt-safe digests, and typed authority;
// it has no prompt, arguments, environment values, auth material, paths, or
// output.
type runReceiptPlanSummary struct {
	target               string
	artifact             *executionreceipt.Artifact
	client               *executionreceipt.Client
	rings                []executionreceipt.Component
	servers              []executionreceipt.Component
	skills               []executionreceipt.Component
	authority            executionreceipt.Authority
	forwardedEnvironment []executionreceipt.EnvironmentForwarding
	effectiveTimeoutNS   *int64
}

// runReceiptResultSummary is derived only from typed lifecycle state. Raw
// execution errors are returned to the caller but never enter this structure.
type runReceiptResultSummary struct {
	phase          executionreceipt.Phase
	outcome        executionreceipt.Outcome
	reason         executionreceipt.ReasonCode
	processStarted bool
	termination    *executionreceipt.Termination
	exit           *executionreceipt.Exit
}

func emptyRunReceiptPlanSummary(target string) runReceiptPlanSummary {
	return runReceiptPlanSummary{
		target: strings.TrimSpace(target),
		rings:  []executionreceipt.Component{}, servers: []executionreceipt.Component{}, skills: []executionreceipt.Component{},
		authority:            executionreceipt.Authority{Requested: []executionreceipt.AuthorityRecord{}, Effective: []executionreceipt.AuthorityRecord{}},
		forwardedEnvironment: []executionreceipt.EnvironmentForwarding{},
	}
}

func beginRunReceipt(path, producerVersion string) (*runReceiptInvocation, error) {
	startedAt := executionReceiptNow().UTC()
	runID, err := newExecutionReceiptRunID()
	if err != nil {
		return nil, err
	}
	return &runReceiptInvocation{
		path: path, runID: runID, producerVersion: producerVersion, startedAt: startedAt,
	}, nil
}

func (i *runReceiptInvocation) finish(plan runReceiptPlanSummary, result runReceiptResultSummary, runErr error) error {
	if i == nil {
		return runErr
	}
	finishedAt := executionReceiptNow().UTC()
	if finishedAt.Before(i.startedAt) {
		finishedAt = i.startedAt
	}
	receipt := buildRunReceipt(runReceiptMetadata{
		runID: i.runID, producerVersion: i.producerVersion, startedAt: i.startedAt, finishedAt: finishedAt,
	}, plan, result)
	receiptErr := writeExecutionReceipt(i.path, receipt)
	if receiptErr != nil {
		receiptErr = fmt.Errorf("write run receipt: %w", receiptErr)
	}
	return errors.Join(runErr, receiptErr)
}

func buildRunReceipt(metadata runReceiptMetadata, plan runReceiptPlanSummary, result runReceiptResultSummary) executionreceipt.Receipt {
	receipt := executionreceipt.Receipt{
		SchemaVersion: executionreceipt.SchemaVersion,
		Evidence: executionreceipt.Evidence{
			Kind: executionreceipt.EvidenceKindSelfReported,
		},
		RunID:          metadata.runID,
		Producer:       executionreceipt.Producer{Name: executionreceipt.ProducerNameMadari, Version: metadata.producerVersion},
		Target:         plan.target,
		StartedAt:      metadata.startedAt,
		FinishedAt:     metadata.finishedAt,
		DurationMS:     metadata.finishedAt.Sub(metadata.startedAt).Milliseconds(),
		Phase:          result.phase,
		Outcome:        result.outcome,
		ReasonCode:     result.reason,
		Artifact:       cloneReceiptArtifact(plan.artifact),
		Client:         cloneReceiptClient(plan.client),
		Rings:          cloneReceiptComponents(plan.rings),
		Servers:        cloneReceiptComponents(plan.servers),
		Skills:         cloneReceiptComponents(plan.skills),
		Authority:      cloneReceiptAuthority(plan.authority),
		ProcessStarted: result.processStarted,
		Termination:    cloneReceiptTermination(result.termination),
		Exit:           cloneReceiptExit(result.exit),
	}
	if result.phase == executionreceipt.PhaseExecution {
		receipt.ForwardedEnvironment = cloneReceiptForwarding(plan.forwardedEnvironment)
		receipt.EffectiveTimeoutNS = cloneInt64(plan.effectiveTimeoutNS)
	} else {
		receipt.ForwardedEnvironment = []executionreceipt.EnvironmentForwarding{}
	}
	return receipt
}

func sanitizeRunPlanForReceipt(plan runLaunchPlan) runReceiptPlanSummary {
	summary := emptyRunReceiptPlanSummary(executionreceipt.TargetCodex)
	summary.rings = receiptComponentsForKnownRings(plan)
	summary.servers = receiptComponentsForPlanServers(plan.Servers)
	summary.skills = receiptComponentsForPlanSkills(plan.Skills)
	summary.authority = receiptAuthorityFromLaunch(plan.Authority)
	if strings.TrimSpace(plan.Target) != "" {
		summary.target = strings.TrimSpace(plan.Target)
	}

	servers := receiptManifestSnapshot(plan)
	summary.forwardedEnvironment = receiptForwardingFromPlan(plan.Env, servers)
	if plan.Artifact == nil {
		return summary
	}

	artifact := plan.Artifact
	summary.artifact = &executionreceipt.Artifact{
		LaunchDigest: prefixedSHA256(artifact.Digest()),
		PolicyDigest: prefixedSHA256(artifact.PolicyDigest()),
	}
	summary.client = &executionreceipt.Client{Name: artifact.Target(), Version: artifact.ClientVersion()}
	hashes := artifact.ReceiptContentHashes()
	summary.rings = receiptComponentsFromHashes(hashes.Rings)
	summary.servers = receiptComponentsFromHashes(hashes.Servers)
	summary.skills = receiptComponentsFromHashes(hashes.Skills)
	summary.authority = receiptAuthorityFromLaunch(artifact.Authority())
	timeoutNS := int64(artifact.MaxDuration())
	summary.effectiveTimeoutNS = &timeoutNS
	return summary
}

func planningBlockedReceiptResult() runReceiptResultSummary {
	return runReceiptResultSummary{
		phase: executionreceipt.PhasePlanning, outcome: executionreceipt.OutcomeBlocked,
		reason: executionreceipt.ReasonLaunchNotReady,
	}
}

func planningFailureReceiptResult() runReceiptResultSummary {
	return runReceiptResultSummary{
		phase: executionreceipt.PhasePlanning, outcome: executionreceipt.OutcomeBlocked,
		reason: executionreceipt.ReasonPlanningFailed,
	}
}

func preparationFailureReceiptResult() runReceiptResultSummary {
	return runReceiptResultSummary{
		phase: executionreceipt.PhaseExecution, outcome: executionreceipt.OutcomeFailure,
		reason: executionreceipt.ReasonPreparationFailed,
	}
}

func receiptResultFromProcess(result proctree.Result) runReceiptResultSummary {
	summary := runReceiptResultSummary{
		phase: executionreceipt.PhaseExecution, processStarted: result.ProcessStarted,
		termination: receiptTerminationFromProcess(result.Termination),
		exit:        receiptExitFromProcess(result.Exit),
	}
	switch result.Outcome {
	case proctree.OutcomeSuccess:
		summary.outcome = executionreceipt.OutcomeSuccess
		summary.reason = executionreceipt.ReasonNone
	case proctree.OutcomeFailure:
		summary.outcome = executionreceipt.OutcomeFailure
		summary.reason = executionreceipt.ReasonProcessFailed
		if result.Exit != nil && result.Exit.Signal == "" && result.Exit.Code == 0 {
			summary.reason = executionreceipt.ReasonContainmentFailed
		}
	case proctree.OutcomeTimeout:
		summary.outcome = executionreceipt.OutcomeTimeout
		summary.reason = executionreceipt.ReasonTimeout
	case proctree.OutcomeCancelled:
		summary.outcome = executionreceipt.OutcomeCancelled
		summary.reason = executionreceipt.ReasonCancelled
	case proctree.OutcomeStartFailed:
		summary.outcome = executionreceipt.OutcomeFailure
		summary.reason = executionreceipt.ReasonProcessStartFailed
	default:
		return preparationFailureReceiptResult()
	}
	return summary
}

func receiptTerminationFromProcess(value *proctree.Termination) *executionreceipt.Termination {
	if value == nil {
		return nil
	}
	return &executionreceipt.Termination{
		Reason:          executionreceipt.TerminationReason(value.Reason),
		TreeTermination: executionreceipt.TreeTermination(value.TreeTermination),
	}
}

func receiptExitFromProcess(value *proctree.Exit) *executionreceipt.Exit {
	if value == nil {
		return nil
	}
	if value.Signal != "" {
		signal := value.Signal
		return &executionreceipt.Exit{Signal: &signal}
	}
	code := value.Code
	return &executionreceipt.Exit{Code: &code}
}

func receiptManifestSnapshot(plan runLaunchPlan) []registry.Manifest {
	if plan.Artifact != nil {
		return plan.Artifact.Servers()
	}
	manifests := make([]registry.Manifest, 0, len(plan.Servers))
	for _, server := range plan.Servers {
		if strings.TrimSpace(server.Manifest.Name) != "" {
			manifests = append(manifests, server.Manifest)
		}
	}
	return manifests
}

func receiptForwardingFromPlan(env []runPlanEnv, servers []registry.Manifest) []executionreceipt.EnvironmentForwarding {
	processKeys := make([]string, 0, len(env))
	for _, requirement := range env {
		if key := strings.TrimSpace(requirement.Key); key != "" {
			processKeys = append(processKeys, key)
		}
	}
	forwarded := []executionreceipt.EnvironmentForwarding{{
		Recipient: executionreceipt.Recipient{Kind: executionreceipt.RecipientCodexProcess, Name: executionreceipt.TargetCodex},
		Keys:      sortedUniqueReceiptStrings(processKeys),
	}}
	for _, manifest := range servers {
		if manifest.IsRemote() {
			if key := strings.TrimSpace(manifest.BearerTokenEnvVar); key != "" {
				forwarded = append(forwarded, executionreceipt.EnvironmentForwarding{
					Recipient: executionreceipt.Recipient{Kind: executionreceipt.RecipientRemoteAuth, Name: manifest.Name},
					Keys:      []string{key},
				})
			}
			continue
		}
		keys := make([]string, 0, len(manifest.Env)+len(manifest.RequiredEnv.Keys)+len(manifest.SecretEnv.Keys))
		for key := range manifest.Env {
			keys = append(keys, key)
		}
		keys = append(keys, manifest.RequiredEnv.Keys...)
		keys = append(keys, manifest.SecretEnv.Keys...)
		forwarded = append(forwarded, executionreceipt.EnvironmentForwarding{
			Recipient: executionreceipt.Recipient{Kind: executionreceipt.RecipientStdioServer, Name: manifest.Name},
			Keys:      sortedUniqueReceiptStrings(keys),
		})
	}
	sort.Slice(forwarded, func(i, j int) bool {
		left := string(forwarded[i].Recipient.Kind) + "\x00" + forwarded[i].Recipient.Name
		right := string(forwarded[j].Recipient.Kind) + "\x00" + forwarded[j].Recipient.Name
		return left < right
	})
	return forwarded
}

func receiptAuthorityFromLaunch(value launch.Authority) executionreceipt.Authority {
	convert := func(values []launch.AuthorityControl) []executionreceipt.AuthorityRecord {
		out := make([]executionreceipt.AuthorityRecord, 0, len(values))
		for _, control := range values {
			out = append(out, executionreceipt.AuthorityRecord{
				Control: executionreceipt.AuthorityControl(control.Control), EnforcedBy: executionreceipt.EnforcedBy(control.EnforcedBy),
				Verification: executionreceipt.Verification(control.Verification), Classification: executionreceipt.Classification(control.Classification),
			})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Control < out[j].Control })
		return out
	}
	return executionreceipt.Authority{Requested: convert(value.Requested), Effective: convert(value.Effective)}
}

func receiptComponentsFromHashes(values []launch.NamedHash) []executionreceipt.Component {
	out := make([]executionreceipt.Component, 0, len(values))
	for _, value := range values {
		digest := prefixedSHA256(value.SHA256)
		out = append(out, executionreceipt.Component{Name: value.Name, SHA256: &digest})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func receiptComponentsForKnownRings(plan runLaunchPlan) []executionreceipt.Component {
	names := append([]string(nil), plan.Rings...)
	for _, server := range plan.Servers {
		names = append(names, server.Rings...)
	}
	for _, skill := range plan.Skills {
		names = append(names, skill.Rings...)
	}
	return receiptComponentsForNames(names)
}

func receiptComponentsForPlanServers(values []runPlanServer) []executionreceipt.Component {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.Name)
	}
	return receiptComponentsForNames(names)
}

func receiptComponentsForPlanSkills(values []runPlanSkill) []executionreceipt.Component {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.Name)
	}
	return receiptComponentsForNames(names)
}

func receiptComponentsForNames(values []string) []executionreceipt.Component {
	names := sortedUniqueReceiptStrings(values)
	out := make([]executionreceipt.Component, 0, len(names))
	for _, name := range names {
		out = append(out, executionreceipt.Component{Name: name})
	}
	return out
}

func sortedUniqueReceiptStrings(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func prefixedSHA256(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "sha256:") {
		return value
	}
	return "sha256:" + value
}

func cloneReceiptArtifact(value *executionreceipt.Artifact) *executionreceipt.Artifact {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneReceiptClient(value *executionreceipt.Client) *executionreceipt.Client {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneReceiptComponents(values []executionreceipt.Component) []executionreceipt.Component {
	out := make([]executionreceipt.Component, len(values))
	for i, value := range values {
		out[i] = value
		if value.SHA256 != nil {
			digest := *value.SHA256
			out[i].SHA256 = &digest
		}
	}
	return out
}

func cloneReceiptAuthority(value executionreceipt.Authority) executionreceipt.Authority {
	requested := make([]executionreceipt.AuthorityRecord, len(value.Requested))
	copy(requested, value.Requested)
	effective := make([]executionreceipt.AuthorityRecord, len(value.Effective))
	copy(effective, value.Effective)
	return executionreceipt.Authority{Requested: requested, Effective: effective}
}

func cloneReceiptForwarding(values []executionreceipt.EnvironmentForwarding) []executionreceipt.EnvironmentForwarding {
	out := make([]executionreceipt.EnvironmentForwarding, len(values))
	for i, value := range values {
		out[i] = value
		out[i].Keys = append([]string(nil), value.Keys...)
		if out[i].Keys == nil {
			out[i].Keys = []string{}
		}
	}
	return out
}

func cloneReceiptTermination(value *executionreceipt.Termination) *executionreceipt.Termination {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneReceiptExit(value *executionreceipt.Exit) *executionreceipt.Exit {
	if value == nil {
		return nil
	}
	copy := *value
	if value.Code != nil {
		code := *value.Code
		copy.Code = &code
	}
	if value.Signal != nil {
		signal := *value.Signal
		copy.Signal = &signal
	}
	return &copy
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
