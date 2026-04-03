// Package skills implements skill format parsing, registry, and loading
// for the JARVIS skills architecture.
package atlas

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill represents a parsed skill with frontmatter metadata and body content.
type Skill struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Always      bool     `yaml:"always"`
	Triggers    []string `yaml:"triggers"`
	Content     string   `yaml:"-"` // markdown body without frontmatter
}

var nameRegex = regexp.MustCompile(`^[a-z0-9-]+$`)

// ParseFrontmatter parses a skill file from reader, extracting YAML frontmatter
// delimited by --- and setting Content to the remaining body.
// If no frontmatter is found, the entire content is treated as the body
// with empty metadata.
func ParseFrontmatter(reader io.Reader) (Skill, error) {
	scanner := bufio.NewScanner(reader)
	var skill Skill

	// Look for opening ---
	foundOpen := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			foundOpen = true
			break
		}
		// If first non-empty line is not ---, no frontmatter
		if strings.TrimSpace(line) != "" {
			slog.Debug("no frontmatter delimiter found, treating entire file as content")
			// Read the rest as content
			var body strings.Builder
			body.WriteString(line)
			body.WriteString("\n")
			for scanner.Scan() {
				body.WriteString(scanner.Text())
				body.WriteString("\n")
			}
			skill.Content = strings.TrimRight(body.String(), "\n")
			return skill, nil
		}
	}

	if !foundOpen {
		slog.Debug("empty file or no frontmatter found")
		return skill, nil
	}

	// Collect YAML between --- delimiters
	var yamlBuf strings.Builder
	foundClose := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			foundClose = true
			break
		}
		yamlBuf.WriteString(line)
		yamlBuf.WriteString("\n")
	}

	if !foundClose {
		return Skill{}, fmt.Errorf("no closing frontmatter delimiter found")
	}

	// Parse YAML — use a map first to detect unknown fields
	rawMap := make(map[string]interface{})
	if err := yaml.Unmarshal([]byte(yamlBuf.String()), &rawMap); err != nil {
		return Skill{}, fmt.Errorf("invalid YAML in frontmatter: %w", err)
	}

	// Check for unknown fields
	knownFields := map[string]bool{
		"name": true, "description": true, "always": true,
		"triggers": true, "license": true, "metadata": true,
	}
	for key := range rawMap {
		if !knownFields[key] {
			slog.Warn("unknown field in frontmatter", "field", key)
		}
	}

	// Parse into struct
	if err := yaml.Unmarshal([]byte(yamlBuf.String()), &skill); err != nil {
		return Skill{}, fmt.Errorf("invalid YAML in frontmatter: %w", err)
	}

	// Read remaining content (body)
	var body strings.Builder
	for scanner.Scan() {
		body.WriteString(scanner.Text())
		body.WriteString("\n")
	}
	skill.Content = strings.TrimRight(body.String(), "\n")

	if skill.Triggers == nil {
		skill.Triggers = []string{}
	}

	slog.Debug("frontmatter parsed successfully", "name", skill.Name)
	return skill, nil
}

// ValidateSkill validates a Skill struct. Name is required and must match
// ^[a-z0-9-]+$. Description is required and max 200 chars.
func ValidateSkill(s Skill) error {
	if s.Name == "" {
		slog.Debug("validation failed: missing name")
		return fmt.Errorf("missing required field: name")
	}
	if !nameRegex.MatchString(s.Name) {
		slog.Debug("validation failed: invalid name format", "name", s.Name)
		return fmt.Errorf("invalid name format: must match ^[a-z0-9-]+$")
	}
	if s.Description == "" {
		slog.Debug("validation failed: missing description")
		return fmt.Errorf("missing required field: description")
	}
	if len(s.Description) > 200 {
		slog.Debug("validation failed: description too long", "length", len(s.Description))
		return fmt.Errorf("description exceeds 200 characters (got %d)", len(s.Description))
	}
	slog.Debug("skill validation passed", "name", s.Name)
	return nil
}
