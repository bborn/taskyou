package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/agentsend"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/question"
)

func TestParseAnswerArgs(t *testing.T) {
	choice := &db.PendingQuestion{TaskID: 42, Kind: db.QuestionChoice, Options: []db.QuestionOption{{Label: "Redis"}, {Label: "In-process LRU"}}}
	multi := &db.PendingQuestion{TaskID: 42, Kind: db.QuestionMultiChoice, Options: []db.QuestionOption{{Label: "A"}, {Label: "B"}, {Label: "C"}}}
	confirm := &db.PendingQuestion{TaskID: 42, Kind: db.QuestionConfirm}
	text := &db.PendingQuestion{TaskID: 42, Kind: db.QuestionText}

	tests := []struct {
		name        string
		q           *db.PendingQuestion
		args        []string
		other       string
		wantChoices []int
		wantOther   string
		wantErr     string
	}{
		{name: "number", q: choice, args: []string{"2"}, wantChoices: []int{2}},
		{name: "label ignores case", q: choice, args: []string{"redis"}, wantChoices: []int{1}},
		{name: "comma list", q: multi, args: []string{"1,3"}, wantChoices: []int{1, 3}},
		{name: "separate words", q: multi, args: []string{"1", "C"}, wantChoices: []int{1, 3}},
		{name: "confirm by word", q: confirm, args: []string{"yes"}, wantChoices: []int{1}},
		{name: "other flag", q: choice, other: "Postgres", wantOther: "Postgres"},
		{name: "text takes the words", q: text, args: []string{"use", "the", "staging", "bucket"}, wantOther: "use the staging bucket"},

		{name: "unknown label", q: choice, args: []string{"Postgres"}, wantErr: `no option "Postgres"`},
		{name: "text answered twice", q: text, args: []string{"one"}, other: "two", wantErr: "not both"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAnswerArgs(tt.q, tt.args, tt.other)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAnswerArgs: %v", err)
			}
			if !reflect.DeepEqual(got.Choices, tt.wantChoices) || got.Other != tt.wantOther {
				t.Errorf("got %+v, want choices %v other %q", got, tt.wantChoices, tt.wantOther)
			}
		})
	}
}

func TestFormatQuestionForCLI(t *testing.T) {
	q := &db.PendingQuestion{
		TaskID:     42,
		Question:   "Which cache backend?",
		Kind:       db.QuestionChoice,
		Options:    []db.QuestionOption{{Label: "Redis", Description: "already in prod"}, {Label: "Memcached"}},
		AllowOther: true,
	}
	out := formatQuestionForCLI(q)
	for _, want := range []string{"Task #42", "Which cache backend?", "1. Redis", "already in prod", "2. Memcached", "ty answer 42 <n>", "--other"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	confirm := formatQuestionForCLI(&db.PendingQuestion{TaskID: 7, Question: "Ship it?", Kind: db.QuestionConfirm})
	if !strings.Contains(confirm, "1. Yes") || !strings.Contains(confirm, "2. No") {
		t.Errorf("confirm not listed as Yes/No:\n%s", confirm)
	}
}

// askOnTask records a pending choice question on a (blocked) task.
func askOnTask(t *testing.T, database *db.DB, task *db.Task) *db.PendingQuestion {
	t.Helper()
	q := &db.PendingQuestion{
		TaskID:   task.ID,
		Question: "Which cache backend?",
		Kind:     db.QuestionChoice,
		Options:  []db.QuestionOption{{Label: "Redis"}, {Label: "Memcached"}},
	}
	if err := database.SetPendingQuestion(q); err != nil {
		t.Fatal(err)
	}
	return q
}

// An answer to a task placed on another host goes where `ty input` goes: over
// ssh to that host's tmux, as one paste followed by its Enter.
func TestAnswerReachesARemotelyPlacedTask(t *testing.T) {
	host := installFakeSSH(t, true)
	database, task := placedTask(t, "ik-agents")
	q := askOnTask(t, database, task)

	text, on, err := runTaskAnswer(context.Background(), database, task, q.ID, question.Response{Choices: []int{2}})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if text != "Selected: Memcached" || on != "ik-agents" {
		t.Errorf("answered %q on %q, want %q on ik-agents", text, on, "Selected: Memcached")
	}
	if pane := host.pane(t); pane != "Selected: Memcached\n[submitted]\n" {
		t.Fatalf("remote pane = %q, want the answer and its Enter", pane)
	}
	if got, _ := database.GetPendingQuestion(task.ID); got != nil {
		t.Errorf("question still pending after a delivered answer: %+v", got)
	}
}

// A remote agent that has gone keeps its question for another try, and the
// error names where ty looked.
func TestAnswerToAGoneRemoteAgentKeepsTheQuestion(t *testing.T) {
	installFakeSSH(t, false)
	database, task := placedTask(t, "ik-agents")
	q := askOnTask(t, database, task)

	_, _, err := runTaskAnswer(context.Background(), database, task, q.ID, question.Response{Choices: []int{1}})
	if !errors.Is(err, agentsend.ErrNoPane) {
		t.Fatalf("err = %v, want a no-pane error", err)
	}
	for _, want := range []string{"ik-agents", "task-daemon-remote-abc123"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if got, _ := database.GetPendingQuestion(task.ID); got == nil || got.ID != q.ID {
		t.Fatalf("question after a failed delivery = %+v, want %d restored", got, q.ID)
	}
}
