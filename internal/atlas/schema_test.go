package atlas

import (
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantSkill   Skill
		wantErr     bool
		errContains string
	}{
		{
			name: "valid minimal frontmatter",
			input: `---
name: foo
description: Does foo
---
# Foo

Body content here.`,
			wantSkill: Skill{
				Name:        "foo",
				Description: "Does foo",
				Triggers:    []string{},
				Content:     "# Foo\n\nBody content here.",
			},
		},
		{
			name: "valid full frontmatter",
			input: `---
name: task-management
description: Create, list, and complete tasks via tool calls.
always: true
triggers:
  - task
  - todo
---
# Task Management

Full body.`,
			wantSkill: Skill{
				Name:        "task-management",
				Description: "Create, list, and complete tasks via tool calls.",
				Always:      true,
				Triggers:    []string{"task", "todo"},
				Content:     "# Task Management\n\nFull body.",
			},
		},
		{
			name: "missing name",
			input: `---
description: bar
---
Body`,
			wantSkill: Skill{
				Description: "bar",
				Triggers:    []string{},
				Content:     "Body",
			},
		},
		{
			name: "missing description",
			input: `---
name: bar
---
Body`,
			wantSkill: Skill{
				Name:     "bar",
				Triggers: []string{},
				Content:  "Body",
			},
		},
		{
			name: "no frontmatter - entire file as content",
			input: `# Just Markdown

This is a regular markdown file.`,
			wantSkill: Skill{
				Content: "# Just Markdown\n\nThis is a regular markdown file.",
			},
		},
		{
			name:        "invalid YAML",
			input:       "---\nname: [invalid yaml\n---\nBody",
			wantErr:     true,
			errContains: "invalid YAML",
		},
		{
			name: "unknown fields tolerated",
			input: `---
name: foo
description: Does foo
custom_field: x
another_one: 42
---
Body`,
			wantSkill: Skill{
				Name:        "foo",
				Description: "Does foo",
				Triggers:    []string{},
				Content:     "Body",
			},
		},
		{
			name:  "empty file",
			input: "",
			wantSkill: Skill{
				Content: "",
			},
		},
		{
			name:        "unclosed frontmatter",
			input:       "---\nname: foo\ndescription: bar\n",
			wantErr:     true,
			errContains: "no closing frontmatter delimiter",
		},
		{
			name: "frontmatter with empty body",
			input: `---
name: foo
description: bar
---
`,
			wantSkill: Skill{
				Name:        "foo",
				Description: "bar",
				Triggers:    []string{},
				Content:     "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFrontmatter(strings.NewReader(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errContains)
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Name != tt.wantSkill.Name {
				t.Errorf("Name = %q, want %q", got.Name, tt.wantSkill.Name)
			}
			if got.Description != tt.wantSkill.Description {
				t.Errorf("Description = %q, want %q", got.Description, tt.wantSkill.Description)
			}
			if got.Always != tt.wantSkill.Always {
				t.Errorf("Always = %v, want %v", got.Always, tt.wantSkill.Always)
			}
			if got.Content != tt.wantSkill.Content {
				t.Errorf("Content = %q, want %q", got.Content, tt.wantSkill.Content)
			}
			// Compare triggers
			if len(got.Triggers) != len(tt.wantSkill.Triggers) {
				t.Errorf("Triggers len = %d, want %d", len(got.Triggers), len(tt.wantSkill.Triggers))
			} else {
				for i, trig := range got.Triggers {
					if trig != tt.wantSkill.Triggers[i] {
						t.Errorf("Triggers[%d] = %q, want %q", i, trig, tt.wantSkill.Triggers[i])
					}
				}
			}
		})
	}
}

func TestValidateSkill(t *testing.T) {
	tests := []struct {
		name        string
		skill       Skill
		wantErr     bool
		errContains string
	}{
		{
			name:  "valid skill",
			skill: Skill{Name: "foo", Description: "Does foo"},
		},
		{
			name:  "valid with hyphens and numbers",
			skill: Skill{Name: "my-skill-2", Description: "A skill"},
		},
		{
			name:        "missing name",
			skill:       Skill{Description: "bar"},
			wantErr:     true,
			errContains: "missing required field: name",
		},
		{
			name:        "missing description",
			skill:       Skill{Name: "bar"},
			wantErr:     true,
			errContains: "missing required field: description",
		},
		{
			name:        "invalid name format - uppercase",
			skill:       Skill{Name: "Foo", Description: "bar"},
			wantErr:     true,
			errContains: "invalid name format",
		},
		{
			name:        "invalid name format - spaces",
			skill:       Skill{Name: "foo bar", Description: "baz"},
			wantErr:     true,
			errContains: "invalid name format",
		},
		{
			name:        "invalid name format - underscore",
			skill:       Skill{Name: "foo_bar", Description: "baz"},
			wantErr:     true,
			errContains: "invalid name format",
		},
		{
			name:        "description too long",
			skill:       Skill{Name: "foo", Description: strings.Repeat("a", 201)},
			wantErr:     true,
			errContains: "description exceeds 200 characters",
		},
		{
			name:  "description exactly 200 chars",
			skill: Skill{Name: "foo", Description: strings.Repeat("a", 200)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSkill(tt.skill)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errContains)
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseFrontmatter_NilReader(t *testing.T) {
	// strings.NewReader("") simulates an empty/nil input.
	// A true nil io.Reader would panic in bufio.NewScanner, which is expected
	// behavior — callers must provide a valid reader.
	skill, err := ParseFrontmatter(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error for empty reader: %v", err)
	}
	if skill.Name != "" {
		t.Errorf("Name = %q, want empty", skill.Name)
	}
	if skill.Content != "" {
		t.Errorf("Content = %q, want empty", skill.Content)
	}
}

func TestValidateSkill_EmptySkill(t *testing.T) {
	err := ValidateSkill(Skill{})
	if err == nil {
		t.Fatal("expected error for empty skill")
	}
	if !strings.Contains(err.Error(), "missing required field: name") {
		t.Errorf("error = %q, want missing name", err.Error())
	}
}
