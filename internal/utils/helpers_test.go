package utils

import "testing"

func TestSplitMessage_MaxLenZero(t *testing.T) {
	content := "hello world"
	result := SplitMessage(content, 0)
	if len(result) != 1 || result[0] != content {
		t.Errorf("SplitMessage(%q, 0) = %v; want [%q]", content, result, content)
	}
}

func TestSplitMessage_MaxLenNegative(t *testing.T) {
	content := "hello world"
	result := SplitMessage(content, -1)
	if len(result) != 1 || result[0] != content {
		t.Errorf("SplitMessage(%q, -1) = %v; want [%q]", content, result, content)
	}
}

func TestSplitMessage_NoSplitNeeded(t *testing.T) {
	content := "hello"
	result := SplitMessage(content, 10)
	if len(result) != 1 || result[0] != content {
		t.Errorf("SplitMessage(%q, 10) = %v; want [%q]", content, result, content)
	}
}

func TestSplitMessage_WordBoundary(t *testing.T) {
	result := SplitMessage("hello world foo bar", 11)
	expected := []string{"hello world", "foo bar"}
	if len(result) != len(expected) {
		t.Fatalf("SplitMessage got %v; want %v", result, expected)
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("part[%d] = %q; want %q", i, result[i], expected[i])
		}
	}
}

func TestSplitMessage_ExactLength(t *testing.T) {
	content := "hello world foo bar"
	result := SplitMessage(content, len(content))
	if len(result) != 1 || result[0] != content {
		t.Errorf("SplitMessage(%q, %d) = %v; want [%q]", content, len(content), result, content)
	}
}
