package main

import (
	"context"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	"chimera/internal/browser"
	"chimera/internal/llm"
	"chimera/internal/scraper"
	"chimera/internal/settings"
)

func main() {
	runtime.LockOSThread()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scraperClient := scraper.New(scraper.Config{})

	var (
		settingsStore *settings.Store
		stored        settings.Data
	)

	if store, err := settings.NewStore("chimera"); err != nil {
		log.Printf("warning: unable to prepare settings store: %v", err)
	} else {
		settingsStore = store
		if data, err := settingsStore.Load(); err != nil {
			log.Printf("warning: unable to load settings: %v", err)
		} else {
			stored = data
		}
	}

	envBase := firstNonEmpty(os.Getenv("CHIMERA_LLM_BASE_URL"), os.Getenv("CHIMERA_LLM_ENDPOINT"), stored.BaseURL)
	envModel := firstNonEmpty(os.Getenv("CHIMERA_LLM_MODEL"), stored.Model)
	envKey := firstNonEmpty(os.Getenv("CHIMERA_LLM_API_KEY"), stored.APIKey)

	useLLM := stored.UseLLM
	if override := strings.TrimSpace(os.Getenv("CHIMERA_USE_LLM")); override != "" {
		useLLM = strings.EqualFold(override, "1")
	}

	llmCfg := llm.Config{
		BaseURL:    envBase,
		Model:      envModel,
		APIKey:     envKey,
		HTTPClient: nil,
		Timeout:    60 * time.Second,
	}

	llmClient := llm.NewClient(llmCfg)

	app, err := browser.NewApp(browser.Config{
		Scraper:       scraperClient,
		LLM:           llmClient,
		LLMConfig:     llmCfg,
		UseLLM:        useLLM,
		SettingsStore: settingsStore,
		AppID:         "com.example.chimera",
		AppTitle:      "Chimera Browser",
	})
	if err != nil {
		log.Fatalf("failed to initialize app: %v", err)
	}

	if err := app.Run(ctx); err != nil {
		log.Fatalf("application error: %v", err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
