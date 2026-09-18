// Package question validates and answers the structured questions an agent
// asks through taskyou_needs_input.
//
// An agent can offer answers — pick one, pick several, yes or no — so a person
// can reply to a blocked task with a tap on their phone or a keypress in the
// TUI instead of typing. The MCP tool, the HTTP API, the TUI and `ty answer`
// all come through here: the rules for what a question may look like, and the
// words the agent reads back, are the same whichever surface was used.
package question

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bborn/workflow/internal/agentsend"
	"github.com/bborn/workflow/internal/db"
)

// Limits on an offered answer. Options are buttons on a phone and lines in a
// terminal: past six the question wants rewording, and a label that needs more
// than a short phrase belongs in its description.
const (
	MinOptions        = 2
	MaxOptions        = 6
	MaxLabelLen       = 100
	MaxDescriptionLen = 500
)

// Kinds lists the valid question kinds, in the order the tool documents them.
func Kinds() []string {
	return []string{db.QuestionText, db.QuestionChoice, db.QuestionMultiChoice, db.QuestionConfirm}
}

// Yes and No are the two answers to a confirm question. They are its options,
// so a confirm is answered exactly like a choice: option 1 or option 2.
const (
	Yes = "Yes"
	No  = "No"
)

// Normalize checks q and fills in its defaults. The errors are written for the
// agent that asked: they say what to change.
//
// A question with no kind is a text question, unless it offers options — then
// it is a choice, which is what an agent listing options almost always means.
func Normalize(q *db.PendingQuestion) error {
	q.Question = strings.TrimSpace(q.Question)
	if q.Question == "" {
		return errors.New("question is required")
	}
	q.Kind = strings.TrimSpace(strings.ToLower(q.Kind))
	if q.Kind == "" {
		q.Kind = db.QuestionText
		if len(q.Options) > 0 {
			q.Kind = db.QuestionChoice
		}
	}

	switch q.Kind {
	case db.QuestionText:
		if len(q.Options) > 0 {
			return errors.New("a text question takes no options; use kind \"choice\" or \"multi_choice\" to offer answers")
		}
	case db.QuestionConfirm:
		if len(q.Options) > 0 {
			return errors.New("a confirm question is answered Yes or No, so it takes no options; use kind \"choice\" to offer your own")
		}
	case db.QuestionChoice, db.QuestionMultiChoice:
		if len(q.Options) < MinOptions || len(q.Options) > MaxOptions {
			return fmt.Errorf("a %s question needs %d to %d options (got %d)", q.Kind, MinOptions, MaxOptions, len(q.Options))
		}
		seen := map[string]int{}
		for i := range q.Options {
			o := &q.Options[i]
			o.Label = strings.TrimSpace(o.Label)
			o.Description = strings.TrimSpace(o.Description)
			if o.Label == "" {
				return fmt.Errorf("option %d has no label", i+1)
			}
			if n := utf8.RuneCountInString(o.Label); n > MaxLabelLen {
				return fmt.Errorf("option %d's label is %d characters; keep labels under %d and put detail in its description", i+1, n, MaxLabelLen)
			}
			if n := utf8.RuneCountInString(o.Description); n > MaxDescriptionLen {
				return fmt.Errorf("option %d's description is %d characters; keep it under %d", i+1, n, MaxDescriptionLen)
			}
			key := strings.ToLower(o.Label)
			if prev, dup := seen[key]; dup {
				return fmt.Errorf("options %d and %d have the same label %q; labels are what the answer reports, so they must differ", prev, i+1, o.Label)
			}
			seen[key] = i + 1
		}
	default:
		return fmt.Errorf("unknown kind %q; use one of %s", q.Kind, strings.Join(Kinds(), ", "))
	}
	return nil
}

// Choices returns the answers a person picks between: the agent's options, or
// Yes and No for a confirm. Numbered from 1 on every surface.
func Choices(q *db.PendingQuestion) []db.QuestionOption {
	if q.Kind == db.QuestionConfirm {
		return []db.QuestionOption{{Label: Yes}, {Label: No}}
	}
	return q.Options
}

// IsStructured reports whether q offers answers to pick from, as opposed to a
// plain question answered in the person's own words.
func IsStructured(q *db.PendingQuestion) bool {
	return q != nil && q.Kind != db.QuestionText && q.Kind != ""
}

// TakesText reports whether q can be answered in the person's own words.
func TakesText(q *db.PendingQuestion) bool {
	return !IsStructured(q) || q.AllowOther
}

// Response is a person's answer.
type Response struct {
	// Choices are the picked answers by 1-based number, as every surface shows
	// them. One for choice and confirm; any number for multi_choice.
	Choices []int `json:"choices,omitempty"`
	// Other is an answer in the person's own words: the whole answer to a text
	// question, or the "Other" field of a question that allows one.
	Other string `json:"other,omitempty"`
}

