package agent

import (
	"net"
	"net/url"
	"strings"

	"github.com/marcomoesman/prompto/internal/config"
)

// LooksLikeLocalProvider reports whether the entry signals a local LLM
// (Ollama, LM Studio, llama.cpp, vLLM, text-generation-webui, etc.).
// Used by the prompt builder to opt-in extra guidance that helps weaker
// models — primarily the anti-injection warning against textual tool
// calls.
//
// Resolution order:
//  1. ProviderEntry.LocalProvider explicit override wins.
//  2. Anthropic Kind never points at a local LLM (no local Claude).
//  3. Otherwise inspect the BaseURL host for loopback/private IPs and
//     known local runtime markers.
//
// An empty BaseURL (the OpenAI default of api.openai.com) is treated as
// non-local. Malformed URLs likewise return false rather than guessing.
func LooksLikeLocalProvider(entry config.ProviderEntry) bool {
	if entry.LocalProvider {
		return true
	}
	if entry.Kind != "openai" {
		return false
	}
	host := hostFromBaseURL(entry.BaseURL)
	if host == "" {
		return false
	}
	switch host {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	if ip := net.ParseIP(host); ip != nil && looksLikeLocalIP(ip) {
		return true
	}
	for _, marker := range localHostMarkers {
		if strings.Contains(host, marker) {
			return true
		}
	}
	return false
}

func looksLikeLocalIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// localHostMarkers are substrings that, when found in the BaseURL host,
// strongly suggest a local LLM runtime. Substring match (not exact) so
// hostnames like "ollama.docker.internal" or "lmstudio.lan" are caught.
var localHostMarkers = []string{
	"ollama",
	"lm-studio",
	"lmstudio",
	"llamacpp",
	"llama-cpp",
	"text-generation-webui",
	"vllm",
}

// hostFromBaseURL returns the lowercased host component of rawURL, or
// "" when the URL is empty / unparseable.
func hostFromBaseURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
