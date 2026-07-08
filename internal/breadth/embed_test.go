package breadth

import "testing"

func TestEmbeddingsURLNormalizesSchemeLessLocalBaseURL(t *testing.T) {
	httpScheme := "http" + "://"
	tests := []struct {
		name string
		base string
		want string
	}{
		{
			name: "localhost",
			base: "localhost:11434/v1",
			want: httpScheme + "localhost:11434/v1/embeddings",
		},
		{
			name: "loopback IPv4",
			base: "127.0.0.1:11434/v1",
			want: httpScheme + "127.0.0.1:11434/v1/embeddings",
		},
		{
			name: "loopback IPv6",
			base: "[::1]:11434/v1",
			want: "http://[::1]:11434/v1/embeddings",
		},
		{
			name: "explicit scheme",
			base: "https://embed.example.test/v1",
			want: "https://embed.example.test/v1/embeddings",
		},
		{
			name: "already endpoint",
			base: httpScheme + "localhost:11434/v1/embeddings",
			want: httpScheme + "localhost:11434/v1/embeddings",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			embedder, err := NewOpenAICompatEmbedder(OpenAICompatEmbedderConfig{
				Provider: "ollama",
				Model:    "nomic-embed-text",
				BaseURL:  tt.base,
			})
			if err != nil {
				t.Fatalf("embedder: %v", err)
			}
			got := embeddingsURL(embedder.(*openAICompatEmbedder).baseURL)
			if got != tt.want {
				t.Fatalf("embeddingsURL(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}