// InvalidError is an answer that does not fit its question — a number out of
// range, two picks for a single choice. Surfaces show it and let the person
// try again; nothing was sent.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalid(format string, args ...interface{}) error {
	return &InvalidError{msg: fmt.Sprintf(format, args...)}
}

// Format turns r into the text the agent reads: "Selected: <label>" for a
// choice, "Selected: A, C" for several, "Yes" or "No" for a confirm, and the
// person's own words as they wrote them.
func Format(q *db.PendingQuestion, r Response) (string, error) {
	other := strings.TrimSpace(r.Other)

	if !IsStructured(q) {
		if len(r.Choices) > 0 {
			return "", invalid("this question has no options to pick; answer it in your own words")
		}
		if other == "" {
			return "", invalid("an answer is required")
		}
		return other, nil
	}

	if other != "" && !q.AllowOther {
		return "", invalid("this question only takes one of its options")
	}

	choices := Choices(q)
	picked := make([]int, 0, len(r.Choices))
	seen := map[int]bool{}
	for _, n := range r.Choices {
		if n < 1 || n > len(choices) {
			return "", invalid("option %d does not exist; pick 1 to %d", n, len(choices))
		}
		if !seen[n] {
			seen[n] = true
			picked = append(picked, n)
		}
	}

	if len(picked) == 0 && other == "" {
		if q.Kind == db.QuestionMultiChoice {
			return "", invalid("pick at least one option")
		}
		return "", invalid("pick an option")
	}

	if q.Kind != db.QuestionMultiChoice {
		if len(picked) > 1 {
			return "", invalid("pick one option, not %d", len(picked))
		}
		if len(picked) == 1 && other != "" {
			return "", invalid("pick an option or answer in your own words, not both")
		}
	}

	if len(picked) == 0 {
		return other, nil
	}
	if q.Kind == db.QuestionConfirm {
		return choices[picked[0]-1].Label, nil
	}

	// Report picks in the order the agent offered them, not the order they
	// were tapped: "A, C" reads the same however the person got there.
	sort.Ints(picked)
	labels := make([]string, len(picked))
	for i, n := range picked {
		labels[i] = choices[n-1].Label
	}
	text := "Selected: " + strings.Join(labels, ", ")
	if other != "" {
		text += "; Other: " + other
	}
	return text, nil
}

// ErrNoQuestion means the task is not waiting on a question: it was never
// asked, it has been answered, or the task has moved on.
var ErrNoQuestion = errors.New("task has no pending question")

// ErrStale means the answer was built against a question that is no longer the
// pending one: another surface answered it first, or the agent asked again.
var ErrStale = errors.New("the question was answered or replaced; reload it and answer again")

// Store is the slice of the database answering needs.
type Store interface {
	GetPendingQuestion(taskID int64) (*db.PendingQuestion, error)
	ClaimPendingQuestion(taskID, id int64) (bool, error)
	RestorePendingQuestion(q *db.PendingQuestion) error
	AppendTaskLog(taskID int64, lineType, content string) error
}

// Deliver sends a prompt to the task's agent. Every caller passes the delivery
// it already uses for typed replies (agentsend: the tagged pane, one bracketed
// paste, the busy check), so an answer travels exactly the way a typed reply
// does.
type Deliver func(agentsend.Prompt) error

// Answer delivers r as the answer to the task's pending question and returns
// the text the agent was sent.
//
// questionID, when non-zero, is the question the person was looking at; if the
// agent has asked something else since, the answer is refused with ErrStale
// rather than applied to a question it was not built for.
//
// The question is claimed before the text goes out, so two surfaces answering
// at once deliver one answer between them. If the delivery fails the question
// is put back for another try.
func Answer(store Store, taskID, questionID int64, r Response, deliver Deliver) (string, error) {
	q, err := store.GetPendingQuestion(taskID)
	if err != nil {
		return "", err
	}
	if q == nil {
		return "", ErrNoQuestion
	}
	if questionID != 0 && q.ID != questionID {
		return "", ErrStale
	}
	text, err := Format(q, r)
	if err != nil {
		return "", err
	}

	claimed, err := store.ClaimPendingQuestion(taskID, q.ID)
	if err != nil {
		return "", err
	}
	if !claimed {
		return "", ErrStale
	}
	if err := deliver(agentsend.Prompt{TaskID: taskID, Text: text, Submit: true}); err != nil {
		if rerr := store.RestorePendingQuestion(q); rerr != nil {
			return "", fmt.Errorf("%w (and the question could not be restored: %v)", err, rerr)
		}
		return "", err
	}
	// "Replied" is what the TUI's prompt tracking reads as a person having
	// answered (see loadChoicePrompt). Best effort: the answer is delivered.
	_ = store.AppendTaskLog(taskID, "user", "Replied: "+text)
	return text, nil
}
