package bridge

import (
	"testing"
)

func TestNew(t *testing.T) {
	b := New("")
	if b.tyPath != "ty" {
		t.Errorf("New(\"\").tyPath = %q, want \"ty\"", b.tyPath)
	}

	b = New("/usr/local/bin/ty")
	if b.tyPath != "/usr/local/bin/ty" {
		t.Errorf("New(\"/usr/local/bin/ty\").tyPath = %q", b.tyPath)
	}
}

func TestIsAvailable(t *testing.T) {
	b := New("nonexistent-binary-that-does-not-exist")
	if b.IsAvailable() {
		t.Error("nonexistent binary should not be available")
	}
}
