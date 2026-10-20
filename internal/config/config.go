// Package config loads the service configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Defaults for values that are not deployment specific.
const (
	DefaultModel      = "@cf/meta/llama-3.2-11b-vision-instruct"
	DefaultAddr       = ":8787"
	DefaultUpstream   = 60 * time.Second
	DefaultRequest    = 90 * time.Second
	DefaultConcurrent = 32
)

// Config holds every runtime setting the service needs. All secrets arrive
// through the environment; nothing here has an insecure fallback.
type Config struct {
	APIKey           string
	AccountID        string
	APIToken         string
	Model            string
	Addr             string
	UpstreamTimeout  time.Duration
	RequestTimeout   time.Duration
	MaxConcurrentOCR int
}

// MissingProvider reports the settings without which no request can ever be
// served, so they are worth refusing to start for.
func (c Config) MissingProvider() []string {
	var missing []string
	if c.AccountID == "" {
		missing = append(missing, "CF_ACCOUNT_ID")
	}
	if c.APIToken == "" {
		missing = append(missing, "CF_API_TOKEN")
	}
	return missing
}

// Missing reports every required setting that is absent. A missing API key is
// not fatal to startup: the service comes up and answers 503, which is the
// behaviour callers have always seen.
func (c Config) Missing() []string {
	var missing []string
	if c.APIKey == "" {
		missing = append(missing, "API_KEY")
	}
	return append(missing, c.MissingProvider()...)
}

// FromEnv reads the configuration, applying defaults and rejecting malformed
// values rather than silently falling back to them. An empty value counts as
// unset, which is how secret managers typically hand over an unused setting.
func FromEnv() (Config, error) {
	cfg := Config{
		APIKey:    os.Getenv("API_KEY"),
		AccountID: os.Getenv("CF_ACCOUNT_ID"),
		APIToken:  os.Getenv("CF_API_TOKEN"),
		Model:     envOr("AI_MODEL", DefaultModel),
		Addr:      envOr("ADDR", DefaultAddr),
	}

	var errs []error
	cfg.UpstreamTimeout, cfg.RequestTimeout = DefaultUpstream, DefaultRequest
	cfg.MaxConcurrentOCR = DefaultConcurrent

	if d, err := durationEnv("UPSTREAM_TIMEOUT", DefaultUpstream); err != nil {
		errs = append(errs, err)
	} else {
		cfg.UpstreamTimeout = d
	}
	if d, err := durationEnv("REQUEST_TIMEOUT", DefaultRequest); err != nil {
		errs = append(errs, err)
	} else {
		cfg.RequestTimeout = d
	}
	if n, err := intEnv("MAX_CONCURRENT_OCR", DefaultConcurrent); err != nil {
		errs = append(errs, err)
	} else {
		cfg.MaxConcurrentOCR = n
	}
	if cfg.UpstreamTimeout > cfg.RequestTimeout {
		errs = append(errs, fmt.Errorf("UPSTREAM_TIMEOUT (%s) must not exceed REQUEST_TIMEOUT (%s)",
			cfg.UpstreamTimeout, cfg.RequestTimeout))
	}

	return cfg, errors.Join(errs...)
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback, fmt.Errorf("%s: %q is not a positive duration", key, v)
	}
	return d, nil
}

func intEnv(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback, fmt.Errorf("%s: %q is not a positive integer", key, v)
	}
	return n, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
