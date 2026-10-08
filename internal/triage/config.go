package triage

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	bigModel   = "claude-opus-5-5"
	smallModel = "claude-haiku-5-5"
)

// Config holds every limit and threshold. Prompts never own a loop bound.
type Config struct {
	// One generative model per role. Planning is bounded and cheap to get slightly
	// wrong: small model. Writing and judging are where quality is paid for: big model.
	ModelPlanner string
	ModelWriter  string
	ModelCritic  string

	AnthropicAPIKey string
	TypeSafeAPIKey  string
	GitHubToken     string

	MaxCriticRounds     int
	MaxEvidenceRequests int
	MaxFileChars        int
	CollectConcurrency  int

	// Decision thresholds. A noul sits at 0.5 when it does not know.
	SkipCodeBelow float64 // P(needs code) at or below this: skip planner and collect
	RubricPass    float64 // every rule at or above this: skip the generative critic
	RubricFail    float64 // any rule below this: reject with templated feedback

	StateDir string
}

// FromEnv reads .env (without overriding the real environment) and then the environment.
func FromEnv() Config {
	loadDotEnv(".env")
	return Config{
		ModelPlanner:        env("TRIAGE_MODEL_PLANNER", smallModel),
		ModelWriter:         env("TRIAGE_MODEL_WRITER", bigModel),
		ModelCritic:         env("TRIAGE_MODEL_CRITIC", bigModel),
		AnthropicAPIKey:     os.Getenv("ANTHROPIC_API_KEY"),
		TypeSafeAPIKey:      os.Getenv("TYPESAFE_API_KEY"),
		GitHubToken:         os.Getenv("GITHUB_TOKEN"),
		MaxCriticRounds:     envInt("TRIAGE_MAX_CRITIC_ROUNDS", 2),
		MaxEvidenceRequests: envInt("TRIAGE_MAX_EVIDENCE", 5),
		MaxFileChars:        6000,
		CollectConcurrency:  4,
		SkipCodeBelow:       envFloat("TRIAGE_SKIP_CODE_BELOW", 0.15),
		RubricPass:          envFloat("TRIAGE_RUBRIC_PASS", 0.85),
		RubricFail:          envFloat("TRIAGE_RUBRIC_FAIL", 0.50),
		StateDir:            env("TRIAGE_STATE_DIR", ".triage"),
	}
}

// Validate reports every misconfiguration at once.
func (c Config) Validate() error {
	var errs []error
	if c.MaxCriticRounds < 1 {
		errs = append(errs, errors.New("TRIAGE_MAX_CRITIC_ROUNDS must be at least 1"))
	}
	if c.MaxEvidenceRequests < 1 {
		errs = append(errs, errors.New("TRIAGE_MAX_EVIDENCE must be at least 1"))
	}
	if c.CollectConcurrency < 1 {
		errs = append(errs, errors.New("collect concurrency must be at least 1"))
	}
	for name, p := range map[string]float64{
		"TRIAGE_SKIP_CODE_BELOW": c.SkipCodeBelow,
		"TRIAGE_RUBRIC_PASS":     c.RubricPass,
		"TRIAGE_RUBRIC_FAIL":     c.RubricFail,
	} {
		if p < 0 || p > 1 {
			errs = append(errs, fmt.Errorf("%s must be a probability, got %v", name, p))
		}
	}
	if c.RubricFail >= c.RubricPass {
		errs = append(errs, errors.New("TRIAGE_RUBRIC_FAIL must be below TRIAGE_RUBRIC_PASS"))
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
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		value = strings.Trim(value, `"'`)
		if _, set := os.LookupEnv(key); !set && value != "" {
			_ = os.Setenv(key, value) // best effort: a .env is a convenience, not a contract
		}
	}
}
