package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthand-studio/fasthand-ocr/internal/config"
	"github.com/fasthand-studio/fasthand-ocr/internal/ocr"
	"github.com/fasthand-studio/fasthand-ocr/web"
)

const testKey = "test-secret-api-key"

// fakeProvider records what the handler asked for and replies from fn.
type fakeProvider struct {
	fn    func(context.Context, *ocr.Image) (string, error)
	mu    sync.Mutex
	calls int
	last  ocr.Image
}

func (f *fakeProvider) Recognize(ctx context.Context, img *ocr.Image) (string, error) {
	f.mu.Lock()
	f.calls++
	f.last = *img
	f.last.Data = append([]byte(nil), img.Data...)
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(ctx, img)
	}
	return "Recognized text", nil
}

func (f *fakeProvider) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func ok(text string) func(context.Context, *ocr.Image) (string, error) {
	return func(context.Context, *ocr.Image) (string, error) { return text, nil }
}

func newServer(t *testing.T, provider ocr.Provider, tweak func(*config.Config)) *httptest.Server {
	t.Helper()
	cfg := config.Config{
		APIKey:                testKey,
		Model:                 config.DefaultModel,
		UpstreamTimeout:       time.Second,
		RequestTimeout:        5 * time.Second,
		MaxConcurrentOCR:      128,
		MaxConcurrentRequests: 128,
	}
	if tweak != nil {
		tweak(&cfg)
	}
	srv := httptest.NewServer(New(provider, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	t.Cleanup(srv.Close)
	return srv
}

// Real signature prefixes are what the handler inspects; the payload after the
// header is not parsed by this service.
var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x01}, 64)...)
	jpegBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0x02}, 64)...)
	webpBytes = append(append([]byte("RIFF"), 0, 0, 0, 0), append([]byte("WEBP"), bytes.Repeat([]byte{0x03}, 64)...)...)
)

func post(t *testing.T, url string, key string, body any) *http.Response {
	t.Helper()
	var payload io.Reader
	switch b := body.(type) {
	case string:
		payload = strings.NewReader(b)
	case nil:
		payload = strings.NewReader("")
	default:
		encoded, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(http.MethodPost, url, payload)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode body %q: %v", raw, err)
	}
	return out
}

func TestOCRSuccess(t *testing.T) {
	for _, tc := range []struct {
		name     string
		image    any
		want     []byte
		wantMIME string
	}{
		{"png", base64.StdEncoding.EncodeToString(pngBytes), pngBytes, "image/png"},
		{"jpeg", base64.StdEncoding.EncodeToString(jpegBytes), jpegBytes, "image/jpeg"},
		{"webp", base64.StdEncoding.EncodeToString(webpBytes), webpBytes, "image/webp"},
		{"data url png", "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes), pngBytes, "image/png"},
		{"data url jpeg", "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpegBytes), jpegBytes, "image/jpeg"},
		{"data url webp", "data:image/webp;base64," + base64.StdEncoding.EncodeToString(webpBytes), webpBytes, "image/webp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &fakeProvider{fn: ok("Organic Whole Milk")}
			srv := newServer(t, provider, nil)

			resp := post(t, srv.URL, testKey, map[string]any{"image": tc.image})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			body := decodeBody(t, resp)
			if body["success"] != true {
				t.Errorf("success = %v, want true", body["success"])
			}
			if body["text"] != "Organic Whole Milk" {
				t.Errorf("text = %v", body["text"])
			}
			words, _ := body["words"].([]any)
			if len(words) != 3 {
				t.Fatalf("words = %v, want 3 entries", body["words"])
			}
			first, _ := words[0].(map[string]any)
			if first["text"] != "Organic" || first["confidence"] != 0.85 {
				t.Errorf("words[0] = %v", first)
			}
			if got := resp.Header.Get("Content-Type"); got != contentTypeJSON {
				t.Errorf("Content-Type = %q", got)
			}

			// The provider must receive the exact decoded bytes, not a
			// re-encoded or truncated form of them.
			if provider.count() != 1 {
				t.Fatalf("provider calls = %d, want 1", provider.count())
			}
			if !bytes.Equal(provider.last.Data, tc.want) {
				t.Errorf("provider got %d bytes, want the %d decoded bytes", len(provider.last.Data), len(tc.want))
			}
			if provider.last.MIME != tc.wantMIME {
				t.Errorf("provider MIME = %q, want %q", provider.last.MIME, tc.wantMIME)
			}
		})
	}
}

func TestAuth(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		cfgKey   string
		wantStat int
		wantMsg  string
	}{
		{"missing key", "", testKey, http.StatusUnauthorized, msgUnauthorized},
		{"wrong key", "definitely-not-the-key", testKey, http.StatusUnauthorized, msgUnauthorized},
		{"same length wrong key", strings.Repeat("a", len(testKey)), testKey, http.StatusUnauthorized, msgUnauthorized},
		{"server has no key", testKey, "", http.StatusServiceUnavailable, msgMisconfigured},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := &fakeProvider{}
			srv := newServer(t, provider, func(cfg *config.Config) { cfg.APIKey = tc.cfgKey })

			resp := post(t, srv.URL, tc.key, map[string]any{
				"image": base64.StdEncoding.EncodeToString(pngBytes),
			})
			if resp.StatusCode != tc.wantStat {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStat)
			}
			body := decodeBody(t, resp)
			if body["error"] != tc.wantMsg {
				t.Errorf("error = %v, want %q", body["error"], tc.wantMsg)
			}
			if body["success"] != false {
				t.Errorf("success = %v, want false", body["success"])
			}
			// Authentication and configuration failures must never reach the
			// paid inference path.
			if provider.count() != 0 {
				t.Errorf("provider called %d times, want 0", provider.count())
			}
		})
	}
}

