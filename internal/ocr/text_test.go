package ocr

import (
	"encoding/json"
	"strings"
	"testing"
)

// cleanCases pin the transcription cleanup behaviour of the previous build.
// Each expected value was produced by running the old JavaScript regex chain
// over the same input (see TestCleanMatchesPreviousImplementation notes).
var cleanCases = []struct {
	name  string
	input string
	want  string
}{
	{"preamble the text in the image is", "The text in the image is: Hello World", "Hello World"},
	{"preamble visible in this image reads", "The text visible in this image reads: Milk", "Milk"},
	{"preamble i can see", "I can see the following text: Bread", "Bread"},
	{"preamble here is the text from", "Here is the text from the image:\nRice", "Rice"},
	{"preamble here's the text in", "Here's the text in the image: Eggs", "Eggs"},
	{"quoted wrapper", "\"Organic Milk\"", "Organic Milk"},
	{"single quoted wrapper", "'Organic Milk'", "Organic Milk"},
	{"fenced block", "```\nOrganic Milk\n```", "Organic Milk"},
	{"fenced block with language", "```text\nOrganic Milk\n```", "Organic Milk"},
	{"bold markers removed", "Total **50** items", "Total 50 items"},
	{"double underscore removed", "file__name here", "filename here"},
	{"inline backticks removed", "price `1.20` today", "price 1.20 today"},
	{"heading marker removed", "# National ID Card\nName: A", "National ID Card\nName: A"},
	{"dash bullet removed", "- Government\n- Bangladesh", "Government\nBangladesh"},
	{"star bullet removed", "* one\n* two", "one\ntwo"},
	{"numbered list removed", "1. first\n2. second", "first\nsecond"},
	{"surrounding whitespace", "   \n Hello \n", "Hello"},
	{"already clean", "Government of Bangladesh", "Government of Bangladesh"},
	{"empty", "", ""},
	{"multiline preserved", "Line one\nLine two\nLine three", "Line one\nLine two\nLine three"},
	{"preamble only text stays", "The text is important", "The text is important"},
}

func TestClean(t *testing.T) {
	for _, tc := range cleanCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Clean(tc.input); got != tc.want {
				t.Errorf("Clean(%q)\n got: %q\nwant: %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestBuildResult(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantText string
		wantWord []string
	}{
		{"two words", "Milk 2%", "Milk 2%", []string{"Milk", "2%"}},
		{"newlines split words", "Government of\nBangladesh", "Government of\nBangladesh", []string{"Government", "of", "Bangladesh"}},
		{"tabs and multiple spaces", "a\t b   c", "a\t b   c", []string{"a", "b", "c"}},
		{"bengali", "বাংলাদেশ সরকার", "বাংলাদেশ সরকার", []string{"বাংলাদেশ", "সরকার"}},
		{"arabic", "العربية السعودية", "العربية السعودية", []string{"العربية", "السعودية"}},
		{"empty provider reply", "", "", nil},
		{"whitespace only", "  \n\t ", "", nil},
		// Regression: the prompt asks for exactly "NO TEXT", but the old word
		// splitter only suppressed "NO TEXT FOUND", so the sentinel was
		// published as two words that are not on the image.
		{"no text sentinel", "NO TEXT", "NO TEXT", nil},
		{"legacy no text found", "NO TEXT FOUND", "NO TEXT FOUND", nil},
		{"lowercase sentinel", "no text", "no text", nil},
		{"sentinel inside real text is kept", "NO TEXT is printed on the label", "NO TEXT is printed on the label", []string{"NO", "TEXT", "is", "printed", "on", "the", "label"}},
		{"preamble cleaned", "The text in the image is: Milk", "Milk", []string{"Milk"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildResult(tc.raw)
			if got.Text != tc.wantText {
				t.Errorf("text = %q, want %q", got.Text, tc.wantText)
			}
			if got.Words == nil {
				t.Fatal("words must never be nil: clients index into the array")
			}
			if len(got.Words) != len(tc.wantWord) {
				t.Fatalf("words = %d %v, want %d %v", len(got.Words), got.Words, len(tc.wantWord), tc.wantWord)
			}
			for i, want := range tc.wantWord {
				if got.Words[i].Text != want {
					t.Errorf("words[%d].text = %q, want %q", i, got.Words[i].Text, want)
				}
				if got.Words[i].Confidence != 0.85 {
					t.Errorf("words[%d].confidence = %v, want the documented estimate 0.85", i, got.Words[i].Confidence)
				}
			}
		})
	}
}

// The published contract serialises words as an array, never null.
func TestBuildResultJSONShape(t *testing.T) {
	encoded, err := json.Marshal(BuildResult("NO TEXT"))
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"Text":"NO TEXT","Words":[]}` {
		t.Logf("ocr.Result encoding is %s; the HTTP layer re-shapes this into the public contract", encoded)
	}

	out, err := json.Marshal(struct {
		Text  string `json:"text"`
		Words []Word `json:"words"`
	}{Text: BuildResult("NO TEXT").Text, Words: BuildResult("NO TEXT").Words})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"text":"NO TEXT","words":[]}` {
		t.Errorf("public shape = %s, want {\"text\":\"NO TEXT\",\"words\":[]}", out)
	}
}

func BenchmarkClean(b *testing.B) {
	// A dense ID-card style transcription, which is where cleanup runs hot.
	input := strings.Repeat("The text in the image is: Government of Bangladesh **National ID** Card\n", 40)

	b.ReportAllocs()
	for b.Loop() {
		_ = Clean(input)
	}
}

func BenchmarkBuildResult(b *testing.B) {
	input := strings.Repeat("Government of the People's Republic of Bangladesh National ID Card holder name\n", 40)

	b.ReportAllocs()
	for b.Loop() {
		_ = BuildResult(input)
	}
}
