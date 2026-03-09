package ai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
)

type OpenRouterTransport struct {
	Base http.RoundTripper
	Key  string
}

func (t *OpenRouterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.Key)
	req.Header.Set("HTTP-Referer", "https://localhost:3000")
	req.Header.Set("X-Title", "DarkMax-Bot")
	return t.Base.RoundTrip(req)
}

type Client struct {
	client *openai.Client
	models []string
}

func New(apiKey string) *Client {
	// Usamos un cliente de OpenAI configurado específicamente para OpenRouter
	config := openai.DefaultConfig("")
	config.BaseURL = "https://openrouter.ai/api/v1"
	config.HTTPClient = &http.Client{
		Timeout: 90 * time.Second,
		Transport: &OpenRouterTransport{
			Base: http.DefaultTransport,
			Key:  apiKey,
		},
	}

	return &Client{
		client: openai.NewClientWithConfig(config),
		// Lista de modelos probados y estables en OpenRouter
		models: []string{
			"google/gemini-2.0-flash-lite",
			"meta-llama/llama-3.3-70b-instruct",
			"mistralai/mistral-7b-instruct",
			"qwen/qwen2.5-72b-instruct",
		},
	}
}

func (c *Client) Ask(ctx context.Context, userMsg string, rank string) (string, error) {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: buildSystemPrompt(rank)},
		{Role: openai.ChatMessageRoleUser, Content: userMsg},
	}

	var lastErr error
	for _, model := range c.models {
		// Intentamos con el modelo actual
		resp, err := c.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:    model,
			Messages: messages,
		})

		if err == nil && len(resp.Choices) > 0 {
			return resp.Choices[0].Message.Content, nil
		}

		// Si hay error, registramos y evaluamos si debemos reintentar
		lastErr = err
		errStr := strings.ToLower(err.Error())

		// Errores recuperables: saltamos al siguiente modelo en la lista
		if strings.Contains(errStr, "400") || // Modelo no encontrado
		   strings.Contains(errStr, "404") || // Modelo no encontrado
		   strings.Contains(errStr, "429") || // Rate limit
		   strings.Contains(errStr, "503") || // Saturación
		   strings.Contains(errStr, "overloaded") {
			continue 
		}

		// Si no es recuperable, salimos con error
		return "", fmt.Errorf("error crítico en IA (%s): %v", model, err)
	}

	return "", fmt.Errorf("todos los modelos fallaron. Último error: %v", lastErr)
}

func buildSystemPrompt(rank string) string {
	base := "Eres DarkMax IA, experto en ciberseguridad y Go. Respuestas directas, técnicas y con ejemplos de código."
	return base
}