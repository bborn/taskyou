package db

import (
	"path/filepath"
	"testing"
)

func questionsDB(t *testing.T, status string) (*DB, int64) {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "questions.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&Project{Name: "storefront", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &Task{Title: "Pick the checkout cache", Status: status, Project: "storefront"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return database, task.ID
}

func choiceQuestion(taskID int64) *PendingQuestion {
	return &PendingQuestion{
		TaskID:   taskID,
		Question: "Which cache backend?",
		Kind:     QuestionChoice,
		Options: []QuestionOption{
			{Label: "Redis", Description: "already in prod"},
			{Label: "Memcached"},
		},
		AllowOther: true,
	}
}

func TestPendingQuestionRoundTrips(t *testing.T) {
	database, id := questionsDB(t, StatusBlocked)
	q := choiceQuestion(id)
	if err := database.SetPendingQuestion(q); err != nil {
		t.Fatalf("SetPendingQuestion: %v", err)
	}
	if q.ID == 0 {
		t.Fatal("SetPendingQuestion did not assign an id")
	}

	got, err := database.GetPendingQuestion(id)
	if err != nil {
		t.Fatalf("GetPendingQuestion: %v", err)
	}
	if got == nil {
		t.Fatal("no pending question on a blocked task that has one")
	}
	if got.ID != q.ID || got.Question != q.Question || got.Kind != QuestionChoice || !got.AllowOther {
		t.Errorf("round trip = %+v, want %+v", got, q)
	}
	if len(got.Options) != 2 || got.Options[0].Label != "Redis" || got.Options[0].Description != "already in prod" {
		t.Errorf("options = %+v", got.Options)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at not recorded")
	}
}

