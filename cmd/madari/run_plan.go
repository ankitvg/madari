package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/ankitvg/madari/internal/clients"
	codexclient "github.com/ankitvg/madari/internal/clients/codex"
	"github.com/ankitvg/madari/internal/launch"
	"github.com/ankitvg/madari/internal/policy"
	"github.com/ankitvg/madari/internal/proctree"
	"github.com/ankitvg/madari/internal/registry"
)

const runDryRunOnlyMessage = "madari run execution is only implemented for codex; pass --dry-run to inspect the launch plan"

type runPlanJSON struct {
	SchemaVersion   int                  `json:"schema_version"`
	Command         string               `json:"command"`
	Target          string               `json:"target"`
	Rings           []string             `json:"rings"`
	Ready           bool                 `json:"ready"`
	RunnerAvailable bool                 `json:"runner_available"`
	PromptProvided  bool                 `json:"prompt_provided"`
	PolicyRequired  bool                 `json:"policy_required"`
	PolicyControls  runPolicyControls    `json:"policy_controls"`
	LaunchDigest    string               `json:"launch_digest,omitempty"`
	PolicyDigest    string               `json:"policy_digest,omitempty"`
	ContentHashes   launch.ContentHashes `json:"content_hashes"`
	Authority       launch.Authority     `json:"authority"`
	Execution       runPlanExecution     `json:"execution"`
	Servers         []runPlanServer      `json:"servers"`
	Skills          []runPlanSkill       `json:"skills"`
	Env             []runPlanEnv         `json:"env"`
	Warnings        []string             `json:"warnings"`
	Errors          []string             `json:"errors"`
}

type runLaunchPlan struct {
	Target          string
	Rings           []string
	Ready           bool
	RunnerAvailable bool
	PromptProvided  bool
	PolicyRequired  bool
	PolicyControls  runPolicyControls
	LaunchDigest    string
	PolicyDigest    string
	ContentHashes   launch.ContentHashes
	Authority       launch.Authority
	Execution       runPlanExecution
	Servers         []runPlanServer
	Skills          []runPlanSkill
	Env             []runPlanEnv
	Warnings        []string
	Errors          []string
	Artifact        *launch.Artifact
}

type runPlanServer struct {
	Name       string              `json:"name"`
	Transport  string              `json:"transport"`
	Endpoint   string              `json:"endpoint"`
	Status     string              `json:"status"`
	Auth       string              `json:"auth,omitempty"`
	RuntimeEnv []string            `json:"runtime_env"`
	Rings      []string            `json:"rings"`
	Issues     []string            `json:"issues"`
	Policy     runPlanServerPolicy `json:"policy"`
	Manifest   registry.Manifest   `json:"-"`
}

type runPolicyControls struct {
	ToolFiltering string `json:"tool_filtering"`
	OAuthScopes   string `json:"oauth_scopes"`
	Approvals     string `json:"approvals"`
	Instructions  string `json:"instructions"`
}

type runPlanServerPolicy struct {
	Declared                  *accessJSON                  `json:"declared,omitempty"`
	RingEnforcement           string                       `json:"ring_enforcement"`
	RequiredBy                []string                     `json:"required_by"`
	Effective                 *runPlanEffectiveCodexPolicy `json:"effective,omitempty"`
	SupportState              string                       `json:"support_state"`
	EnforcementClassification string                       `json:"enforcement_classification"`
}

type runPlanEffectiveCodexPolicy struct {
	EnabledTools             []string          `json:"enabled_tools"`
	DisabledTools            []string          `json:"disabled_tools"`
	RequestedOAuthScopes     []string          `json:"requested_oauth_scopes"`
	DefaultToolsApprovalMode string            `json:"default_tools_approval_mode,omitempty"`
	ToolApprovalModes        map[string]string `json:"tool_approval_modes"`
}

type runPlanSkill struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Rings  []string `json:"rings"`
	Issues []string `json:"issues"`
}

type runPlanEnv struct {
	Key     string   `json:"key"`
	Present bool     `json:"present"`
	Servers []string `json:"servers"`
}

type runPlanExecution struct {
	AmbientEnv         string `json:"ambient_env"`
	Sandbox            string `json:"sandbox"`
	MaxDuration        string `json:"max_duration"`
	CredentialExposure string `json:"credential_exposure"`
	Supported          bool   `json:"supported"`
	Declared           bool   `json:"declared"`
	Required           bool   `json:"required"`
	StdioConfinement   string `json:"stdio_confinement"`
}

type runBuildOptions struct {
	maxDuration *time.Duration
}

