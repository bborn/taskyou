package db

import "testing"

func TestQuestionOptionsFor(t *testing.T) {
	opts := `[{"label":"Redis","description":"already in prod"},{"label":"Memcached"}]`
	chronological := []*TaskLog{
		{ID: 1, LineType: "tool", Content: "Bash: go test"},
		{ID: 2, LineType: LogQuestionOptions, Content: opts},
		{ID: 3, LineType: "question", Content: "Which cache backend?"},
		{ID: 4, LineType: "question", Content: "And the TTL?"},
	}
	if got := QuestionOptionsFor(chronological, 2); len(got) != 2 || got[1].Label != "Memcached" {
		t.Errorf("chronological: got %+v", got)
	}
	newestFirst := []*TaskLog{chronological[3], chronological[2], chronological[1], chronological[0]}
	if got := QuestionOptionsFor(newestFirst, 1); len(got) != 2 || got[0].Description != "already in prod" {
		t.Errorf("newest first: got %+v", got)
	}
	// The later question offered nothing; the options belong to the one after them.
	if got := QuestionOptionsFor(chronological, 3); got != nil {
		t.Errorf("a plain question borrowed the previous one's options: %+v", got)
	}
	if got := QuestionOptionsFor(newestFirst, 0); got != nil {
		t.Errorf("newest first, plain question: %+v", got)
	}
	if got := FormatQuestionOptions(QuestionOptionsFor(chronological, 2)); got != "1. Redis · 2. Memcached" {
		t.Errorf("FormatQuestionOptions = %q", got)
	}
	if ParseQuestionOptions("not json") != nil {
		t.Error("parsed garbage")
	}
}
