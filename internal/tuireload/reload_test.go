package tuireload

import (
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestRequestsAreSharedOnlyWithinDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	writer, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	other, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	previous := ""
	for i := 0; i < 2; i++ {
		if err := Request(writer); err != nil {
			t.Fatal(err)
		}
		token, err := Token(reader)
		if err != nil || token == "" || token == previous {
			t.Fatal("request not visible or not unique", err)
		}
		previous = token
	}
	if token, err := Token(other); err != nil || token != "" {
		t.Fatal("request crossed database boundary")
	}
}
