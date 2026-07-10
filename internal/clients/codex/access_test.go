package codex

import (
	"reflect"
	"testing"

	"github.com/ankitvg/madari/internal/registry"
)

func TestCompileAccessMapsPortableVocabularyAndSortsSets(t *testing.T) {
	allowed := []string{"tools.write", "tools.read"}
	denied := []string{"tools.remove", "tools.delete"}
	scopes := []string{"repo.write", "repo.read"}
	defaultApproval := registry.ApprovalBehaviorAlwaysPrompt
	toolApprovals := map[string]registry.ApprovalBehavior{
		"tools.write": registry.ApprovalBehaviorAlwaysAllow,
		"tools.read":  registry.ApprovalBehaviorAutomatic,
	}
	access := &registry.AccessProfile{
		AllowedTools:    &allowed,
		DeniedTools:     &denied,
		OAuthScopes:     &scopes,
		DefaultApproval: &defaultApproval,
		ToolApprovals:   &toolApprovals,
	}

	compiled := CompileAccess(access)
	if got, want := *compiled.EnabledTools, []string{"tools.read", "tools.write"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("enabled tools mismatch: got %#v want %#v", got, want)
	}
	if got, want := *compiled.DisabledTools, []string{"tools.delete", "tools.remove"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("disabled tools mismatch: got %#v want %#v", got, want)
	}
	if got, want := *compiled.Scopes, []string{"repo.read", "repo.write"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scopes mismatch: got %#v want %#v", got, want)
	}
	if compiled.DefaultApproval == nil || *compiled.DefaultApproval != "prompt" {
		t.Fatalf("default approval mismatch: %#v", compiled.DefaultApproval)
	}
	wantApprovals := map[string]string{"tools.read": "auto", "tools.write": "approve"}
	if compiled.ToolApprovals == nil || !reflect.DeepEqual(*compiled.ToolApprovals, wantApprovals) {
		t.Fatalf("tool approvals mismatch: got %#v want %#v", compiled.ToolApprovals, wantApprovals)
	}

	// Compilation must not reorder or alias registry input.
	if got, want := allowed, []string{"tools.write", "tools.read"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("compiler mutated allowed tools: got %#v want %#v", got, want)
	}
	(*compiled.EnabledTools)[0] = "changed"
	if allowed[0] == "changed" || allowed[1] == "changed" {
		t.Fatalf("compiled tools alias registry input: %#v", allowed)
	}
}

func TestCompileAccessPreservesAbsenceAndExplicitClear(t *testing.T) {
	if got := CompileAccess(nil); got.EnabledTools != nil || got.DisabledTools != nil || got.Scopes != nil || got.DefaultApproval != nil || got.ToolApprovals != nil {
		t.Fatalf("nil profile must compile to undeclared fields: %#v", got)
	}

	emptyTools := []string{}
	emptyApprovals := map[string]registry.ApprovalBehavior{}
	inherit := registry.ApprovalBehaviorInherit
	compiled := CompileAccess(&registry.AccessProfile{
		AllowedTools:    &emptyTools,
		DefaultApproval: &inherit,
		ToolApprovals:   &emptyApprovals,
	})
	if compiled.EnabledTools == nil || len(*compiled.EnabledTools) != 0 {
		t.Fatalf("explicit empty tools must remain a present clear: %#v", compiled.EnabledTools)
	}
	if compiled.DisabledTools != nil || compiled.Scopes != nil {
		t.Fatalf("undeclared fields must remain absent: %#v", compiled)
	}
	if compiled.DefaultApproval == nil || *compiled.DefaultApproval != "" {
		t.Fatalf("inherit must compile to a present native omission: %#v", compiled.DefaultApproval)
	}
	if compiled.ToolApprovals == nil || len(*compiled.ToolApprovals) != 0 {
		t.Fatalf("explicit empty approvals must remain a present clear: %#v", compiled.ToolApprovals)
	}
}

func TestCompileAccessMapsPerToolInheritToOmission(t *testing.T) {
	approvals := map[string]registry.ApprovalBehavior{
		"tools.inherit": registry.ApprovalBehaviorInherit,
		"tools.prompt":  registry.ApprovalBehaviorAlwaysPrompt,
	}
	compiled := CompileAccess(&registry.AccessProfile{ToolApprovals: &approvals})
	if compiled.ToolApprovals == nil {
		t.Fatal("expected declared tool approval table")
	}
	if got := (*compiled.ToolApprovals)["tools.inherit"]; got != "" {
		t.Fatalf("inherit should compile to native omission, got %q", got)
	}
	if got := (*compiled.ToolApprovals)["tools.prompt"]; got != "prompt" {
		t.Fatalf("prompt mapping mismatch: %q", got)
	}
}
