package ui

import (
	"strings"
	"testing"
)

func TestMatchTerminalApp(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"term_program from iTerm", "iTerm.app", "iTerm"},
		{"iTerm restoration server path", "/Users/x/Library/Application Support/iTerm2/iTermServer-3.6.6", "iTerm"},
		{"term_program from Terminal.app", "Apple_Terminal", "Terminal"},
		{"Terminal.app executable path", "/System/Applications/Utilities/Terminal.app/Contents/MacOS/Terminal", "Terminal"},
		{"ghostty", "ghostty", "Ghostty"},
		{"tmux is not a terminal", "tmux", ""},
		{"empty", "", ""},
		{"unrelated process", "/usr/bin/login", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchTerminalApp(tt.input); got != tt.want {
				t.Errorf("matchTerminalApp(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// psSample is the shape `ps -axo pid=,ppid=,comm=` prints on macOS. The iTerm
// row matters most: its path contains spaces, so a naive field split loses it.
const psSample = `  9684 19536 ty
 19536 19534 -zsh
 19534  2187 /usr/bin/login
  2187     1 /Users/bruno/Library/Application Support/iTerm2/iTermServer-3.6.6
     1     0 /sbin/launchd
`

func TestParseProcessTableKeepsPathsWithSpaces(t *testing.T) {
	table := parseProcessTable(psSample)

	if len(table) != 5 {
		t.Fatalf("parsed %d rows, want 5", len(table))
	}
	got, ok := table[2187]
	if !ok {
		t.Fatal("pid 2187 missing from table")
	}
	if got.ppid != 1 {
		t.Errorf("ppid = %d, want 1", got.ppid)
	}
	want := "/Users/bruno/Library/Application Support/iTerm2/iTermServer-3.6.6"
	if got.comm != want {
		t.Errorf("comm = %q, want %q", got.comm, want)
	}
}

func TestParseProcessTableSkipsGarbage(t *testing.T) {
	table := parseProcessTable("\nnot a row\n  12 nope\n  42 7 /bin/sh\n")
	if len(table) != 1 {
		t.Fatalf("parsed %d rows, want 1", len(table))
	}
	if table[42].comm != "/bin/sh" {
		t.Errorf("comm = %q, want /bin/sh", table[42].comm)
	}
}

func TestParsePIDList(t *testing.T) {
	got := parsePIDList("  9684\n 19536\n\nbogus\n 19534\n")
	want := []int{9684, 19536, 19534}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestTerminalInAncestryWalksPastShellAndLogin(t *testing.T) {
	table := parseProcessTable(psSample)
	// Start where ps -t <tty> starts us: the process running on the client tty.
	if got := terminalInAncestry([]int{9684}, table); got != "iTerm" {
		t.Errorf("terminalInAncestry = %q, want iTerm", got)
	}
}

func TestTerminalInAncestryNoTerminal(t *testing.T) {
	table := parseProcessTable("  42 7 /bin/sh\n   7 1 /usr/bin/login\n")
	if got := terminalInAncestry([]int{42}, table); got != "" {
		t.Errorf("terminalInAncestry = %q, want empty", got)
	}
}

func TestTerminalInAncestryStopsOnCycle(t *testing.T) {
	// A parent cycle must not spin; the hop bound ends the walk.
	table := map[int]procInfo{
		10: {ppid: 11, comm: "/bin/a"},
		11: {ppid: 10, comm: "/bin/b"},
	}
	if got := terminalInAncestry([]int{10}, table); got != "" {
		t.Errorf("terminalInAncestry = %q, want empty", got)
	}
}

func TestShellSingleQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/tmp/plain", "'/tmp/plain'"},
		{"/tmp/with space", "'/tmp/with space'"},
		{"/tmp/it's", `'/tmp/it'\''s'`},
	}
	for _, tt := range tests {
		if got := shellSingleQuote(tt.in); got != tt.want {
			t.Errorf("shellSingleQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAppleScriptQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{`plain`, `"plain"`},
		{`say "hi"`, `"say \"hi\""`},
		{`back\slash`, `"back\\slash"`},
	}
	for _, tt := range tests {
		if got := appleScriptQuote(tt.in); got != tt.want {
			t.Errorf("appleScriptQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestITermNewTabScriptQuotesAwkwardPaths(t *testing.T) {
	script := iTermNewTabScript(`/tmp/a dir/it's "quoted"`)

	// The cd must survive both layers: a shell-safe word inside an AppleScript
	// string literal. An unescaped quote here would end the literal early and
	// make osascript fail to compile the script.
	wantCd := `"cd '/tmp/a dir/it'\\''s \"quoted\"'"`
	if !strings.Contains(script, wantCd) {
		t.Errorf("script does not contain %s\ngot:\n%s", wantCd, script)
	}
	if !strings.Contains(script, "create tab with default profile") {
		t.Error("script does not create a tab")
	}
}

func TestBuildTerminalCommand(t *testing.T) {
	tests := []struct {
		name     string
		app      string
		wantArgv []string // nil means "check the program only"
		wantProg string
	}{
		{name: "iTerm is scripted", app: "iTerm", wantProg: "osascript"},
		{name: "Terminal is scripted", app: "Terminal", wantProg: "osascript"},
		{
			name:     "unknown terminal goes through open",
			app:      "Ghostty",
			wantProg: "open",
			wantArgv: []string{"open", "-a", "Ghostty", "/tmp/work"},
		},
		{
			name:     "undetected falls back to Terminal",
			app:      "",
			wantProg: "open",
			wantArgv: []string{"open", "-a", "Terminal", "/tmp/work"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := buildTerminalCommand(tt.app, "/tmp/work")
			if !strings.HasSuffix(cmd.Path, tt.wantProg) {
				t.Errorf("program = %q, want %q", cmd.Path, tt.wantProg)
			}
			if tt.wantArgv == nil {
				return
			}
			if len(cmd.Args) != len(tt.wantArgv) {
				t.Fatalf("args = %v, want %v", cmd.Args, tt.wantArgv)
			}
			for i := range tt.wantArgv {
				if cmd.Args[i] != tt.wantArgv[i] {
					t.Fatalf("args = %v, want %v", cmd.Args, tt.wantArgv)
				}
			}
		})
	}
}

func TestBuildTerminalCommandScriptMentionsWorkdir(t *testing.T) {
	cmd := buildTerminalCommand("iTerm", "/tmp/work")
	if len(cmd.Args) != 3 || cmd.Args[1] != "-e" {
		t.Fatalf("expected osascript -e <script>, got %v", cmd.Args)
	}
	if !strings.Contains(cmd.Args[2], "/tmp/work") {
		t.Errorf("script does not mention the workdir: %s", cmd.Args[2])
	}
}
