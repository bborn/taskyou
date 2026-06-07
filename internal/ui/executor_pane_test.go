package ui

import "testing"

func TestDecidePreviewAction(t *testing.T) {
	cases := []struct {
		name       string
		selectedID int64
		selHasExec bool
		joinedID   int64
		visible    bool
		want       previewAction
	}{
		{"hidden, nothing joined", 5, true, 0, false, previewNoop},
		{"hidden, something joined", 5, true, 5, false, previewCollapse},
		{"visible, no selection", 0, false, 0, true, previewNoop},
		{"visible, selected has no executor, none joined", 7, false, 0, true, previewNoop},
		{"visible, selected has no executor, stale joined", 7, false, 9, true, previewCollapse},
		{"visible, selected has executor, none joined", 5, true, 0, true, previewSwap},
		{"visible, selected has executor, different joined", 5, true, 9, true, previewSwap},
		{"visible, selected has executor, already joined", 5, true, 5, true, previewNoop},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decidePreviewAction(c.selectedID, c.selHasExec, c.joinedID, c.visible)
			if got != c.want {
				t.Fatalf("decidePreviewAction(%d,%v,%d,%v) = %v, want %v",
					c.selectedID, c.selHasExec, c.joinedID, c.visible, got, c.want)
			}
		})
	}
}

func TestValidatePanePct(t *testing.T) {
	cases := []struct{ in, def, want string }{
		{"", "45%", "45%"},
		{"garbage", "45%", "45%"},
		{"45", "45%", "45%"},  // missing %
		{"5%", "45%", "45%"},  // below min
		{"95%", "45%", "45%"}, // above max
		{"30%", "45%", "30%"}, // valid
		{"90%", "45%", "90%"}, // valid edge
		{"10%", "45%", "10%"}, // valid edge
	}
	for _, c := range cases {
		if got := validatePanePct(c.in, c.def); got != c.want {
			t.Fatalf("validatePanePct(%q,%q) = %q, want %q", c.in, c.def, got, c.want)
		}
	}
}
