package utils

import "testing"

func TestResolveStringFallbacksFirstWins(t *testing.T) {
	if got := ResolveStringFallbacks("a", "b", "c"); got != "a" {
		t.Errorf("got %q, want %q", got, "a")
	}
}

func TestResolveStringFallbacksSkipsEmpty(t *testing.T) {
	if got := ResolveStringFallbacks("", "b"); got != "b" {
		t.Errorf("got %q, want %q", got, "b")
	}
}

func TestResolveStringFallbacksSkipsNone(t *testing.T) {
	if got := ResolveStringFallbacks("None", "b"); got != "b" {
		t.Errorf("got %q, want %q", got, "b")
	}
}

func TestResolveStringFallbacksSkipsEmptyAndNone(t *testing.T) {
	if got := ResolveStringFallbacks("", "None", "c"); got != "c" {
		t.Errorf("got %q, want %q", got, "c")
	}
}

func TestResolveStringFallbacksAllEmptyReturnsEmpty(t *testing.T) {
	if got := ResolveStringFallbacks("", "", "None"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestResolveStringFallbacksNoArgs(t *testing.T) {
	if got := ResolveStringFallbacks(); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestResolveStringFallbacksSingleValid(t *testing.T) {
	if got := ResolveStringFallbacks("only"); got != "only" {
		t.Errorf("got %q, want %q", got, "only")
	}
}
