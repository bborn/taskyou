package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// PiRPCEvent represents a structured event from Pi in RPC mode
type PiRPCEvent struct {
	Type                  string                 `json:"type"`
	Message               *PiMessage             `json:"message"`
	AssistantMessageEvent *PiAssistantMessageEvent `json:"assistantMessageEvent"`
	ToolCallID            string                 `json:"toolCallId"`
	ToolName              string                 `json:"toolName"`
	Args                  map[string]interface{} `json:"args"`
	Result                *PiToolResultContent   `json:"result"`
	IsError               bool                   `json:"isError"`
}

type PiMessage struct {
	Role    string      `json:"role"`
	Content []PiContent `json:"content"`
}

type PiContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type PiAssistantMessageEvent struct {
	Type         string          `json:"type"` // text_start, text_delta, text_end, toolcall_start, etc.
	Delta        string          `json:"delta"`
	ContentIndex int             `json:"contentIndex"`
	ToolCall     *PiToolCallData `json:"toolCall"`
}

type PiToolCallData struct {
	ID   string                 `json:"id"`
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"arguments"`
}

type PiToolResultContent struct {
	Content []PiContent `json:"content"`
}

// piWrapperCmd represents the pi-wrapper command
var piWrapperCmd = &cobra.Command{
	Use:    "pi-wrapper",
	Short:  "Wrapper for Pi agent to handle RPC mode",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		taskID, _ := cmd.Flags().GetInt64("task-id")
		
		// Remaining args are passed to pi
		// Check if --mode rpc is already present, if not add it
		piArgs := []string{"--mode", "rpc"}
		
		// Add other args
		// We need to be careful not to duplicate flags if they are already in args
		for _, arg := range args {
			if arg == "--mode" || arg == "rpc" {
				continue
			}
			piArgs = append(piArgs, arg)
		}

		if err := runPiWrapper(taskID, piArgs); err != nil {
			fmt.Fprintf(os.Stderr, "Error running pi wrapper: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	piWrapperCmd.Flags().Int64("task-id", 0, "Task ID to update")
}

func runPiWrapper(taskID int64, piArgs []string) error {
	// Open database connection
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Prepare the command
	cmd := exec.Command("pi", piArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr // Forward stderr directly

	// Capture stdout pipe
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start pi: %w", err)
	}

	// Styles for output
	userStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#3B82F6")).Bold(true) // Blue
	aiStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981")).Bold(true)   // Green
	toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))            // Yellow
	resultStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8B5CF6"))          // Purple

	scanner := bufio.NewScanner(stdout)
	
	// Text buffer for logging
	var textBuffer strings.Builder
	
	// Flush buffer function
	flushText := func() {
		if textBuffer.Len() > 0 {
			content := textBuffer.String()
			database.AppendTaskLog(taskID, "output", content)
			textBuffer.Reset()
		}
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		
		var event PiRPCEvent
		if err := json.Unmarshal(line, &event); err != nil {
			// If not JSON, just print it (maybe raw output?)
			fmt.Println(string(line))
			continue
		}

		// Handle events
		switch event.Type {
		case "message_start":
			flushText()
			if event.Message != nil {
				if event.Message.Role == "user" {
					fmt.Println(userStyle.Render("\n> User"))
					// Log to DB
					database.AppendTaskLog(taskID, "system", "User input:")
                    
                    // User message content
                    for _, content := range event.Message.Content {
                        if content.Type == "text" {
                            fmt.Println(content.Text)
							database.AppendTaskLog(taskID, "question", content.Text)
                        }
                    }
				} else if event.Message.Role == "assistant" {
					fmt.Println(aiStyle.Render("\n> Pi"))
				}
			}

		case "message_update":
			if event.AssistantMessageEvent != nil {
				// Stream text
				if event.AssistantMessageEvent.Type == "text_delta" {
					fmt.Print(event.AssistantMessageEvent.Delta)
					textBuffer.WriteString(event.AssistantMessageEvent.Delta)
				}
			}

		case "message_end":
			flushText()
			fmt.Println() // Ensure newline after message

		case "tool_execution_start":
			flushText()
			toolName := event.ToolName
			argsBytes, _ := json.Marshal(event.Args)
			argsStr := string(argsBytes)
			
			display := fmt.Sprintf("\n[Tool Use] %s %s", toolName, argsStr)
			fmt.Println(toolStyle.Render(display))
			
			// Log to DB
			database.AppendTaskLog(taskID, "tool", fmt.Sprintf("Executing %s: %s", toolName, argsStr))

		case "tool_execution_end":
			flushText()
			if event.Result != nil {
				for _, content := range event.Result.Content {
					if content.Type == "text" {
						fmt.Println(resultStyle.Render("[Tool Result]"))
						fmt.Println(content.Text)
						
						// Log to DB - truncate if too long?
						// Executor usually logs full output, but maybe split into lines?
						// AppendTaskLog handles single entry.
						database.AppendTaskLog(taskID, "tool", "Result: "+content.Text)
					}
				}
			}
			if event.IsError {
				fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Render("[Tool Error]"))
				database.AppendTaskLog(taskID, "error", "Tool execution failed")
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("pi exited with error: %w", err)
	}

	return nil
}
