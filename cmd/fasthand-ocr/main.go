// Command fasthand-ocr serves the Fasthand OCR API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fasthand-studio/fasthand-ocr/internal/config"
	"github.com/fasthand-studio/fasthand-ocr/internal/ocr"
	"github.com/fasthand-studio/fasthand-ocr/internal/server"
)

// shutdownGrace bounds how long in-flight requests get to finish after a
// stop signal.
const shutdownGrace = 15 * time.Second

// uploadAllowance is how long an 8 MB body is given to arrive. http.Server's
// WriteTimeout starts when the request headers are read, not when the body is
// done, so a write deadline sized only around inference would cut off a phone
// on a poor connection before the model had been called at all. Inference is
// bounded separately by REQUEST_TIMEOUT.
const uploadAllowance = 5 * time.Minute

func newHTTPServer(cfg config.Config, provider ocr.Provider, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.New(provider, cfg, log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      cfg.RequestTimeout + uploadAllowance,
		IdleTimeout:       2 * time.Minute,
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fasthand-ocr:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if missing := cfg.MissingProvider(); len(missing) > 0 {
		// Failing here beats starting a server that cannot serve a single
		// request; the names below are exactly what has to be set.
		return fmt.Errorf("missing required settings: %s", strings.Join(missing, ", "))
	}
	if cfg.APIKey == "" {
		// Not fatal: every OCR request is refused with 503 until it is set,
		// which is what the service has always done when the secret is absent.
		log.Warn("API_KEY is not set; all OCR requests will be refused with 503")
	}

	provider := ocr.NewWorkerAI(
		"https://api.cloudflare.com",
		cfg.AccountID,
		cfg.APIToken,
		cfg.Model,
		cfg.UpstreamTimeout,
	)

	httpSrv := newHTTPServer(cfg, provider, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "model", cfg.Model,
			"max_concurrent_ocr", cfg.MaxConcurrentOCR,
			"upstream_timeout", cfg.UpstreamTimeout.String(),
			"request_timeout", cfg.RequestTimeout.String())
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Info("shutting down", "grace", shutdownGrace.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
