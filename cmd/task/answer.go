package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/agentsend"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/question"
)

// `ty answer` is the CLI face of a blocked task's pending question — the one an
// agent asked through taskyou_needs_input. With only a task id it shows the
// question and its numbered options; with answers it delivers them through the
// same core the GUI and TUI use (internal/question), so a script reads and
// answers exactly what a person on their phone would.

func newAnswerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "answer <task-id> [option...]",
		Short:             "Show or answer the question a blocked task is waiting on",
		ValidArgsFunction: completeTaskIDs,
		Long: `Show or answer the question a blocked task's agent asked with taskyou_needs_input.

With just a task id, prints the question and its numbered options. With
answers, sends them to the agent:

  ty answer 42                  # show the question
  ty answer 42 2                # pick option 2
  ty answer 42 1,3              # pick options 1 and 3 (multi_choice)
  ty answer 42 yes              # options can be named by label
  ty answer 42 --other "Use the staging bucket instead"
  ty answer 42 use the staging bucket   # a plain question takes your own words

The agent receives "Selected: <label>" (or "Yes"/"No", or your own words). The
answer goes through the same delivery as ty input, so it is refused while the
agent is still working.`,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(args[0]), "#"), 10, 64)
			if err != nil {
				return fmt.Errorf("invalid task id: %s", args[0])
			}
			other, _ := cmd.Flags().GetString("other")
			outputJSON, _ := cmd.Flags().GetBool("json")
			questionID, _ := cmd.Flags().GetInt64("question-id")

			database, err := openTaskDB(db.DefaultPath())
			if err != nil {
				return err
			}
			defer database.Close()

			q, err := database.GetPendingQuestion(taskID)
			if err != nil {
				return err
			}
			if q == nil {
				if outputJSON && len(args) == 1 && other == "" {
					fmt.Println(`{"question":null}`)
					return nil
				}
				return fmt.Errorf("task #%d is not waiting on a question", taskID)
			}

			if len(args) == 1 && strings.TrimSpace(other) == "" {
				if outputJSON {
					return printQuestionJSON(q)
				}
				fmt.Print(formatQuestionForCLI(q))
				return nil
			}

			resp, err := parseAnswerArgs(q, args[1:], other)
			if err != nil {
				return err
			}
			if questionID == 0 {
				questionID = q.ID
			}
			sender := agentsend.New(&execCommandRunner{}, database)
			text, err := question.Answer(database, taskID, questionID, resp, func(p agentsend.Prompt) error {
				return sender.Send(p)
			})
			if errors.Is(err, agentsend.ErrBusy) {
				return fmt.Errorf("%v\nWait for it to finish, then answer again", err)
			}
			if err != nil {
				return err
			}
			waitForEventHooks()
			if outputJSON {
				out, _ := json.Marshal(map[string]interface{}{"ok": true, "answer": text})
				fmt.Println(string(out))
				return nil
			}
			fmt.Println(successStyle.Render(fmt.Sprintf("Answered task #%d: %s", taskID, text)))
			return nil
		},
	}
	cmd.Flags().StringP("other", "o", "", "Answer in your own words (for a question that allows it)")
	cmd.Flags().Bool("json", false, "Print the question (or the delivered answer) as JSON")
	cmd.Flags().Int64("question-id", 0, "Refuse to answer unless this is still the pending question's id")
	return cmd
}

// parseAnswerArgs turns the words after the task id into a Response. Each word
// may hold several picks separated by commas; a pick is an option number or an
// option's label. A plain question has no options, so its words are the answer.
func parseAnswerArgs(q *db.PendingQuestion, args []string, other string) (question.Response, error) {
	resp := question.Response{Other: other}
	if !question.IsStructured(q) {
		words := strings.TrimSpace(strings.Join(args, " "))
		if words != "" && strings.TrimSpace(other) != "" {
			return resp, fmt.Errorf("give the answer once: as words or with --other, not both")
		}
		if words != "" {
			resp.Other = words
		}
		return resp, nil
	}

	choices := question.Choices(q)
	for _, arg := range args {
		for _, part := range strings.Split(arg, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if n, err := strconv.Atoi(part); err == nil {
				resp.Choices = append(resp.Choices, n)
				continue
			}
			n := optionByLabel(choices, part)
			if n == 0 {
				return resp, fmt.Errorf("no option %q; pick a number from 1 to %d, or use --other for your own words", part, len(choices))
			}
			resp.Choices = append(resp.Choices, n)
		}
	}
	return resp, nil
}

// optionByLabel returns the 1-based number of the option labelled label
// (ignoring case), or 0.
func optionByLabel(choices []db.QuestionOption, label string) int {
	for i, c := range choices {
		if strings.EqualFold(c.Label, label) {
			return i + 1
		}
	}
	return 0
}

// formatQuestionForCLI renders a pending question the way `ty answer <id>`
// prints it: the question, its numbered options, and how to answer.
func formatQuestionForCLI(q *db.PendingQuestion) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task #%d is waiting on a question", q.TaskID)
	if question.IsStructured(q) {
		fmt.Fprintf(&b, " (%s)", strings.ReplaceAll(q.Kind, "_", " "))
	}
	fmt.Fprintf(&b, ":\n\n  %s\n", q.Question)

	choices := question.Choices(q)
	if len(choices) > 0 {
		b.WriteString("\n")
		for i, c := range choices {
			line := fmt.Sprintf("  %d. %s", i+1, c.Label)
			if c.Description != "" {
				line += dimStyle.Render(" — " + c.Description)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	switch {
	case !question.IsStructured(q):
		fmt.Fprintf(&b, "Answer with: ty answer %d <your answer>\n", q.TaskID)
	case q.Kind == db.QuestionMultiChoice:
		fmt.Fprintf(&b, "Answer with: ty answer %d <n>[,<n>...]", q.TaskID)
	default:
		fmt.Fprintf(&b, "Answer with: ty answer %d <n>", q.TaskID)
	}
	if question.IsStructured(q) {
		if q.AllowOther {
			b.WriteString(`   or --other "your own words"`)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func printQuestionJSON(q *db.PendingQuestion) error {
	out := map[string]interface{}{
		"question": map[string]interface{}{
			"id":          q.ID,
			"task_id":     q.TaskID,
			"question":    q.Question,
			"kind":        q.Kind,
			"options":     question.Choices(q),
			"allow_other": q.AllowOther,
		},
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(data, '\n'))
	return err
}
