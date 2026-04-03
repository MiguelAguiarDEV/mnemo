package atlas

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkillFile creates a skill .md file in the given directory.
func writeSkillFile(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create dir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", filename, err)
	}
}

// writeSkillDir creates a skill directory with SKILL.md inside.
func writeSkillDir(t *testing.T, parentDir, skillName, content string) {
	t.Helper()
	dir := filepath.Join(parentDir, skillName)
	writeSkillFile(t, dir, "SKILL.md", content)
}

func TestRegistry_Build_MultiTier(t *testing.T) {
	globalDir := t.TempDir()
	opsDir := t.TempDir()
	projectDir := t.TempDir()

	writeSkillFile(t, globalDir, "skill-a.md", `---
name: skill-a
description: Global skill A
---
Body A`)

	writeSkillFile(t, opsDir, "skill-b.md", `---
name: skill-b
description: Ops skill B
---
Body B`)

	writeSkillFile(t, projectDir, "skill-c.md", `---
name: skill-c
description: Project skill C
---
Body C`)

	r := NewRegistry()
	err := r.Build([]CatalogPath{
		{Path: globalDir, Tier: "global"},
		{Path: opsDir, Tier: "ops"},
		{Path: projectDir, Tier: "project"},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if !r.Has("skill-a") {
		t.Error("expected skill-a to exist")
	}
	if !r.Has("skill-b") {
		t.Error("expected skill-b to exist")
	}
	if !r.Has("skill-c") {
		t.Error("expected skill-c to exist")
	}

	entry, ok := r.Get("skill-a")
	if !ok {
		t.Fatal("skill-a not found")
	}
	if entry.Tier != "global" {
		t.Errorf("skill-a tier = %q, want %q", entry.Tier, "global")
	}
}

func TestRegistry_Build_NameCollision_ProjectWins(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()

	writeSkillFile(t, globalDir, "shared.md", `---
name: shared
description: Global version
---
Global body`)

	writeSkillFile(t, projectDir, "shared.md", `---
name: shared
description: Project version
---
Project body`)

	r := NewRegistry()
	err := r.Build([]CatalogPath{
		{Path: globalDir, Tier: "global"},
		{Path: projectDir, Tier: "project"},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	entry, ok := r.Get("shared")
	if !ok {
		t.Fatal("shared skill not found")
	}
	if entry.Description != "Project version" {
		t.Errorf("expected project version to win, got description %q", entry.Description)
	}
	if entry.Tier != "project" {
		t.Errorf("tier = %q, want %q", entry.Tier, "project")
	}
}

func TestRegistry_Build_EmptyCatalog(t *testing.T) {
	emptyDir := t.TempDir()

	r := NewRegistry()
	err := r.Build([]CatalogPath{
		{Path: emptyDir, Tier: "global"},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if len(r.entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(r.entries))
	}
}

func TestRegistry_Build_NonexistentPath(t *testing.T) {
	r := NewRegistry()
	err := r.Build([]CatalogPath{
		{Path: "/nonexistent/path/12345", Tier: "global"},
	})
	if err != nil {
		t.Fatalf("Build should not fail for nonexistent path: %v", err)
	}
	if len(r.entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(r.entries))
	}
}

func TestRegistry_AlwaysSkills(t *testing.T) {
	dir := t.TempDir()

	writeSkillFile(t, dir, "guardrails.md", `---
name: guardrails
description: Safety rules
always: true
---
Body`)

	writeSkillFile(t, dir, "testing.md", `---
name: testing
description: Testing patterns
---
Body`)

	writeSkillFile(t, dir, "knowledge.md", `---
name: knowledge
description: Server knowledge
always: true
---
Body`)

	r := NewRegistry()
	if err := r.Build([]CatalogPath{{Path: dir, Tier: "ops"}}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	always := r.AlwaysSkills()
	if len(always) != 2 {
		t.Fatalf("expected 2 always-skills, got %d", len(always))
	}

	names := make(map[string]bool)
	for _, s := range always {
		names[s.Name] = true
	}
	if !names["guardrails"] {
		t.Error("expected guardrails in always-skills")
	}
	if !names["knowledge"] {
		t.Error("expected knowledge in always-skills")
	}
}

func TestRegistry_CompactIndex(t *testing.T) {
	dir := t.TempDir()

	writeSkillFile(t, dir, "alpha.md", `---
name: alpha
description: Alpha skill
---
Body`)

	writeSkillFile(t, dir, "beta.md", `---
name: beta
description: Beta skill
always: true
---
Body`)

	r := NewRegistry()
	if err := r.Build([]CatalogPath{{Path: dir, Tier: "global"}}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	index := r.CompactIndex()
	if !strings.Contains(index, "## Available Skills") {
		t.Error("index missing header")
	}
	if !strings.Contains(index, "| alpha |") {
		t.Error("index missing alpha")
	}
	if !strings.Contains(index, "[always loaded]") {
		t.Error("index missing [always loaded] marker for beta")
	}
	if !strings.Contains(index, "| Name | Description |") {
		t.Error("index missing table header")
	}
}

func TestRegistry_CompactIndex_Empty(t *testing.T) {
	r := NewRegistry()
	index := r.CompactIndex()
	if index != "" {
		t.Errorf("expected empty index, got %q", index)
	}
}

func TestRegistry_Build_SkillDir(t *testing.T) {
	dir := t.TempDir()

	writeSkillDir(t, dir, "my-skill", `---
name: my-skill
description: A skill in a directory
---
Body`)

	r := NewRegistry()
	if err := r.Build([]CatalogPath{{Path: dir, Tier: "project"}}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if !r.Has("my-skill") {
		t.Error("expected my-skill from directory")
	}
}

func TestRegistry_Build_InvalidFrontmatter_Skipped(t *testing.T) {
	dir := t.TempDir()

	writeSkillFile(t, dir, "good.md", `---
name: good
description: Good skill
---
Body`)

	writeSkillFile(t, dir, "bad.md", `---
name: [invalid
---
Body`)

	r := NewRegistry()
	if err := r.Build([]CatalogPath{{Path: dir, Tier: "global"}}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if !r.Has("good") {
		t.Error("expected good skill to be registered")
	}
	if r.Has("[invalid") {
		t.Error("bad skill should not be registered")
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected not found")
	}
}

func TestRegistry_Has_NotFound(t *testing.T) {
	r := NewRegistry()
	if r.Has("nonexistent") {
		t.Error("expected false for nonexistent")
	}
}

func TestRegistry_Build_NoNameSkipped(t *testing.T) {
	dir := t.TempDir()

	// A file with no name in frontmatter
	writeSkillFile(t, dir, "noname.md", `---
description: No name here
---
Body`)

	r := NewRegistry()
	if err := r.Build([]CatalogPath{{Path: dir, Tier: "global"}}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if len(r.entries) != 0 {
		t.Errorf("expected 0 entries (nameless skill skipped), got %d", len(r.entries))
	}
}

func TestRegistry_Build_LowerTierDoesNotOverride(t *testing.T) {
	projectDir := t.TempDir()
	globalDir := t.TempDir()

	writeSkillFile(t, projectDir, "skill.md", `---
name: skill
description: Project version
---
Body`)

	writeSkillFile(t, globalDir, "skill.md", `---
name: skill
description: Global version
---
Body`)

	r := NewRegistry()
	// Build project first, then global — global should NOT override
	err := r.Build([]CatalogPath{
		{Path: projectDir, Tier: "project"},
		{Path: globalDir, Tier: "global"},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	entry, _ := r.Get("skill")
	if entry.Description != "Project version" {
		t.Errorf("expected project version to remain, got %q", entry.Description)
	}
}
