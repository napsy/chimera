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
)

func main() {
	runtime.LockOSThread()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scraperClient := scraper.New(scraper.Config{})

	llmCfg := llm.Config{
		BaseURL:    firstNonEmpty(os.Getenv("CHIMERA_LLM_BASE_URL"), os.Getenv("CHIMERA_LLM_ENDPOINT")),
		Model:      os.Getenv("CHIMERA_LLM_MODEL"),
		APIKey:     os.Getenv("CHIMERA_LLM_API_KEY"),
		HTTPClient: nil,
		Timeout:    60 * time.Second,
	}

	llmClient := llm.NewClient(llmCfg)

	app, err := browser.NewApp(browser.Config{
		Scraper:   scraperClient,
		LLM:       llmClient,
		LLMConfig: llmCfg,
		UseLLM:    strings.EqualFold(os.Getenv("CHIMERA_USE_LLM"), "1"),
		AppID:     "com.example.chimera",
		AppTitle:  "Chimera Browser",
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
