package ui

import (
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// webDetectMime mirrors the MIME detection the web GUI's handleAddAttachment
// uses (internal/web/handlers_gui.go:158-164): stdlib extension lookup then
// content sniffing. The TUI's detectMimeType must produce the same value for a
// given (filename, data) so attachments added through either surface are served
// by the web GUI with the same Content-Type. It is the spec the fix targets.
func webDetectMime(filename string, data []byte) string {
	mt := mime.TypeByExtension(filepath.Ext(filename))
	if mt == "" {
		mt = http.DetectContentType(data)
	}
	return mt
}

// pngSig, webpSig, etc. are minimal magic-byte signatures that net/http's
// content sniffer (a pure-Go, OS-independent matcher) recognises deterministically
// across Go versions, independent of the platform MIME registry.
func pngSig() []byte  { return []byte("\x89PNG\r\n\x1a\n") }
func webpSig() []byte { return []byte("RIFF\x00\x00\x00\x00WEBP") }
func jpegSig() []byte { return []byte("\xff\xd8\xff\xe0") }
func gifSig() []byte  { return []byte("GIF89a") }
func zipSig() []byte  { return []byte("PK\x03\x04") }
func pdfSig() []byte  { return []byte("%PDF-1.4\n") }

// TestDetectMimeType_AgreesWithWebHandlerDetection asserts the central fix:
// for every input, the TUI detector returns exactly what the web GUI's
// stdlib-based detector returns. Both sides run the same stdlib calls in the
// same environment, so equivalence is guaranteed if (and only if) the TUI no
// longer uses the old hand-rolled table.
func TestDetectMimeType_AgreesWithWebHandlerDetection(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		data     []byte
	}{
		{"extensionless text", "notes", []byte("reminder notes\n")},
		{"extensionless makefile", "Makefile", []byte("all: build\n\tgo build ./...\n")},
		{"extensionless dockerfile", "Dockerfile", []byte("FROM golang:1.26\n")},
		{"csv", "data.csv", []byte("name,age\nalice,30\n")},
		{"yaml", "config.yaml", []byte("key: value\n")},
		{"yml", "config.yml", []byte("key: value\n")},
		{"shell", "script.sh", []byte("#!/bin/sh\necho hi\n")},
		{"toml", "Cargo.toml", []byte("[package]\nname = \"x\"\n")},
		{"typescript", "app.ts", []byte("const x: number = 1;\n")},
		{"tsx", "comp.tsx", []byte("export const C = () => <div/>;\n")},
		{"webp", "photo.webp", webpSig()},
		{"bmp", "photo.bmp", []byte("BM\x00\x00\x00\x00")},
		{"png", "photo.png", pngSig()},
		{"jpeg", "photo.jpg", jpegSig()},
		{"gif", "photo.gif", gifSig()},
		{"zip", "archive.zip", zipSig()},
		{"pdf", "doc.pdf", pdfSig()},
		{"html", "page.html", []byte("<html></html>")},
		{"css", "style.css", []byte("body{color:red}")},
		{"js", "app.js", []byte("const x = 1;")},
		{"json", "data.json", []byte(`{"a":1}`)},
		{"xml", "data.xml", []byte("<x/>")},
		{"md", "readme.md", []byte("# Title\n")},
		{"txt", "notes.txt", []byte("hello world")},
		{"go source", "app.go", []byte("package main\nfunc main(){}\n")},
		{"python", "app.py", []byte("print('hi')\n")},
		{"ruby", "app.rb", []byte("puts 'hi'\n")},
		{"rust", "app.rs", []byte("fn main(){}\n")},
		{"binary unknown", "blob.bin", []byte("\x00\x01\x02\x03\x04\x05")},
		{"empty data", "empty.txt", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectMimeType(tc.filename, tc.data)
			want := webDetectMime(tc.filename, tc.data)
			if got != want {
				t.Errorf("detectMimeType(%q) = %q; web handler detection = %q (must agree)", tc.filename, got, want)
			}
		})
	}
}

