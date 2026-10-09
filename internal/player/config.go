package player

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	largeModel = "claude-opus-5-5"
	smallModel = "claude-haiku-5-5"
)

// Config holds every limit and threshold. Prompts never own a loop bound.
type Config struct {
	World string

	// One generative model per role. Proposing candidate commands is bounded and cheap to
	// get slightly wrong: small model. Solving a riddle is real reading: large model.
	ModelProposer string
	ModelSolver   string

	AnthropicAPIKey string
	TypeSafeAPIKey  string

	// RiskThreshold is the P(lethal) at or above which code refuses to explore an exit on
	// its own; models and humans may still choose it.
	RiskThreshold float64

	MaxTurns          int // hard stop, win or not
	MaxDeaths         int // deaths before a human is asked
	StuckTurns        int // turns without progress before a human is asked
	MaxRiddleAttempts int // wrong answers before a human is asked

	StateDir string
}

// FromEnv reads .env (without overriding the real environment) and then the environment.
func FromEnv() Config {
	loadDotEnv(".env")
	return Config{
		World:             env("PLAY_WORLD", "cellar"),
		ModelProposer:     env("PLAY_MODEL_PROPOSER", smallModel),
		ModelSolver:       env("PLAY_MODEL_SOLVER", largeModel),
		AnthropicAPIKey:   os.Getenv("ANTHROPIC_API_KEY"),
		TypeSafeAPIKey:    os.Getenv("TYPESAFE_API_KEY"),
		RiskThreshold:     envFloat("PLAY_RISK_THRESHOLD", 0.6),
		MaxTurns:          envInt("PLAY_MAX_TURNS", 60),
		MaxDeaths:         envInt("PLAY_MAX_DEATHS", 3),
		StuckTurns:        envInt("PLAY_STUCK_TURNS", 8),
		MaxRiddleAttempts: envInt("PLAY_MAX_RIDDLE_ATTEMPTS", 3),
		StateDir:          env("PLAY_STATE_DIR", ".play"),
	}
}

// Validate reports every misconfiguration at once.
func (c Config) Validate() error {
	var errs []error
	for name, v := range map[string]int{
		"PLAY_MAX_TURNS":           c.MaxTurns,
		"PLAY_MAX_DEATHS":          c.MaxDeaths,
		"PLAY_STUCK_TURNS":         c.StuckTurns,
		"PLAY_MAX_RIDDLE_ATTEMPTS": c.MaxRiddleAttempts,
	} {
		if v < 1 {
			errs = append(errs, fmt.Errorf("%s must be at least 1", name))
		}
	}
	if c.RiskThreshold <= 0 || c.RiskThreshold > 1 {
		errs = append(errs, fmt.Errorf("PLAY_RISK_THRESHOLD must be in (0, 1], got %v", c.RiskThreshold))
	}
	return errors.Join(errs...)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return def
}

// loadDotEnv is deliberately tiny: KEY=VALUE lines, # comments, no interpolation.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		value = strings.Trim(value, `"'`)
		if _, set := os.LookupEnv(key); !set && value != "" {
			_ = os.Setenv(key, value) // best effort: a .env is a convenience, not a contract
		}
	}
}
