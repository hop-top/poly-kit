package util

import (
	"testing"
	"time"
)

func TestEnvString(t *testing.T) {
	t.Setenv("TEST_ENV_STR", "hello")
	if got := EnvString("TEST_ENV_STR", "fallback"); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestEnvStringDefault(t *testing.T) {
	if got := EnvString("TEST_ENV_STR_UNSET", "fallback"); got != "fallback" {
		t.Errorf("got %q, want %q", got, "fallback")
	}
}

func TestEnvInt(t *testing.T) {
	t.Setenv("TEST_ENV_INT", "42")
	if got := EnvInt("TEST_ENV_INT", 0); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestEnvIntDefault(t *testing.T) {
	t.Setenv("TEST_ENV_INT_BAD", "notanumber")
	if got := EnvInt("TEST_ENV_INT_BAD", 7); got != 7 {
		t.Errorf("got %d, want 7", got)
	}
}

func TestEnvBoolTruthy(t *testing.T) {
	for _, v := range []string{"1", "true", "yes", "on", "TRUE", "Yes"} {
		t.Setenv("TEST_ENV_BOOL", v)
		if got := EnvBool("TEST_ENV_BOOL", false); !got {
			t.Errorf("EnvBool(%q) = false, want true", v)
		}
	}
}

func TestEnvBoolFalsy(t *testing.T) {
	for _, v := range []string{"0", "false", "no", "off"} {
		t.Setenv("TEST_ENV_BOOL", v)
		if got := EnvBool("TEST_ENV_BOOL", true); got {
			t.Errorf("EnvBool(%q) = true, want false", v)
		}
	}
}

func TestEnvBoolDefault(t *testing.T) {
	if got := EnvBool("TEST_ENV_BOOL_UNSET", true); !got {
		t.Error("expected default true")
	}
}

func TestEnvDuration(t *testing.T) {
	t.Setenv("TEST_ENV_DUR", "5s")
	if got := EnvDuration("TEST_ENV_DUR", 0); got != 5*time.Second {
		t.Errorf("got %v, want 5s", got)
	}
	t.Setenv("TEST_ENV_DUR", "100ms")
	if got := EnvDuration("TEST_ENV_DUR", 0); got != 100*time.Millisecond {
		t.Errorf("got %v, want 100ms", got)
	}
}

func TestEnvDurationDefault(t *testing.T) {
	t.Setenv("TEST_ENV_DUR_BAD", "notaduration")
	if got := EnvDuration("TEST_ENV_DUR_BAD", 3*time.Second); got != 3*time.Second {
		t.Errorf("got %v, want 3s", got)
	}
}
