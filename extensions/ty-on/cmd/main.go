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

Reads one JSON request on stdin, writes one JSON response on stdout.

    echo '{"event":"task.placement","task":{"project":"taskyou"}}' | ty-on
    echo '{"event":"task.hosts","task":{"project":"taskyou"}}' | ty-on

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

	emit(answer(context.Background(), os.Stdin))
}

// answer reads one request and produces the response for whichever question it
// asks: task.placement ("where does this task go") or task.hosts ("where could
// it go"). Anything unreadable is treated as a placement question and answered
// locally with an explanation — that caller is in the spawn path and must never
// be failed.
func answer(ctx context.Context, stdin io.Reader) any {
	req, unreadable, ok := decode(stdin)
	if !ok {
		return unreadable
	}

	resolver := placement.Resolver{Timeout: timeout()}
	if req.Event == placement.HostsEvent {
		// No probe and no ranking: a form is open and waiting, and every eligible
		// host is a legitimate answer to "which ones are there".
		return resolver.Hosts(req)
	}
	return resolver.Resolve(ctx, req)
}

// decode reads the request. A request that cannot be read at all is not a
// question anyone can answer, so it comes back as the local placement that every
// failure in this resolver becomes, and ok is false.
func decode(stdin io.Reader) (req placement.Request, unreadable placement.Response, ok bool) {
	body, err := io.ReadAll(io.LimitReader(stdin, maxRequest))
	if err != nil {
		return req, placement.Local("placement request could not be read: %v", err), false
	}
	if len(body) == 0 {
		return req, placement.Local("empty placement request"), false
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return req, placement.Local("placement request is not valid JSON: %v", err), false
	}
	return req, placement.Response{}, true
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

func emit(resp any) {
	out, err := json.Marshal(resp)
	if err != nil {
		// Response is three strings; this cannot fail in practice, but a
		// hand-written fallback still beats writing nothing at all.
		out = []byte(`{"target":"","workdir":"","reason":"placement response could not be encoded"}`)
	}
	fmt.Fprintf(os.Stdout, "%s\n", out)
}
