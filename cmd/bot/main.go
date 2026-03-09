package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"darkmax/internal/admin"
	"darkmax/internal/auth"
	"darkmax/internal/handlers"
	"darkmax/internal/logger"
)

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

	// 3. Inicializar almacenamiento (Cargar JSON)
	store, err := auth.NewStore("data/keys.json")
	if err != nil {
		log.Fatalf("Error al cargar store: %v", err)
	}

	// 4. Inicializar manager de admins
	adminMgr := admin.NewManager(store, appLog)

	// 5. Variables de entorno (Obligatorias para producción)
	telegramToken := os.Getenv("TELEGRAM_TOKEN")
	openRouterKey := os.Getenv("OPENROUTER_KEY")

	if telegramToken == "" || openRouterKey == "" {
		log.Fatal("❌ ERROR: TELEGRAM_TOKEN o OPENROUTER_KEY no definidos en el entorno.")
	}

	// 6. Inicializar bot handler
	bot, err := handlers.NewBot(telegramToken, openRouterKey, store, adminMgr, appLog)
	if err != nil {
		log.Fatalf("Error crítico al crear bot: %v", err)
	}

	appLog.Info("✅ DarkMax IA activo y listo")

	// 7. Manejo de señales para cierre limpio (Graceful shutdown)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Iniciar bot en su propia rutina
	go bot.Start()

	<-quit
	appLog.Info("🛑 DarkMax IA apagándose...")
	bot.Stop()
}