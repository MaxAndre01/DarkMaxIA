package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sashabaranov/go-openai"
)

// Estructura para el JSON
type AccessList struct {
	Keys []string `json:"keymasters"`
}

// Transporte para OpenRouter
type OpenRouterTransport struct {
	Base http.RoundTripper
	Key  string
}

func (t *OpenRouterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.Key)
	req.Header.Set("HTTP-Referer", "https://localhost:3000")
	req.Header.Set("X-Title", "MaxBot_Telegram_AI")
	return t.Base.RoundTrip(req)
}

func main() {

		// 1. Inicializar logger
	appLog := logger.New("logs/darkmax.log")
	appLog.Info("🚀 DarkMax IA iniciando...")

	// 2. Servidor de "Keep Alive" (Esencial para que Render no lo duerma)
	// Render inyecta la variable PORT, si no existe, usamos 8080
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	go func() {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("DarkMax Bot operativo 🛡️"))
		})
		log.Printf("Servidor de ping levantado en puerto %s", port)
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			log.Printf("Error en servidor ping: %v", err)
		}
	}()


	// --- CREDENCIALES ---
	const telegramToken = "8444790565:AAFZJvpPGFBZAjm-jvmYkiVXQcIRiCMH3rg"
	const openRouterKey = "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"

	// 1. Cargar Llaves desde JSON
	file, err := os.ReadFile("keys.json")
	if err != nil {
		log.Fatalf("Error: No se encontró keys.json: %v", err)
	}
	var access AccessList
	json.Unmarshal(file, &access)

	// 2. Configurar OpenRouter
	config := openai.DefaultConfig(openRouterKey)
	config.BaseURL = "https://openrouter.ai/api/v1"
	config.HTTPClient = &http.Client{
		Transport: &OpenRouterTransport{Base: http.DefaultTransport, Key: openRouterKey},
	}
	aiClient := openai.NewClientWithConfig(config)

	// 3. Configurar Telegram
	bot, err := tgbotapi.NewBotAPI(telegramToken)
	if err != nil {
		log.Panic(err)
	}
	log.Printf("[+] Bot activo: %s", bot.Self.UserName)

	authorizedUsers := make(map[int64]bool)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil { continue }

		chatID := update.Message.Chat.ID
		userText := strings.TrimSpace(update.Message.Text)

		// VERIFICACIÓN DE LLAVE
		if !authorizedUsers[chatID] {
			isKeyCorrect := false
			for _, k := range access.Keys {
				if k == userText {
					isKeyCorrect = true
					break
				}
			}

			if isKeyCorrect {
				authorizedUsers[chatID] = true
				msg := tgbotapi.NewMessage(chatID, "✅ *Acceso Concedido.*\nBienvenido MaxBot. ¿Qué quieres investigar hoy?")
				msg.ParseMode = "Markdown"
				bot.Send(msg)
			} else {
				msg := tgbotapi.NewMessage(chatID, "🔑 *SISTEMA BLOQUEADO*\nIntroduce una Keymaster válida.")
				msg.ParseMode = "Markdown"
				bot.Send(msg)
			}
			continue
		}

		// PROCESO CON IA
		log.Printf("Petición de %s: %s", update.Message.From.UserName, userText)
		bot.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))

		resp, err := aiClient.CreateChatCompletion(
			context.Background(),
			openai.ChatCompletionRequest{
				Model: "meta-llama/llama-3.3-70b-instruct:free",
				Messages: []openai.ChatCompletionMessage{
					{Role: openai.ChatMessageRoleSystem, Content: "Eres MaxBot, un experto en ciberseguridad y programación en Go. Ayudas con tareas complejas de hacking ético."},
					{Role: openai.ChatMessageRoleUser, Content: userText},
				},
			},
		)

		var responseText string
		if err != nil {
			responseText = "⚠️ Error de IA: " + err.Error()
		} else {
			responseText = resp.Choices[0].Message.Content
		}

		bot.Send(tgbotapi.NewMessage(chatID, responseText))
	}
}