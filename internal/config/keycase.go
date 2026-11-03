package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/go-viper/mapstructure/v2"
)

// Viper treats config keys as case-insensitive: when it reads a file it
// lowercases every map key it finds, recursively and inside lists
// (insensitiviseMap in viper's util.go, called from unmarshalReader). That
// is right for the config's own field names, but wrong for maps whose keys
// are user data: operations.mcp.servers[].env (GITHUB_TOKEN reached the MCP
// subprocess as github_token, and env names are case-sensitive),
// operations.mcp.servers[].headers and every extra_params block (nested
// keys such as metadata or chat_template_kwargs are sent to the API as
// written). Viper 1.21 has no option to keep the case.
//
// restoreKeyCase puts the file's spelling back. It walks the decoded config
// alongside the same file parsed with yaml.v3, which keeps the case, and
// renames each key of every string-keyed map (and of maps nested in
// map[string]any values) to the key the file used. Values are left as
// viper decoded them, so EZYAPPER_* environment overrides of map entries
// still apply. Struct fields are matched to file keys case-insensitively,
// as viper matches them.
//
// Keys that differ only in case (Foo and foo in one map) are a single key
// to viper, which keeps one of the values at random. Each of them is
// decoded from the file instead, so both survive.
func restoreKeyCase(v reflect.Value, raw any) {
	if raw == nil || !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			restoreKeyCase(v.Elem(), raw)
		}
	case reflect.Interface:
		if v.IsNil() || !v.CanSet() {
			return
		}
		inner := v.Elem()
		cp := reflect.New(inner.Type()).Elem()
		cp.Set(inner)
		restoreKeyCase(cp, raw)
		v.Set(cp)
	case reflect.Struct:
		rm := stringKeyed(raw)
		if rm == nil {
			return
		}
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, opts, _ := strings.Cut(f.Tag.Get("mapstructure"), ",")
			if strings.Contains(opts, "squash") {
				restoreKeyCase(v.Field(i), raw)
				continue
			}
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			if child, ok := lookupFold(rm, name); ok {
				restoreKeyCase(v.Field(i), child)
			}
		}
	case reflect.Slice, reflect.Array:
		rs, ok := raw.([]any)
		// A list of another length did not come from the file (an
		// environment override replaced it); leave it alone.
		if !ok || len(rs) != v.Len() {
			return
		}
		for i := range rs {
			restoreKeyCase(v.Index(i), rs[i])
		}
	case reflect.Map:
		if v.IsNil() || v.Type().Key().Kind() != reflect.String || !v.CanSet() {
			return
		}
		rm := stringKeyed(raw)
		if rm == nil {
			return
		}
		byLower := make(map[string][]string, len(rm))
		for k := range rm {
			lk := strings.ToLower(k)
			byLower[lk] = append(byLower[lk], k)
		}
		keyType, elemType := v.Type().Key(), v.Type().Elem()
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			dk, dv := iter.Key(), iter.Value()
			cands := byLower[strings.ToLower(dk.String())]
			switch len(cands) {
			case 0:
				out.SetMapIndex(dk, dv)
			case 1:
				e := reflect.New(elemType).Elem()
				e.Set(dv)
				restoreKeyCase(e, rm[cands[0]])
				out.SetMapIndex(reflect.ValueOf(cands[0]).Convert(keyType), e)
			default:
				sort.Strings(cands)
				for _, c := range cands {
					e := reflect.New(elemType).Elem()
					if err := mapstructure.WeakDecode(rm[c], e.Addr().Interface()); err != nil {
						e.Set(dv)
					}
					out.SetMapIndex(reflect.ValueOf(c).Convert(keyType), e)
				}
			}
		}
		v.Set(out)
	}
}

// stringKeyed returns a YAML mapping with string keys, or nil if raw is not
// a mapping.
func stringKeyed(raw any) map[string]any {
	switch m := raw.(type) {
	case map[string]any:
		return m
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[fmt.Sprint(k)] = val
		}
		return out
	}
	return nil
}

// lookupFold finds a struct field's key in a YAML mapping the way viper
// does: an exact match first, then any key equal ignoring case.
func lookupFold(m map[string]any, name string) (any, bool) {
	if val, ok := m[name]; ok {
		return val, true
	}
	var keys []string
	for k := range m {
		if strings.EqualFold(k, name) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, false
	}
	sort.Strings(keys)
	return m[keys[0]], true
}
