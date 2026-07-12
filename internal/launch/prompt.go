package launch

import (
	"fmt"
	"strings"

	"github.com/ankitvg/madari/internal/registry"
)

func compilePrompt(rings []registry.Ring, skills []registry.SkillPackage, prompt, workingDirectory string) string {
	var b strings.Builder
	fmt.Fprintln(&b, "You are running through Madari with MCP capability rings selected for this session.")
	fmt.Fprintln(&b, "Use only external MCP capabilities made available by the selected Madari rings.")
	fmt.Fprintf(&b, "Original working directory: %s\n", workingDirectory)
	fmt.Fprintln(&b, "Codex is launched from an isolated temporary working directory so project-scoped Codex config cannot add capabilities outside these rings.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Selected rings:")
	for _, ring := range rings {
		fmt.Fprintf(&b, "- %s", ring.Name)
		if strings.TrimSpace(ring.Description) != "" {
			fmt.Fprintf(&b, ": %s", strings.TrimSpace(ring.Description))
		}
		fmt.Fprintln(&b)
		appendRingContract(&b, ring.Contract)
	}
	if len(skills) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Selected skills:")
		for _, pkg := range skills {
			fmt.Fprintf(&b, "- %s", pkg.Skill.Name)
			if strings.TrimSpace(pkg.Skill.Description) != "" {
				fmt.Fprintf(&b, ": %s", strings.TrimSpace(pkg.Skill.Description))
			}
			fmt.Fprintln(&b)
		}
		fmt.Fprintln(&b, "Selected ring skills are materialized as project skills for this session.")
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "User prompt:")
	fmt.Fprintln(&b, prompt)
	return b.String()
}

func appendRingContract(b *strings.Builder, contract *registry.RingContract) {
	if contract.Empty() {
		return
	}
	if strings.TrimSpace(contract.Summary) != "" {
		fmt.Fprintf(b, "  summary: %s\n", strings.TrimSpace(contract.Summary))
	}
	appendPromptList(b, "good_for", contract.GoodFor)
	appendPromptList(b, "not_for", contract.NotFor)
	appendPromptList(b, "required_context", contract.RequiredContext)
	appendPromptList(b, "optional_context", contract.OptionalContext)
	appendPromptList(b, "expected_outputs", contract.ExpectedOutputs)
}

func appendPromptList(b *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "  %s:\n", label)
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			fmt.Fprintf(b, "  - %s\n", value)
		}
	}
}
