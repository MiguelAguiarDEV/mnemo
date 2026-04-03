package atlas

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// SkillEntry wraps a Skill with registry metadata.
type SkillEntry struct {
	Skill
	Path string // absolute filesystem path to the skill file
	Tier string // "global", "ops", or "project"
}

// CatalogPath describes a directory to scan for atlas.
type CatalogPath struct {
	Path    string // directory path to scan
	Tier    string // "global", "ops", or "project"
	Project string // project identifier (for logging)
}

// Registry holds all discovered skills indexed by name.
type Registry struct {
	entries map[string]SkillEntry
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	slog.Debug("new registry created")
	return &Registry{
		entries: make(map[string]SkillEntry),
	}
}

// tierPriority returns a numeric priority for tier override ordering.
// Higher value = higher priority.
func tierPriority(tier string) int {
	switch tier {
	case "project":
		return 3
	case "ops":
		return 2
	case "global":
		return 1
	default:
		return 0
	}
}

// Build scans the given catalog paths for skill files (.md),
// parses their frontmatter, and populates the registry.
// Skills with the same name are resolved by tier priority: project > ops > global.
func (r *Registry) Build(catalogPaths []CatalogPath) error {
	slog.Info("building skill registry", "catalogs", len(catalogPaths))

	for _, cp := range catalogPaths {
		if _, err := os.Stat(cp.Path); os.IsNotExist(err) {
			slog.Warn("catalog path does not exist, skipping", "path", cp.Path, "tier", cp.Tier)
			continue
		}

		entries, err := os.ReadDir(cp.Path)
		if err != nil {
			slog.Warn("failed to read catalog directory", "path", cp.Path, "err", err)
			continue
		}

		for _, entry := range entries {
			var skillPath string

			if entry.IsDir() {
				// Look for SKILL.md inside directory
				candidate := filepath.Join(cp.Path, entry.Name(), "SKILL.md")
				if _, err := os.Stat(candidate); err == nil {
					skillPath = candidate
				} else {
					continue
				}
			} else if strings.HasSuffix(entry.Name(), ".md") {
				skillPath = filepath.Join(cp.Path, entry.Name())
			} else {
				continue
			}

			f, err := os.Open(skillPath)
			if err != nil {
				slog.Warn("failed to open skill file", "path", skillPath, "err", err)
				continue
			}

			skill, err := ParseFrontmatter(f)
			f.Close()
			if err != nil {
				slog.Warn("failed to parse skill frontmatter", "path", skillPath, "err", err)
				continue
			}

			// Skip skills with no name (no valid frontmatter)
			if skill.Name == "" {
				slog.Debug("skill has no name, skipping", "path", skillPath)
				continue
			}

			se := SkillEntry{
				Skill: skill,
				Path:  skillPath,
				Tier:  cp.Tier,
			}

			// Tier override: higher priority wins
			if existing, ok := r.entries[skill.Name]; ok {
				if tierPriority(cp.Tier) > tierPriority(existing.Tier) {
					slog.Warn("skill name collision, overriding with higher-tier",
						"name", skill.Name,
						"existing_tier", existing.Tier,
						"new_tier", cp.Tier,
					)
					r.entries[skill.Name] = se
				} else {
					slog.Debug("skill name collision, keeping existing higher-tier",
						"name", skill.Name,
						"existing_tier", existing.Tier,
						"new_tier", cp.Tier,
					)
				}
			} else {
				r.entries[skill.Name] = se
				slog.Debug("skill registered", "name", skill.Name, "tier", cp.Tier, "path", skillPath)
			}
		}
	}

	slog.Info("skill registry built", "total_skills", len(r.entries))
	return nil
}

// Get returns the SkillEntry for the given name.
func (r *Registry) Get(name string) (SkillEntry, bool) {
	slog.Debug("registry lookup", "name", name)
	entry, ok := r.entries[name]
	return entry, ok
}

// Has returns true if a skill with the given name exists.
func (r *Registry) Has(name string) bool {
	slog.Debug("registry has check", "name", name)
	_, ok := r.entries[name]
	return ok
}

// AlwaysSkills returns all skills with Always==true.
func (r *Registry) AlwaysSkills() []SkillEntry {
	var result []SkillEntry
	for _, entry := range r.entries {
		if entry.Always {
			result = append(result, entry)
		}
	}
	slog.Debug("always-skills query", "count", len(result))
	return result
}

// CompactIndex generates a markdown table of all skills for system prompt injection.
func (r *Registry) CompactIndex() string {
	if len(r.entries) == 0 {
		slog.Debug("compact index: no skills")
		return ""
	}

	var b strings.Builder
	b.WriteString("## Available Skills\n\n")
	b.WriteString("Call load_skill(name) to load any of these:\n\n")
	b.WriteString("| Name | Description |\n")
	b.WriteString("|------|-------------|\n")

	for _, entry := range r.entries {
		desc := entry.Description
		if entry.Always {
			desc = "[always loaded] " + desc
		}
		b.WriteString(fmt.Sprintf("| %s | %s |\n", entry.Name, desc))
	}

	slog.Debug("compact index generated", "skills", len(r.entries))
	return b.String()
}
