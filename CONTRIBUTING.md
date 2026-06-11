# Contributing to Task You

Thanks for your interest in contributing!

## Development Setup

```bash
git clone https://github.com/bborn/taskyou
cd taskyou
make build
```

**Prerequisites:** Go 1.24+ (or use `mise install` to set up automatically).

## Common Make Targets

| Target | Description |
|--------|-------------|
| `make build` | Build `ty` and `taskd` binaries |
| `make test` | Run tests with race detector |
| `make lint` | Run golangci-lint |
| `make vet` | Run `go vet` |
| `make vuln` | Run `govulncheck` |
| `make audit` | Run vet + vuln + lint + test |
| `make fmt` | Format code |
| `make coverage` | Generate HTML coverage report |

## Submitting a Pull Request

1. Fork the repo and create your branch from `main`.
2. Make your changes.
3. Run `make audit` to verify everything passes.
4. Open a pull request with a clear description of the change.

## Code Style

- Follow standard Go conventions (`gofmt`, `goimports`).
- Keep PRs focused — one logical change per PR.
- Add tests for new functionality.

## Reporting Bugs

Open an issue at [github.com/bborn/taskyou/issues](https://github.com/bborn/taskyou/issues) with:
- Steps to reproduce
- Expected vs actual behavior
- Go version and OS
