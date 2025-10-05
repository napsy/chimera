package browser

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"chimera/internal/browser/webkit"
	"chimera/internal/llm"
	"chimera/internal/scraper"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

// Config controls app setup.
type Config struct {
	Scraper   *scraper.Scraper
	LLM       *llm.Client
	LLMConfig llm.Config
	UseLLM    bool
	AppID     string
	AppTitle  string
}

// App wires the GTK UI with the scraping and LLM pipeline.
type App struct {
	cfg Config

	mu           sync.RWMutex
	llmClient    *llm.Client
	llmSettings  appLLMSettings
	llmPreferred bool
	llmTimeout   time.Duration
	llmLastMode  bool
	llmLastSet   bool
	lastSource   string
}

// NewApp validates the configuration and returns a ready application.
func NewApp(cfg Config) (*App, error) {
	if cfg.Scraper == nil {
		return nil, fmt.Errorf("scraper is required")
	}

	if cfg.AppID == "" {
		cfg.AppID = "com.example.chimera"
	}
	if cfg.AppTitle == "" {
		cfg.AppTitle = "Chimera Browser"
	}

	timeout := cfg.LLMConfig.Timeout
	if timeout <= 0 {
		timeout = 55 * time.Second
	}

	app := &App{
		cfg:        cfg,
		llmTimeout: timeout,
	}

	app.mu.Lock()
	app.llmClient = cfg.LLM
	app.llmPreferred = cfg.UseLLM
	app.llmSettings = appLLMSettings{
		BaseURL: strings.TrimSpace(cfg.LLMConfig.BaseURL),
		Model:   strings.TrimSpace(cfg.LLMConfig.Model),
		APIKey:  strings.TrimSpace(cfg.LLMConfig.APIKey),
	}
	app.mu.Unlock()

	return app, nil
}

// Run starts the GTK main loop and blocks until the app exits.
func (a *App) Run(ctx context.Context) error {
	application, err := gtk.ApplicationNew(a.cfg.AppID, glib.APPLICATION_FLAGS_NONE)
	if err != nil {
		return fmt.Errorf("create application: %w", err)
	}

	application.Connect("activate", func() {
		if err := a.activate(ctx, application); err != nil {
			log.Printf("activate error: %v", err)
		}
	})

	go func() {
		<-ctx.Done()
		glib.IdleAdd(func() bool {
			application.Quit()
			return false
		})
	}()

	application.Run(nil)
	return nil
}

func (a *App) activate(ctx context.Context, app *gtk.Application) error {
	window, err := gtk.ApplicationWindowNew(app)
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	window.SetDefaultSize(1100, 800)
	window.SetTitle(a.cfg.AppTitle)

	box, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 6)
	if err != nil {
		return fmt.Errorf("create layout: %w", err)
	}

	toolbar, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 6)
	if err != nil {
		return fmt.Errorf("create toolbar: %w", err)
	}

	entry, err := gtk.EntryNew()
	if err != nil {
		return fmt.Errorf("create entry: %w", err)
	}
	entry.SetPlaceholderText("Enter URL, e.g. https://example.com")

	scrapeBtn, err := gtk.ButtonNewWithLabel("Scrape Only")
	if err != nil {
		return fmt.Errorf("create scrape button: %w", err)
	}

	llmBtn, err := gtk.ButtonNewWithLabel("LLM Compose")
	if err != nil {
		return fmt.Errorf("create llm button: %w", err)
	}

	settingsBtn, err := gtk.ButtonNewWithLabel("LLM Settings")
	if err != nil {
		return fmt.Errorf("create settings button: %w", err)
	}

	infoLabel, err := gtk.LabelNew("Ready")
	if err != nil {
		return fmt.Errorf("create info label: %w", err)
	}
	infoLabel.SetXAlign(0)

	toolbar.PackStart(entry, true, true, 0)
	toolbar.PackStart(scrapeBtn, false, false, 0)
	toolbar.PackStart(llmBtn, false, false, 0)
	toolbar.PackStart(settingsBtn, false, false, 0)

	scroll, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		return fmt.Errorf("create scroller: %w", err)
	}

	webView, err := webkit.NewWebView()
	if err != nil {
		return fmt.Errorf("create webview: %w", err)
	}

	scroll.Add(webView.Widget())

	box.PackStart(toolbar, false, false, 0)
	box.PackStart(infoLabel, false, false, 0)
	box.PackStart(scroll, true, true, 0)

	window.Add(box)
	window.ShowAll()

	a.updateLLMButton(llmBtn)

	webView.OnNavigate(func(target string) bool {
		resolved, ok := a.resolveTarget(target)
		if !ok {
			return false
		}

		glib.IdleAdd(func() bool {
			entry.SetText(resolved)
			return false
		})

		a.setStatus(infoLabel, "Scraping...")

		useLLM := a.navigationMode()
		a.setLastMode(useLLM)

		go a.handleScrape(ctx, resolved, webView, infoLabel, useLLM)
		return true
	})

	scrape := func(useLLM bool) {
		url, err := entry.GetText()
		if err != nil {
			a.setStatus(infoLabel, fmt.Sprintf("failed to read entry: %v", err))
			return
		}
		trimmed := strings.TrimSpace(url)
		if trimmed == "" {
			a.setStatus(infoLabel, "Please provide a URL")
			return
		}

		a.setStatus(infoLabel, "Scraping...")
		a.setLastMode(useLLM)
		go a.handleScrape(ctx, trimmed, webView, infoLabel, useLLM)
	}

	scrapeBtn.Connect("clicked", func() {
		scrape(false)
	})
	llmBtn.Connect("clicked", func() {
		scrape(true)
	})

	entry.Connect("activate", func() {
		scrape(a.prefersLLM())
	})

	settingsBtn.Connect("clicked", func() {
		if err := a.openSettingsDialog(window, llmBtn, infoLabel); err != nil {
			a.setStatus(infoLabel, fmt.Sprintf("Settings error: %v", err))
		}
	})

	return nil
}

