// Command ty-on decides which host a TaskYou task should run on.
//
// It reads one JSON placement request on stdin and writes one JSON response on
// stdout:
//
//	$ echo '{"event":"task.placement","task":{"project":"taskyou"}}' | ty-on
//	{"target":"mona","workdir":"~/Projects/taskyou","reason":"only host serving taskyou"}
//
// An empty target means "run locally", and is the answer to every question this
// resolver cannot confidently answer. It exits 0 in all cases: it is called in
// the task spawn path and must never fail a task.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bborn/workflow/extensions/ty-on/internal/placement"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

// maxRequest caps how much stdin we will read. A placement request is a few
// hundred bytes; anything near this is a malformed caller.
const maxRequest = 1 << 20

const usage = `ty-on — placement resolver for TaskYou

Reads one JSON placement request on stdin, writes one JSON response on stdout.

    echo '{"event":"task.placement","task":{"project":"taskyou"}}' | ty-on

Reads the same host inventory as the "on" CLI: $ON_HOSTS, else
$XDG_CONFIG_HOME/on/hosts.yaml, else ~/.config/on/hosts.yaml.

Flags:
    -h, --help       show this help
    -v, --version    print the version

Environment:
    ON_HOSTS         host inventory path
    TY_ON_TIMEOUT    budget for the "on ls" probe (default 3s)
`

func main() {
	for _, arg := range os.Args[1:] {
		switch arg {
		case "-h", "--help", "help":
			fmt.Print(usage)
			return
		case "-v", "--version", "version":
			fmt.Println(version)
			return
		}
	}

	emit(resolve(context.Background(), os.Stdin))
}

// resolve turns whatever is on stdin into a placement response. Every failure
// mode becomes a local placement carrying an explanation.
func resolve(ctx context.Context, stdin io.Reader) placement.Response {
	body, err := io.ReadAll(io.LimitReader(stdin, maxRequest))
	if err != nil {
		return placement.Local("placement request could not be read: %v", err)
	}
	if len(body) == 0 {
		return placement.Local("empty placement request")
	}

	var req placement.Request
	if err := json.Unmarshal(body, &req); err != nil {
		return placement.Local("placement request is not valid JSON: %v", err)
	}

	return placement.Resolver{Timeout: timeout()}.Resolve(ctx, req)
}

// timeout reads the probe budget from TY_ON_TIMEOUT, falling back to the
// default when it is unset or nonsense.
func timeout() time.Duration {
	raw := os.Getenv("TY_ON_TIMEOUT")
	if raw == "" {
		return placement.DefaultTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return placement.DefaultTimeout
	}
	return d
}

func emit(resp placement.Response) {
	out, err := json.Marshal(resp)
	if err != nil {
		// Response is three strings; this cannot fail in practice, but a
		// hand-written fallback still beats writing nothing at all.
		out = []byte(`{"target":"","workdir":"","reason":"placement response could not be encoded"}`)
	}
	fmt.Fprintf(os.Stdout, "%s\n", out)
}
