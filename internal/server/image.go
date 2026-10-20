package server

import (
	"encoding/base64"
	"errors"
	"io"
	"regexp"
	"strings"
)

// Decoding failures are mapped to the client-facing messages in server.go.
var (
	errDecodeBase64    = errors.New("malformed base64")
	errDecodedTooLarge = errors.New("decoded image over limit")
)

// dataURLPrefix matches a leading "data:image/<type>;base64,".
var dataURLPrefix = regexp.MustCompile(`^data:image/[^;,]+;base64,`)

// imageFromBase64 decodes a strict base64 image that may carry a data-URL
// prefix. Padding, alphabet and line breaks all follow the previous strict
// validator, and decoding happens once: the old build validated with a regex,
// probed with atob and then decoded a third time.
func imageFromBase64(value string) ([]byte, error) {
	encoded := dataURLPrefix.ReplaceAllString(value, "")
	if encoded == "" {
		return nil, errDecodeBase64
	}
	// Go's decoder skips embedded \r and \n; the published contract is strict
	// base64, so line-wrapped payloads stay rejected instead of silently
	// decoding into a different byte count than the caller advertised.
	if strings.ContainsAny(encoded, " \t\n\r\v\f") {
		return nil, errDecodeBase64
	}

	stream := base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))
	data, err := io.ReadAll(io.LimitReader(stream, MaxImageBytes+1))
	if err != nil || len(data) == 0 {
		return nil, errDecodeBase64
	}
	if len(data) > MaxImageBytes {
		return nil, errDecodedTooLarge
	}
	return data, nil
}

// sniffFormat reports the content type of the image bytes, or "" when they are
// not one of the supported formats.
//
// Format checks are limited to the three formats the product documents. The
// previous build forwarded any decodable bytes, so a blob of arbitrary data
// reached the model as though it were an image.
func sniffFormat(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}
