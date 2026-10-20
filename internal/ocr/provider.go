// Package ocr turns image bytes into transcribed text.
package ocr

import "context"

// Image is a decoded image ready for recognition.
type Image struct {
	Data []byte
	MIME string
}

// Word is one whitespace-separated token of the transcription.
//
// Confidence is an estimate, not a model output: Workers AI does not report
// per-word confidence for the vision models, so every word carries the same
// conservative value. It is kept because the published API contract exposes it.
type Word struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// Result is the OCR outcome for a successful recognition.
type Result struct {
	Text  string
	Words []Word
}

// Provider performs recognition. Implementations must honour ctx cancellation
// and are safe for concurrent use.
type Provider interface {
	Recognize(ctx context.Context, img *Image) (string, error)
}

// Prompt asks the vision model for a literal transcription. Kept verbatim from
// the previous implementation because model output quality depends on it.
const Prompt = "TRANSCRIBE ALL TEXT VISIBLE IN THIS IMAGE, CHARACTER FOR CHARACTER, IN ITS ORIGINAL LAYOUT AND ORDER. " +
	"Include every label, heading, name, number, line, and field value exactly as written, preserving case and special characters. " +
	"Do NOT describe the image, do NOT summarize, do NOT add commentary or bullets. " +
	"Output only the raw text. If no text is legible, output exactly: NO TEXT"
