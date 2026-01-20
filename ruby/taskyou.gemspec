# frozen_string_literal: true

require_relative "lib/taskyou/version"

Gem::Specification.new do |spec|
  spec.name = "taskyou"
  spec.version = Taskyou::VERSION
  spec.authors = ["TaskYou Contributors"]
  spec.email = ["hello@taskyou.dev"]

  spec.summary = "A task queue for AI coding agents"
  spec.description = "TaskYou is a terminal-based task management system designed for AI coding agents like Claude Code and Codex."
  spec.homepage = "https://github.com/bborn/taskyou"
  spec.license = "MIT"
  spec.required_ruby_version = ">= 3.2.0"

  spec.metadata["homepage_uri"] = spec.homepage
  spec.metadata["source_code_uri"] = spec.homepage
  spec.metadata["changelog_uri"] = "#{spec.homepage}/blob/main/CHANGELOG.md"

  spec.files = Dir.glob("{exe,lib}/**/*") + %w[LICENSE.txt README.md]
  spec.bindir = "exe"
  spec.executables = %w[task taskd]
  spec.require_paths = ["lib"]

  # Core TUI framework (charm-ruby ecosystem)
  spec.add_dependency "bubbletea", "~> 0.1"
  spec.add_dependency "bubbles", "~> 0.1"
  spec.add_dependency "lipgloss", "~> 0.2"
  spec.add_dependency "glamour", "~> 0.2"
  spec.add_dependency "huh", "~> 1.0"

  # Database
  spec.add_dependency "sqlite3", "~> 2.0"

  # CLI framework
  spec.add_dependency "thor", "~> 1.3"

  # SSH server (for remote TUI)
  spec.add_dependency "net-ssh", "~> 7.2"

  # GitHub integration
  spec.add_dependency "octokit", "~> 9.0"

  # Fuzzy matching
  spec.add_dependency "fuzzy-string-match", "~> 1.0"

  # HTTP client for autocomplete API
  spec.add_dependency "httpx", "~> 1.3"
end
