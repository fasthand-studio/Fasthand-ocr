package ocr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testAccount = "acc-12345678"
	testToken   = "cf-token-secret"
	testModel   = "@cf/meta/llama-3.2-11b-vision-instruct"
)

// upstream records what the provider sent so the request shape can be checked.
type upstream struct {
	path    string
	auth    string
	ctype   string
	payload runRequest
}

func newUpstream(t *testing.T, reply func(w http.ResponseWriter, r *http.Request, body string)) (*WorkerAI, *upstream) {
	t.Helper()
	rec := &upstream{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.path = r.URL.Path
		rec.auth = r.Header.Get("Authorization")
		rec.ctype = r.Header.Get("Content-Type")
		_ = json.Unmarshal(body, &rec.payload)
		reply(w, r, string(body))
	}))
	t.Cleanup(srv.Close)

	return NewWorkerAI(srv.URL, testAccount, testToken, testModel, 2*time.Second), rec
}

func jsonReply(text string) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, _ *http.Request, _ string) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  map[string]any{"response": text},
		})
	}
}

func TestRecognizeRequestShape(t *testing.T) {
	provider, rec := newUpstream(t, jsonReply("Milk 2%"))

	data := []byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3}
	got, err := provider.Recognize(context.Background(), &Image{Data: data, MIME: "image/jpeg"})
	if err != nil {
		t.Fatalf("Recognize: %v", err)
	}
	if got != "Milk 2%" {
		t.Errorf("text = %q, want %q", got, "Milk 2%")
	}

	wantPath := "/client/v4/accounts/" + testAccount + "/ai/run/" + testModel
	if rec.path != wantPath {
		t.Errorf("path = %q, want %q", rec.path, wantPath)
	}
	if rec.auth != "Bearer "+testToken {
		t.Errorf("Authorization = %q, want a bearer token", rec.auth)
	}
	if rec.ctype != "application/json" {
		t.Errorf("Content-Type = %q", rec.ctype)
	}

	// The image must travel as base64. The previous build sent an array of
	// every byte value, which turned a 4 MB image into a 14 MB JSON document.
	if rec.payload.Image != base64.StdEncoding.EncodeToString(data) {
		t.Errorf("image field = %q, want base64 of the bytes", rec.payload.Image)
	}
	if strings.HasPrefix(rec.payload.Image, "[") {
		t.Error("image field looks like a byte array")
	}
	if rec.payload.Prompt != Prompt {
		t.Error("prompt sent to the model differs from the OCR prompt")
	}
	if rec.payload.MaxTokens != DefaultMaxTokens {
		t.Errorf("max_tokens = %d, want %d", rec.payload.MaxTokens, DefaultMaxTokens)
	}
}

func TestRecognizeResponseShapes(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantText string
		wantErr  bool
	}{
		{"response field", 200, `{"success":true,"result":{"response":"Hello"}}`, "Hello", false},
		{"description field", 200, `{"success":true,"result":{"description":"Legacy"}}`, "Legacy", false},
		{"response wins over description", 200, `{"success":true,"result":{"response":"A","description":"B"}}`, "A", false},
		{"empty transcription is a success", 200, `{"success":true,"result":{"response":""}}`, "", false},
		{"no usable field", 200, `{"success":true,"result":{"usage":{"prompt_tokens":1}}}`, "", true},
		{"missing result", 200, `{"success":true}`, "", true},
		{"provider failure envelope", 200, `{"success":false,"errors":[{"code":5004293,"message":"Too many requests"}]}`, "", true},
		{"http 500 html body", 500, `<html>gateway exploded</html>`, "", true},
		{"http 401 with token echo", 401, `{"success":false,"errors":[{"code":10000,"message":"Invalid API token"}]}`, "", true},
		{"not json", 200, `plain text reply`, "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider, _ := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ string) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})

			text, err := provider.Recognize(context.Background(), &Image{Data: pngBytes(), MIME: "image/png"})
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got text %q", text)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if text != tc.wantText {
				t.Errorf("text = %q, want %q", text, tc.wantText)
			}
		})
	}
}

// An unknown reply shape must be an error. The previous build serialised the
// whole object into the text field, which publishes raw provider internals as
// though the model had spoken them.
func TestRecognizeNeverFabricatesText(t *testing.T) {
	provider, _ := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ string) {
		_, _ = io.WriteString(w, `{"success":true,"result":{"unexpected":{"a":1}}}`)
	})

	text, err := provider.Recognize(context.Background(), &Image{Data: pngBytes(), MIME: "image/png"})
	if err == nil {
		t.Fatalf("expected error, got %q", text)
	}
	if strings.Contains(text, "unexpected") || text != "" {
		t.Errorf("text = %q, want empty on failure", text)
	}
}

func TestRecognizeErrorMessagesStayInternal(t *testing.T) {
	provider, _ := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ string) {
		w.WriteHeader(500)
		_, _ = io.WriteString(w, "Bearer "+testToken+" is invalid")
	})

	_, err := provider.Recognize(context.Background(), &Image{Data: pngBytes(), MIME: "image/png"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "upstream status 500") {
		t.Errorf("err = %v, want the status to be recorded", err)
	}
	// The message is for logs, and a very long body must not be carried along.
	if len(err.Error()) > 400 {
		t.Errorf("error message is %d chars, want it bounded", len(err.Error()))
	}
}