func (a cliApp) cmdRun(args []string) error {
	if len(args) == 0 {
		return commandUsageError("run", "madari run <client> --ring <ring> [--ring <ring> ...] [--max-duration <duration>] [--receipt <path>] [--dry-run] -- <prompt>")
	}
	if isHelpToken(args[0]) {
		printRunHelp(a.stdout)
		return nil
	}

	target := strings.TrimSpace(args[0])
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var rings stringList
	var dryRun bool
	var jsonOutput bool
	var maxDurationText string
	var receiptPathText string
	fs.Var(&rings, "ring", "Ring to include in the launch plan (repeatable)")
	fs.BoolVar(&dryRun, "dry-run", false, "Inspect the launch plan without starting the client")
	fs.BoolVar(&jsonOutput, "json", false, "Emit JSON instead of text")
	fs.StringVar(&maxDurationText, "max-duration", "", "Shorten the maximum run duration")
	fs.StringVar(&receiptPathText, "receipt", "", "Write a versioned execution receipt")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRunHelp(a.stdout)
			return nil
		}
		return commandInputError("run", err.Error())
	}

	if target == "" {
		return commandInputError("run", "client is required")
	}
	receiptRequested := false
	fs.Visit(func(value *flag.Flag) {
		if value.Name == "receipt" {
			receiptRequested = true
		}
	})
	if jsonOutput && !dryRun {
		return commandInputError("run", "--json is only supported with --dry-run")
	}
	if receiptRequested && dryRun {
		return commandInputError("run", "--receipt cannot be used with --dry-run")
	}
	if receiptRequested && target != codexclient.Target {
		return commandInputError("run", "--receipt is only supported for codex")
	}
	normalizedRings := normalizedRingNames(rings)
	if len(normalizedRings) == 0 {
		return commandInputError("run", "--ring is required")
	}
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		return commandInputError("run", "prompt is required")
	}

	var buildOptions runBuildOptions
	if maxDurationText != "" {
		if maxDurationText != strings.TrimSpace(maxDurationText) {
			return commandInputError("run", "--max-duration must not have leading or trailing whitespace")
		}
		duration, err := time.ParseDuration(maxDurationText)
		if err != nil || duration <= 0 {
			return commandInputError("run", "--max-duration must be a positive Go duration")
		}
		buildOptions.maxDuration = &duration
	}
	var receiptPath string
	var receiptInvocation *runReceiptInvocation
	if receiptRequested {
		resolved, err := resolveRunReceiptPath(receiptPathText)
		if err != nil {
			return commandInputError("run", fmt.Sprintf("invalid --receipt: %v", err))
		}
		receiptPath = resolved
		receiptInvocation, err = beginRunReceipt(receiptPath, version)
		if err != nil {
			return fmt.Errorf("initialize run receipt: %w", err)
		}
	}

	plan, err := a.buildRunPlanWithOptions(target, normalizedRings, prompt, buildOptions)
	if err != nil {
		return receiptInvocation.finish(emptyRunReceiptPlanSummary(target), planningFailureReceiptResult(), err)
	}
	var receiptPlan runReceiptPlanSummary
	if receiptInvocation != nil {
		receiptPlan = sanitizeRunPlanForReceipt(plan)
	}
	if dryRun && jsonOutput {
		if err := writeJSON(a.stdout, plan.toJSON()); err != nil {
			return err
		}
	} else if dryRun {
		printRunPlan(a.stdout, plan)
	}
	if !plan.Ready {
		if !dryRun {
			printRunPlan(a.stdout, plan)
		}
		runErr := commandInputError("run", "launch plan is not ready")
		return receiptInvocation.finish(receiptPlan, planningBlockedReceiptResult(), runErr)
	}
	if dryRun {
		return nil
	}
	executor, ok := runExecutorForTarget(target)
	if !ok {
		runErr := commandInputError("run", runDryRunOnlyMessage)
		return receiptInvocation.finish(receiptPlan, preparationFailureReceiptResult(), runErr)
	}
	ctx, stop := runExecutionContext()
	defer stop()
	result, runErr := executor(ctx, a, plan)
	if runErr == nil && result.Outcome != proctree.OutcomeSuccess {
		runErr = fmt.Errorf("run executor returned non-success outcome %q without an error", result.Outcome)
	}
	return receiptInvocation.finish(receiptPlan, receiptResultFromProcess(result), runErr)
}

func (a cliApp) buildRunPlan(target string, ringNames []string, prompt string) (runLaunchPlan, error) {
	return a.buildRunPlanWithOptions(target, ringNames, prompt, runBuildOptions{})
}

