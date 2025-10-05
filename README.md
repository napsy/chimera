# Chimera Browser

Chimera is a lightweight desktop browser written in Go that uses WebKitGTK for rendering. Instead of directly showing the fetched page, it first scrapes the target site into structured data. You can then render that data either with an inbuilt HTML template or by handing it to a local LLM that synthesises a bespoke page.

## Features

- WebKitGTK UI with URL entry and two rendering modes (scrape-only, LLM composed)
- Structured scraping pipeline that extracts titles, headings, highlighted paragraphs, and outbound links
- Optional hand-off to a local LLM endpoint (e.g. Ollama, llama.cpp HTTP server) for bespoke HTML generation
- Graceful fallback to the built-in summary template when the LLM is disabled or fails

## Prerequisites

### System packages

Ensure the GTK and WebKit development libraries are installed. On Debian/Ubuntu:

```bash
sudo apt install -y libgtk-3-dev libwebkit2gtk-4.1-dev pkg-config
```

On Fedora:

```bash
sudo dnf install gtk3-devel webkit2gtk4.1-devel
```

### Go dependencies

The module depends on `github.com/gotk3/gotk3` and `github.com/PuerkitoBio/goquery`. Fetch them with:

```bash
GOCACHE=$(pwd)/.gocache go mod tidy
```

> **Note:** Network access is required to download the modules. If the environment is sandboxed, allow outbound access temporarily or vendor the required modules.

> **Vendored GTK bindings:** Because upstream gotk3 v0.6.4 is missing an import needed when building with recent Go releases, the repository vendors a patched copy under `third_party/gotk3`. Remove the `replace` directive in `go.mod` if you are using a newer upstream release that already includes the fix.

## Building and running

```bash
GOCACHE=$(pwd)/.gocache go run ./cmd/chimera
```

The UI exposes four primary controls:

- A URL entry box. Press Enter to trigger scraping. Press Enter to trigger scraping; it defaults to the "Scrape Only" flow unless you explicitly click "LLM Compose".
- `Scrape Only` button to build a summary using the internal template.
- `LLM Compose` button to call the configured OpenAI-compatible endpoint; the model infers the page theme, preserves every detail, and renders a tailored HTML experience.
- `LLM Settings` button to edit the endpoint, model, API key, and default behaviour at runtime.
- Click any link inside the rendered page to fetch and render that destination using the current mode.

## LLM integration

Set the following environment variables before launching the app:

- `CHIMERA_LLM_BASE_URL` **or** `CHIMERA_LLM_ENDPOINT`: Base URL of an OpenAI-compatible API. The client automatically appends `/v1/chat/completions` if the path is omitted. Examples:
  - `http://localhost:11434` (Ollama with [`openai` compatibility](https://github.com/ollama/ollama/blob/main/docs/openai-compatibility.md))
  - `https://api.openai.com/v1`
  - `https://openrouter.ai/api`
- `CHIMERA_LLM_MODEL`: Name of the chat completion model (e.g. `gpt-4o-mini`, `mistral-nemo`, `llama3`).
- `CHIMERA_LLM_API_KEY`: Bearer token for providers that require authentication (OpenAI, OpenRouter, etc.). Leave empty for unauthenticated local endpoints.
- `CHIMERA_USE_LLM=1` (optional): Auto-trigger the LLM path when pressing Enter in the URL field.

The request payload matches the OpenAI Chat Completions schema. The system prompt instructs the model to emit a complete HTML document; the scraped data is supplied as a single user message. Responses are expected in the `choices[0].message.content` field.
If the LLM returns a rate-limit (HTTP 429), Chimera automatically falls back to the scraped view and switches subsequent link navigations to template mode until you manually trigger LLM Compose again.
The assistant re-styles the page but must not summarise or drop content; all sections, wording, and links from the scrape are preserved in the generated HTML.

When the endpoint is reachable the `LLM Compose` button becomes active. Errors from the LLM call are surfaced inside the web view and the app automatically falls back to the template-based rendering.

## Project structure

```
cmd/chimera/        # Application entry point
internal/browser/   # GTK + WebKit UI and rendering helpers
internal/scraper/   # HTTP fetch + goquery based extraction
internal/llm/       # Client for local LLM services
```

## Next steps

- Persist browsing history and scraped datasets locally.
- Cache scraped results to avoid repeated downloads when iterating with the LLM.
- Provide configuration UI for toggling automatic LLM usage and model selection at runtime.
- Add tests for the scraper pipeline (mocking responses) and the HTML renderer.
