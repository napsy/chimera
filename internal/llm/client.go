package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"chimera/internal/scraper"
)

// ErrUnavailable indicates the LLM client is disabled or unreachable.
var ErrUnavailable = errors.New("llm unavailable")

// Config configures the LLM client.
type Config struct {
	BaseURL    string
	Model      string
	APIKey     string
	HTTPClient *http.Client
	Timeout    time.Duration
}

// Client talks to a local LLM endpoint (e.g. Ollama or llama.cpp HTTP binding).
type Client struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

// NewClient builds a new LLM client. If the endpoint is empty the client will be disabled.
func NewClient(cfg Config) *Client {
	if cfg.BaseURL == "" {
		return &Client{}
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 55 * time.Second
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	return &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		model:   cfg.Model,
		apiKey:  cfg.APIKey,
		client:  httpClient,
	}
}

// Available reports whether the LLM client can be used.
func (c *Client) Available() bool {
	return c != nil && c.baseURL != ""
}

// GeneratePage asks the local LLM to turn the scrape result into standalone HTML.
func (c *Client) GeneratePage(ctx context.Context, data *scraper.Result) (string, error) {
	if !c.Available() {
		return "", ErrUnavailable
	}

	payload := chatCompletionRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: buildPrompt(data)},
		},
		Temperature: 0.2,
	}

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.completionsURL(), buf)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("post llm request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", &HTTPError{Status: resp.StatusCode, Body: string(body)}
	}

	var parsed chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode llm response: %w", err)
	}

	html := sanitizeLLMOutput(parsed.FirstMessage())
	if html == "" {
		return "", errors.New("llm response empty")
	}

	return html, nil
}

func buildPrompt(data *scraper.Result) string {
	var builder strings.Builder
	builder.WriteString("You are a helpful assistant that converts scraped website data into clean HTML.\n")
	builder.WriteString("Study the information, infer the primary theme or purpose of the source page, and reflect it in the layout and copy.\n")
	builder.WriteString("Reimagine the page with modern styling and structure while faithfully preserving all information, wording, lists, tables, media references, and outbound links.\n")
	builder.WriteString("Do not summarise or omit details—represent the source content in full, simply with improved presentation.\n")
	builder.WriteString("Use semantic HTML5, include a descriptive hero or title section, themed subsections, and contextual highlights that match the inferred theme.\n")
	builder.WriteString("Ensure every original link is present and clickable, and reference the original source prominently.\n")
	builder.WriteString("Do not wrap the output in Markdown code fences.\n\n")

	builder.WriteString("Source URL: ")
	builder.WriteString(data.SourceURL)
	builder.WriteString("\n")

	if data.Title != "" {
		builder.WriteString("Title: ")
		builder.WriteString(data.Title)
		builder.WriteString("\n")
	}

	if data.Description != "" {
		builder.WriteString("Description: ")
		builder.WriteString(data.Description)
		builder.WriteString("\n")
	}

	if len(data.Headings) > 0 {
		builder.WriteString("Headings:\n")
		for _, h := range data.Headings {
			builder.WriteString(fmt.Sprintf("- H%d %s\n", h.Level, h.Text))
		}
	}

	if len(data.Paragraphs) > 0 {
		builder.WriteString("Paragraphs:\n")
		for _, p := range data.Paragraphs {
			builder.WriteString("- ")
			builder.WriteString(p)
			builder.WriteString("\n")
		}
	}

	if len(data.Links) > 0 {
		builder.WriteString("Links:\n")
		for _, link := range data.Links {
			builder.WriteString("- ")
			builder.WriteString(link.Text)
			builder.WriteString(" -> ")
			builder.WriteString(link.Href)
			builder.WriteString("\n")
		}
	}

	builder.WriteString("\nReturn only raw HTML inside <html> tags.")

	return builder.String()
}

func (c *Client) completionsURL() string {
	if c.baseURL == "" {
		return ""
	}

	if strings.HasSuffix(c.baseURL, "/v1/chat/completions") {
		return c.baseURL
	}

	trimmed := strings.TrimRight(c.baseURL, "/")
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed + "/chat/completions"
	}

	return trimmed + "/v1/chat/completions"
}

const systemPrompt = "You are a helpful assistant that turns structured website data into clean, self-contained HTML pages without using Markdown code fences. Infer the purpose or theme of the content, tailor the layout accordingly, and preserve every piece of information and link without summarising or omitting details."

// HTTPError represents a non-successful HTTP status returned by the LLM endpoint.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("llm returned status %d", e.Status)
}

// IsRateLimited reports whether the given error is a rate-limit response.
func IsRateLimited(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status == http.StatusTooManyRequests
	}
	return false
}

func sanitizeLLMOutput(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)

	if strings.HasPrefix(trimmed, "html") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "html"))
	}

	if idx := strings.Index(trimmed, "```"); idx >= 0 {
		trimmed = trimmed[:idx]
	}

	return strings.TrimSpace(trimmed)
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

func (r chatCompletionResponse) FirstMessage() string {
	if len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Content
}
