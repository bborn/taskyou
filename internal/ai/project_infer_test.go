package ai

import "testing"

func TestParseInferenceJSON(t *testing.T) {
	meta, err := parseInferenceJSON(`{"name":"acme-rocket","alias":"acme","description":"Rust CLI for rockets"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Name != "acme-rocket" || meta.Alias != "acme" || meta.Description != "Rust CLI for rockets" {
		t.Errorf("got %+v", meta)
	}

	meta, err = parseInferenceJSON("Here you go:\n```json\n{\"name\":\"foo\",\"alias\":\"f\",\"description\":\"d\"}\n```")
	if err != nil || meta.Name != "foo" {
		t.Errorf("fenced parse failed: %+v err=%v", meta, err)
	}

	if _, err := parseInferenceJSON("not json at all"); err == nil {
		t.Error("expected error on non-JSON")
	}

	// Prose containing a stray brace before the real JSON object.
	meta, err = parseInferenceJSON(`note: use {curly} then {"name":"bar","alias":"b","description":"d"}`)
	if err != nil || meta.Name != "bar" {
		t.Errorf("stray-brace parse failed: %+v err=%v", meta, err)
	}
}

func TestInferProjectMetadata_DegradesWhenClaudeMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty PATH dir => claude not found
	_, err := InferProjectMetadata(t.TempDir(), "")
	if err == nil {
		t.Error("expected error when claude binary is unavailable")
	}
}
