// Package hotreload applies a changed configuration to the running bot
// without a restart.
//
// A reload has three steps:
//
//  1. Load and fully validate the new file (config.Load). A file that does
//     not parse or validate is rejected and the running config is kept.
//  2. Diff the old and new config (Diff) to get the changed key paths, and
//     ask every Component to Prepare: build whatever the change needs that
//     can fail (open the new Discord session, connect MCP servers, build the
//     new memory service, bind the new web port, ...). Nothing visible
//     changes in this phase.
//  3. If every Component prepared successfully, publish the new config and
//     Commit each prepared change (pointer swaps that cannot fail). If any
//     Component failed, Abort every prepared change, so the bot keeps
//     running on the previous config exactly as before.
//
// Only key paths are logged, never values, so secrets (tokens, API keys,
// passwords, MCP headers) never reach the log.
package hotreload

import (
	"reflect"
	"sort"
	"strings"

	"ezyapper/internal/config"
)

// sectionPaths maps the top-level fields of config.Config to the key path
// they have in the config file (config.Load flattens the file's sections).
var sectionPaths = map[string]string{
	"Discord":        "core.discord",
	"AI":             "core.ai",
	"Decision":       "core.decision",
	"Embedding":      "memory_pipeline.embedding",
	"Memory":         "memory_pipeline.memory",
	"Qdrant":         "memory_pipeline.qdrant",
	"Blacklist":      "access_control.blacklist",
	"Whitelist":      "access_control.whitelist",
	"MessageFilters": "access_control.message_filters",
	"Web":            "operations.web",
	"Logging":        "operations.logging",
	"Plugins":        "operations.plugins",
	"MCP":            "operations.mcp",
	"Operations":     "operations.runtime",
}

// Changes is the sorted list of changed key paths, in config-file notation
// (e.g. "core.discord.token", "operations.mcp.servers").
type Changes []string

// Any reports whether any changed path equals one of prefixes or lies below
// it ("core.ai" matches "core.ai.model" but not "core.ai_x").
func (c Changes) Any(prefixes ...string) bool {
	for _, p := range c {
		for _, pre := range prefixes {
			if p == pre || strings.HasPrefix(p, pre+".") {
				return true
			}
		}
	}
	return false
}

// String joins the paths for logging.
func (c Changes) String() string { return strings.Join(c, ", ") }

// Diff returns the key paths whose values differ between old and next.
//
// Structs are compared field by field; slices and maps are compared as a
// whole and reported at their own path (so "operations.mcp.servers" rather
// than an index), with nil and empty treated as equal because a file that
// lists no entries and one that omits the key load differently. Unexported
// fields (the compiled message filter, the file path) are ignored.
func Diff(old, next *config.Config) Changes {
	if old == nil || next == nil {
		if old == next {
			return nil
		}
		return Changes{"*"}
	}
	var out []string
	ov := reflect.ValueOf(old).Elem()
	nv := reflect.ValueOf(next).Elem()
	t := ov.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		path, ok := sectionPaths[f.Name]
		if !ok {
			path = fieldKey(f)
		}
		diffValue(path, ov.Field(i), nv.Field(i), &out)
	}
	sort.Strings(out)
	return out
}

func diffValue(path string, a, b reflect.Value, out *[]string) {
	switch a.Kind() {
	case reflect.Struct:
		t := a.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			key := fieldKey(f)
			sub := path
			if key != "" {
				sub = path + "." + key
			}
			diffValue(sub, a.Field(i), b.Field(i), out)
		}
	case reflect.Slice, reflect.Map:
		if !equalValues(a, b) {
			*out = append(*out, path)
		}
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				*out = append(*out, path)
			}
			return
		}
		diffValue(path, a.Elem(), b.Elem(), out)
	default:
		if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			*out = append(*out, path)
		}
	}
}

// fieldKey returns the config-file key of a struct field: its mapstructure
// name, "" for a squashed embedded struct, or the lowercased field name.
func fieldKey(f reflect.StructField) string {
	tag := f.Tag.Get("mapstructure")
	name, opts, _ := strings.Cut(tag, ",")
	if strings.Contains(opts, "squash") {
		return ""
	}
	if name != "" && name != "-" {
		return name
	}
	if f.Anonymous {
		return ""
	}
	return strings.ToLower(f.Name)
}

// equalValues is reflect.DeepEqual with nil and empty slices/maps treated
// as equal at every depth (a server entry without args loads with nil Args
// from one file and an empty list from another).
func equalValues(a, b reflect.Value) bool {
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}
	if a.Type() != b.Type() {
		return false
	}
	switch a.Kind() {
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !equalValues(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if a.Len() != b.Len() {
			return false
		}
		iter := a.MapRange()
		for iter.Next() {
			bv := b.MapIndex(iter.Key())
			if !bv.IsValid() || !equalValues(iter.Value(), bv) {
				return false
			}
		}
		return true
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if !a.Type().Field(i).IsExported() {
				continue
			}
			if !equalValues(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return equalValues(a.Elem(), b.Elem())
	default:
		return reflect.DeepEqual(a.Interface(), b.Interface())
	}
}
