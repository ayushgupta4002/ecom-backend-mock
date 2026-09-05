// Package config loads service configuration from the environment, with an
// optional .env file as a convenience for local development.
//
// Real environment variables always take precedence over .env entries, so a
// deployment can override anything without editing files.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	// DatabaseURL is the Postgres connection string.
	DatabaseURL string
	// HTTPAddr is the listen address for the API server.
	HTTPAddr string
	// MigrationsDir / SeedDir are filesystem paths to the .sql files.
	MigrationsDir string
	SeedDir       string
	// SkipSeed disables loading seed data (e.g. in production).
	SkipSeed bool

	// RewardMilestoneN is "n": every nth successfully placed order makes one
	// coupon available.
	RewardMilestoneN int
	// RewardDiscountPercent is "x": the percentage discount a generated
	// coupon grants.
	RewardDiscountPercent int
}

// Load reads .env (if present) then the environment, validates the result,
// and returns the configuration. It fails fast on invalid values rather
// than silently falling back to a default, because a wrong reward
// configuration silently changes business behavior.
func Load(dotenvPath string) (Config, error) {
	if err := loadDotEnv(dotenvPath); err != nil {
		return Config{}, err
	}

	cfg := Config{
		DatabaseURL:   getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/ecom?sslmode=disable"),
		HTTPAddr:      getenv("HTTP_ADDR", ":8080"),
		MigrationsDir: getenv("MIGRATIONS_DIR", "migrations"),
		SeedDir:       getenv("SEED_DIR", "seed"),
		SkipSeed:      getenv("SKIP_SEED", "false") == "true",
	}

	n, err := getenvInt("REWARD_MILESTONE_N", 5)
	if err != nil {
		return Config{}, err
	}
	if n <= 0 {
		return Config{}, fmt.Errorf("REWARD_MILESTONE_N must be greater than 0, got %d", n)
	}
	cfg.RewardMilestoneN = n

	x, err := getenvInt("REWARD_DISCOUNT_PERCENT", 10)
	if err != nil {
		return Config{}, err
	}
	if x < 0 || x > 100 {
		return Config{}, fmt.Errorf("REWARD_DISCOUNT_PERCENT must be between 0 and 100, got %d", x)
	}
	cfg.RewardDiscountPercent = x

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL must not be empty")
	}

	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", key, raw)
	}
	return v, nil
}

// loadDotEnv parses a simple KEY=VALUE file. Blank lines and lines starting
// with # are ignored, an optional leading "export " is stripped, and values
// may be wrapped in single or double quotes. Keys already present in the
// real environment are left untouched.
//
// A missing file is not an error: .env is a local-development convenience,
// and deployments are expected to set real environment variables.
func loadDotEnv(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, found := strings.Cut(line, "=")
		if !found {
			return fmt.Errorf("%s line %d: expected KEY=VALUE, got %q", path, lineNo, line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		if _, exists := os.LookupEnv(key); exists {
			continue // real environment wins
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}
