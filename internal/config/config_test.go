package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ayush/checkout-rewards/internal/config"
)

func writeEnvFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return path
}

// clearEnv makes a test hermetic. Real environment variables intentionally
// take precedence over .env, so any of these leaking in from the shell (for
// example because `make` exported .env) would otherwise change the result.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"DATABASE_URL", "HTTP_ADDR", "MIGRATIONS_DIR", "SEED_DIR", "SKIP_SEED",
		"REWARD_MILESTONE_N", "REWARD_DISCOUNT_PERCENT", "QUOTED",
	} {
		if original, ok := os.LookupEnv(key); ok {
			t.Cleanup(func() { os.Setenv(key, original) })
			os.Unsetenv(key)
		} else {
			t.Cleanup(func() { os.Unsetenv(key) })
		}
	}
}

func TestLoad_ReadsRewardParametersFromDotEnv(t *testing.T) {
	clearEnv(t)
	path := writeEnvFile(t, `
# a comment
DATABASE_URL=postgres://user:pass@localhost:5432/mydb?sslmode=disable
HTTP_ADDR=:9090
REWARD_MILESTONE_N=3
REWARD_DISCOUNT_PERCENT=25
export SKIP_SEED=true
QUOTED="quoted-value"
`)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.RewardMilestoneN != 3 {
		t.Errorf("expected n=3, got %d", cfg.RewardMilestoneN)
	}
	if cfg.RewardDiscountPercent != 25 {
		t.Errorf("expected x=25, got %d", cfg.RewardDiscountPercent)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Errorf("expected addr :9090, got %s", cfg.HTTPAddr)
	}
	if !cfg.SkipSeed {
		t.Error("expected SkipSeed to be true")
	}
	if got := os.Getenv("QUOTED"); got != "quoted-value" {
		t.Errorf("expected quotes to be stripped, got %q", got)
	}
}

func TestLoad_RealEnvironmentTakesPrecedenceOverDotEnv(t *testing.T) {
	clearEnv(t)
	path := writeEnvFile(t, "REWARD_DISCOUNT_PERCENT=10\n")
	t.Setenv("REWARD_DISCOUNT_PERCENT", "40")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RewardDiscountPercent != 40 {
		t.Fatalf("expected real environment (40) to win over .env (10), got %d", cfg.RewardDiscountPercent)
	}
}

// Invalid reward configuration must fail fast at startup rather than
// silently changing business behavior.
func TestLoad_RejectsInvalidRewardParameters(t *testing.T) {
	clearEnv(t)
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"zero milestone", "REWARD_MILESTONE_N", "0"},
		{"negative milestone", "REWARD_MILESTONE_N", "-2"},
		{"non-numeric milestone", "REWARD_MILESTONE_N", "five"},
		{"discount above 100", "REWARD_DISCOUNT_PERCENT", "101"},
		{"negative discount", "REWARD_DISCOUNT_PERCENT", "-5"},
		{"non-numeric discount", "REWARD_DISCOUNT_PERCENT", "ten"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := config.Load(""); err == nil {
				t.Fatalf("expected error for %s=%s, got nil", tc.key, tc.value)
			}
		})
	}
}
