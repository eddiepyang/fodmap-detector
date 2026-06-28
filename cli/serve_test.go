package cli

import (
	"testing"
)

func TestResolveLLMURL(t *testing.T) {
	tests := []struct {
		name         string
		extractorURL string
		want         string
	}{
		{
			name:         "Empty URL falls back to Gemini",
			extractorURL: "",
			want:         "https://generativelanguage.googleapis.com/v1beta/openai/",
		},
		{
			name:         "Local python scraper URL falls back to Gemini",
			extractorURL: "http://localhost:8765",
			want:         "https://generativelanguage.googleapis.com/v1beta/openai/",
		},
		{
			name:         "Random string falls back to Gemini",
			extractorURL: "some-random-endpoint",
			want:         "https://generativelanguage.googleapis.com/v1beta/openai/",
		},
		{
			name:         "Gemini URL remains unchanged",
			extractorURL: "https://generativelanguage.googleapis.com/v1beta/openai/",
			want:         "https://generativelanguage.googleapis.com/v1beta/openai/",
		},
		{
			name:         "OpenAI URL remains unchanged",
			extractorURL: "https://api.openai.com/v1",
			want:         "https://api.openai.com/v1",
		},
		{
			name:         "Local OpenAI proxy remains unchanged",
			extractorURL: "http://localhost:8000/v1/openai",
			want:         "http://localhost:8000/v1/openai",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveLLMURL(tt.extractorURL)
			if got != tt.want {
				t.Errorf("resolveLLMURL(%q) = %q, want %q", tt.extractorURL, got, tt.want)
			}
		})
	}
}
