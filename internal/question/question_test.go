package question

import (
	"errors"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/agentsend"
	"github.com/bborn/workflow/internal/db"
)

func opts(labels ...string) []db.QuestionOption {
	out := make([]db.QuestionOption, len(labels))
	for i, l := range labels {
		out[i] = db.QuestionOption{Label: l}
	}
	return out
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name     string
		q        db.PendingQuestion
		wantKind string
		wantErr  string
	}{
		{name: "plain question is text", q: db.PendingQuestion{Question: "What next?"}, wantKind: db.QuestionText},
		{name: "options without a kind are a choice", q: db.PendingQuestion{Question: "Which?", Options: opts("A", "B")}, wantKind: db.QuestionChoice},
		{name: "kind is case-insensitive", q: db.PendingQuestion{Question: "Which?", Kind: "Multi_Choice", Options: opts("A", "B")}, wantKind: db.QuestionMultiChoice},
		{name: "confirm needs no options", q: db.PendingQuestion{Question: "Ship it?", Kind: "confirm"}, wantKind: db.QuestionConfirm},
		{name: "six options is the limit", q: db.PendingQuestion{Question: "Which?", Kind: "choice", Options: opts("A", "B", "C", "D", "E", "F")}, wantKind: db.QuestionChoice},

		{name: "question required", q: db.PendingQuestion{Question: "  "}, wantErr: "question is required"},
		{name: "choice with one option", q: db.PendingQuestion{Question: "Which?", Kind: "choice", Options: opts("A")}, wantErr: "needs 2 to 6 options (got 1)"},
		{name: "choice with no options", q: db.PendingQuestion{Question: "Which?", Kind: "choice"}, wantErr: "needs 2 to 6 options (got 0)"},
		{name: "too many options", q: db.PendingQuestion{Question: "Which?", Kind: "multi_choice", Options: opts("A", "B", "C", "D", "E", "F", "G")}, wantErr: "needs 2 to 6 options (got 7)"},
		{name: "blank label", q: db.PendingQuestion{Question: "Which?", Kind: "choice", Options: opts("A", " ")}, wantErr: "option 2 has no label"},
		{name: "duplicate labels", q: db.PendingQuestion{Question: "Which?", Kind: "choice", Options: opts("Redis", "redis")}, wantErr: "same label"},
		{name: "overlong label", q: db.PendingQuestion{Question: "Which?", Kind: "choice", Options: opts("A", strings.Repeat("x", MaxLabelLen+1))}, wantErr: "option 2's label"},
		{name: "text with options", q: db.PendingQuestion{Question: "What?", Kind: "text", Options: opts("A", "B")}, wantErr: "text question takes no options"},
		{name: "confirm with options", q: db.PendingQuestion{Question: "Ok?", Kind: "confirm", Options: opts("Sure", "Nope")}, wantErr: "answered Yes or No"},
		{name: "unknown kind", q: db.PendingQuestion{Question: "Which?", Kind: "ranking", Options: opts("A", "B")}, wantErr: `unknown kind "ranking"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := tt.q
			err := Normalize(&q)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Normalize error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if q.Kind != tt.wantKind {
				t.Errorf("kind = %q, want %q", q.Kind, tt.wantKind)
			}
		})
	}
}

func TestNormalizeTrimsLabels(t *testing.T) {
	q := db.PendingQuestion{Question: "  Which?  ", Options: []db.QuestionOption{{Label: " Redis ", Description: " fast "}, {Label: "Memcached"}}}
	if err := Normalize(&q); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if q.Question != "Which?" || q.Options[0].Label != "Redis" || q.Options[0].Description != "fast" {
		t.Errorf("not trimmed: %+v", q)
	}
}

func TestFormat(t *testing.T) {
	choice := &db.PendingQuestion{Kind: db.QuestionChoice, Options: opts("Redis", "Memcached", "LRU")}
	choiceOther := &db.PendingQuestion{Kind: db.QuestionChoice, Options: opts("Redis", "Memcached"), AllowOther: true}
	multi := &db.PendingQuestion{Kind: db.QuestionMultiChoice, Options: opts("A", "B", "C"), AllowOther: true}
	confirm := &db.PendingQuestion{Kind: db.QuestionConfirm}
	text := &db.PendingQuestion{Kind: db.QuestionText}

	tests := []struct {
		name    string
		q       *db.PendingQuestion
		r       Response
		want    string
		wantErr string
	}{
		{name: "choice", q: choice, r: Response{Choices: []int{2}}, want: "Selected: Memcached"},
		{name: "multi in option order", q: multi, r: Response{Choices: []int{3, 1}}, want: "Selected: A, C"},
		{name: "multi repeated pick counts once", q: multi, r: Response{Choices: []int{1, 1}}, want: "Selected: A"},
		{name: "multi with other", q: multi, r: Response{Choices: []int{2}, Other: "and D"}, want: "Selected: B; Other: and D"},
		{name: "multi other alone", q: multi, r: Response{Other: "none of these"}, want: "none of these"},
		{name: "confirm yes", q: confirm, r: Response{Choices: []int{1}}, want: "Yes"},
		{name: "confirm no", q: confirm, r: Response{Choices: []int{2}}, want: "No"},
		{name: "other as written", q: choiceOther, r: Response{Other: "  Postgres, actually  "}, want: "Postgres, actually"},
		{name: "text", q: text, r: Response{Other: "use the staging bucket"}, want: "use the staging bucket"},

		{name: "out of range", q: choice, r: Response{Choices: []int{4}}, wantErr: "option 4 does not exist"},
		{name: "zero", q: choice, r: Response{Choices: []int{0}}, wantErr: "option 0 does not exist"},
		{name: "two for a choice", q: choice, r: Response{Choices: []int{1, 2}}, wantErr: "pick one option"},
		{name: "nothing picked", q: choice, r: Response{}, wantErr: "pick an option"},
		{name: "nothing picked multi", q: multi, r: Response{}, wantErr: "pick at least one"},
		{name: "other not allowed", q: choice, r: Response{Other: "Postgres"}, wantErr: "only takes one of its options"},
		{name: "pick and other on a choice", q: choiceOther, r: Response{Choices: []int{1}, Other: "x"}, wantErr: "not both"},
		{name: "confirm out of range", q: confirm, r: Response{Choices: []int{3}}, wantErr: "pick 1 to 2"},
		{name: "text needs words", q: text, r: Response{Other: " "}, wantErr: "answer is required"},
		{name: "text has no options", q: text, r: Response{Choices: []int{1}}, wantErr: "no options"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Format(tt.q, tt.r)
			if tt.wantErr != "" {
				var invalid *InvalidError
				if !errors.As(err, &invalid) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Format error = %v, want an InvalidError containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			if got != tt.want {
				t.Errorf("Format = %q, want %q", got, tt.want)
			}
		})
	}
}

// fakeStore records what Answer did to the question.
type fakeStore struct {
	q         *db.PendingQuestion
	claimed   bool
	restored  bool
	claimFail bool // someone else claimed it first
	logs      []string
}

func (s *fakeStore) GetPendingQuestion(int64) (*db.PendingQuestion, error) {
	if s.claimed {
		return nil, nil
	}
	return s.q, nil
}

func (s *fakeStore) ClaimPendingQuestion(_, id int64) (bool, error) {
	if s.claimFail || s.claimed || s.q == nil || s.q.ID != id {
		return false, nil
	}
	s.claimed = true
	return true, nil
}

func (s *fakeStore) RestorePendingQuestion(*db.PendingQuestion) error {
	s.claimed = false
	s.restored = true
	return nil
}

func (s *fakeStore) AppendTaskLog(_ int64, lineType, content string) error {
	s.logs = append(s.logs, lineType+": "+content)
	return nil
}

func cacheQuestion() *db.PendingQuestion {
	return &db.PendingQuestion{ID: 7, TaskID: 42, Question: "Which cache?", Kind: db.QuestionChoice, Options: opts("Redis", "Memcached")}
}

func TestAnswerDeliversThroughTheCallersSender(t *testing.T) {
	store := &fakeStore{q: cacheQuestion()}
	var sent []agentsend.Prompt
	text, err := Answer(store, 42, 7, Response{Choices: []int{1}}, func(p agentsend.Prompt) error {
		sent = append(sent, p)
		return nil
	})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if text != "Selected: Redis" {
		t.Errorf("text = %q", text)
	}
	if len(sent) != 1 || sent[0].TaskID != 42 || sent[0].Text != "Selected: Redis" || !sent[0].Submit || sent[0].Force {
		t.Fatalf("delivered %+v, want one submitted, unforced prompt", sent)
	}
	if !store.claimed {
		t.Error("the question was not claimed")
	}
	if len(store.logs) != 1 || store.logs[0] != "user: Replied: Selected: Redis" {
		t.Errorf("logs = %v", store.logs)
	}
}

func TestAnswerSendsNothingWhenItCannotApply(t *testing.T) {
	deliverNever := func(t *testing.T) Deliver {
		return func(p agentsend.Prompt) error {
			t.Fatalf("delivered %+v; nothing should have been sent", p)
			return nil
		}
	}
	t.Run("no question", func(t *testing.T) {
		_, err := Answer(&fakeStore{}, 42, 0, Response{Choices: []int{1}}, deliverNever(t))
		if !errors.Is(err, ErrNoQuestion) {
			t.Fatalf("err = %v, want ErrNoQuestion", err)
		}
	})
	t.Run("stale question id", func(t *testing.T) {
		_, err := Answer(&fakeStore{q: cacheQuestion()}, 42, 6, Response{Choices: []int{1}}, deliverNever(t))
		if !errors.Is(err, ErrStale) {
			t.Fatalf("err = %v, want ErrStale", err)
		}
	})
	t.Run("answered elsewhere first", func(t *testing.T) {
		_, err := Answer(&fakeStore{q: cacheQuestion(), claimFail: true}, 42, 7, Response{Choices: []int{1}}, deliverNever(t))
		if !errors.Is(err, ErrStale) {
			t.Fatalf("err = %v, want ErrStale", err)
		}
	})
	t.Run("invalid answer", func(t *testing.T) {
		store := &fakeStore{q: cacheQuestion()}
		_, err := Answer(store, 42, 7, Response{Choices: []int{3}}, deliverNever(t))
		var invalid *InvalidError
		if !errors.As(err, &invalid) {
			t.Fatalf("err = %v, want InvalidError", err)
		}
		if store.claimed {
			t.Error("an invalid answer claimed the question")
		}
	})
}

func TestAnswerRestoresTheQuestionWhenDeliveryFails(t *testing.T) {
	store := &fakeStore{q: cacheQuestion()}
	_, err := Answer(store, 42, 7, Response{Choices: []int{2}}, func(agentsend.Prompt) error {
		return &agentsend.NoPaneError{TaskID: 42}
	})
	if !errors.Is(err, agentsend.ErrNoPane) {
		t.Fatalf("err = %v, want the delivery error", err)
	}
	if !store.restored {
		t.Error("the question was not put back after a failed delivery")
	}
	if len(store.logs) != 0 {
		t.Errorf("an undelivered answer was logged as a reply: %v", store.logs)
	}
}
