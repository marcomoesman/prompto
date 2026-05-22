package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/httpattr"
	"github.com/marcomoesman/prompto/internal/provider/internal/providerhttp"
)

// LocalHealthTimeout caps the startup probe of /v1/models. Localhost
// endpoints return in single-digit ms; LAN hosts (e.g. a llama.cpp box
// at 192.168.x.x) need more headroom — Wi-Fi RTT + a busy server loading
// a model can easily exceed a sub-second ceiling. 3s catches the slow
// cases without making "server is genuinely down" diagnosis painful at
// startup (the warning is non-fatal regardless).
const LocalHealthTimeout = 3 * time.Second

// CheckLocalOpenAI verifies that an OpenAI-compatible local endpoint is
// reachable without making a completion request. It probes /v1/models, which
// llama.cpp, LM Studio, vLLM, and Ollama's OpenAI shim normally expose.
func CheckLocalOpenAI(ctx context.Context, cfg api.ProviderConfig) error {
	if cfg.Kind != "openai" || strings.TrimSpace(cfg.BaseURL) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, LocalHealthTimeout)
	defer cancel()

	url := modelsURL(strings.TrimRight(cfg.BaseURL, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build health request: %w", err)
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	req.Header.Set("Accept", "application/json")
	httpattr.Apply(req)

	client := &http.Client{Transport: providerhttp.DefaultTransport()}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s unreachable: %w", url, err)
	}
	defer func() { _ = res.Body.Close() }()

	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		if cfg.Model != "" && modelListMissing(res, cfg.Model) {
			return fmt.Errorf("%s reachable, but model %q was not listed by /v1/models", url, cfg.Model)
		}
		return nil
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%s reachable, but auth was rejected (%s)", url, res.Status)
	default:
		return fmt.Errorf("%s reachable, but /v1/models returned %s", url, res.Status)
	}
}

func modelsURL(baseURL string) string {
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/models"
	}
	return baseURL + "/v1/models"
}

func modelListMissing(res *http.Response, model string) bool {
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || len(body.Data) == 0 {
		return false
	}
	for _, item := range body.Data {
		if item.ID == model {
			return false
		}
	}
	return true
}
