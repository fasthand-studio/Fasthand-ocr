package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fasthand-studio/fasthand-ocr/internal/config"
	"github.com/fasthand-studio/fasthand-ocr/internal/ocr"
)

type stubProvider struct{}

func (stubProvider) Recognize(context.Context, *ocr.Image) (string, error) { return "", nil }

// Regression: the write deadline used to be REQUEST_TIMEOUT + 5s. Because
// WriteTimeout is measured from the end of the request headers, a body that
// takes longer than that to upload killed the connection before inference
// began, so the client got a truncated response instead of JSON.
func TestWriteTimeoutCoversTheBodyUpload(t *testing.T) {
	cfg := config.Config{Addr: ":0", RequestTimeout: 90 * time.Second, UpstreamTimeout: 60 * time.Second}
	srv := newHTTPServer(cfg, stubProvider{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if srv.WriteTimeout <= cfg.RequestTimeout {
		t.Errorf("WriteTimeout = %s, must exceed REQUEST_TIMEOUT (%s) or slow uploads are cut off",
			srv.WriteTimeout, cfg.RequestTimeout)
	}
	if srv.WriteTimeout != cfg.RequestTimeout+uploadAllowance {
		t.Errorf("WriteTimeout = %s, want REQUEST_TIMEOUT + uploadAllowance (%s)",
			srv.WriteTimeout, cfg.RequestTimeout+uploadAllowance)
	}
	if srv.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout must be set: it is the slowloris guard")
	}
	// ReadTimeout stays unset on purpose; a set value would cap the whole body
	// read and reintroduce the failure above.
	if srv.ReadTimeout != 0 {
		t.Errorf("ReadTimeout = %s, want 0 so large uploads are not penalised", srv.ReadTimeout)
	}
}

func TestShutdownGraceLeavesTimeForRequestTimeout(t *testing.T) {
	if shutdownGrace >= config.DefaultRequest {
		t.Errorf("shutdownGrace = %s, must be shorter than the request deadline (%s) or shutdown never waits for a real request",
			shutdownGrace, config.DefaultRequest)
	}
}

// The startup contract is that a deployment which could never serve a request
// stops here and names the variables, rather than binding a port and answering
// 500 to everything.
func TestRunRefusesWithoutProviderSettings(t *testing.T) {
	for _, key := range []string{"CF_ACCOUNT_ID", "CF_API_TOKEN", "API_KEY", "ADDR"} {
		t.Setenv(key, "")
	}
	err := run()
	if err == nil {
		t.Fatal("run() started without provider credentials, want an error")
	}
	for _, want := range []string{"missing required settings", "CF_ACCOUNT_ID", "CF_API_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
