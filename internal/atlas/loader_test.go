package atlas

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLoader_Load_Success(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "my-skill.md", `---
name: my-skill
description: A test skill
---
# My Skill

This is the body.`)

	r := NewRegistry()
	if err := r.Build([]CatalogPath{{Path: dir, Tier: "project"}}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	loader := NewLoader(r)
	content, err := loader.Load("my-skill")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !strings.Contains(content, "# My Skill") {
		t.Errorf("content = %q, expected to contain %q", content, "# My Skill")
	}
	if strings.Contains(content, "name: my-skill") {
		t.Error("content should not contain frontmatter YAML")
	}
}

func TestLoader_Load_NotFound(t *testing.T) {
	r := NewRegistry()
	loader := NewLoader(r)

	_, err := loader.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
	if !strings.Contains(err.Error(), "skill not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "skill not found")
	}
}

func TestLoader_Load_UnreadableFile(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "broken.md")
	writeSkillFile(t, dir, "broken.md", `---
name: broken
description: A broken skill
---
Body`)

	r := NewRegistry()
	if err := r.Build([]CatalogPath{{Path: dir, Tier: "project"}}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Make the file unreadable
	if err := os.Chmod(skillPath, 0o000); err != nil {
		t.Skipf("cannot change file permissions: %v", err)
	}
	defer os.Chmod(skillPath, 0o644) // restore for cleanup

	loader := NewLoader(r)
	_, err := loader.Load("broken")
	if err == nil {
		t.Fatal("expected error for unreadable file")
	}
	if !strings.Contains(err.Error(), "failed to read skill") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "failed to read skill")
	}
}

func TestLoader_Load_CachesContent(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "cached.md", `---
name: cached
description: Cached skill
---
Original body`)

	r := NewRegistry()
	if err := r.Build([]CatalogPath{{Path: dir, Tier: "project"}}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	loader := NewLoader(r)

	// First load
	content1, err := loader.Load("cached")
	if err != nil {
		t.Fatalf("First load failed: %v", err)
	}

	// Modify the file on disk
	if err := os.WriteFile(filepath.Join(dir, "cached.md"), []byte(`---
name: cached
description: Cached skill
---
Modified body`), 0o644); err != nil {
		t.Fatalf("failed to modify file: %v", err)
	}

	// Second load should return cached content (not re-read file)
	content2, err := loader.Load("cached")
	if err != nil {
		t.Fatalf("Second load failed: %v", err)
	}

	if content1 != content2 {
		t.Errorf("cached load returned different content:\nfirst:  %q\nsecond: %q", content1, content2)
	}
	if strings.Contains(content2, "Modified") {
		t.Error("second load should return cached content, not re-read from disk")
	}
}

func TestLoader_LoadMultiple_Success(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "skill-a.md", `---
name: skill-a
description: Skill A
---
Body A`)
	writeSkillFile(t, dir, "skill-b.md", `---
name: skill-b
description: Skill B
---
Body B`)

	r := NewRegistry()
	if err := r.Build([]CatalogPath{{Path: dir, Tier: "project"}}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	loader := NewLoader(r)
	result, err := loader.LoadMultiple([]string{"skill-a", "skill-b"})
	if err != nil {
		t.Fatalf("LoadMultiple failed: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if !strings.Contains(result["skill-a"], "Body A") {
		t.Error("skill-a content missing")
	}
	if !strings.Contains(result["skill-b"], "Body B") {
		t.Error("skill-b content missing")
	}
}

func TestLoader_LoadMultiple_OneNotFound(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "exists.md", `---
name: exists
description: Exists
---
Body`)

	r := NewRegistry()
	if err := r.Build([]CatalogPath{{Path: dir, Tier: "project"}}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	loader := NewLoader(r)
	_, err := loader.LoadMultiple([]string{"exists", "missing"})
	if err == nil {
		t.Fatal("expected error when one skill is missing")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should mention missing skill: %v", err)
	}
}

func TestLoader_Load_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "concurrent.md", `---
name: concurrent
description: Concurrent access test
---
Concurrent body`)

	r := NewRegistry()
	if err := r.Build([]CatalogPath{{Path: dir, Tier: "project"}}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	loader := NewLoader(r)

	// Note: The loader cache is not mutex-protected, so this test documents
	// the current behavior. In production, the orchestrator processes one
	// chat at a time, so concurrent access is not expected.
	// If concurrency is needed in the future, a sync.RWMutex should be added.
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			content, err := loader.Load("concurrent")
			if err != nil {
				errors <- err
				return
			}
			if !strings.Contains(content, "Concurrent body") {
				errors <- fmt.Errorf("unexpected content: %s", content)
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent load error: %v", err)
	}
}