// TestDetectMimeType_FixesPreviouslyBrokenFiles asserts the user-visible fix:
// files that the old 18-entry table mapped to application/octet-stream now get a
// renderable type, so the browser shows them inline instead of downloading.
func TestDetectMimeType_FixesPreviouslyBrokenFiles(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		data     []byte
		family   string // expected top-level family, where deterministic
	}{
		{"extensionless text notes", "notes", []byte("reminder notes\n"), "text/"},
		{"extensionless makefile", "Makefile", []byte("all: build\n"), "text/"},
		{"extensionless dockerfile", "Dockerfile", []byte("FROM golang:1.26\n"), "text/"},
		{"csv", "data.csv", []byte("name,age\nalice,30\n"), "text/"},
		{"yaml", "config.yaml", []byte("key: value\n"), ""},
		{"shell", "script.sh", []byte("#!/bin/sh\necho hi\n"), "text/"},
		{"webp image", "photo.webp", webpSig(), "image/"},
		{"bmp image", "photo.bmp", []byte("BM\x00\x00\x00\x00"), "image/"},
		{"typescript", "app.ts", []byte("const x: number = 1;\n"), "text/"},
		{"toml", "Cargo.toml", []byte("[package]\nname = \"x\"\n"), "text/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectMimeType(tc.filename, tc.data)
			if got == "application/octet-stream" {
				t.Errorf("detectMimeType(%q) = application/octet-stream; expected a renderable type so the browser renders inline", tc.filename)
			}
			if tc.family != "" && !strings.HasPrefix(got, tc.family) {
				t.Errorf("detectMimeType(%q) = %q; expected %s* type", tc.filename, got, tc.family)
			}
		})
	}
}

// TestDetectMimeType_OldMappedExtensionsDoNotRegress ensures every extension
// the old hardcoded table handled still resolves to a renderable type under the
// stdlib-based detector (text content sniffs to text/* even when the platform
// MIME registry lacks a given extension).
func TestDetectMimeType_OldMappedExtensionsDoNotRegress(t *testing.T) {
	cases := []struct {
		filename string
		data     []byte
	}{
		{"notes.txt", []byte("hello world")},
		{"readme.md", []byte("# Title\n")},
		{"data.json", []byte(`{"a":1}`)},
		{"data.xml", []byte("<x/>")},
		{"page.html", []byte("<html></html>")},
		{"style.css", []byte("body{color:red}")},
		{"app.js", []byte("const x = 1;")},
		{"photo.png", pngSig()},
		{"photo.jpg", jpegSig()},
		{"photo.jpeg", jpegSig()},
		{"photo.gif", gifSig()},
		{"logo.svg", []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>")},
		{"doc.pdf", pdfSig()},
		{"archive.zip", zipSig()},
		{"app.go", []byte("package main\nfunc main(){}\n")},
		{"app.py", []byte("print('hi')\n")},
		{"app.rb", []byte("puts 'hi'\n")},
		{"app.rs", []byte("fn main(){}\n")},
	}
	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			got := detectMimeType(tc.filename, tc.data)
			if got == "" {
				t.Errorf("detectMimeType(%q) returned empty string", tc.filename)
			}
			if got == "application/octet-stream" {
				t.Errorf("detectMimeType(%q) = application/octet-stream; extension regressed from a renderable type", tc.filename)
			}
		})
	}
}

// TestDetectMimeType_DeterministicSniffedTypes asserts exact values for inputs
// resolved by net/http's pure-Go content sniffer, which is OS- and
// Go-version-independent. These cases do not rely on the platform MIME registry.
func TestDetectMimeType_DeterministicSniffedTypes(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		data     []byte
		want     string
	}{
		{"extensionless utf-8 text", "notes", []byte("reminder notes\n"), "text/plain; charset=utf-8"},
		{"png magic", "image.png", pngSig(), "image/png"},
		{"webp magic", "image.webp", webpSig(), "image/webp"},
		{"jpeg magic", "image.jpg", jpegSig(), "image/jpeg"},
		{"gif magic", "image.gif", gifSig(), "image/gif"},
		{"zip magic", "archive.zip", zipSig(), "application/zip"},
		{"pdf magic", "doc.pdf", pdfSig(), "application/pdf"},
		{"unknown binary", "blob.bin", []byte("\x00\x01\x02\x03\x04\x05"), "application/octet-stream"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectMimeType(tc.filename, tc.data); got != tc.want {
				t.Errorf("detectMimeType(%q) = %q; want %q", tc.filename, got, tc.want)
			}
		})
	}
}

// TestDetectMimeType_AlwaysNonEmpty guarantees the persisted value is never the
// empty string, so handleGetAttachment's `if contentType == ""` fallback to
// application/octet-stream never needs to fire for TUI-added rows and the stored
// type is always usable as a Content-Type header.
func TestDetectMimeType_AlwaysNonEmpty(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		data     []byte
	}{
		{"nil data no extension", "notes", nil},
		{"empty data no extension", "notes", []byte{}},
		{"nil data with extension", "notes.txt", nil},
		{"empty data unknown extension", "blob.xyz", []byte{}},
		{"nil data known extension", "notes.csv", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectMimeType(tc.filename, tc.data); got == "" {
				t.Errorf("detectMimeType(%q) returned empty string; must always be a usable Content-Type", tc.filename)
			}
		})
	}
}
