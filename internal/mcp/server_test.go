package mcp

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// testDB creates a temporary database for testing.
func testDB(t *testing.T) *db.DB {
	tmpDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	return database
}

// createTestTask creates a task for testing.
func createTestTask(t *testing.T, database *db.DB) *db.Task {
	task := &db.Task{
		Title:   "Test Task",
		Status:  db.StatusProcessing,
		Project: "test-project",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}
	return task
}

// testServer creates a server with mocked IO for testing.
func testServer(database *db.DB, taskID int64, input string) (*Server, *bytes.Buffer) {
	output := &bytes.Buffer{}
	server := &Server{
		db:     database,
		taskID: taskID,
		reader: bufio.NewReader(strings.NewReader(input)),
		writer: output,
	}
	return server, output
}

func TestSaveScreenshot(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	// Create a small test PNG image (1x1 red pixel)
	// Minimal valid PNG: IHDR chunk, IDAT chunk, IEND chunk
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, // IHDR length
		0x49, 0x48, 0x44, 0x52, // IHDR type
		0x00, 0x00, 0x00, 0x01, // width = 1
		0x00, 0x00, 0x00, 0x01, // height = 1
		0x08, 0x02, // 8-bit RGB
		0x00, 0x00, 0x00, // compression, filter, interlace
		0x90, 0x77, 0x53, 0xDE, // CRC
		0x00, 0x00, 0x00, 0x0C, // IDAT length
		0x49, 0x44, 0x41, 0x54, // IDAT type
		0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, 0x00, 0x01, 0x01, 0x01, 0x00, // compressed data
		0x18, 0xDD, 0x8D, 0xB4, // CRC
		0x00, 0x00, 0x00, 0x00, // IEND length
		0x49, 0x45, 0x4E, 0x44, // IEND type
		0xAE, 0x42, 0x60, 0x82, // CRC
	}
	base64Data := base64.StdEncoding.EncodeToString(pngData)

	t.Run("saves screenshot with base64 data", func(t *testing.T) {
		request := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name": "workflow_save_screenshot",
				"arguments": map[string]interface{}{
					"image_data":  base64Data,
					"filename":    "test-screenshot.png",
					"description": "Test screenshot",
				},
			},
		}
		reqBytes, _ := json.Marshal(request)
		reqBytes = append(reqBytes, '\n')

		server, output := testServer(database, task.ID, string(reqBytes))
		server.Run()

		// Parse the response
		var resp jsonRPCResponse
		if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if resp.Error != nil {
			t.Fatalf("unexpected error: %s", resp.Error.Message)
		}

		// Verify attachment was created
		attachments, err := database.ListAttachments(task.ID)
		if err != nil {
			t.Fatalf("failed to list attachments: %v", err)
		}
		if len(attachments) != 1 {
			t.Fatalf("expected 1 attachment, got %d", len(attachments))
		}
		if attachments[0].Filename != "test-screenshot.png" {
			t.Errorf("expected filename 'test-screenshot.png', got '%s'", attachments[0].Filename)
		}
		if attachments[0].MimeType != "image/png" {
			t.Errorf("expected MIME type 'image/png', got '%s'", attachments[0].MimeType)
		}

		// Verify the attachment data
		attachment, err := database.GetAttachment(attachments[0].ID)
		if err != nil {
			t.Fatalf("failed to get attachment: %v", err)
		}
		if !bytes.Equal(attachment.Data, pngData) {
			t.Error("attachment data does not match original")
		}
	})

	t.Run("saves screenshot with data URI format", func(t *testing.T) {
		dataURI := "data:image/png;base64," + base64Data
		request := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name": "workflow_save_screenshot",
				"arguments": map[string]interface{}{
					"image_data": dataURI,
				},
			},
		}
		reqBytes, _ := json.Marshal(request)
		reqBytes = append(reqBytes, '\n')

		server, output := testServer(database, task.ID, string(reqBytes))
		server.Run()

		var resp jsonRPCResponse
		if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if resp.Error != nil {
			t.Fatalf("unexpected error: %s", resp.Error.Message)
		}

		// Verify attachment was created with auto-generated filename
		attachments, err := database.ListAttachments(task.ID)
		if err != nil {
			t.Fatalf("failed to list attachments: %v", err)
		}
		// We should have 2 attachments now (from previous test + this one)
		if len(attachments) != 2 {
			t.Fatalf("expected 2 attachments, got %d", len(attachments))
		}
		// The second attachment should have auto-generated name
		if !strings.HasPrefix(attachments[1].Filename, "screenshot-") {
			t.Errorf("expected filename to start with 'screenshot-', got '%s'", attachments[1].Filename)
		}
		if !strings.HasSuffix(attachments[1].Filename, ".png") {
			t.Errorf("expected filename to end with '.png', got '%s'", attachments[1].Filename)
		}
	})

	t.Run("handles JPEG data URI", func(t *testing.T) {
		// Just use PNG data with JPEG header for testing MIME detection
		jpegDataURI := "data:image/jpeg;base64," + base64Data
		request := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      3,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name": "workflow_save_screenshot",
				"arguments": map[string]interface{}{
					"image_data": jpegDataURI,
					"filename":   "test-jpeg",
				},
			},
		}
		reqBytes, _ := json.Marshal(request)
		reqBytes = append(reqBytes, '\n')

		server, output := testServer(database, task.ID, string(reqBytes))
		server.Run()

		var resp jsonRPCResponse
		if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if resp.Error != nil {
			t.Fatalf("unexpected error: %s", resp.Error.Message)
		}

		attachments, err := database.ListAttachments(task.ID)
		if err != nil {
			t.Fatalf("failed to list attachments: %v", err)
		}
		// Find the JPEG attachment
		var jpegAttachment *db.Attachment
		for _, a := range attachments {
			if a.MimeType == "image/jpeg" {
				jpegAttachment = a
				break
			}
		}
		if jpegAttachment == nil {
			t.Fatal("expected to find JPEG attachment")
		}
		if jpegAttachment.Filename != "test-jpeg.jpg" {
			t.Errorf("expected filename 'test-jpeg.jpg', got '%s'", jpegAttachment.Filename)
		}
	})

	t.Run("returns error for missing image_data", func(t *testing.T) {
		request := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      4,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name":      "workflow_save_screenshot",
				"arguments": map[string]interface{}{},
			},
		}
		reqBytes, _ := json.Marshal(request)
		reqBytes = append(reqBytes, '\n')

		server, output := testServer(database, task.ID, string(reqBytes))
		server.Run()

		var resp jsonRPCResponse
		if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if resp.Error == nil {
			t.Fatal("expected error for missing image_data")
		}
		if !strings.Contains(resp.Error.Message, "image_data is required") {
			t.Errorf("expected error about missing image_data, got: %s", resp.Error.Message)
		}
	})

	t.Run("returns error for invalid base64", func(t *testing.T) {
		request := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      5,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name": "workflow_save_screenshot",
				"arguments": map[string]interface{}{
					"image_data": "not-valid-base64!!!",
				},
			},
		}
		reqBytes, _ := json.Marshal(request)
		reqBytes = append(reqBytes, '\n')

		server, output := testServer(database, task.ID, string(reqBytes))
		server.Run()

		var resp jsonRPCResponse
		if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if resp.Error == nil {
			t.Fatal("expected error for invalid base64")
		}
		if !strings.Contains(resp.Error.Message, "Failed to decode base64") {
			t.Errorf("expected error about base64 decoding, got: %s", resp.Error.Message)
		}
	})
}

func TestToolsList(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}
	reqBytes, _ := json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	server, output := testServer(database, task.ID, string(reqBytes))
	server.Run()

	var resp jsonRPCResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	// Check that the tools list includes workflow_save_screenshot
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatal("expected result to be a map")
	}
	tools, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatal("expected tools to be an array")
	}

	var foundScreenshot bool
	for _, toolI := range tools {
		tool, ok := toolI.(map[string]interface{})
		if !ok {
			continue
		}
		if tool["name"] == "workflow_save_screenshot" {
			foundScreenshot = true
			// Verify the tool has proper schema
			schema, ok := tool["inputSchema"].(map[string]interface{})
			if !ok {
				t.Error("expected inputSchema to be a map")
			}
			props, ok := schema["properties"].(map[string]interface{})
			if !ok {
				t.Error("expected properties to be a map")
			}
			if _, ok := props["image_data"]; !ok {
				t.Error("expected image_data property")
			}
			if _, ok := props["filename"]; !ok {
				t.Error("expected filename property")
			}
			if _, ok := props["description"]; !ok {
				t.Error("expected description property")
			}
		}
	}

	if !foundScreenshot {
		t.Error("workflow_save_screenshot not found in tools list")
	}
}
