package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
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
