# TODOs

## Homebrew Tap

**What:** Create a `homebrew-tap` repo (`bborn/homebrew-tap`) so users can `brew install bborn/tap/ty`.

**Why:** The install script (`curl | bash`) works but Homebrew is the standard macOS distribution channel. It gives users automatic updates via `brew upgrade`, dependency management, and trust signals (checksums verified by Homebrew).

**Context:** GoReleaser can auto-generate Homebrew formulae via the `brews:` config section in `.goreleaser.yaml`. This requires:
1. A separate GitHub repo `bborn/homebrew-tap` with a `Formula/` directory.
2. A `brews:` block in `.goreleaser.yaml` pointing to that repo.
3. A GitHub token with write access to the tap repo (configured as a secret in the main repo).

GoReleaser docs: https://goreleaser.com/customization/homebrew/

**Depends on:** GoReleaser release pipeline working end-to-end (this PR).
