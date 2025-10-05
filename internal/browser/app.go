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
	persist "chimera/internal/settings"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

// Config controls app setup.
type Config struct {
	Scraper       *scraper.Scraper
	LLM           *llm.Client
	LLMConfig     llm.Config
	UseLLM        bool
	SettingsStore *persist.Store
	AppID         string
	AppTitle      string
}

// App wires the GTK UI with the scraping and LLM pipeline.
type App struct {
	cfg Config

	mu            sync.RWMutex
	llmClient     *llm.Client
	llmSettings   appLLMSettings
	llmPreferred  bool
	llmTimeout    time.Duration
	llmLastMode   bool
	llmLastSet    bool
	lastSource    string
	settingsStore *persist.Store
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
		cfg:           cfg,
		llmTimeout:    timeout,
		settingsStore: cfg.SettingsStore,
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
	ensureTheme()

	window, err := gtk.ApplicationWindowNew(app)
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	window.SetDefaultSize(1180, 820)
	window.SetTitle(a.cfg.AppTitle)
	window.SetName("chimera-window")

	root, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 18)
	if err != nil {
		return fmt.Errorf("create root layout: %w", err)
	}
	root.SetName("chimera-root")
	root.SetBorderWidth(18)

	toolbar, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 10)
	if err != nil {
		return fmt.Errorf("create toolbar: %w", err)
	}
	toolbar.SetName("chimera-toolbar")
	toolbar.SetMarginTop(6)
	toolbar.SetMarginBottom(6)
	toolbar.SetMarginStart(6)
	toolbar.SetMarginEnd(6)

	entry, err := gtk.EntryNew()
	if err != nil {
		return fmt.Errorf("create entry: %w", err)
	}
	entry.SetPlaceholderText("Paste a URL, e.g. https://example.com")
	entry.SetWidthChars(48)
	entry.SetIconFromIconName(gtk.ENTRY_ICON_SECONDARY, "system-search-symbolic")
	entry.SetHasFrame(false)
	entry.SetName("chimera-url-entry")
	entry.SetHExpand(true)

	scrapeBtn, err := gtk.ButtonNewWithLabel("Reader Mode")
	if err != nil {
		return fmt.Errorf("create scrape button: %w", err)
	}
	scrapeBtn.SetName("chimera-btn-secondary")
	if ctx, err := scrapeBtn.GetStyleContext(); err == nil {
		ctx.AddClass("flat")
	}
	scrapeBtn.SetTooltipText("Render using the built-in reader")

	llmBtn, err := gtk.ButtonNewWithLabel("Compose with LLM")
	if err != nil {
		return fmt.Errorf("create llm button: %w", err)
	}
	llmBtn.SetName("chimera-btn-primary")
	if ctx, err := llmBtn.GetStyleContext(); err == nil {
		ctx.AddClass("suggested-action")
	}

	settingsBtn, err := gtk.ButtonNewWithLabel("LLM Settings")
	if err != nil {
		return fmt.Errorf("create settings button: %w", err)
	}
	settingsBtn.SetName("chimera-btn-ghost")
	if ctx, err := settingsBtn.GetStyleContext(); err == nil {
		ctx.AddClass("flat")
	}
	settingsBtn.SetTooltipText("Adjust endpoint, model, and defaults")

	buttonRow, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	if err != nil {
		return fmt.Errorf("create action row: %w", err)
	}
	buttonRow.SetName("chimera-action-row")
	buttonRow.SetHAlign(gtk.ALIGN_END)
	buttonRow.SetVAlign(gtk.ALIGN_CENTER)
	buttonRow.PackStart(scrapeBtn, false, false, 0)
	buttonRow.PackStart(llmBtn, false, false, 0)
	buttonRow.PackStart(settingsBtn, false, false, 0)

	infoLabel, err := gtk.LabelNew("Ready")
	if err != nil {
		return fmt.Errorf("create info label: %w", err)
	}
	infoLabel.SetXAlign(0)
	infoLabel.SetName("chimera-status-text")

	statusBar, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 6)
	if err != nil {
		return fmt.Errorf("create status bar: %w", err)
	}
	statusBar.SetName("chimera-status-bar")
	statusBar.SetMarginTop(6)
	statusBar.SetMarginBottom(10)
	statusBar.PackStart(infoLabel, true, true, 0)

	toolbar.PackStart(entry, true, true, 0)
	toolbar.PackStart(buttonRow, false, false, 0)

	headerBar, err := gtk.HeaderBarNew()
	if err != nil {
		return fmt.Errorf("create header bar: %w", err)
	}
	headerBar.SetShowCloseButton(true)
	headerBar.SetTitle(a.cfg.AppTitle)
	headerBar.SetName("chimera-header")
	headerBar.SetCustomTitle(toolbar)
	window.SetTitlebar(headerBar)

	scroll, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		return fmt.Errorf("create scroller: %w", err)
	}
	scroll.SetName("chimera-scroll")

	webView, err := webkit.NewWebView()
	if err != nil {
		return fmt.Errorf("create webview: %w", err)
	}
	webView.Widget().SetName("chimera-webview")

	spinner, err := gtk.SpinnerNew()
	if err != nil {
		return fmt.Errorf("create spinner: %w", err)
	}
	spinner.SetName("chimera-spinner")
	spinner.SetHAlign(gtk.ALIGN_CENTER)
	spinner.SetVAlign(gtk.ALIGN_CENTER)
	spinner.Hide()

	overlay, err := gtk.OverlayNew()
	if err != nil {
		return fmt.Errorf("create overlay: %w", err)
	}
	overlay.Add(webView.Widget())
	overlay.AddOverlay(spinner)

	scroll.Add(overlay)

	root.PackStart(statusBar, false, false, 0)
	root.PackStart(scroll, true, true, 0)

	window.Add(root)
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

		go a.handleScrape(ctx, resolved, webView, infoLabel, spinner, useLLM)
		return true
	})

	scrape := func(useLLM bool) {
		urlText, err := entry.GetText()
		if err != nil {
			a.setStatus(infoLabel, fmt.Sprintf("failed to read entry: %v", err))
			return
		}
		trimmed := strings.TrimSpace(urlText)
		if trimmed == "" {
			a.setStatus(infoLabel, "Please provide a URL")
			return
		}

		a.setStatus(infoLabel, "Scraping...")
		a.setLastMode(useLLM)
		go a.handleScrape(ctx, trimmed, webView, infoLabel, spinner, useLLM)
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

func (a *App) handleScrape(ctx context.Context, target string, view *webkit.WebView, info *gtk.Label, spinner *gtk.Spinner, useLLM bool) {
	a.startSpinner(spinner)
	defer a.stopSpinner(spinner)

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
			a.setStatus(info, "LLM rate limited — showing reader mode")
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
	glib.IdleAdd(func() bool {
		view.InjectStatusBubble("Something went wrong", msg)
		info.SetText("Error")
		return false
	})
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

func (a *App) startSpinner(spinner *gtk.Spinner) {
	if spinner == nil {
		return
	}
	glib.IdleAdd(func() bool {
		spinner.Show()
		spinner.Start()
		return false
	})
}

func (a *App) stopSpinner(spinner *gtk.Spinner) {
	if spinner == nil {
		return
	}
	glib.IdleAdd(func() bool {
		spinner.Stop()
		spinner.Hide()
		return false
	})
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
	grid.SetRowSpacing(10)
	grid.SetColumnSpacing(14)
	grid.SetMarginTop(14)
	grid.SetMarginBottom(14)
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
	baseEntry.SetWidthChars(42)
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

	updated := appLLMSettings{
		BaseURL: strings.TrimSpace(base),
		Model:   strings.TrimSpace(model),
		APIKey:  strings.TrimSpace(key),
	}

	preferLLM := preferCheck.GetActive()

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

	if a.settingsStore != nil {
		data := persist.Data{
			BaseURL: settings.BaseURL,
			Model:   settings.Model,
			APIKey:  settings.APIKey,
			UseLLM:  prefer,
		}
		if err := a.settingsStore.Save(data); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
	}

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

var cssOnce sync.Once

func ensureTheme() {
	cssOnce.Do(func() {
		provider, err := gtk.CssProviderNew()
		if err != nil {
			return
		}
		if err := provider.LoadFromData(appCSS); err != nil {
			return
		}
		screen, err := gdk.ScreenGetDefault()
		if err != nil || screen == nil {
			return
		}
		gtk.AddProviderForScreen(screen, provider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
	})
}

const appCSS = `
#chimera-window {
    background: #eef1f8;
}

#chimera-header {
    background: transparent;
    padding: 0;
    border-bottom: 0;
}

#chimera-root {
    background: transparent;
    spacing: 18px;
}

#chimera-toolbar {
    background: #ffffff;
    padding: 16px;
    border-radius: 18px;
    border: 1px solid rgba(34, 51, 84, 0.08);
    box-shadow: 0 8px 24px rgba(15, 35, 95, 0.08);
}

#chimera-action-row > button {
    margin-left: 6px;
}

#chimera-url-entry {
    padding: 12px 16px;
    border-radius: 14px;
    background: rgba(255, 255, 255, 0.85);
    border: 1px solid rgba(57, 88, 157, 0.18);
    font-size: 14px;
}

#chimera-btn-primary, #chimera-btn-secondary, #chimera-btn-ghost {
    border-radius: 999px;
    padding: 8px 18px;
    font-weight: 600;
    letter-spacing: 0.2px;
}

#chimera-btn-primary {
    background: linear-gradient(135deg, #4f6ef7, #7b5ffc);
    color: #fff;
}

#chimera-btn-primary:disabled {
    background: rgba(79, 110, 247, 0.35);
}

#chimera-btn-secondary {
    background: rgba(79, 110, 247, 0.1);
    color: #465275;
}

#chimera-btn-ghost {
    color: #566289;
}

#chimera-status-bar {
    background: #ffffff;
    padding: 10px 16px;
    border-radius: 14px;
    border: 1px solid rgba(34, 51, 84, 0.06);
    color: #4c5678;
    font-size: 13px;
}

#chimera-status-text {
    font-weight: 500;
}

#chimera-scroll {
    background: transparent;
}

#chimera-webview {
    border-radius: 22px;
    border: 1px solid rgba(34, 51, 84, 0.08);
    background: #ffffff;
}

#chimera-spinner {
    min-width: 48px;
    min-height: 48px;
    border-radius: 24px;
    background: rgba(239, 242, 255, 0.86);
    padding: 12px;
}
`
