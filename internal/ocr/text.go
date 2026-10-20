package ocr

import (
	"regexp"
	"strings"
	"unicode"
)

// estimatedConfidence is the value published for every word. Workers AI does
// not report per-word confidence for its vision models, so this is an estimate
// and is documented as such; it is not a model measurement.
const estimatedConfidence = 0.85

// cleanSteps removes LLM preamble and markdown artifacts in the same order as
// the previous implementation, so transcriptions stay byte-compatible.
var cleanSteps = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)^The text (?:visible )?in (?:the|this) image (?:is|says|reads):?\s*`), ""},
	{regexp.MustCompile(`(?i)^I (?:can )?see the following text:?\s*`), ""},
	{regexp.MustCompile(`(?i)^(?:Here is |Here's )?the text (?:from|in) the image:?\s*`), ""},
	{regexp.MustCompile(`(?s)^["'](.+)["']$`), "$1"},
	{regexp.MustCompile("(?i)^\\s*```[a-z]*\\s*\\n?"), ""},
	{regexp.MustCompile("\\s*```\\s*$"), ""},
	{regexp.MustCompile("(\\*\\*|__|`)"), ""},
	{regexp.MustCompile(`(?m)^#{1,6}\s*`), ""},
	{regexp.MustCompile(`(?m)^\s*[-*]\s+`), ""},
	{regexp.MustCompile(`(?m)^\s{0,3}\d+\.\s+`), ""},
}

// Clean strips the model's formatting habits from a transcription.
func Clean(text string) string {
	for _, step := range cleanSteps {
		text = step.re.ReplaceAllString(text, step.repl)
	}
	return strings.TrimSpace(text)
}

// noText answers are what the model returns when it cannot read anything.
// The prompt asks for exactly "NO TEXT"; older clients also saw "NO TEXT FOUND".
func isNoText(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "no text", "no text found":
		return true
	}
	return false
}

// BuildResult cleans a raw model reply and splits it into words.
func BuildResult(raw string) Result {
	text := Clean(raw)
	if text == "" || isNoText(text) {
		// A "no text" answer is transcribed literally for compatibility, but it
		// is not a page of words: the previous implementation split it into
		// {"NO"}, {"TEXT"}, reporting tokens that are not on the image at all.
		return Result{Text: text, Words: []Word{}}
	}

	words := strings.FieldsFunc(text, unicode.IsSpace)
	result := make([]Word, len(words))
	for i, w := range words {
		result[i] = Word{Text: w, Confidence: estimatedConfidence}
	}
	return Result{Text: text, Words: result}
}
