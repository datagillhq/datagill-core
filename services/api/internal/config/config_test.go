package config

import (
	"strings"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func validEnv() map[string]string {
	return map[string]string{
		"APP_DATABASE_URL": "postgres://localhost/app",
		"AUTH_URL":         "http://localhost:3001/api/auth",
		"TEMPORAL_ADDRESS": "localhost:7233",
		"ENCRYPTION_KEY":   strings.Repeat("ab", 32),
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := load(env(validEnv()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Port != "8082" || cfg.Environment != "development" || cfg.AllowedOrigin != "http://localhost:3001" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if len(cfg.EncryptionKey) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(cfg.EncryptionKey))
	}
}

func TestLoad_ReportsEveryProblem(t *testing.T) {
	_, err := load(env(map[string]string{
		"ENCRYPTION_KEY":             "not-hex",
		"GITHUB_APP_PRIVATE_KEY_B64": "%%%",
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"APP_DATABASE_URL", "AUTH_URL", "TEMPORAL_ADDRESS", "ENCRYPTION_KEY", "GITHUB_APP_PRIVATE_KEY_B64"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention %s, got: %v", want, err)
		}
	}
}

func TestLoad_RejectsShortEncryptionKey(t *testing.T) {
	values := validEnv()
	values["ENCRYPTION_KEY"] = "abcd"
	if _, err := load(env(values)); err == nil || !strings.Contains(err.Error(), "ENCRYPTION_KEY") {
		t.Fatalf("expected ENCRYPTION_KEY error, got %v", err)
	}
}