func TestRecognizeHonoursCancellation(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	provider, _ := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		_, _ = io.WriteString(w, `{"success":true,"result":{"response":"too late"}}`)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already gone: the client hung up

	start := time.Now()
	_, err := provider.Recognize(ctx, &Image{Data: pngBytes(), MIME: "image/png"})
	if err == nil {
		t.Fatal("expected an error after cancellation")
	}
	if time.Since(start) > time.Second {
		t.Errorf("Recognize took %v after cancellation, want it to return promptly", time.Since(start))
	}
}

func TestRecognizeUpstreamTimeout(t *testing.T) {
	provider, _ := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
		_, _ = io.WriteString(w, `{"success":true,"result":{"response":"late"}}`)
	})

	// Rebuild with a short client timeout against the same test server.
	provider.client.Timeout = 100 * time.Millisecond

	start := time.Now()
	_, err := provider.Recognize(context.Background(), &Image{Data: pngBytes(), MIME: "image/png"})
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("timed out after %v, want ~100ms", elapsed)
	}
}

func TestRecognizeConnectionFailure(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()

	provider := NewWorkerAI(url, testAccount, testToken, testModel, time.Second)
	if _, err := provider.Recognize(context.Background(), &Image{Data: pngBytes(), MIME: "image/png"}); err == nil {
		t.Fatal("expected an error for an unreachable provider")
	}
}

// One provider instance must reuse its transport across calls.
func TestProviderReusesConnections(t *testing.T) {
	var conns []string
	provider, _ := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		conns = append(conns, r.RemoteAddr)
		_, _ = io.WriteString(w, `{"success":true,"result":{"response":"ok"}}`)
	})

	for i := 0; i < 5; i++ {
		if _, err := provider.Recognize(context.Background(), &Image{Data: pngBytes(), MIME: "image/png"}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if len(conns) != 5 {
		t.Fatalf("got %d replies, want 5", len(conns))
	}
	// A reused keep-alive connection reports the same peer address.
	for _, c := range conns[1:] {
		if c != conns[0] {
			t.Errorf("connection changed between calls: %s vs %s", c, conns[0])
		}
	}
}

func pngBytes() []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), []byte("0123456789")...)
}

// The prompt is the product: transcription quality depends on its exact
// wording. It was copied out of the deployed Worker, so pin it against that
// string instead of against the Go constant, which any edit satisfies by
// definition.
func TestPromptIsTheDeployedWorkerString(t *testing.T) {
	const deployed = `TRANSCRIBE ALL TEXT VISIBLE IN THIS IMAGE, CHARACTER FOR CHARACTER, IN ITS ORIGINAL LAYOUT AND ORDER. Include every label, heading, name, number, line, and field value exactly as written, preserving case and special characters. Do NOT describe the image, do NOT summarize, do NOT add commentary or bullets. Output only the raw text. If no text is legible, output exactly: NO TEXT`
	if Prompt != deployed {
		t.Errorf("prompt drifted from the Worker\n got: %s\nwant: %s", Prompt, deployed)
	}
}

// A rejected token must read differently from a model fault, because it is the
// one upstream failure an operator can fix, and the status alone does not say so.
func TestRecognizeNamesCredentialFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		provider, _ := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ string) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"success":false,"errors":[{"code":10000,"message":"Invalid API token"}]}`)
		})
		_, err := provider.Recognize(context.Background(), &Image{Data: pngBytes(), MIME: "image/png"})
		if err == nil {
			t.Fatalf("status %d: want an error", status)
		}
		for _, want := range []string{"credentials", "CF_API_TOKEN", "Invalid API token"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("status %d: error %q does not mention %q", status, err, want)
			}
		}
		if strings.Contains(err.Error(), testToken) {
			t.Errorf("error echoed the token: %v", err)
		}
	}
}

func TestRecognizeOversizedUpstreamResponse(t *testing.T) {
	provider, _ := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ string) {
		_, _ = io.WriteString(w, `{"success":true,"result":{"response":"`)
		_, _ = io.WriteString(w, strings.Repeat("x", maxUpstreamResponse))
		_, _ = io.WriteString(w, `"}}`)
	})
	_, err := provider.Recognize(context.Background(), &Image{Data: pngBytes(), MIME: "image/png"})
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v, want a truncation error, not a JSON parse failure", err)
	}
}

// The cap must not bite a reply that fits: a dense full-page transcription is
// legitimately large.
func TestRecognizeLargeButWithinCap(t *testing.T) {
	text := strings.Repeat("transcribed ", (maxUpstreamResponse/12)-8)
	provider, _ := newUpstream(t, jsonReply(text))

	got, err := provider.Recognize(context.Background(), &Image{Data: pngBytes(), MIME: "image/png"})
	if err != nil {
		t.Fatalf("Recognize: %v", err)
	}
	if len(got) != len(text) {
		t.Errorf("got %d bytes, want %d", len(got), len(text))
	}
}

func BenchmarkRecognizePayload(b *testing.B) {
	// Local cost of building the upstream request for a 1 MB image; network
	// time and model inference are deliberately excluded from this number.
	image := make([]byte, 1024*1024)
	for i := range image {
		image[i] = byte(i % 251)
	}
	req := runRequest{
		Image:     base64.StdEncoding.EncodeToString(image),
		Prompt:    Prompt,
		MaxTokens: DefaultMaxTokens,
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(req); err != nil {
			b.Fatal(err)
		}
	}
}