func (a *App) handleScrape(ctx context.Context, target string, view *webkit.WebView, info *gtk.Label, useLLM bool) {
	result, err := a.cfg.Scraper.Scrape(ctx, target)
	if err != nil {
		a.renderError(view, info, fmt.Sprintf("Scrape failed: %v", err))
		return
	}

	a.setLastSource(result.SourceURL)

	client := a.currentLLM()

	if useLLM && client != nil && client.Available() {
		html, err := client.GeneratePage(ctx, result)
		if err == nil {
			a.renderHTML(view, info, html)
			return
		}

		if llm.IsRateLimited(err) {
			log.Printf("llm rate limited; falling back to scraped view: %v", err)
			a.setStatus(info, "LLM rate limited; showing scraped view")
			a.setLastMode(false)
		} else {
			a.renderError(view, info, fmt.Sprintf("LLM fallback: %v", err))
			return
		}
	}

	html, err := renderSimple(result)
	if err != nil {
		a.renderError(view, info, fmt.Sprintf("Render error: %v", err))
		return
	}
	a.renderHTML(view, info, html)
}

func (a *App) setStatus(label *gtk.Label, text string) {
	glib.IdleAdd(func() bool {
		label.SetText(text)
		return false
	})
}

func (a *App) renderHTML(view *webkit.WebView, info *gtk.Label, html string) {
	glib.IdleAdd(func() bool {
		view.LoadHTML(html, "")
		info.SetText("Done")
		return false
	})
}

