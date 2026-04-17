// Package prompt provides utilities for prompt template compilation and management
package prompt

import (
	"strings"
)

// Compiler handles safe template compilation with variable substitution.
// It preserves unknown variables instead of failing, enabling flexible
// template reuse across different contexts.
type Compiler struct{}

// NewCompiler creates a new prompt compiler
func NewCompiler() *Compiler {
	return &Compiler{}
}

// Compile substitutes variables into a template.
// Unknown variables are preserved in their original {variable} form.
// This allows templates to be compiled in stages with different variable sets.
func (c *Compiler) Compile(template string, vars map[string]string) string {
	result := template

	for key, value := range vars {
		placeholder := "{" + key + "}"
		result = strings.ReplaceAll(result, placeholder, value)
	}

	return result
}

// SafeCompile substitutes variables while escaping literal braces.
// Use this when templates may contain literal {} characters that should be preserved.
func (c *Compiler) SafeCompile(template string, vars map[string]string) string {
	// Escape literal {} by doubling them
	escaped := strings.ReplaceAll(template, "{}", "{{}}")

	// Substitute known variables
	for key, value := range vars {
		placeholder := "{" + key + "}"
		escaped = strings.ReplaceAll(escaped, placeholder, value)
	}

	// Restore escaped braces
	return strings.ReplaceAll(escaped, "{{}}", "{}")
}

// PartialCompile compiles only the variables that exist in the provided map.
// This is useful for multi-stage compilation where some variables are not yet available.
func (c *Compiler) PartialCompile(template string, vars map[string]string) string {
	return c.Compile(template, vars)
}

// ExtractVariables finds all {variable} placeholders in a template.
// Returns a slice of variable names without the braces.
func (c *Compiler) ExtractVariables(template string) []string {
	var variables []string
	var current strings.Builder
	inVar := false

	for i := 0; i < len(template); i++ {
		char := template[i]

		switch char {
		case '{':
			if inVar {
				// Nested brace, reset
				current.Reset()
			}
			inVar = true
		case '}':
			if inVar && current.Len() > 0 {
				variables = append(variables, current.String())
				current.Reset()
			}
			inVar = false
		default:
			if inVar {
				current.WriteByte(char)
			}
		}
	}

	return variables
}

// HasVariable checks if a template contains a specific variable placeholder.
func (c *Compiler) HasVariable(template, variable string) bool {
	placeholder := "{" + variable + "}"
	return strings.Contains(template, placeholder)
}

// Registry manages named prompt templates for different scenarios.
type Registry struct {
	templates map[string]string
}

// NewRegistry creates a new prompt registry
func NewRegistry() *Registry {
	return &Registry{
		templates: make(map[string]string),
	}
}

// Register adds a template to the registry with the given ID.
func (r *Registry) Register(id, template string) {
	r.templates[id] = template
}

// Get retrieves a template by ID.
// Returns an empty string if the ID is not found.
func (r *Registry) Get(id string) string {
	return r.templates[id]
}

// GetWithCompile retrieves and compiles a template in one step.
func (r *Registry) GetWithCompile(id string, vars map[string]string) string {
	template := r.templates[id]
	if template == "" {
		return ""
	}

	compiler := NewCompiler()
	return compiler.Compile(template, vars)
}

// List returns all registered template IDs.
func (r *Registry) List() []string {
	ids := make([]string, 0, len(r.templates))
	for id := range r.templates {
		ids = append(ids, id)
	}
	return ids
}