func (a cliApp) buildRunPlanWithOptions(target string, ringNames []string, prompt string, options runBuildOptions) (runLaunchPlan, error) {
	plan := runLaunchPlan{
		Target:          target,
		Rings:           nonNilStrings(normalizedRingNames(ringNames)),
		RunnerAvailable: false,
		PromptProvided:  strings.TrimSpace(prompt) != "",
		PolicyControls: runPolicyControls{
			ToolFiltering: "client-enforced",
			OAuthScopes:   "requested/client-configured/provider-unverified",
			Approvals:     "client-control/not-authorization",
			Instructions:  "contracts-and-skills-advisory",
		},
	}
	addPlanError := func(message string) {
		plan.Errors = append(plan.Errors, message)
	}

	if _, ok := clientTargetByName(target); !ok {
		addPlanError(fmt.Sprintf("unsupported run target %q (supported: %s)", target, strings.Join(sortedClientTargetNames(), ", ")))
		plan.finish()
		return plan, nil
	}
	rt, runnerImplemented := runTargetByName(target)
	if runnerImplemented {
		plan.RunnerAvailable = true
		if strings.TrimSpace(rt.executable) != "" {
			if _, err := exec.LookPath(rt.executable); err != nil {
				plan.RunnerAvailable = false
				addPlanError(fmt.Sprintf("%s executable not found in PATH; install the %s CLI before running this target", rt.executable, target))
			}
		}
		if rt.planPreflight != nil {
			if err := rt.planPreflight(); err != nil {
				addPlanError(err.Error())
			}
		}
	}
	for ring, count := range countStrings(plan.Rings) {
		if count > 1 {
			addPlanError(fmt.Sprintf("duplicate ring %q", ring))
		}
	}

	manifests, err := a.store.List()
	if err != nil {
		return runLaunchPlan{}, err
	}
	manifestByName := make(map[string]registry.Manifest, len(manifests))
	for _, manifest := range manifests {
		manifestByName[manifest.Name] = manifest
	}

	serverRings := map[string][]string{}
	skillRings := map[string][]string{}
	selectedRings := make([]registry.Ring, 0, len(plan.Rings))
	requiredByServer := map[string][]string{}
	policySupportByServer := map[string]string{}
	for _, name := range plan.Rings {
		ring, err := a.store.GetRing(name)
		if err != nil {
			if errors.Is(err, registry.ErrRingNotFound) {
				addPlanError(fmt.Sprintf("ring %q not found", name))
				continue
			}
			return runLaunchPlan{}, err
		}
		selectedRings = append(selectedRings, ring)
		validation := policy.ValidateRequiredRing(ring, manifests, target, policy.SurfaceRun)
		if err := validation.Err(); err != nil {
			addPlanError(err.Error())
		}
		if ring.RequiresPolicyEnforcement() {
			plan.PolicyRequired = true
			for _, member := range ring.Members {
				member = strings.TrimSpace(member)
				if member != "" {
					requiredByServer[member] = appendUniqueName(requiredByServer[member], name)
					policySupportByServer[member] = strongestPolicySupport(policySupportByServer[member], "supported")
				}
			}
			for _, issue := range validation.Issues {
				state := policySupportForIssue(issue)
				if issue.Member != "" {
					policySupportByServer[issue.Member] = strongestPolicySupport(policySupportByServer[issue.Member], state)
					continue
				}
				for _, member := range ring.Members {
					member = strings.TrimSpace(member)
					if member != "" {
						policySupportByServer[member] = strongestPolicySupport(policySupportByServer[member], state)
					}
				}
			}
		}
		for _, member := range ring.Members {
			member = strings.TrimSpace(member)
			if member != "" {
				serverRings[member] = appendUniqueName(serverRings[member], name)
			}
		}
		for _, skill := range ring.Skills {
			skill = strings.TrimSpace(skill)
			if skill != "" {
				skillRings[skill] = appendUniqueName(skillRings[skill], name)
			}
		}
	}
	declaredAccessServers := map[string]bool{}
	for name := range serverRings {
		if manifest, exists := manifestByName[name]; exists && manifest.Access != nil {
			declaredAccessServers[name] = true
		}
	}
	baselineEnv := map[string]string(nil)
	clientInput := launch.ClientInput{}
	clientCompatibilityBlocked := false
	if target == codexclient.Target && plan.RunnerAvailable {
		baselineEnv = codexPlatformBaseline()
		clientInput, err = inspectCodexRunClient(baselineEnv)
		if err != nil {
			clientCompatibilityBlocked = true
			addPlanError(err.Error())
		}
	}
	policyRuntimeBlocked := false
	if target == codexclient.Target && (plan.PolicyRequired || len(declaredAccessServers) > 0) {
		if !plan.RunnerAvailable {
			policyRuntimeBlocked = true
		} else if clientCompatibilityBlocked {
			policyRuntimeBlocked = true
		}
		if policyRuntimeBlocked {
			for name := range requiredByServer {
				policySupportByServer[name] = strongestPolicySupport(policySupportByServer[name], "unsupported")
			}
			for name := range declaredAccessServers {
				policySupportByServer[name] = strongestPolicySupport(policySupportByServer[name], "unsupported")
			}
		}
	}

	envRequirements := map[string][]string{}
	serverNames := sortedStringSliceMapKeys(serverRings)
	for _, name := range serverNames {
		rings := serverRings[name]
		server := runPlanServer{
			Name:       name,
			Status:     "ready",
			RuntimeEnv: []string{},
			Rings:      nonNilStrings(append([]string(nil), rings...)),
			Issues:     []string{},
		}
		manifest, exists := manifestByName[name]
		if !exists {
			server.Status = "blocked"
			server.Issues = append(server.Issues, "server is missing from the registry")
			policySupportByServer[name] = strongestPolicySupport(policySupportByServer[name], "invalid")
			server.Policy = buildRunPlanServerPolicy(nil, target, requiredByServer[name], policySupportByServer[name], true)
			plan.Servers = append(plan.Servers, server)
			addPlanError(fmt.Sprintf("ring member %s no longer exists in the registry", name))
			continue
		}
		server.Manifest = manifest
		server.Transport = manifest.TransportType()
		server.Endpoint = manifestEndpoint(manifest)
		server.Auth = runPlanAuth(manifest)
		server.RuntimeEnv = runtimeEnvKeys(manifest.RequiredEnv.Keys, manifest.SecretEnv.Keys)
		if manifest.RequiresBearerTokenEnv() {
			server.RuntimeEnv = runtimeEnvKeys(server.RuntimeEnv, []string{manifest.BearerTokenEnvVar})
		}

		if !manifest.Enabled {
			server.Issues = append(server.Issues, "server is disabled")
		}
		if !manifest.HasClient(target) {
			server.Issues = append(server.Issues, fmt.Sprintf("server does not target %s", target))
		}
		if manifest.IsRemote() {
			if detail, unsupported := unsupportedRemoteForTarget(manifest, target); unsupported {
				if detail.Auth != "" {
					server.Issues = append(server.Issues, fmt.Sprintf("requires %s, which %s run does not support yet", detail.Auth, target))
				} else {
					server.Issues = append(server.Issues, fmt.Sprintf("uses %s transport, which %s run does not support yet", detail.Transport, target))
				}
			}
			if runnerImplemented {
				if secretNames := manifest.SecretHeaderNames(); len(secretNames) > 0 {
					server.Issues = append(server.Issues, fmt.Sprintf("static secret header values cannot be passed to %s run: %s", target, strings.Join(secretNames, ", ")))
				}
			}
		} else if err := clients.ValidateCommandPath(manifest.Command); err != nil {
			server.Issues = append(server.Issues, err.Message)
		}
		if runnerImplemented && rt.serverPreflight != nil {
			server.Issues = append(server.Issues, rt.serverPreflight(manifest)...)
		}

		for _, key := range server.RuntimeEnv {
			envRequirements[key] = appendUniqueName(envRequirements[key], name)
			if !runtimeEnvPresentForRun(target, key) {
				server.Issues = append(server.Issues, fmt.Sprintf("runtime env %s is missing", key))
			}
		}

		if len(server.Issues) > 0 {
			server.Status = "blocked"
			for _, issue := range server.Issues {
				addPlanError(fmt.Sprintf("server %s: %s", name, issue))
			}
		}
		server.Policy = buildRunPlanServerPolicy(&manifest, target, requiredByServer[name], policySupportByServer[name], policyRuntimeBlocked || server.Status == "blocked")
		if server.Policy.EnforcementClassification == "blocked" && server.Status != "blocked" {
			server.Status = "blocked"
			issue := fmt.Sprintf("capability policy enforcement is blocked: support=%s", server.Policy.SupportState)
			server.Issues = append(server.Issues, issue)
			addPlanError(fmt.Sprintf("server %s: %s", name, issue))
		}
		plan.Servers = append(plan.Servers, server)
	}

	if len(skillRings) > 0 && !supportsRunSkillMaterialization(target) {
		addPlanError(fmt.Sprintf("%s does not support run skill materialization (supported run skill targets: %s)", target, strings.Join(supportedRunSkillTargets(), ", ")))
	}
	skillNames := sortedStringSliceMapKeys(skillRings)
	selectedSkills := make([]registry.SkillPackage, 0, len(skillNames))
	for _, name := range skillNames {
		skill := runPlanSkill{
			Name:   name,
			Status: "ready",
			Rings:  nonNilStrings(append([]string(nil), skillRings[name]...)),
			Issues: []string{},
		}
		pkg, err := a.store.GetSkillPackage(name)
		if err != nil {
			if errors.Is(err, registry.ErrSkillNotFound) {
				skill.Issues = append(skill.Issues, "skill is missing from the registry")
			} else {
				skill.Issues = append(skill.Issues, fmt.Sprintf("skill package cannot be materialized: %v", err))
			}
		} else {
			selectedSkills = append(selectedSkills, pkg)
		}
		if !supportsRunSkillMaterialization(target) {
			skill.Issues = append(skill.Issues, fmt.Sprintf("%s does not support run skill materialization", target))
		}
		if len(skill.Issues) > 0 {
			skill.Status = "blocked"
			for _, issue := range skill.Issues {
				addPlanError(fmt.Sprintf("skill %s: %s", name, issue))
			}
		}
		plan.Skills = append(plan.Skills, skill)
	}

	envKeys := sortedStringSliceMapKeys(envRequirements)
	for _, key := range envKeys {
		plan.Env = append(plan.Env, runPlanEnv{
			Key:     key,
			Present: runtimeEnvPresentForRun(target, key),
			Servers: nonNilStrings(envRequirements[key]),
		})
	}

	selectedServers := make([]registry.Manifest, 0, len(plan.Servers))
	for _, server := range plan.Servers {
		if server.Manifest.Name != "" {
			selectedServers = append(selectedServers, server.Manifest)
		}
	}
	execution, executionIssues := compileRunExecution(selectedRings, selectedServers, options)
	for _, issue := range executionIssues {
		addPlanError(issue)
	}
	if execution.Required {
		if target != codexclient.Target {
			addPlanError(fmt.Sprintf("required execution policy is unsupported for %s run; supported target: %s", target, codexclient.Target))
		} else if execution.HasStdio {
			addPlanError("required read-only sandbox cannot confine local stdio MCP server filesystem or network access; use remote MCP servers or an OS/container boundary")
		}
	}
	plan.Execution = runPlanExecution{
		AmbientEnv: execution.AmbientEnv, Sandbox: execution.Sandbox,
		MaxDuration: execution.MaxDuration.String(), CredentialExposure: execution.CredentialExposure,
		Supported: target == codexclient.Target, Declared: execution.Declared, Required: execution.Required, StdioConfinement: "not-applicable",
	}
	if target != codexclient.Target {
		plan.Execution.StdioConfinement = "unsupported"
	} else if execution.HasStdio {
		plan.Execution.StdioConfinement = "unverified"
	}
	plan.Authority = launch.ExplainAuthority(target, selectedRings, selectedServers, selectedSkills, execution)

	if len(plan.Errors) == 0 && target == codexclient.Target {
		workingDirectory, err := os.Getwd()
		if err != nil {
			addPlanError(fmt.Sprintf("resolve current working directory: %v", err))
		} else {
			auth, authErr := readCodexRunAuthSnapshot()
			if authErr != nil {
				addPlanError(authErr.Error())
			} else {
				clientInput.Auth = auth
				artifact, compileErr := launch.Compile(launch.Input{
					Target: target, WorkingDirectory: workingDirectory, Prompt: prompt,
					Rings: selectedRings, Servers: selectedServers, Skills: selectedSkills,
					Environment: launch.EnvironmentInput{
						Baseline: baselineEnv, Declared: captureDeclaredRunEnvironment(plan.Env),
					},
					Client: clientInput, Execution: execution,
				})
				if compileErr != nil {
					addPlanError(fmt.Sprintf("compile immutable launch artifact: %v", compileErr))
				} else {
					plan.Artifact = artifact
					plan.LaunchDigest = artifact.Digest()
					plan.PolicyDigest = artifact.PolicyDigest()
					plan.ContentHashes = artifact.ReceiptContentHashes()
					plan.Authority = artifact.Authority()
				}
			}
		}
	}
	if target == codexclient.Target && plan.Artifact == nil {
		plan.Authority = blockedRunAuthority(plan.Authority)
	}
	plan.finish()
	return plan, nil
}

