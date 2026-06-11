# ty-web

Web kanban board for TaskYou. Serves a browser UI that talks to the `ty serve` HTTP API.

## Prerequisites

`ty serve` must be running:

```bash
ty serve --addr :8080
```

## Install

```bash
go install github.com/bborn/workflow/extensions/ty-web/cmd@latest
```

Or build locally:

```bash
cd extensions/ty-web
go build -o ty-web ./cmd
```

## Usage

```bash
ty-web --port 3000 --api http://localhost:8080
```

Then open http://localhost:3000 in your browser.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `3000` | Port for the web UI |
| `--api` | `http://localhost:8080` | URL of the `ty serve` API |

## How It Works

ty-web is a simple static file server with a reverse proxy:

- Serves the embedded kanban board UI at `/`
- Proxies all `/api/*` requests to the `ty serve` backend
- No external dependencies, single binary
