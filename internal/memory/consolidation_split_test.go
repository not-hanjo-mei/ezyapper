package memory

import "testing"

func TestSplitFactKeyValue_Colon(t *testing.T) {
	key, value, ok := splitFactKeyValue("name:Alice")
	if !ok || key != "name" || value != "Alice" {
		t.Errorf("splitFactKeyValue(%q) = (%q, %q, %v), want (name, Alice, true)",
			"name:Alice", key, value, ok)
	}
}

func TestSplitFactKeyValue_Equals(t *testing.T) {
	key, value, ok := splitFactKeyValue("name=Alice")
	if !ok || key != "name" || value != "Alice" {
		t.Errorf("splitFactKeyValue(%q) = (%q, %q, %v), want (name, Alice, true)",
			"name=Alice", key, value, ok)
	}
}

// TestSplitFactKeyValue_ColonPreferredOverEquals pins ":"-first precedence:
// for input with both separators, ":" wins even if "=" appears earlier.
// strings.IndexAny would break this contract — do not switch to it.
func TestSplitFactKeyValue_ColonPreferredOverEquals(t *testing.T) {
	key, value, ok := splitFactKeyValue("a=b:c")
	if !ok || key != "a=b" || value != "c" {
		t.Errorf("splitFactKeyValue(%q) = (%q, %q, %v), want (a=b, c, true)",
			"a=b:c", key, value, ok)
	}
}

// TestSplitFactKeyValue_FallsThroughOnEmptyKey pins the fall-through behavior:
// when a separator is found but the normalized key is empty, the loop continues
// to the next separator rather than returning false immediately.
func TestSplitFactKeyValue_FallsThroughOnEmptyKey(t *testing.T) {
	key, value, ok := splitFactKeyValue(":value")
	if ok || key != "" || value != "" {
		t.Errorf("splitFactKeyValue(%q) = (%q, %q, %v), want (\"\", \"\", false)",
			":value", key, value, ok)
	}
}

// TestSplitFactKeyValue_FallsThroughOnEmptyValue pins the fall-through behavior:
// when a separator is found but the normalized value is empty, the loop
// continues to the next separator.
func TestSplitFactKeyValue_FallsThroughOnEmptyValue(t *testing.T) {
	key, value, ok := splitFactKeyValue("key:")
	if ok || key != "" || value != "" {
		t.Errorf("splitFactKeyValue(%q) = (%q, %q, %v), want (\"\", \"\", false)",
			"key:", key, value, ok)
	}
}

// TestSplitFactKeyValue_NoSeparator verifies input without any separator
// returns false.
func TestSplitFactKeyValue_NoSeparator(t *testing.T) {
	key, value, ok := splitFactKeyValue("noseparator")
	if ok || key != "" || value != "" {
		t.Errorf("splitFactKeyValue(%q) = (%q, %q, %v), want (\"\", \"\", false)",
			"noseparator", key, value, ok)
	}
}

// TestSplitFactKeyValue_Normalization verifies the full normalization pipeline
// applied to both key and value: TrimSpace, Trim quote chars, TrimSuffix ".",
// and ToLower on the key.
func TestSplitFactKeyValue_Normalization(t *testing.T) {
	key, value, ok := splitFactKeyValue(" \"Name\" : \"Alice.\" ")
	if !ok || key != "name" || value != "Alice" {
		t.Errorf("splitFactKeyValue(%q) = (%q, %q, %v), want (name, Alice, true)",
			" \"Name\" : \"Alice.\" ", key, value, ok)
	}
}

// TestTrySplitOnSep_SepAbsent verifies the helper returns false when the
// separator is not present in the content.
func TestTrySplitOnSep_SepAbsent(t *testing.T) {
	key, value, ok := trySplitOnSep("abc", ":")
	if ok || key != "" || value != "" {
		t.Errorf("trySplitOnSep(%q, %q) = (%q, %q, %v), want (\"\", \"\", false)",
			"abc", ":", key, value, ok)
	}
}

// TestTrySplitOnSep_EmptyNormalizedKey verifies the helper returns false when
// the separator is present but the normalized key is empty.
func TestTrySplitOnSep_EmptyNormalizedKey(t *testing.T) {
	key, value, ok := trySplitOnSep(":v", ":")
	if ok || key != "" || value != "" {
		t.Errorf("trySplitOnSep(%q, %q) = (%q, %q, %v), want (\"\", \"\", false)",
			":v", ":", key, value, ok)
	}
}

// TestTrySplitOnSep_EmptyNormalizedValue verifies the helper returns false
// when the separator is present but the normalized value is empty.
func TestTrySplitOnSep_EmptyNormalizedValue(t *testing.T) {
	key, value, ok := trySplitOnSep("k:", ":")
	if ok || key != "" || value != "" {
		t.Errorf("trySplitOnSep(%q, %q) = (%q, %q, %v), want (\"\", \"\", false)",
			"k:", ":", key, value, ok)
	}
}

// TestTrySplitOnSep_Valid verifies the helper splits a valid key:value pair.
func TestTrySplitOnSep_Valid(t *testing.T) {
	key, value, ok := trySplitOnSep("a:b", ":")
	if !ok || key != "a" || value != "b" {
		t.Errorf("trySplitOnSep(%q, %q) = (%q, %q, %v), want (a, b, true)",
			"a:b", ":", key, value, ok)
	}
}
