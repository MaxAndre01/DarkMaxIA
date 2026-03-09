package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"darkmax/internal/admin"
	"darkmax/internal/auth"
	"darkmax/internal/handlers"
	"darkmax/internal/logger"
)

func main() {
	// Inicializar logger
	appLog := logger.New("logs/darkmax.log")
	appLog.Info("🚀 DarkMax IA iniciando...")

	// Inicializar almacenamiento
	store, err := auth.NewStore("data/keys.json")
	if err != nil {
		log.Fatalf("Error al cargar store: %v", err)
	}

	// Inicializar admin
	adminMgr := admin.NewManager(store, appLog)

	// Inicializar bot handler
	telegramToken := getEnv("TELEGRAM_TOKEN", "8444790565:AAFZJvpPGFBZAjm-jvmYkiVXQcIRiCMH3rg")
	openRouterKey := getEnv("OPENROUTER_KEY", "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")

	bot, err := handlers.NewBot(telegramToken, openRouterKey, store, adminMgr, appLog)
	if err != nil {
		log.Fatalf("Error al crear bot: %v", err)
	}

	appLog.Info("✅ DarkMax IA activo y listo")

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go bot.Start()

	<-quit
	appLog.Info("🛑 DarkMax IA apagándose...")
	bot.Stop()
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}