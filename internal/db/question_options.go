package db

import (
	"encoding/json"
	"fmt"
	"strings"
)

// LogQuestionOptions is the line type of the answers an agent offered with a
// taskyou_needs_input question: a JSON array of QuestionOption. It is written
// immediately BEFORE the question's own "question" line, so everything that
// looks for the latest question, or the latest line, keeps finding the question
// itself.
const LogQuestionOptions = "question_options"

// QuestionOption is one answer an agent offered. Picking it sends Label to the
// agent as the reply.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// ParseQuestionOptions decodes a LogQuestionOptions line, or returns nil.
func ParseQuestionOptions(content string) []QuestionOption {
	var opts []QuestionOption
	if err := json.Unmarshal([]byte(content), &opts); err != nil {
		return nil
	}
	return opts
}

// QuestionOptionsFor returns the options offered with the question at logs[i]:
// the options line written just before it, which sits next to it whichever
// order the slice is in. Nil when the question offered none.
func QuestionOptionsFor(logs []*TaskLog, i int) []QuestionOption {
	for _, j := range []int{i - 1, i + 1} {
		if j >= 0 && j < len(logs) && logs[j].LineType == LogQuestionOptions && logs[j].ID < logs[i].ID {
			return ParseQuestionOptions(logs[j].Content)
		}
	}
	return nil
}

// FormatQuestionOptions renders offered options on one line for a terminal:
// "1. Redis · 2. Memcached".
func FormatQuestionOptions(opts []QuestionOption) string {
	parts := make([]string, len(opts))
	for i, o := range opts {
		parts[i] = fmt.Sprintf("%d. %s", i+1, o.Label)
	}
	return strings.Join(parts, " · ")
}
