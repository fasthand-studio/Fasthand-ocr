package ocr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultMaxTokens bounds the transcription length, matching the previous
// implementation so responses stay comparable.
const DefaultMaxTokens = 4096

// maxUpstreamResponse caps how much of a provider reply is read. Model output
// is text; anything beyond this is a malfunction, not a valid answer.
const maxUpstreamResponse = 1 << 20

// WorkerAI calls the Cloudflare Workers AI REST API.
//
// The REST API is the documented way to run Workers AI models from outside a
// Worker: POST {BaseURL}/client/v4/accounts/{account_id}/ai/run/{model-name}
// with an API token as a bearer secret.
type WorkerAI struct {
	baseURL   string
	accountID string
	token     string
	model     string
	maxTokens int
	client    *http.Client
}

// NewWorkerAI builds a provider around a shared client. The client is created
// once so that connections and the transport are reused across requests.
func NewWorkerAI(baseURL, accountID, token, model string, timeout time.Duration) *WorkerAI {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 32
	return &WorkerAI{
		baseURL:   strings.TrimRight(baseURL, "/"),
		accountID: accountID,
		token:     token,
		model:     model,
		maxTokens: DefaultMaxTokens,
		client:    &http.Client{Transport: transport, Timeout: timeout},
	}
}

type runRequest struct {
	Image     string `json:"image"`
	Prompt    string `json:"prompt"`
	MaxTokens int    `json:"max_tokens"`
}

type runResponse struct {
	Success *bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result struct {
		Response    *string `json:"response"`
		Description *string `json:"description"`
	} `json:"result"`
}

// Recognize sends the image and returns the raw model text.
func (w *WorkerAI) Recognize(ctx context.Context, img *Image) (string, error) {
	payload, err := json.Marshal(runRequest{
		Image:     base64.StdEncoding.EncodeToString(img.Data),
		Prompt:    Prompt,
		MaxTokens: w.maxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("encoding upstream request: %w", err)
	}

	url := fmt.Sprintf("%s/client/v4/accounts/%s/ai/run/%s", w.baseURL, w.accountID, w.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("building upstream request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("upstream request cancelled: %w", ctx.Err())
		}
		return "", fmt.Errorf("upstream request: %w", err)
	}
	defer resp.Body.Close()

	// One byte past the cap is read so an oversized reply is reported as
	// truncated rather than failing later as invalid JSON.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamResponse+1))
	if err != nil {
		return "", fmt.Errorf("reading upstream response: %w", err)
	}
	if len(body) > maxUpstreamResponse {
		return "", fmt.Errorf("upstream response larger than %d bytes", maxUpstreamResponse)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			// A rejected token looks identical to a model fault in the logs
			// unless it is named, and this is the one failure an operator can
			// actually fix. No credential is echoed.
			return "", fmt.Errorf("upstream rejected the credentials (status %d): %s; check CF_ACCOUNT_ID and that CF_API_TOKEN is allowed to run Workers AI models",
				resp.StatusCode, providerMessage(body))
		}
		return "", fmt.Errorf("upstream status %d: %s", resp.StatusCode, providerMessage(body))
	}

	var decoded runResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("decoding upstream response: %w", err)
	}
	if decoded.Success != nil && !*decoded.Success {
		return "", fmt.Errorf("upstream error: %s", firstError(decoded.Errors))
	}

	// The binding historically returned "description"; the REST API returns
	// "response". Accept either, including an empty transcription, but never
	// fabricate text from a shape nobody has read.
	switch {
	case decoded.Result.Response != nil:
		return *decoded.Result.Response, nil
	case decoded.Result.Description != nil:
		return *decoded.Result.Description, nil
	default:
		return "", fmt.Errorf("upstream response contained no text")
	}
}

func providerMessage(body []byte) string {
	var decoded runResponse
	if err := json.Unmarshal(body, &decoded); err == nil && len(decoded.Errors) > 0 {
		return firstError(decoded.Errors)
	}
	// Anything else is summarized without echoing a potentially large or
	// credential-bearing body back towards the client.
	if len(body) > 200 {
		body = body[:200]
	}
	return strings.TrimSpace(string(body))
}

func firstError(errs []struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}) string {
	if len(errs) == 0 {
		return "unknown provider error"
	}
	if errs[0].Message != "" {
		return errs[0].Message
	}
	return fmt.Sprintf("provider error code %d", errs[0].Code)
}