func (a *App) renderError(view *webkit.WebView, info *gtk.Label, msg string) {
	log.Println(msg)
	html := fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="utf-8"><style>body{font-family:sans-serif;background:#222;color:#eee;padding:2rem;}h1{color:#f66;}code{color:#9cf;}</style></head><body><h1>Chimera Error</h1><p>%s</p></body></html>`, template.HTMLEscapeString(msg))
	a.renderHTML(view, info, html)
}

var simpleTmpl = template.Must(template.New("simple").Funcs(template.FuncMap{
	"formatTime": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format("02 Jan 2006 15:04 MST")
	},
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8" />
<title>{{ if .Title }}{{ .Title }} — Chimera{{ else }}Chimera Summary{{ end }}</title>
<style>
body { font-family: "Inter", "Segoe UI", sans-serif; margin: 0 auto; max-width: 960px; padding: 2rem; background: #f5f7fb; color: #1d2433; }
header { border-bottom: 1px solid #d4d9e2; margin-bottom: 1.5rem; padding-bottom: 1rem; }
h1 { margin: 0 0 .5rem 0; font-size: 2.4rem; }
section { margin-bottom: 2rem; background: #fff; border-radius: 12px; padding: 1.5rem; box-shadow: 0 1px 3px rgba(0,0,0,.08); }
h2 { font-size: 1.5rem; margin-top: 0; }
ul { padding-left: 1.2rem; }
a { color: #2b5dcc; text-decoration: none; }
a:hover { text-decoration: underline; }
small { color: #5b6576; }
</style>
</head>
<body>
<header>
  <h1>{{ if .Title }}{{ .Title }}{{ else }}Scraped Summary{{ end }}</h1>
  <small>Source: <a href="{{ .SourceURL }}">{{ .SourceURL }}</a>{{ if .FetchedAt }} • {{ formatTime .FetchedAt }}{{ end }}</small>
  {{ if .Description }}<p>{{ .Description }}</p>{{ end }}
</header>
<section>
  <h2>Key Headings</h2>
  {{ if .Headings }}
  <ul>
    {{ range .Headings }}<li><strong>H{{ .Level }}</strong> — {{ .Text }}</li>{{ end }}
  </ul>
  {{ else }}<p>No major headings detected.</p>{{ end }}
</section>
<section>
  <h2>Highlights</h2>
  {{ if .Paragraphs }}
  {{ range .Paragraphs }}<p>{{ . }}</p>{{ end }}
  {{ else }}<p>Not enough textual content found.</p>{{ end }}
</section>
<section>
  <h2>Links</h2>
  {{ if .Links }}
  <ul>
    {{ range .Links }}<li><a href="{{ .Href }}" target="_blank" rel="noopener">{{ .Text }}</a></li>{{ end }}
  </ul>
  {{ else }}<p>No links captured.</p>{{ end }}
</section>
</body>
</html>`))

func renderSimple(data *scraper.Result) (string, error) {
	var builder strings.Builder
	if err := simpleTmpl.Execute(&builder, data); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func (a *App) currentLLM() *llm.Client {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.llmClient
}

func (a *App) llmAvailable() bool {
	client := a.currentLLM()
	return client != nil && client.Available()
}

func (a *App) prefersLLM() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.llmPreferred {
		return false
	}
	if a.llmClient == nil {
		return false
	}
	return a.llmClient.Available()
}

func (a *App) navigationMode() bool {
	a.mu.RLock()
	use := a.llmLastMode
	set := a.llmLastSet
	client := a.llmClient
	preferred := a.llmPreferred
	a.mu.RUnlock()

	available := client != nil && client.Available()
	if set {
		if use && available {
			return true
		}
		return false
	}

	return preferred && available
}

func (a *App) setLastMode(use bool) {
	a.mu.Lock()
	a.llmLastMode = use
	a.llmLastSet = true
	a.mu.Unlock()
}

func (a *App) setLastSource(src string) {
	trimmed := strings.TrimSpace(src)
	a.mu.Lock()
	a.lastSource = trimmed
	a.mu.Unlock()
}

func (a *App) lastSourceURL() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastSource
}

func (a *App) resolveTarget(target string) (string, bool) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return "", false
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", false
	}

	if parsed.IsAbs() {
		switch parsed.Scheme {
		case "http", "https":
			return parsed.String(), true
		default:
			return "", false
		}
	}

	base := a.lastSourceURL()
	if base == "" {
		return "", false
	}

	baseURL, err := url.Parse(base)
	if err != nil {
		return "", false
	}

	resolved := baseURL.ResolveReference(parsed)
	switch resolved.Scheme {
	case "http", "https":
		return resolved.String(), true
	default:
		return "", false
	}
}

func (a *App) updateLLMButton(button *gtk.Button) {
	available := a.llmAvailable()
	button.SetSensitive(available)
	if available {
		button.SetTooltipText("Generate a composed page via the configured LLM")
	} else {
		button.SetTooltipText("Configure an OpenAI-compatible endpoint to enable")
	}
}

func (a *App) openSettingsDialog(parent *gtk.ApplicationWindow, llmBtn *gtk.Button, status *gtk.Label) error {
	dialog, err := gtk.DialogNew()
	if err != nil {
		return fmt.Errorf("create dialog: %w", err)
	}
	defer dialog.Destroy()

	dialog.SetTitle("LLM Settings")
	dialog.SetModal(true)
	dialog.SetTransientFor(parent)
	dialog.AddButton("Cancel", gtk.RESPONSE_CANCEL)
	dialog.AddButton("Save", gtk.RESPONSE_OK)

	content, err := dialog.GetContentArea()
	if err != nil {
		return fmt.Errorf("access content area: %w", err)
	}

	grid, err := gtk.GridNew()
	if err != nil {
		return fmt.Errorf("create grid: %w", err)
	}
	grid.SetRowSpacing(8)
	grid.SetColumnSpacing(12)
	grid.SetMarginTop(12)
	grid.SetMarginBottom(12)
	grid.SetMarginStart(18)
	grid.SetMarginEnd(18)

	snapshot, prefer := a.settingsSnapshot()

	baseLabel, err := gtk.LabelNew("Base URL")
	if err != nil {
		return fmt.Errorf("create base label: %w", err)
	}
	baseLabel.SetXAlign(0)
	grid.Attach(baseLabel, 0, 0, 1, 1)

	baseEntry, err := gtk.EntryNew()
	if err != nil {
		return fmt.Errorf("create base entry: %w", err)
	}
	baseEntry.SetPlaceholderText("https://api.openai.com")
	baseEntry.SetWidthChars(40)
	baseEntry.SetText(snapshot.BaseURL)
	grid.Attach(baseEntry, 1, 0, 1, 1)

	modelLabel, err := gtk.LabelNew("Model")
	if err != nil {
		return fmt.Errorf("create model label: %w", err)
	}
	modelLabel.SetXAlign(0)
	grid.Attach(modelLabel, 0, 1, 1, 1)

	modelEntry, err := gtk.EntryNew()
	if err != nil {
		return fmt.Errorf("create model entry: %w", err)
	}
	modelEntry.SetPlaceholderText("gpt-4o-mini, llama3, mistral-nemo...")
	modelEntry.SetText(snapshot.Model)
	grid.Attach(modelEntry, 1, 1, 1, 1)

	keyLabel, err := gtk.LabelNew("API Key")
	if err != nil {
		return fmt.Errorf("create key label: %w", err)
	}
	keyLabel.SetXAlign(0)
	grid.Attach(keyLabel, 0, 2, 1, 1)

	keyEntry, err := gtk.EntryNew()
	if err != nil {
		return fmt.Errorf("create key entry: %w", err)
	}
	keyEntry.SetVisibility(false)
	keyEntry.SetInputPurpose(gtk.INPUT_PURPOSE_PASSWORD)
	keyEntry.SetText(snapshot.APIKey)
	grid.Attach(keyEntry, 1, 2, 1, 1)

	preferCheck, err := gtk.CheckButtonNewWithLabel("Use LLM by default when pressing Enter")
	if err != nil {
		return fmt.Errorf("create preference checkbox: %w", err)
	}
	preferCheck.SetActive(prefer)
	grid.Attach(preferCheck, 0, 3, 2, 1)

	content.Add(grid)
	dialog.ShowAll()

	response := dialog.Run()
	if response != gtk.RESPONSE_OK {
		return nil
	}

	base, err := baseEntry.GetText()
	if err != nil {
		return fmt.Errorf("read base URL: %w", err)
	}
	model, err := modelEntry.GetText()
	if err != nil {
		return fmt.Errorf("read model: %w", err)
	}
	key, err := keyEntry.GetText()
	if err != nil {
		return fmt.Errorf("read API key: %w", err)
	}

	preferLLM := preferCheck.GetActive()

	updated := appLLMSettings{
		BaseURL: strings.TrimSpace(base),
		Model:   strings.TrimSpace(model),
		APIKey:  strings.TrimSpace(key),
	}

	if err := a.applySettings(updated, preferLLM); err != nil {
		return fmt.Errorf("apply settings: %w", err)
	}

	a.updateLLMButton(llmBtn)

	switch {
	case preferLLM && !a.llmAvailable():
		a.setStatus(status, "LLM preference saved but endpoint unavailable")
	case a.llmAvailable():
		a.setStatus(status, "LLM configured")
	default:
		a.setStatus(status, "LLM disabled")
	}

	return nil
}

func (a *App) applySettings(settings appLLMSettings, prefer bool) error {
	settings = appLLMSettings{
		BaseURL: strings.TrimSpace(settings.BaseURL),
		Model:   strings.TrimSpace(settings.Model),
		APIKey:  strings.TrimSpace(settings.APIKey),
	}

	cfg := llm.Config{
		BaseURL: settings.BaseURL,
		Model:   settings.Model,
		APIKey:  settings.APIKey,
		Timeout: a.llmTimeout,
	}

	client := llm.NewClient(cfg)

	a.mu.Lock()
	a.llmClient = client
	a.llmPreferred = prefer
	a.llmSettings = settings
	a.cfg.LLM = client
	a.cfg.UseLLM = prefer
	a.cfg.LLMConfig = cfg
	a.mu.Unlock()

	return nil
}

func (a *App) settingsSnapshot() (appLLMSettings, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.llmSettings, a.llmPreferred
}

type appLLMSettings struct {
	BaseURL string
	Model   string
	APIKey  string
}
