# Repository Guidelines

## Project Structure & Module Organization
- `cmd/chimera/`: GTK entry point wiring the scraper, LLM client, and settings store.
- `internal/browser/`: WebKit UI, navigation handling, theme CSS, and settings dialog.
- `internal/scraper/`: HTTP fetch + goquery extraction of titles, headings, text, and links.
- `internal/llm/`: OpenAI-compatible chat client, prompt builder, and response sanitizer.
- `internal/settings/`: JSON-backed persistence for LLM configuration.
- `third_party/gotk3/`: Vendored GTK bindings used via `replace` in `go.mod`.

## Build, Test, and Development Commands
- `CCACHE_DISABLE=1 GOCACHE=$(pwd)/.gocache go build ./cmd/chimera`: compile the app (spinner-heavy; increase timeout if sandboxed).
- `CCACHE_DISABLE=1 GOCACHE=$(pwd)/.gocache go run ./cmd/chimera`: run with live UI.
- `CCACHE_DISABLE=1 GOCACHE=$(pwd)/.gocache go test ./...`: execute unit tests (add mocks before enabling).

## Coding Style & Naming Conventions
- Go files formatted with `gofmt`; run `gofmt -w` on touched directories.
- Package naming mirrors folder structure (`browser`, `scraper`, `settings`).
- CSS identifiers follow `chimera-*` pattern for theme selectors.
- Prefer minimal comments; rely on descriptive names (`handleScrape`, `sanitizeLLMOutput`).

## Testing Guidelines
- Current tests minimal; plan table-driven tests under `*_test.go` aligned with packages.
- Name tests `TestComponent_Action` (e.g., `TestScraper_Scrape`).
- Use `go test ./internal/scraper` when validating scraping logic in isolation.

## Commit & Pull Request Guidelines
- Commit messages in imperative mood (e.g., `Add LLM settings persistence`).
- Reference related issues in the body; group UI + backend changes logically.
- PRs should describe behavior changes, include screenshots for UI tweaks, list manual test steps, and note build/test command results.

## Configuration & Security Notes
- Persisted settings located at `~/.config/chimera/settings.json`; redact API keys from screenshots/logs.
- Environment variables (`CHIMERA_LLM_BASE_URL`, `CHIMERA_LLM_API_KEY`, `CHIMERA_USE_LLM`) override stored preferences at launch.
