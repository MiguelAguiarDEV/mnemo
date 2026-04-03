package atlas

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIntegration_EndToEndSkillLoading verifies the full pipeline:
// temp dir with catalogs + skill files -> registry build -> compact index -> loader
func TestIntegration_EndToEndSkillLoading(t *testing.T) {
	// Set up temp dir structure simulating multi-tier catalogs
	globalDir := t.TempDir()
	opsDir := t.TempDir()
	projectDir := t.TempDir()

	// Global skills
	writeIntegrationSkill(t, globalDir, "go-testing", "Go testing patterns", false, []string{"go", "test"})
	writeIntegrationSkill(t, globalDir, "pr-review", "Pull request review guide", false, nil)

	// Ops skills (one is always:true)
	writeIntegrationSkill(t, opsDir, "server-guardrails", "Safety rules for server ops", true, nil)
	writeIntegrationSkill(t, opsDir, "task-management", "Task CRUD operations", false, []string{"task"})

	// Project skills (one is always:true, one overrides global)
	writeIntegrationSkill(t, projectDir, "server-knowledge", "Current server state", true, nil)
	writeIntegrationSkill(t, projectDir, "go-testing", "Project-specific Go testing", false, nil) // overrides global

	// Build registry from all three tiers
	registry := NewRegistry()
	err := registry.Build([]CatalogPath{
		{Path: globalDir, Tier: "global", Project: "kb"},
		{Path: opsDir, Tier: "ops", Project: "jarvis"},
		{Path: projectDir, Tier: "project", Project: "jarvis"},
	})
	if err != nil {
		t.Fatalf("Build() failed: %v", err)
	}

	// Verify total skills: 2 global + 2 ops + 2 project - 1 override = 5 unique
	allSkills := countSkills(registry)
	if allSkills != 5 {
		t.Errorf("expected 5 unique skills, got %d", allSkills)
	}

	// Verify multi-tier override: go-testing should be from project tier
	entry, ok := registry.Get("go-testing")
	if !ok {
		t.Fatal("go-testing not found in registry")
	}
	if entry.Tier != "project" {
		t.Errorf("go-testing tier = %q, want %q (project should override global)", entry.Tier, "project")
	}

	// Verify always-skills are identified correctly
	alwaysSkills := registry.AlwaysSkills()
	alwaysNames := make(map[string]bool)
	for _, s := range alwaysSkills {
		alwaysNames[s.Name] = true
	}
	if !alwaysNames["server-guardrails"] {
		t.Error("server-guardrails should be an always-skill")
	}
	if !alwaysNames["server-knowledge"] {
		t.Error("server-knowledge should be an always-skill")
	}
	if len(alwaysSkills) != 2 {
		t.Errorf("expected 2 always-skills, got %d", len(alwaysSkills))
	}

	// Verify compact index contains all skills
	index := registry.CompactIndex()
	if !strings.Contains(index, "go-testing") {
		t.Error("compact index should contain go-testing")
	}
	if !strings.Contains(index, "server-guardrails") {
		t.Error("compact index should contain server-guardrails")
	}
	if !strings.Contains(index, "server-knowledge") {
		t.Error("compact index should contain server-knowledge")
	}
	if !strings.Contains(index, "pr-review") {
		t.Error("compact index should contain pr-review")
	}
	if !strings.Contains(index, "task-management") {
		t.Error("compact index should contain task-management")
	}
	if !strings.Contains(index, "[always loaded]") {
		t.Error("compact index should mark always-loaded skills")
	}

	// Verify loader can load each skill
	loader := NewLoader(registry)

	for _, name := range []string{"go-testing", "pr-review", "server-guardrails", "task-management", "server-knowledge"} {
		content, err := loader.Load(name)
		if err != nil {
			t.Errorf("Load(%q) failed: %v", name, err)
			continue
		}
		if content == "" {
			t.Errorf("Load(%q) returned empty content", name)
		}
		// Content should NOT contain frontmatter
		if strings.Contains(content, "---") && strings.Contains(content, "name:") {
			t.Errorf("Load(%q) returned frontmatter in content", name)
		}
	}

	// Verify go-testing content is from project (overridden)
	content, _ := loader.Load("go-testing")
	if !strings.Contains(content, "Project-specific Go testing") {
		t.Error("go-testing content should be from project tier (override)")
	}

	// Verify idempotent load (cache hit)
	content2, err := loader.Load("go-testing")
	if err != nil {
		t.Fatalf("second Load() failed: %v", err)
	}
	if content != content2 {
		t.Error("cached load should return same content")
	}
}