func blockedRunAuthority(authority launch.Authority) launch.Authority {
	authority.Requested = append([]launch.AuthorityControl(nil), authority.Requested...)
	authority.Effective = append([]launch.AuthorityControl(nil), authority.Effective...)
	for i := range authority.Effective {
		control := &authority.Effective[i]
		control.EnforcedBy = launch.EnforcedByNone
		control.Verification = launch.VerificationUnverified
		switch control.Classification {
		case launch.ClassificationExact:
			control.Classification = launch.ClassificationBlocked
		case launch.ClassificationAdvisory:
			control.Classification = launch.ClassificationDegraded
		}
	}
	return authority
}

func (p *runLaunchPlan) finish() {
	p.Errors = nonNilStrings(sortedUniqueStrings(p.Errors))
	p.Warnings = nonNilStrings(sortedUniqueStrings(p.Warnings))
	p.Ready = len(p.Errors) == 0
}

func (p runLaunchPlan) toJSON() runPlanJSON {
	return runPlanJSON{
		SchemaVersion:   jsonSchemaVersion,
		Command:         "run",
		Target:          p.Target,
		Rings:           nonNilStrings(p.Rings),
		Ready:           p.Ready,
		RunnerAvailable: p.RunnerAvailable,
		PromptProvided:  p.PromptProvided,
		PolicyRequired:  p.PolicyRequired,
		PolicyControls:  p.PolicyControls,
		LaunchDigest:    p.LaunchDigest,
		PolicyDigest:    p.PolicyDigest,
		ContentHashes:   nonNilContentHashes(p.ContentHashes),
		Authority:       nonNilAuthority(p.Authority),
		Execution:       p.Execution,
		Servers:         nonNilRunPlanServers(p.Servers),
		Skills:          nonNilRunPlanSkills(p.Skills),
		Env:             nonNilRunPlanEnv(p.Env),
		Warnings:        nonNilStrings(p.Warnings),
		Errors:          nonNilStrings(p.Errors),
	}
}