func TestRequestValidation(t *testing.T) {
	image := base64.StdEncoding.EncodeToString(pngBytes)

	tests := []struct {
		name     string
		body     any
		wantStat int
		wantMsg  string
	}{
		{"empty body", "", http.StatusBadRequest, msgBadJSON},
		{"truncated json", `{"image":`, http.StatusBadRequest, msgBadJSON},
		{"not json at all", "hello", http.StatusBadRequest, msgBadJSON},
		{"trailing garbage", `{} extra`, http.StatusBadRequest, msgBadJSON},
		// Well-formed JSON that simply is not an object has no image field. The
		// previous build answered "No image provided" for these, and a valid-JSON
		// body should not be reported as a parse failure.
		{"json array", `["a"]`, http.StatusBadRequest, msgNoImage},
		{"json string", `"just a string"`, http.StatusBadRequest, msgNoImage},
		{"json number", `42`, http.StatusBadRequest, msgNoImage},
		{"json boolean", `true`, http.StatusBadRequest, msgNoImage},
		{"json null", `null`, http.StatusBadRequest, msgNoImage},
		{"missing image field", map[string]any{}, http.StatusBadRequest, msgNoImage},
		{"null image", map[string]any{"image": nil}, http.StatusBadRequest, msgNoImage},
		{"empty string image", map[string]any{"image": ""}, http.StatusBadRequest, msgNoImage},
		{"numeric image", map[string]any{"image": 12345}, http.StatusBadRequest, msgNoImage},
		{"object image", map[string]any{"image": map[string]any{"a": "b"}}, http.StatusBadRequest, msgNoImage},
		{"array image", map[string]any{"image": []string{image}}, http.StatusBadRequest, msgNoImage},
		{"malformed base64 chars", map[string]any{"image": "!!!!not base64!!!!"}, http.StatusBadRequest, msgBadBase64},
		{"base64 wrong padding", map[string]any{"image": "AAAA="}, http.StatusBadRequest, msgBadBase64},
		{"base64 unpadded", map[string]any{"image": "QUJDRA"}, http.StatusBadRequest, msgBadBase64},
		{"base64 with newline", map[string]any{"image": image[:20] + "\n" + image[20:]}, http.StatusBadRequest, msgBadBase64},
		{"base64 with spaces", map[string]any{"image": image[:20] + " " + image[20:]}, http.StatusBadRequest, msgBadBase64},
		{"base64 padding midstream", map[string]any{"image": "AA==AAAA"}, http.StatusBadRequest, msgBadBase64},
		{"data url with no payload", map[string]any{"image": "data:image/png;base64,"}, http.StatusBadRequest, msgBadBase64},
		{"uppercase data url", map[string]any{"image": "DATA:image/png;base64," + image}, http.StatusBadRequest, msgBadBase64},
		{"plain text bytes", map[string]any{"image": base64.StdEncoding.EncodeToString([]byte("this is not an image at all"))}, http.StatusBadRequest, msgUnsupported},
		{"gif bytes", map[string]any{"image": base64.StdEncoding.EncodeToString(append([]byte("GIF89a"), bytes.Repeat([]byte{1}, 32)...))}, http.StatusBadRequest, msgUnsupported},
		{"shorter than a signature", map[string]any{"image": base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8})}, http.StatusBadRequest, msgUnsupported},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := &fakeProvider{}
			srv := newServer(t, provider, nil)

			resp := post(t, srv.URL, testKey, tc.body)
			if resp.StatusCode != tc.wantStat {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStat)
			}
			body := decodeBody(t, resp)
			if body["error"] != tc.wantMsg {
				t.Errorf("error = %v, want %q", body["error"], tc.wantMsg)
			}
			if provider.count() != 0 {
				t.Errorf("provider called %d times for an invalid request, want 0", provider.count())
			}
		})
	}
}

