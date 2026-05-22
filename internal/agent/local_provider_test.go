package agent

import (
	"testing"

	"github.com/marcomoesman/prompto/internal/config"
)

func TestLooksLikeLocalProvider(t *testing.T) {
	cases := []struct {
		name  string
		entry config.ProviderEntry
		want  bool
	}{
		{"explicit override wins over anthropic kind", config.ProviderEntry{Kind: "anthropic", LocalProvider: true}, true},
		{"explicit override wins over cloud openai", config.ProviderEntry{Kind: "openai", BaseURL: "https://api.openai.com", LocalProvider: true}, true},
		{"anthropic without override is never local", config.ProviderEntry{Kind: "anthropic"}, false},
		{"openai with empty BaseURL is cloud", config.ProviderEntry{Kind: "openai"}, false},
		{"openai api.openai.com is cloud", config.ProviderEntry{Kind: "openai", BaseURL: "https://api.openai.com/v1"}, false},
		{"openai with localhost is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://localhost:11434/v1"}, true},
		{"openai with 127.0.0.1 is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://127.0.0.1:1234/v1"}, true},
		{"openai with private 10/8 IP is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://10.0.0.2:8000/v1"}, true},
		{"openai with private 172.16/12 IP is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://172.16.1.2:8000/v1"}, true},
		{"openai with private 192.168/16 IP is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://192.168.1.50:8000/v1"}, true},
		{"openai with link-local IP is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://169.254.1.2:8000/v1"}, true},
		{"openai with public IP is cloud", config.ProviderEntry{Kind: "openai", BaseURL: "http://8.8.8.8:8000/v1"}, false},
		{"openai with ::1 is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://[::1]:11434/v1"}, true},
		{"openai with 0.0.0.0 is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://0.0.0.0:8080/v1"}, true},
		{"openai with ollama in host is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://ollama.docker.internal/v1"}, true},
		{"openai with lm-studio in host is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://lm-studio.lan/v1"}, true},
		{"openai with lmstudio in host is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://lmstudio.local/v1"}, true},
		{"openai with vllm in host is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://vllm.internal/v1"}, true},
		{"openai with llamacpp in host is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://llamacpp.dev/v1"}, true},
		{"openai with llama-cpp in host is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://llama-cpp.host/v1"}, true},
		{"openai with text-generation-webui in host is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://text-generation-webui.local/v1"}, true},
		{"openai unrelated host is cloud", config.ProviderEntry{Kind: "openai", BaseURL: "https://api.together.xyz/v1"}, false},
		{"openai with case-mismatched host is local", config.ProviderEntry{Kind: "openai", BaseURL: "http://OLLAMA.local/v1"}, true},
		{"malformed URL returns false (no guess)", config.ProviderEntry{Kind: "openai", BaseURL: "not a url ::"}, false},
		{"unknown kind without override is not local", config.ProviderEntry{Kind: "groq"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LooksLikeLocalProvider(tc.entry)
			if got != tc.want {
				t.Errorf("LooksLikeLocalProvider(%+v) = %v, want %v", tc.entry, got, tc.want)
			}
		})
	}
}

func TestHostFromBaseURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"  ", ""},
		{"https://api.openai.com/v1", "api.openai.com"},
		{"http://localhost:11434/v1", "localhost"},
		{"http://127.0.0.1:1234/", "127.0.0.1"},
		{"http://[::1]:11434/v1", "::1"},
		{"http://EXAMPLE.com/path", "example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := hostFromBaseURL(tc.in); got != tc.want {
				t.Errorf("hostFromBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
