# Phase 03: Contributing Guide

This phase creates a comprehensive CONTRIBUTING.md guide that makes it easy for new contributors to understand the development workflow, coding standards, and how to submit quality pull requests. A good contributing guide reduces friction for potential contributors and helps maintain consistent code quality.

## Tasks

- [x] Create CONTRIBUTING.md with development setup instructions:
  - Prerequisites section (Go 1.22+, Git, optional Docker)
  - Step-by-step local development setup:
    - Clone the repository
    - Copy `.env.example` to `.env` and configure
    - Build the project (`go build ./cmd/...`)
    - Run the server locally
    - Run the CLI
  - IDE recommendations (VS Code with Go extension, GoLand)
  - Useful development commands quick reference

- [x] Add testing documentation to CONTRIBUTING.md:
  - How to run the full test suite (`go test ./...`)
  - How to run tests with race detection (`go test -race ./...`)
  - How to run E2E tests (`go test -tags=e2e ./test/e2e/...`)
  - How to run tests with coverage (`go test -cover ./...`)
  - Explain the test quality standards mentioned in README:
    - Race-free tests
    - Deterministic tests using `require.Eventually()`
    - Proper cleanup with `t.Cleanup()`
    - Time-injectable services
  - Note about live provider tests (require API keys, use sparingly)

  ✅ Completed: Added comprehensive testing documentation section to CONTRIBUTING.md with all subtasks covered.

- [x] Add code style and formatting guidelines to CONTRIBUTING.md:
  - Go formatting (`go fmt ./...`)
  - Linting recommendations (`golangci-lint` if used)
  - Naming conventions (follow existing patterns in codebase)
  - Error handling patterns (check existing code for examples)
  - Logging patterns (reference internal/logging package)
  - Comment guidelines (when and how to document code)

  ✅ Completed: Added comprehensive code style and formatting section to CONTRIBUTING.md covering:
     - Formatting with `go fmt` and IDE integration
     - Linting with `go vet` and optional `golangci-lint`
     - Naming conventions (packages, types, functions, constants, variables)
     - Error handling patterns (sentinel errors, custom types, wrapping, helper functions)
     - Logging patterns using `internal/logging` package with structured logging
     - Comment guidelines (when to comment, style, avoiding unnecessary comments)
     - Code organization (file structure, import ordering)

- [ ] Add pull request process to CONTRIBUTING.md:
  - Fork and branch workflow
  - Branch naming conventions (feature/, fix/, docs/)
  - Commit message format (conventional commits style)
  - PR description template expectations
  - Code review process and expectations
  - CI/CD checks that must pass
  - Merge requirements

- [ ] Add issue reporting guidelines to CONTRIBUTING.md:
  - Bug report template suggestions (steps to reproduce, expected vs actual)
  - Feature request guidance
  - Security vulnerability reporting process
  - Label explanations if applicable

- [ ] Commit contributing guide:
  - Stage CONTRIBUTING.md
  - Create commit with message: "docs: add comprehensive contributing guide"