// Malformed input must not leak the reason for failure the way the previous
// build did by returning error.Error() to the client.
func TestInternalErrorsAreNotLeaked(t *testing.T) {
	provider := &fakeProvider{fn: func(context.Context, *ocr.Image) (string, error) {
		return "", errors.New("dial api.cloudflare.com:443: token sk-supersecret-value expired")
	}}
	srv := newServer(t, provider, nil)

	resp := post(t, srv.URL, testKey, map[string]any{"image": base64.StdEncoding.EncodeToString(pngBytes)})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	for _, leak := range []string{"sk-supersecret", "cloudflare", "token", "dial"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("response leaks %q: %s", leak, raw)
		}
	}
	body := decodeBody(t, &http.Response{Body: io.NopCloser(bytes.NewReader(raw)), Header: resp.Header})
	if body["error"] != msgInternal {
		t.Errorf("error = %v, want %q", body["error"], msgInternal)
	}
}

// A cancelled or expired request must reach the provider context so the
// upstream call stops instead of running on after the client is gone.
func TestUpstreamCancellationPropagates(t *testing.T) {
	ctxErr := make(chan error, 1)
	provider := &fakeProvider{fn: func(ctx context.Context, _ *ocr.Image) (string, error) {
		<-ctx.Done()
		ctxErr <- ctx.Err()
		return "", ctx.Err()
	}}
	srv := newServer(t, provider, func(cfg *config.Config) { cfg.RequestTimeout = 80 * time.Millisecond })

	resp := post(t, srv.URL, testKey, map[string]any{"image": base64.StdEncoding.EncodeToString(pngBytes)})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}

	select {
	case err := <-ctxErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("provider saw %v, want deadline exceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider context was never cancelled")
	}
}

func TestOversizedBodyRejected(t *testing.T) {
	// A base64 image that makes the JSON body exceed the 8 MB cap.
	big := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x5A}, (MaxBodyBytes/3*4)-1024))

	provider := &fakeProvider{}
	srv := newServer(t, provider, nil)

	resp := post(t, srv.URL, testKey, `{"image":"`+big+`"}`)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["error"] != msgBodyTooLarge {
		t.Errorf("error = %v, want %q", body["error"], msgBodyTooLarge)
	}
	if provider.count() != 0 {
		t.Errorf("provider called %d times, want 0", provider.count())
	}
}

// unknownSizedBody hides the length from the transport so the request goes out
// chunked, with no Content-Length header at all.
type unknownSizedBody struct{ r *strings.Reader }

func (u *unknownSizedBody) Read(p []byte) (int, error) { return u.r.Read(p) }

