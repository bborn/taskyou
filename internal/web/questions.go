package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bborn/workflow/internal/agentsend"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/question"
)

// questionJSON is a blocked task's pending question as the GUI renders it.
//
// Options are the answers to pick from, numbered from 1 in the order given — a
// confirm's are Yes and No, so every kind that offers answers is rendered and
// answered the same way. A text question has none.
type questionJSON struct {
	ID         int64               `json:"id"`
	Question   string              `json:"question"`
	Kind       string              `json:"kind"`
	Options    []db.QuestionOption `json:"options"`
	AllowOther bool                `json:"allow_other"`
	CreatedAt  string              `json:"created_at"`
}

func toQuestionJSON(q *db.PendingQuestion) *questionJSON {
	if q == nil {
		return nil
	}
	opts := question.Choices(q)
	if opts == nil {
		opts = []db.QuestionOption{}
	}
	return &questionJSON{
		ID:         q.ID,
		Question:   q.Question,
		Kind:       q.Kind,
		Options:    opts,
		AllowOther: q.AllowOther,
		CreatedAt:  apiTime(q.CreatedAt.Time),
	}
}

// attachQuestions fills in the pending question of every blocked task in
// result, with one query for the lot.
func (s *Server) attachQuestions(result []*taskJSON) {
	blocked := false
	for _, t := range result {
		if t.Status == db.StatusBlocked {
			blocked = true
			break
		}
	}
	if !blocked {
		return
	}
	questions, err := s.db.PendingQuestions()
	if err != nil {
		return // a card without its question still works; the reply box does
	}
	for _, t := range result {
		t.Question = toQuestionJSON(questions[t.ID])
	}
}

// handleGetQuestion returns the question a task is blocked on, or null.
func (s *Server) handleGetQuestion(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}
	q, err := s.db.GetPendingQuestion(task.ID)
	if err != nil {
		jsonErr(w, "failed to load question", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{"question": toQuestionJSON(q)})
}

type answerRequest struct {
	// QuestionID is the question the person was looking at. When set and the
	// agent has since asked something else, the answer is refused rather than
	// applied to a question it was not built for.
	QuestionID int64 `json:"question_id"`
	// Choices are picked option numbers, from 1.
	Choices []int `json:"choices"`
	// Other is an answer in the person's own words.
	Other string `json:"other"`
}

// handleAnswerQuestion answers a blocked task's pending question.
//
// The answer is checked against the question first; only a valid one resolves
// the agent's pane, and it is delivered by the same sender every typed reply
// uses (see resolveInputRoute), so it lands as one paste in the tagged pane.
func (s *Server) handleAnswerQuestion(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}
	var req answerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}

	deliver := func(p agentsend.Prompt) error {
		route, rerr := s.resolveInputRoute(r, task)
		if rerr != nil {
			return rerr
		}
		return route.target.send(route.sender, p)
	}
	text, err := question.Answer(s.db, task.ID, req.QuestionID,
		question.Response{Choices: req.Choices, Other: req.Other}, deliver)

	var invalid *question.InvalidError
	var rerr *routeError
	switch {
	case err == nil:
		jsonOK(w, map[string]interface{}{"ok": true, "answer": text})
	case errors.As(err, &invalid):
		jsonErrCode(w, invalid.Error(), "invalid_answer", http.StatusBadRequest)
	case errors.Is(err, question.ErrNoQuestion):
		jsonErrCode(w, "task #"+r.PathValue("id")+" is not waiting on a question", "no_question", http.StatusConflict)
	case errors.Is(err, question.ErrStale):
		jsonErrCode(w, err.Error(), "question_changed", http.StatusConflict)
	case errors.As(err, &rerr):
		jsonErr(w, rerr.msg, rerr.status)
	default:
		writeSendErr(w, err, "failed to deliver answer")
	}
}