// Asking again replaces the question and gives it a new id, so an answer built
// against the first cannot be applied to the second.
func TestAskingAgainReplacesTheQuestion(t *testing.T) {
	database, id := questionsDB(t, StatusBlocked)
	first := choiceQuestion(id)
	database.SetPendingQuestion(first)
	second := &PendingQuestion{TaskID: id, Question: "Ship it?", Kind: QuestionConfirm}
	if err := database.SetPendingQuestion(second); err != nil {
		t.Fatalf("SetPendingQuestion: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("a new question kept the old question's id")
	}
	got, _ := database.GetPendingQuestion(id)
	if got == nil || got.ID != second.ID || got.Kind != QuestionConfirm {
		t.Fatalf("pending question = %+v, want the second", got)
	}
}

// A question only exists while the task is blocked on it: stored before the
// block lands, it is not shown; and leaving blocked clears it for good.
func TestPendingQuestionFollowsTheBlockedStatus(t *testing.T) {
	database, id := questionsDB(t, StatusProcessing)
	q := choiceQuestion(id)
	database.SetPendingQuestion(q)

	if got, _ := database.GetPendingQuestion(id); got != nil {
		t.Fatalf("question shown on a task that is not blocked: %+v", got)
	}

	if err := database.SetTaskStatus(id, StatusBlocked, ActorMCP, "asked", NoEvidence); err != nil {
		t.Fatalf("block: %v", err)
	}
	if got, _ := database.GetPendingQuestion(id); got == nil {
		t.Fatal("question not shown once the task blocked on it")
	}

	if err := database.SetTaskStatus(id, StatusProcessing, ActorHook, "prompt submitted", NoEvidence); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	if err := database.SetTaskStatus(id, StatusBlocked, ActorHook, "idle again", NoEvidence); err != nil {
		t.Fatalf("re-block: %v", err)
	}
	if got, _ := database.GetPendingQuestion(id); got != nil {
		t.Fatalf("an answered question came back when the task blocked again: %+v", got)
	}
}

// Two surfaces answering at once deliver one answer between them.
func TestClaimPendingQuestionOnlyOnce(t *testing.T) {
	database, id := questionsDB(t, StatusBlocked)
	q := choiceQuestion(id)
	database.SetPendingQuestion(q)

	ok, err := database.ClaimPendingQuestion(id, q.ID)
	if err != nil || !ok {
		t.Fatalf("first claim = %v, %v; want true", ok, err)
	}
	ok, err = database.ClaimPendingQuestion(id, q.ID)
	if err != nil || ok {
		t.Fatalf("second claim = %v, %v; want false", ok, err)
	}
	if got, _ := database.GetPendingQuestion(id); got != nil {
		t.Errorf("claimed question still pending: %+v", got)
	}
}

func TestClaimRefusesAReplacedQuestion(t *testing.T) {
	database, id := questionsDB(t, StatusBlocked)
	old := choiceQuestion(id)
	database.SetPendingQuestion(old)
	database.SetPendingQuestion(&PendingQuestion{TaskID: id, Question: "Ship it?", Kind: QuestionConfirm})

	if ok, _ := database.ClaimPendingQuestion(id, old.ID); ok {
		t.Fatal("claimed a question the agent had already replaced")
	}
	if got, _ := database.GetPendingQuestion(id); got == nil {
		t.Fatal("a refused claim took down the current question")
	}
}

// A delivery that failed puts the question back — unless the agent has asked
// something newer, or the task has moved on.
func TestRestorePendingQuestion(t *testing.T) {
	t.Run("restores after a failed delivery", func(t *testing.T) {
		database, id := questionsDB(t, StatusBlocked)
		q := choiceQuestion(id)
		database.SetPendingQuestion(q)
		database.ClaimPendingQuestion(id, q.ID)

		if err := database.RestorePendingQuestion(q); err != nil {
			t.Fatalf("RestorePendingQuestion: %v", err)
		}
		got, _ := database.GetPendingQuestion(id)
		if got == nil || got.ID != q.ID || len(got.Options) != 2 {
			t.Fatalf("restored question = %+v, want %d back", got, q.ID)
		}
	})

	t.Run("does not clobber a newer question", func(t *testing.T) {
		database, id := questionsDB(t, StatusBlocked)
		q := choiceQuestion(id)
		database.SetPendingQuestion(q)
		database.ClaimPendingQuestion(id, q.ID)
		newer := &PendingQuestion{TaskID: id, Question: "Ship it?", Kind: QuestionConfirm}
		database.SetPendingQuestion(newer)

		database.RestorePendingQuestion(q)
		got, _ := database.GetPendingQuestion(id)
		if got == nil || got.ID != newer.ID {
			t.Fatalf("pending question = %+v, want the newer one", got)
		}
	})

	t.Run("not onto a task that moved on", func(t *testing.T) {
		database, id := questionsDB(t, StatusBlocked)
		q := choiceQuestion(id)
		database.SetPendingQuestion(q)
		database.ClaimPendingQuestion(id, q.ID)
		database.SetTaskStatus(id, StatusProcessing, ActorHook, "prompt submitted", NoEvidence)

		database.RestorePendingQuestion(q)
		database.SetTaskStatus(id, StatusBlocked, ActorHook, "idle again", NoEvidence)
		if got, _ := database.GetPendingQuestion(id); got != nil {
			t.Fatalf("a question restored onto a task that had moved on: %+v", got)
		}
	})
}

func TestPendingQuestionsListsOnlyBlockedTasks(t *testing.T) {
	database, blocked := questionsDB(t, StatusBlocked)
	database.SetPendingQuestion(choiceQuestion(blocked))

	working := &Task{Title: "Still going", Status: StatusProcessing, Project: "storefront"}
	database.CreateTask(working)
	database.SetPendingQuestion(choiceQuestion(working.ID))

	all, err := database.PendingQuestions()
	if err != nil {
		t.Fatalf("PendingQuestions: %v", err)
	}
	if len(all) != 1 || all[blocked] == nil {
		t.Fatalf("PendingQuestions = %v, want only task %d", all, blocked)
	}
	if len(all[blocked].Options) != 2 {
		t.Errorf("options = %+v", all[blocked].Options)
	}
}