// A missing Content-Length must not bypass the limit. The old build read the
// header, treated "absent" as zero, and then parsed the whole oversized body.
func TestBodyWithoutContentLengthIsStillCapped(t *testing.T) {
	provider := &fakeProvider{}
	srv := newServer(t, provider, nil)

	payload := `{"image":"` + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x5A}, MaxBodyBytes)) + `"}`
	req, err := http.NewRequest(http.MethodPost, srv.URL, &unknownSizedBody{r: strings.NewReader(payload)})
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if req.ContentLength != 0 {
		t.Fatalf("test is not chunked: ContentLength = %d", req.ContentLength)
	}
	req.Header.Set("X-API-Key", testKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	if provider.count() != 0 {
		t.Errorf("provider called %d times, want 0", provider.count())
	}
}

func TestConcurrencyIsBounded(t *testing.T) {
	const limit = 1

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	peak, live := 0, 0

	provider := &fakeProvider{fn: func(context.Context, *ocr.Image) (string, error) {
		mu.Lock()
		live++
		if live > peak {
			peak = live
		}
		mu.Unlock()

		once.Do(func() { close(entered) })
		<-release

		mu.Lock()
		live--
		mu.Unlock()
		return "text", nil
	}}
	srv := newServer(t, provider, func(cfg *config.Config) { cfg.MaxConcurrentOCR = limit })

	body := map[string]any{"image": base64.StdEncoding.EncodeToString(pngBytes)}
	first := make(chan *http.Response, 1)
	go func() { first <- post(t, srv.URL, testKey, body) }()

	// Wait until the first request is inside the provider and holding a slot.
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider never started the first request")
	}

	// Bounded so a regression that stops refusing shows up as a failure rather
	// than as a test that waits for the suite timeout.
	overflowCh := make(chan *http.Response, 1)
	go func() { overflowCh <- post(t, srv.URL, testKey, body) }()
	var overflow *http.Response
	select {
	case overflow = <-overflowCh:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("second request was not refused: the inference limit is not enforced")
	}
	if overflow.StatusCode != http.StatusTooManyRequests {
		t.Errorf("overflow status = %d, want 429", overflow.StatusCode)
	}
	if got := overflow.Header.Get("Retry-After"); got == "" {
		t.Error("429 is missing Retry-After")
	}
	overflowBody := decodeBody(t, overflow)
	if overflowBody["error"] != msgBusy {
		t.Errorf("overflow error = %v, want %q", overflowBody["error"], msgBusy)
	}

	close(release)
	done := make(chan *http.Response, 1)
	go func() { done <- <-first }()

	var okResp *http.Response
	select {
	case okResp = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("first request never completed")
	}
	if okResp.StatusCode != http.StatusOK {
		t.Errorf("first status = %d, want 200", okResp.StatusCode)
	}

	// The slot must be returned even though a request was refused.
	after := post(t, srv.URL, testKey, body)
	if after.StatusCode != http.StatusOK {
		t.Errorf("status after release = %d, want 200 (slots leaked)", after.StatusCode)
	}

	mu.Lock()
	got := peak
	mu.Unlock()
	if got > limit {
		t.Errorf("peak concurrency = %d, want <= %d", got, limit)
	}
}

// Slots must be returned even when the provider fails.
func TestSlotsReleasedAfterFailure(t *testing.T) {
	provider := &fakeProvider{fn: func(context.Context, *ocr.Image) (string, error) {
		return "", errors.New("boom")
	}}
	srv := newServer(t, provider, func(cfg *config.Config) { cfg.MaxConcurrentOCR = 1 })

	for i := 0; i < 3; i++ {
		resp := post(t, srv.URL, testKey, map[string]any{"image": base64.StdEncoding.EncodeToString(pngBytes)})
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("request %d: status = %d, want 500", i, resp.StatusCode)
		}
	}
}

