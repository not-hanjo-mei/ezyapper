package plugin

import (
	"strings"
	"testing"
)

func TestIsSecretEnvVar_DetectsToken(t *testing.T) {
	if !isSecretEnvVar("MY_TOKEN") {
		t.Fatal("MY_TOKEN should be detected as secret")
	}
}

func TestIsSecretEnvVar_DetectsKey(t *testing.T) {
	if !isSecretEnvVar("API_KEY") {
		t.Fatal("API_KEY should be detected as secret")
	}
}

func TestIsSecretEnvVar_DetectsPassword(t *testing.T) {
	if !isSecretEnvVar("DB_PASSWORD") {
		t.Fatal("DB_PASSWORD should be detected as secret")
	}
}

func TestIsSecretEnvVar_DetectsAuth(t *testing.T) {
	if !isSecretEnvVar("X_AUTH") {
		t.Fatal("X_AUTH should be detected as secret")
	}
}

func TestIsSecretEnvVar_CaseInsensitive(t *testing.T) {
	if !isSecretEnvVar("my_token") {
		t.Fatal("my_token should be detected as secret (case-insensitive)")
	}
}

func TestIsSecretEnvVar_SafeVarReturnsFalse(t *testing.T) {
	if isSecretEnvVar("PATH") {
		t.Fatal("PATH should not be detected as secret")
	}
}

func TestIsSecretEnvVar_EmptyKey(t *testing.T) {
	if isSecretEnvVar("") {
		t.Fatal("empty key should not be detected as secret")
	}
}

func TestBuildPluginEnv_FiltersSecretsAndKeepsSafeAndExtra(t *testing.T) {
	t.Setenv("MY_API_TOKEN", "x")
	t.Setenv("EZYAPPER_TEST_SAFE_VAR", "kept")

	env := buildPluginEnv("dir", "EXTRA=1")

	for _, e := range env {
		if e == "MY_API_TOKEN=x" {
			t.Fatal("secret env var MY_API_TOKEN should be filtered out")
		}
	}

	var foundSafe bool
	for _, e := range env {
		if e == "EZYAPPER_TEST_SAFE_VAR=kept" {
			foundSafe = true
			break
		}
	}
	if !foundSafe {
		t.Fatal("non-secret env var EZYAPPER_TEST_SAFE_VAR should be present")
	}

	if len(env) == 0 {
		t.Fatal("buildPluginEnv returned empty slice")
	}
	if last := env[len(env)-1]; last != "EXTRA=1" {
		t.Fatalf("last element should be EXTRA=1, got %q", last)
	}

	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			return
		}
	}
}
