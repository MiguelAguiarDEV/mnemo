package atlas

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// Loader loads skill content from the filesystem using the registry.
// It caches loaded content to avoid repeated file reads.
type Loader struct {
	registry *Registry
	mu       sync.RWMutex
	cache    map[string]string
}

// NewLoader creates a Loader backed by the given registry.
func NewLoader(registry *Registry) *Loader {
	slog.Debug("skill loader created")
	return &Loader{
		registry: registry,
		cache:    make(map[string]string),
	}
}

// Load looks up a skill by name in the registry, reads the file from disk,
// strips frontmatter, and returns the markdown body. Results are cached.
func (l *Loader) Load(name string) (string, error) {
	// Check cache first (read lock)
	l.mu.RLock()
	if content, ok := l.cache[name]; ok {
		l.mu.RUnlock()
		slog.Debug("skill loaded from cache", "name", name)
		return content, nil
	}
	l.mu.RUnlock()

	entry, ok := l.registry.Get(name)
	if !ok {
		slog.Warn("skill not found in registry", "name", name)
		return "", fmt.Errorf("skill not found: %s", name)
	}

	data, err := os.ReadFile(entry.Path)
	if err != nil {
		slog.Error("failed to read skill file", "name", name, "path", entry.Path, "err", err)
		return "", fmt.Errorf("failed to read skill: %s: %w", name, err)
	}

	// Parse frontmatter to get just the body content
	skill, err := ParseFrontmatter(strings.NewReader(string(data)))
	if err != nil {
		slog.Error("failed to parse skill frontmatter on load", "name", name, "err", err)
		return "", fmt.Errorf("failed to parse skill: %s: %w", name, err)
	}

	l.mu.Lock()
	l.cache[name] = skill.Content
	l.mu.Unlock()
	slog.Info("skill loaded", "name", name, "path", entry.Path, "bytes", len(skill.Content))
	return skill.Content, nil
}

// LoadMultiple loads multiple skills by name and returns a map of name to content.
// If any skill fails to load, an error is returned with the failing skill name.
func (l *Loader) LoadMultiple(names []string) (map[string]string, error) {
	slog.Info("loading multiple skills", "count", len(names))
	result := make(map[string]string, len(names))
	for _, name := range names {
		content, err := l.Load(name)
		if err != nil {
			return nil, fmt.Errorf("failed to load skill %q: %w", name, err)
		}
		result[name] = content
	}
	slog.Debug("multiple skills loaded", "count", len(result))
	return result, nil
}