// The request permit spans the whole handler, so buffered bodies cannot pile up
// past MAX_CONCURRENT_REQUESTS even while inference slots are still free. This
// is the memory bound: the legacy build had none.
func TestConcurrentRequestsAreBounded(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	provider := &fakeProvider{fn: func(context.Context, *ocr.Image) (string, error) {
		once.Do(func() { close(entered) })
		<-release
		return "text", nil
	}}
	srv := newServer(t, provider, func(cfg *config.Config) {
		cfg.MaxConcurrentRequests = 1
		cfg.MaxConcurrentOCR = 8 // inference must not be the limit under test
	})

	body := map[string]any{"image": base64.StdEncoding.EncodeToString(pngBytes)}
	first := make(chan *http.Response, 1)
	go func() { first <- post(t, srv.URL, testKey, body) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider never started the first request")
	}

	overflow := make(chan *http.Response, 1)
	go func() { overflow <- post(t, srv.URL, testKey, body) }()
	var refused *http.Response
	select {
	case refused = <-overflow:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("second request was neither refused nor served: the request bound is not enforced")
	}
	if refused.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 while the request permit is held", refused.StatusCode)
	}
	if refused.Header.Get("Retry-After") == "" {
		t.Error("429 is missing Retry-After")
	}

	// The documentation page buffers nothing, so it must stay available while
	// the OCR path is saturated.
	docs, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Body.Close()
	if docs.StatusCode != http.StatusOK {
		t.Errorf("docs status = %d, want 200 while OCR is saturated", docs.StatusCode)
	}

	close(release)
	resp := <-first
	if resp.StatusCode != http.StatusOK {
		t.Errorf("first status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	after := post(t, srv.URL, testKey, body)
	if after.StatusCode != http.StatusOK {
		t.Errorf("status after release = %d, want 200 (request permit leaked)", after.StatusCode)
	}
	after.Body.Close()
}

func TestRouting(t *testing.T) {
	provider := &fakeProvider{}
	srv := newServer(t, provider, nil)

	t.Run("unknown get path", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/nope")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})

	// Regression: the previous build ran a paid OCR request for POST on any
	// path, so a typo such as /ocr was billed like a real call.
	t.Run("unknown post path", func(t *testing.T) {
		for _, path := range []string{"/ocr", "/v1/ocr", "/OCR", "/extra"} {
			req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(`{"image":"AAAA"}`))
			req.Header.Set("X-API-Key", testKey)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s: status = %d (%s), want 404", path, resp.StatusCode, raw)
			}
		}
	})

	t.Run("put on root is 405", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/", strings.NewReader("{}"))
		req.Header.Set("X-API-Key", testKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", resp.StatusCode)
		}
		if string(raw) != methodNotAllowed {
			t.Errorf("body = %q, want %q", raw, methodNotAllowed)
		}
		if got := resp.Header.Get("Allow"); got != allowMethods {
			t.Errorf("Allow = %q, want %q", got, allowMethods)
		}
	})

	t.Run("delete on root is 405", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/", nil)
		req.Header.Set("X-API-Key", testKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", resp.StatusCode)
		}
	})

	if provider.count() != 0 {
		t.Errorf("routing probes reached the provider %d times, want 0", provider.count())
	}
}

