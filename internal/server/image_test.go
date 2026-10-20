package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
)

// overCapBase64 is the shortest base64 form that decodes past MaxImageBytes.
var overCapBase64 = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x7F}, MaxImageBytes+1))

func TestImageFromBase64(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(pngBytes)

	tests := []struct {
		name    string
		input   string
		want    []byte
		wantErr error
	}{
		{"plain", valid, pngBytes, nil},
		{"data url stripped", "data:image/png;base64," + valid, pngBytes, nil},
		{"data url jpeg type on png bytes", "data:image/jpeg;base64," + valid, pngBytes, nil},
		{"data url webp type", "data:image/webp;base64," + valid, pngBytes, nil},
		{"empty", "", nil, errDecodeBase64},
		{"only prefix", "data:image/png;base64,", nil, errDecodeBase64},
		{"bad alphabet", "not!! base64!!", nil, errDecodeBase64},
		{"bad padding", "AAAA=", nil, errDecodeBase64},
		{"unpadded", "QUJDRA", nil, errDecodeBase64},
		{"padding mid stream", "AA==AAAA", nil, errDecodeBase64},
		{"wrapped with newline", valid[:20] + "\n" + valid[20:], nil, errDecodeBase64},
		{"url safe alphabet rejected", base64.URLEncoding.EncodeToString([]byte{0xFB, 0xFF, 0xFE}), nil, errDecodeBase64},
		// A data URL carrying an extra parameter is not stripped, and the
		// prefix then fails base64 validation. Same as the previous build.
		{"data url with charset param", "data:image/png;charset=utf-8;base64," + valid, nil, errDecodeBase64},
		{"over decoded cap", overCapBase64, nil, errDecodedTooLarge},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := imageFromBase64(tc.input)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && !bytes.Equal(got, tc.want) {
				t.Errorf("decoded %d bytes, want the %d expected bytes", len(got), len(tc.want))
			}
			if tc.wantErr != nil && len(got) != 0 {
				t.Errorf("got %d bytes alongside an error, want none", len(got))
			}
		})
	}
}

func TestSniffFormat(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{"jpeg", jpegBytes, "image/jpeg"},
		{"png", pngBytes, "image/png"},
		{"webp", webpBytes, "image/webp"},
		{"gif", append([]byte("GIF89a"), make([]byte, 32)...), ""},
		{"pdf", append([]byte("%PDF-1.4 "), make([]byte, 32)...), ""},
		{"bmp", append([]byte("BM"), make([]byte, 32)...), ""},
		{"svg text", []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>"), ""},
		{"plain text", []byte("hello world, plainly not an image"), ""},
		{"empty", nil, ""},
		{"riff with wrong brand", append(append([]byte("RIFF"), 0, 0, 0, 0), append([]byte("AVI "), make([]byte, 32)...)...), ""},
		{"jpeg marker truncated", []byte{0xFF, 0xD8}, ""},
		{"png signature exactly", []byte("\x89PNG\r\n\x1a\n"), "image/png"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sniffFormat(tc.input); got != tc.want {
				t.Errorf("sniffFormat(%q...) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// The 8 MB body cap already bounds a decode to 6 MB; this pins the two
// documented limits to their 4:3 relationship so they cannot drift apart.
func TestBodyAndImageLimitsAreConsistent(t *testing.T) {
	if MaxImageBytes != MaxBodyBytes/4*3 {
		t.Errorf("MaxImageBytes = %d, want %d", MaxImageBytes, MaxBodyBytes/4*3)
	}
	if MaxBodyBytes != 8*1024*1024 || MaxImageBytes != 6*1024*1024 {
		t.Errorf("advertised limits changed: body=%d image=%d", MaxBodyBytes, MaxImageBytes)
	}
}

func BenchmarkImageFromBase64(b *testing.B) {
	// 256 KB is a realistic phone-camera label crop once JPEG-compressed.
	image := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0x42}, 256*1024)...)
	plain := base64.StdEncoding.EncodeToString(image)
	dataURL := "data:image/jpeg;base64," + plain

	benchmarks := []struct {
		name  string
		input string
	}{
		{"raw", plain},
		{"data_url", dataURL},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				data, err := imageFromBase64(bm.input)
				if err != nil {
					b.Fatal(err)
				}
				if sniffFormat(data) == "" {
					b.Fatal("unexpected format")
				}
			}
		})
	}
}

// BenchmarkRequestBodyPipeline measures the whole local path for one request:
// JSON parse, base64 decode and format check. The previous build paid for a
// regex scan plus two full decodes, then re-serialised the bytes as an array
// of integers on the way to the model.
func BenchmarkRequestBodyPipeline(b *testing.B) {
	image := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0x42}, 256*1024)...)
	body := []byte(`{"image":"` + base64.StdEncoding.EncodeToString(image) + `"}`)

	b.ReportAllocs()
	for b.Loop() {
		var payload struct {
			Image string `json:"image"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			b.Fatal(err)
		}
		data, err := imageFromBase64(payload.Image)
		if err != nil {
			b.Fatal(err)
		}
		if sniffFormat(data) == "" {
			b.Fatal("unexpected format")
		}
	}
}
