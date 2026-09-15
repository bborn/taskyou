package panel

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRemotePreviewProtocol(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# Workspace"), 0600)
	run := func(resource, mode string) ([]byte, error) {
		return exec.Command(python, "-c", remoteReadScript, root, resource, mode).Output()
	}
	out, err := run("README.md", "file")
	if err != nil {
		t.Fatal(err)
	}
	var c Content
	if err = json.Unmarshal(out, &c); err != nil || c.Kind != "markdown" || c.Text != "# Workspace" {
		t.Fatalf("remote protocol: %+v %v", c, err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("private"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape"))
	if _, err = run("escape", "file"); err == nil {
		t.Fatal("remote symlink accepted")
	}
	if _, err = run("../outside", "file"); err == nil {
		t.Fatal("remote traversal accepted")
	}
	os.WriteFile(filepath.Join(root, "binary"), []byte{0, 1}, 0600)
	if _, err = run("binary", "file"); err == nil {
		t.Fatal("remote binary accepted")
	}
	out, err = run(".", "directory")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(out, &c); err != nil || c.Kind != "files" || len(c.Entries) != 3 {
		t.Fatalf("remote directory: %+v %v", c, err)
	}
}
