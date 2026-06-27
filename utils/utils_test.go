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

func TestResolveEnvReturnsDev(t *testing.T) {
	if got := ResolveEnv("dev"); got != "dev" {
		t.Errorf("got %q, want %q", got, "dev")
	}
}

func TestResolveEnvReturnsProd(t *testing.T) {
	if got := ResolveEnv("prod"); got != "prod" {
		t.Errorf("got %q, want %q", got, "prod")
	}
}

func TestResolveEnvFirstWins(t *testing.T) {
	if got := ResolveEnv("dev", "prod"); got != "dev" {
		t.Errorf("got %q, want %q", got, "dev")
	}
}

func TestResolveEnvSkipsEmpty(t *testing.T) {
	if got := ResolveEnv("", "prod"); got != "prod" {
		t.Errorf("got %q, want %q", got, "prod")
	}
}

func TestResolveEnvSkipsUnrecognized(t *testing.T) {
	if got := ResolveEnv("staging", "dev"); got != "dev" {
		t.Errorf("got %q, want %q", got, "dev")
	}
}

func TestResolveEnvFallsBackToDevWhenNoValidArgs(t *testing.T) {
	if got := ResolveEnv("", "staging", "None"); got != "dev" {
		t.Errorf("got %q, want %q", got, "dev")
	}
}

func TestResolveEnvNoArgs(t *testing.T) {
	if got := ResolveEnv(); got != "dev" {
		t.Errorf("got %q, want %q", got, "dev")
	}
}