func TestPreflight(t *testing.T) {
	srv := newServer(t, &fakeProvider{}, nil)

	for _, path := range []string{"/", "/anything"} {
		req, _ := http.NewRequest(http.MethodOptions, srv.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("%s: status = %d, want 204", path, resp.StatusCode)
		}
		if got := resp.Body; got == nil {
			t.Error("preflight body should be empty")
		}
		for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers", "Access-Control-Max-Age"} {
			if resp.Header.Get(h) == "" {
				t.Errorf("%s: missing %s", path, h)
			}
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv := newServer(t, &fakeProvider{fn: ok("text")}, nil)

	cases := map[string]*http.Response{
		"ocr":      post(t, srv.URL, testKey, map[string]any{"image": base64.StdEncoding.EncodeToString(pngBytes)}),
		"reject":   post(t, srv.URL, "wrong", nil),
		"prefight": func() *http.Response { r, _ := http.DefaultClient.Do(mustOptions(srv.URL + "/")); return r }(),
		"docs":     func() *http.Response { r, _ := http.Get(srv.URL + "/"); return r }(),
	}

	for name, resp := range cases {
		if resp == nil {
			t.Fatalf("%s: no response", name)
		}
		for _, kv := range baseHeaders {
			if got := resp.Header.Get(kv[0]); got != kv[1] {
				t.Errorf("%s: %s = %q, want %q", name, kv[0], got, kv[1])
			}
		}
	}

	docs := cases["docs"]
	if got := docs.Header.Get("Content-Security-Policy"); got != docsCSP {
		t.Errorf("docs CSP = %q, want %q", got, docsCSP)
	}
	if got := docs.Header.Get("Content-Type"); got != contentTypeDocs {
		t.Errorf("docs Content-Type = %q, want %q", got, contentTypeDocs)
	}
}

func mustOptions(url string) *http.Request {
	req, _ := http.NewRequest(http.MethodOptions, url, nil)
	return req
}
func TestDocsServedUnchanged(t *testing.T) {
	srv := newServer(t, &fakeProvider{}, nil)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !bytes.Equal(body, web.Docs) {
		t.Errorf("docs body differs from the embedded page (%d vs %d bytes)", len(body), len(web.Docs))
	}
	for _, needle := range []string{"Fasthand OCR", "X-API-Key", "Try it"} {
		if !strings.Contains(string(body), needle) {
			t.Errorf("docs page is missing %q", needle)
		}
	}
}

func TestEmptyAndNoTextResults(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		wantText string
		wantLen  int
	}{
		{"no text sentinel", "NO TEXT", "NO TEXT", 0},
		{"legacy sentinel", "NO TEXT FOUND", "NO TEXT FOUND", 0},
		{"lowercase sentinel", "no text", "no text", 0},
		{"provider returned nothing", "", "", 0},
		{"whitespace only", "  \n\t ", "", 0},
		{"one word", "Receipt", "Receipt", 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, &fakeProvider{fn: ok(tc.provider)}, nil)
			resp := post(t, srv.URL, testKey, map[string]any{"image": base64.StdEncoding.EncodeToString(pngBytes)})
			body := decodeBody(t, resp)

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if body["text"] != tc.wantText {
				t.Errorf("text = %q, want %q", body["text"], tc.wantText)
			}
			// words must always be present and never null: clients index into it.
			words, isArray := body["words"].([]any)
			if !isArray {
				t.Fatalf("words = %#v, want an array", body["words"])
			}
			if len(words) != tc.wantLen {
				t.Errorf("words = %v, want %d entries", words, tc.wantLen)
			}
		})
	}
}

func TestResponseShapeMatchesContract(t *testing.T) {
	srv := newServer(t, &fakeProvider{fn: ok("Government of Bangladesh\nNational ID Card")}, nil)
	resp := post(t, srv.URL, testKey, map[string]any{"image": base64.StdEncoding.EncodeToString(pngBytes)})

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"success":true,"text":"Government of Bangladesh\nNational ID Card","words":[{"text":"Government","confidence":0.85},{"text":"of","confidence":0.85},{"text":"Bangladesh","confidence":0.85},{"text":"National","confidence":0.85},{"text":"ID","confidence":0.85},{"text":"Card","confidence":0.85}]}`
	if string(raw) != want {
		t.Errorf("body mismatch\n got: %s\nwant: %s", raw, want)
	}
}

func ExampleServer() {
	provider := &fakeProvider{fn: ok("Milk 2%")}
	cfg := config.Config{APIKey: testKey, MaxConcurrentOCR: 4, MaxConcurrentRequests: 4, RequestTimeout: time.Second, UpstreamTimeout: time.Second}
	srv := httptest.NewServer(New(provider, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(fmt.Sprintf(`{"image":%q}`, base64.StdEncoding.EncodeToString(pngBytes))))
	req.Header.Set("X-API-Key", testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Println(resp.StatusCode, string(body))
	// Output: 200 {"success":true,"text":"Milk 2%","words":[{"text":"Milk","confidence":0.85},{"text":"2%","confidence":0.85}]}
}
