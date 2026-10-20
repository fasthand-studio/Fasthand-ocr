package config

import (
	"strings"
	"testing"
	"time"
)

// clearEnv removes inherited settings so a developer's own shell cannot change
// what these tests observe.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"API_KEY", "CF_ACCOUNT_ID", "CF_API_TOKEN", "AI_MODEL", "ADDR",
		"UPSTREAM_TIMEOUT", "REQUEST_TIMEOUT", "MAX_CONCURRENT_OCR",
	} {
		t.Setenv(key, "")
	}
}

func TestFromEnvDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("API_KEY", "key")
	t.Setenv("CF_ACCOUNT_ID", "acct")
	t.Setenv("CF_API_TOKEN", "token")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.Model != DefaultModel {
		t.Errorf("Model = %q, want %q", cfg.Model, DefaultModel)
	}
	if cfg.Addr != DefaultAddr {
		t.Errorf("Addr = %q, want %q", cfg.Addr, DefaultAddr)
	}
	if cfg.UpstreamTimeout != DefaultUpstream {
		t.Errorf("UpstreamTimeout = %v, want %v", cfg.UpstreamTimeout, DefaultUpstream)
	}
	if cfg.RequestTimeout != DefaultRequest {
		t.Errorf("RequestTimeout = %v, want %v", cfg.RequestTimeout, DefaultRequest)
	}
	if cfg.MaxConcurrentOCR != DefaultConcurrent {
		t.Errorf("MaxConcurrentOCR = %d, want %d", cfg.MaxConcurrentOCR, DefaultConcurrent)
	}
	if cfg.MaxConcurrentRequests != DefaultConcurrentRequests {
		t.Errorf("MaxConcurrentRequests = %d, want %d", cfg.MaxConcurrentRequests, DefaultConcurrentRequests)
	}
	if cfg.MaxConcurrentRequests < cfg.MaxConcurrentOCR {
		t.Errorf("the default request bound (%d) must not sit below the inference bound (%d)",
			cfg.MaxConcurrentRequests, cfg.MaxConcurrentOCR)
	}
	if missing := cfg.Missing(); len(missing) != 0 {
		t.Errorf("Missing = %v, want none", missing)
	}
}

func TestFromEnvOverrides(t *testing.T) {
	t.Setenv("API_KEY", "key")
	t.Setenv("CF_ACCOUNT_ID", "acct")
	t.Setenv("CF_API_TOKEN", "token")
	t.Setenv("AI_MODEL", "@cf/meta/other-model")
	t.Setenv("ADDR", "0.0.0.0:9000")
	t.Setenv("UPSTREAM_TIMEOUT", "45s")
	t.Setenv("REQUEST_TIMEOUT", "2m")
	t.Setenv("MAX_CONCURRENT_OCR", "7")
	t.Setenv("MAX_CONCURRENT_REQUESTS", "16")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.Model != "@cf/meta/other-model" || cfg.Addr != "0.0.0.0:9000" {
		t.Errorf("overrides ignored: %+v", cfg)
	}
	if cfg.UpstreamTimeout != 45*time.Second || cfg.RequestTimeout != 2*time.Minute {
		t.Errorf("timeouts = %v/%v", cfg.UpstreamTimeout, cfg.RequestTimeout)
	}
	if cfg.MaxConcurrentOCR != 7 {
		t.Errorf("MaxConcurrentOCR = %d, want 7", cfg.MaxConcurrentOCR)
	}
	if cfg.MaxConcurrentRequests != 16 {
		t.Errorf("MaxConcurrentRequests = %d, want 16", cfg.MaxConcurrentRequests)
	}
}

// A misconfigured deployment must name the setting that is missing, not fail
// on the first request.
func TestFromEnvMissingSecrets(t *testing.T) {
	clearEnv(t)
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	got := cfg.Missing()
	want := []string{"API_KEY", "CF_ACCOUNT_ID", "CF_API_TOKEN"}
	if len(got) != len(want) {
		t.Fatalf("Missing = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Missing[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFromEnvPartialSecrets(t *testing.T) {
	clearEnv(t)
	t.Setenv("API_KEY", "key")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	got := cfg.Missing()
	if len(got) != 2 || got[0] != "CF_ACCOUNT_ID" || got[1] != "CF_API_TOKEN" {
		t.Errorf("Missing = %v, want the two Cloudflare settings", got)
	}
}

// An unset API key must not block startup: the service comes up and answers
// 503, which is the behaviour callers already rely on. Only the provider
// credentials are fatal.
func TestMissingProviderIsFatalButAPIKeyIsNot(t *testing.T) {
	clearEnv(t)
	t.Setenv("API_KEY", "key")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if got := cfg.MissingProvider(); len(got) != 2 {
		t.Errorf("MissingProvider = %v, want both Cloudflare settings", got)
	}

	t.Setenv("CF_ACCOUNT_ID", "acct")
	t.Setenv("CF_API_TOKEN", "token")
	t.Setenv("API_KEY", "")
	cfg, err = FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if got := cfg.MissingProvider(); len(got) != 0 {
		t.Errorf("MissingProvider = %v, want none when only API_KEY is unset", got)
	}
	if got := cfg.Missing(); len(got) != 1 || got[0] != "API_KEY" {
		t.Errorf("Missing = %v, want just API_KEY", got)
	}
}

func TestFromEnvInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		need  string
	}{
		{"bad duration", "UPSTREAM_TIMEOUT", "soon", "UPSTREAM_TIMEOUT"},
		{"negative duration", "REQUEST_TIMEOUT", "-5s", "REQUEST_TIMEOUT"},
		{"zero duration", "REQUEST_TIMEOUT", "0s", "REQUEST_TIMEOUT"},
		{"non numeric concurrency", "MAX_CONCURRENT_OCR", "many", "MAX_CONCURRENT_OCR"},
		{"zero concurrency", "MAX_CONCURRENT_OCR", "0", "MAX_CONCURRENT_OCR"},
		{"negative concurrency", "MAX_CONCURRENT_OCR", "-3", "MAX_CONCURRENT_OCR"},
		{"non numeric request limit", "MAX_CONCURRENT_REQUESTS", "many", "MAX_CONCURRENT_REQUESTS"},
		{"zero request limit", "MAX_CONCURRENT_REQUESTS", "0", "MAX_CONCURRENT_REQUESTS"},
		{"upstream above request", "UPSTREAM_TIMEOUT", "5m", "must not exceed"},
		// Raising inference past the buffering bound would make the inference
		// limit unreachable, so it is rejected rather than silently ignored.
		{"inference above request bound", "MAX_CONCURRENT_OCR", "128", "MAX_CONCURRENT_REQUESTS"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(tc.key, tc.value)
			_, err := FromEnv()
			if err == nil {
				t.Fatalf("%s=%q accepted, want rejection", tc.key, tc.value)
			}
			if !contains(err.Error(), tc.need) {
				t.Errorf("error %q does not mention %q", err, tc.need)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
