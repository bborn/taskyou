package widget

import (
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	js := Generate("http://localhost:8090", "myapp")

	if !strings.Contains(js, "http://localhost:8090") {
		t.Error("widget does not contain API URL")
	}
	if !strings.Contains(js, "myapp") {
		t.Error("widget does not contain project name")
	}
	if !strings.Contains(js, "ty-feedback-btn") {
		t.Error("widget does not contain button ID")
	}
	if !strings.Contains(js, "/api/feedback") {
		t.Error("widget does not contain feedback endpoint")
	}
}

func TestScriptTag(t *testing.T) {
	tag := ScriptTag("http://localhost:8090", "")
	if !strings.Contains(tag, "http://localhost:8090/widget.js") {
		t.Errorf("ScriptTag = %q, want to contain widget.js URL", tag)
	}
	if strings.Contains(tag, "data-api-key") {
		t.Error("empty api key should not produce data-api-key attribute")
	}

	tag = ScriptTag("http://localhost:8090", "secret")
	if !strings.Contains(tag, `data-api-key="secret"`) {
		t.Errorf("ScriptTag with key = %q, want data-api-key", tag)
	}
}