func runPlanAuth(manifest registry.Manifest) string {
	switch {
	case manifest.RequiresBearerTokenEnv():
		return "bearer_token_env_var"
	case strings.TrimSpace(manifest.OAuthResource) != "":
		return "oauth_resource"
	default:
		return ""
	}
}

func strongestPolicySupport(current, candidate string) string {
	rank := map[string]int{"": 0, "not-declared": 1, "supported": 2, "unsupported": 3, "invalid": 4}
	if rank[candidate] > rank[current] {
		return candidate
	}
	return current
}

func policySupportForIssue(issue policy.Issue) string {
	switch issue.Code {
	case policy.IssueUnsupportedCompiler, policy.IssueUnsupportedFeature,
		policy.IssueUnsupportedTransport, policy.IssueUnsupportedServerField:
		return "unsupported"
	default:
		return "invalid"
	}
}

func buildRunPlanServerPolicy(manifest *registry.Manifest, target string, requiredBy []string, support string, blocked bool) runPlanServerPolicy {
	requiredBy = nonNilStrings(sortedUniqueStrings(requiredBy))
	out := runPlanServerPolicy{
		RingEnforcement: "none",
		RequiredBy:      requiredBy,
		SupportState:    support,
	}
	if manifest != nil {
		out.Declared = accessToJSON(manifest.Access)
	}
	if len(requiredBy) > 0 {
		out.RingEnforcement = registry.PolicyEnforcementRequired
		if out.SupportState == "" {
			out.SupportState = "supported"
		}
		if blocked || out.SupportState != "supported" {
			out.EnforcementClassification = "blocked"
		} else {
			out.EnforcementClassification = "exact"
		}
	} else if manifest != nil && manifest.Access != nil {
		capabilities, known := policy.CapabilitiesFor(target, policy.SurfaceRun)
		if !known || !capabilities.Compiler {
			out.SupportState = "unsupported"
		} else {
			out.SupportState = strongestPolicySupport(out.SupportState, "supported")
		}
		if blocked && out.SupportState != "supported" {
			out.EnforcementClassification = "blocked"
		} else {
			out.EnforcementClassification = "advisory"
		}
	} else {
		out.EnforcementClassification = "none"
		out.SupportState = "not-declared"
	}

	if manifest != nil && manifest.Access != nil && target == codexclient.Target {
		capabilities, known := policy.CapabilitiesFor(target, policy.SurfaceRun)
		if known && capabilities.Compiler {
			compiled := codexclient.CompileAccess(manifest.Access)
			effective := &runPlanEffectiveCodexPolicy{
				EnabledTools:         []string{},
				DisabledTools:        []string{},
				RequestedOAuthScopes: []string{},
				ToolApprovalModes:    map[string]string{},
			}
			if compiled.EnabledTools != nil {
				effective.EnabledTools = append([]string(nil), (*compiled.EnabledTools)...)
			}
			if compiled.DisabledTools != nil {
				effective.DisabledTools = append([]string(nil), (*compiled.DisabledTools)...)
			}
			if compiled.Scopes != nil {
				effective.RequestedOAuthScopes = append([]string(nil), (*compiled.Scopes)...)
			}
			if compiled.DefaultApproval != nil {
				effective.DefaultToolsApprovalMode = *compiled.DefaultApproval
			}
			if compiled.ToolApprovals != nil {
				for tool, approval := range *compiled.ToolApprovals {
					if approval != "" {
						effective.ToolApprovalModes[tool] = approval
					}
				}
			}
			out.Effective = effective
		}
	}
	return out
}

func normalizedRingNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func compileRunExecution(rings []registry.Ring, servers []registry.Manifest, options runBuildOptions) (launch.ExecutionConfig, []string) {
	config := launch.ExecutionConfig{
		AmbientEnv: launch.AmbientEnvDeny, Sandbox: launch.SandboxReadOnly,
		CredentialExposure: launch.CredentialExposureRunProcess,
	}
	for _, ring := range rings {
		if ring.Policy == nil || ring.Policy.Execution == nil {
			continue
		}
		config.Declared = true
		if ring.RequiresPolicyEnforcement() {
			config.Required = true
		}
		duration, err := time.ParseDuration(ring.Policy.Execution.MaxDuration)
		if err != nil || duration <= 0 {
			return config, []string{fmt.Sprintf("ring %s has invalid execution max_duration", ring.Name)}
		}
		if config.MaxDuration == 0 || duration < config.MaxDuration {
			config.MaxDuration = duration
		}
	}
	if config.MaxDuration == 0 {
		config.MaxDuration = launch.DefaultMaxDuration
	}
	for _, server := range servers {
		if !server.IsRemote() {
			config.HasStdio = true
			break
		}
	}
	var issues []string
	if options.maxDuration != nil {
		if *options.maxDuration > config.MaxDuration {
			issues = append(issues, fmt.Sprintf("--max-duration %s exceeds the selected ring maximum %s", options.maxDuration.String(), config.MaxDuration))
		} else {
			config.MaxDuration = *options.maxDuration
		}
	}
	return config, issues
}

func countStrings(names []string) map[string]int {
	counts := map[string]int{}
	for _, name := range names {
		counts[name]++
	}
	return counts
}

func sortedUniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		seen[value] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedStringSliceMapKeys(values map[string][]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func runtimeEnvPresent(key string) bool {
	value, ok := os.LookupEnv(key)
	return ok && strings.TrimSpace(value) != ""
}

func runtimeEnvPresentForRun(target, key string) bool {
	if target == codexclient.Target && codexGeneratedEnvKey(key) {
		return true
	}
	return runtimeEnvPresent(key)
}

func nonNilRunPlanServers(servers []runPlanServer) []runPlanServer {
	if servers == nil {
		return []runPlanServer{}
	}
	return servers
}

func nonNilRunPlanSkills(skills []runPlanSkill) []runPlanSkill {
	if skills == nil {
		return []runPlanSkill{}
	}
	return skills
}

func nonNilRunPlanEnv(env []runPlanEnv) []runPlanEnv {
	if env == nil {
		return []runPlanEnv{}
	}
	return env
}

func nonNilContentHashes(value launch.ContentHashes) launch.ContentHashes {
	if value.Rings == nil {
		value.Rings = []launch.NamedHash{}
	}
	if value.Servers == nil {
		value.Servers = []launch.NamedHash{}
	}
	if value.Skills == nil {
		value.Skills = []launch.NamedHash{}
	}
	return value
}

func nonNilAuthority(value launch.Authority) launch.Authority {
	if value.Requested == nil {
		value.Requested = []launch.AuthorityControl{}
	}
	if value.Effective == nil {
		value.Effective = []launch.AuthorityControl{}
	}
	return value
}

func printRunPlan(out io.Writer, plan runLaunchPlan) {
	status := "ready"
	if !plan.Ready {
		status = "blocked"
	}
	fmt.Fprintf(out, "run target: %s\n", plan.Target)
	fmt.Fprintf(out, "rings: %s\n", formatNameList(plan.Rings))
	fmt.Fprintf(out, "status: %s\n", status)
	if plan.RunnerAvailable {
		fmt.Fprintln(out, "runner: available")
	} else {
		fmt.Fprintln(out, "runner: unavailable")
	}
	fmt.Fprintf(out, "policy controls: tool-filtering=%s oauth-scopes=%s approvals=%s instructions=%s\n",
		plan.PolicyControls.ToolFiltering,
		plan.PolicyControls.OAuthScopes,
		plan.PolicyControls.Approvals,
		plan.PolicyControls.Instructions,
	)
	if plan.LaunchDigest != "" {
		fmt.Fprintf(out, "launch digest: %s\n", plan.LaunchDigest)
		fmt.Fprintf(out, "policy digest: %s\n", plan.PolicyDigest)
		printRunAuthority(out, plan.Authority)
	}
	fmt.Fprintf(out, "execution policy: ambient_env=%s sandbox=%s max_duration=%s credential_exposure=%s supported=%t declared=%t required=%t stdio_confinement=%s\n",
		plan.Execution.AmbientEnv, plan.Execution.Sandbox, plan.Execution.MaxDuration,
		plan.Execution.CredentialExposure, plan.Execution.Supported, plan.Execution.Declared, plan.Execution.Required, plan.Execution.StdioConfinement)
	fmt.Fprintln(out)

	fmt.Fprintln(out, "servers:")
	if len(plan.Servers) == 0 {
		fmt.Fprintln(out, "  -")
	} else {
		for _, server := range plan.Servers {
			detail := fmt.Sprintf("  %s %s %s", server.Name, server.Status, server.Transport)
			if server.Auth != "" {
				detail += " auth=" + server.Auth
			}
			if len(server.RuntimeEnv) > 0 {
				detail += " env=" + formatNameList(server.RuntimeEnv)
			}
			if len(server.Rings) > 0 {
				detail += " rings=" + formatNameList(server.Rings)
			}
			fmt.Fprintln(out, detail)
			for _, issue := range server.Issues {
				fmt.Fprintf(out, "    issue: %s\n", issue)
			}
			printRunPlanServerPolicy(out, server.Policy)
		}
	}

	fmt.Fprintln(out, "skills:")
	if len(plan.Skills) == 0 {
		fmt.Fprintln(out, "  -")
	} else {
		for _, skill := range plan.Skills {
			detail := fmt.Sprintf("  %s %s", skill.Name, skill.Status)
			if len(skill.Rings) > 0 {
				detail += " rings=" + formatNameList(skill.Rings)
			}
			fmt.Fprintln(out, detail)
			for _, issue := range skill.Issues {
				fmt.Fprintf(out, "    issue: %s\n", issue)
			}
		}
	}

	fmt.Fprintln(out, "runtime env:")
	if len(plan.Env) == 0 {
		fmt.Fprintln(out, "  -")
	} else {
		for _, env := range plan.Env {
			status := "missing"
			if env.Present {
				status = "present"
			}
			fmt.Fprintf(out, "  %s %s", env.Key, status)
			if len(env.Servers) > 0 {
				fmt.Fprintf(out, " servers=%s", formatNameList(env.Servers))
			}
			fmt.Fprintln(out)
		}
	}

	fmt.Fprintln(out, "prompt: provided")
	if plan.RunnerAvailable {
		fmt.Fprintln(out, "execution: available when --dry-run is omitted")
	} else {
		fmt.Fprintln(out, "execution: not implemented for this target; dry-run only")
	}
	if len(plan.Warnings) > 0 {
		fmt.Fprintln(out, "warnings:")
		for _, warning := range plan.Warnings {
			fmt.Fprintf(out, "  %s\n", warning)
		}
	}
	if len(plan.Errors) > 0 {
		fmt.Fprintln(out, "errors:")
		for _, issue := range plan.Errors {
			fmt.Fprintf(out, "  %s\n", issue)
		}
	}
}

func printRunAuthority(out io.Writer, authority launch.Authority) {
	for _, group := range []struct {
		name     string
		controls []launch.AuthorityControl
	}{{"requested authority", authority.Requested}, {"effective authority", authority.Effective}} {
		if len(group.controls) == 0 {
			continue
		}
		fmt.Fprintf(out, "%s:\n", group.name)
		for _, control := range group.controls {
			fmt.Fprintf(out, "  %s enforced_by=%s verification=%s classification=%s\n", control.Control, control.EnforcedBy, control.Verification, control.Classification)
		}
	}
}

func printRunPlanServerPolicy(out io.Writer, policyPlan runPlanServerPolicy) {
	fmt.Fprintf(out, "    policy: support=%s enforcement=%s ring_enforcement=%s",
		policyPlan.SupportState,
		policyPlan.EnforcementClassification,
		policyPlan.RingEnforcement,
	)
	if len(policyPlan.RequiredBy) > 0 {
		fmt.Fprintf(out, " required_by=%s", formatNameList(policyPlan.RequiredBy))
	}
	fmt.Fprintln(out)
	if policyPlan.Declared == nil {
		fmt.Fprintln(out, "    declared policy: none")
	} else {
		fmt.Fprintf(out, "    declared policy: allowed_tools=%s denied_tools=%s oauth_scopes=%s default_approval=%s tool_approvals=%s\n",
			formatOptionalStringList(policyPlan.Declared.AllowedTools),
			formatOptionalStringList(policyPlan.Declared.DeniedTools),
			formatOptionalStringList(policyPlan.Declared.OAuthScopes),
			formatOptionalString(policyPlan.Declared.DefaultApproval),
			formatOptionalStringMap(policyPlan.Declared.ToolApprovals),
		)
	}
	if policyPlan.Effective == nil {
		fmt.Fprintln(out, "    effective policy: none")
		return
	}
	fmt.Fprintf(out, "    effective policy: enabled_tools=%s disabled_tools=%s requested_oauth_scopes=%s default_tools_approval_mode=%s tool_approval_modes=%s\n",
		formatEffectiveStringList(policyPlan.Effective.EnabledTools),
		formatEffectiveStringList(policyPlan.Effective.DisabledTools),
		formatEffectiveStringList(policyPlan.Effective.RequestedOAuthScopes),
		formatOptionalEffectiveString(policyPlan.Effective.DefaultToolsApprovalMode),
		formatStringMap(policyPlan.Effective.ToolApprovalModes),
	)
}

func formatEffectiveStringList(values []string) string {
	if len(values) == 0 {
		return "target-default"
	}
	return strings.Join(values, ",")
}

func formatOptionalStringList(values *[]string) string {
	if values == nil {
		return "absent"
	}
	if len(*values) == 0 {
		return "[]"
	}
	return "[" + strings.Join(*values, ",") + "]"
}

func formatOptionalString(value *string) string {
	if value == nil {
		return "absent"
	}
	return *value
}

func formatOptionalEffectiveString(value string) string {
	if value == "" {
		return "target-default"
	}
	return value
}

func formatOptionalStringMap(values *map[string]string) string {
	if values == nil {
		return "absent"
	}
	return formatStringMap(*values)
}

func formatStringMap(values map[string]string) string {
	if len(values) == 0 {
		return "{}"
	}
	keys := sortedMapKeys(values)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+values[key])
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func printRunHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari run <client> --ring <ring> [--ring <ring> ...] [--max-duration <duration>] [--receipt <path>] [--dry-run] -- <prompt>")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --ring <ring>             Ring to include in the launch plan (repeatable)")
	fmt.Fprintln(out, "  --dry-run                 Inspect the launch plan without starting the client")
	fmt.Fprintln(out, "  --json                    Emit JSON instead of text (requires --dry-run)")
	fmt.Fprintln(out, "  --max-duration <duration> Shorten (never extend) the selected ring maximum")
	fmt.Fprintln(out, "  --receipt <path>          Write a versioned execution receipt (Codex execution only)")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Plan or start an ephemeral client launch from one or more rings. Codex")
	fmt.Fprintln(out, "  execution starts `codex exec --ephemeral --ignore-user-config")
	fmt.Fprintln(out, "  --skip-git-repo-check --sandbox read-only`, clears inherited MCP")
	fmt.Fprintln(out, "  config, and injects selected ring MCP servers as required config")
	fmt.Fprintln(out, "  overrides from an isolated working root and materializes selected")
	fmt.Fprintln(out, "  ring skills into that temporary root. Codex receives only a documented")
	fmt.Fprintln(out, "  platform baseline, isolated HOME/CODEX_HOME/temp paths, frozen host auth,")
	fmt.Fprintln(out, "  and explicitly declared server variables. Caller home paths are not")
	fmt.Fprintln(out, "  forwarded into stdio server configuration. Other clients are dry-run only.")
	fmt.Fprintln(out, "  Planning freezes normalized rings, servers, skills, the prompt, and Codex")
	fmt.Fprintln(out, "  overrides into one immutable launch artifact. Execution never rereads the")
	fmt.Fprintln(out, "  registry. Dry-run reports content digests and requested/effective authority")
	fmt.Fprintln(out, "  with enforced_by, verification, and enforcement classifications.")
	fmt.Fprintln(out, "  Every Codex run uses --strict-config, an inherit-none shell environment")
	fmt.Fprintln(out, "  policy, a validated stable CLI 0.139.x release, and a bounded process tree.")
	fmt.Fprintln(out, "  The default maximum is 15m; selected rings may lower it and the CLI may")
	fmt.Fprintln(out, "  shorten, never extend, that maximum. Required execution policy blocks local")
	fmt.Fprintln(out, "  stdio servers because their filesystem and network confinement is unverified.")
	fmt.Fprintln(out, "  Dry-run reports declared/effective policy separately from client enforcement and")
	fmt.Fprintln(out, "  advisory instructions.")
	fmt.Fprintln(out, "  Run never writes client config, managed state, or permanent skill files.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Examples:")
	fmt.Fprintln(out, "  madari run codex --ring cloudsql-readonly -- \"Who are the top 5 ebook creators?\"")
	fmt.Fprintln(out, "  madari run codex --ring cloudsql-readonly --ring research --dry-run --json -- \"Summarize the target plan\"")
}