// TestIntegration_EmptyCatalog verifies that an empty catalog dir doesn't break the registry.
func TestIntegration_EmptyCatalog(t *testing.T) {
	emptyDir := t.TempDir()
	populatedDir := t.TempDir()
	writeIntegrationSkill(t, populatedDir, "my-skill", "A real skill", false, nil)

	registry := NewRegistry()
	err := registry.Build([]CatalogPath{
		{Path: emptyDir, Tier: "global"},
		{Path: populatedDir, Tier: "ops"},
	})
	if err != nil {
		t.Fatalf("Build() with empty catalog failed: %v", err)
	}

	if countSkills(registry) != 1 {
		t.Errorf("expected 1 skill, got %d", countSkills(registry))
	}
}

// TestIntegration_NonexistentCatalogPath verifies missing paths are skipped.
func TestIntegration_NonexistentCatalogPath(t *testing.T) {
	realDir := t.TempDir()
	writeIntegrationSkill(t, realDir, "real-skill", "Exists on disk", false, nil)

	registry := NewRegistry()
	err := registry.Build([]CatalogPath{
		{Path: "/nonexistent/catalog/path", Tier: "global"},
		{Path: realDir, Tier: "ops"},
	})
	if err != nil {
		t.Fatalf("Build() should not fail with nonexistent path: %v", err)
	}

	if countSkills(registry) != 1 {
		t.Errorf("expected 1 skill (from real dir), got %d", countSkills(registry))
	}
}

// TestIntegration_SkillInSubdirectory verifies skills inside subdirs (SKILL.md pattern).
func TestIntegration_SkillInSubdirectory(t *testing.T) {
	catalogDir := t.TempDir()

	// Create skill in subdirectory
	skillDir := filepath.Join(catalogDir, "my-cool-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: my-cool-skill\ndescription: A cool skill in a subdirectory\n---\n# My Cool Skill\n\nContent here."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	err := registry.Build([]CatalogPath{
		{Path: catalogDir, Tier: "project"},
	})
	if err != nil {
		t.Fatalf("Build() failed: %v", err)
	}

	entry, ok := registry.Get("my-cool-skill")
	if !ok {
		t.Fatal("my-cool-skill not found")
	}
	if entry.Tier != "project" {
		t.Errorf("tier = %q, want %q", entry.Tier, "project")
	}

	loader := NewLoader(registry)
	loaded, err := loader.Load("my-cool-skill")
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if !strings.Contains(loaded, "Content here") {
		t.Error("loaded content should contain body text")
	}
}

// --- helpers ---

func writeIntegrationSkill(t *testing.T, dir, name, desc string, always bool, triggers []string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + name + "\n")
	b.WriteString("description: " + desc + "\n")
	if always {
		b.WriteString("always: true\n")
	}
	if len(triggers) > 0 {
		b.WriteString("triggers:\n")
		for _, tr := range triggers {
			b.WriteString("  - " + tr + "\n")
		}
	}
	b.WriteString("---\n")
	b.WriteString("# " + name + "\n\n")
	b.WriteString(desc + " content body.\n")

	filename := filepath.Join(dir, name+".md")
	if err := os.WriteFile(filename, []byte(b.String()), 0644); err != nil {
		t.Fatalf("failed to write skill file %s: %v", filename, err)
	}
}

func countSkills(r *Registry) int {
	count := 0
	for range r.entries {
		count++
	}
	return count
}
