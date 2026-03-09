package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"darkmax/internal/admin"
	"darkmax/internal/ai"
	"darkmax/internal/auth"
	"darkmax/internal/logger"
	"darkmax/internal/ratelimit"
)

type Bot struct {
	api        *tgbotapi.BotAPI
	ai         *ai.Client
	store      *auth.Store
	admin      *admin.Manager
	log        *logger.Logger
	limiter    *ratelimit.UserLimiter
	stopChan   chan struct{}
	adminState map[int64]*adminWizard
}

type adminWizard struct {
	step    string
	data    map[string]string
	expires time.Time
}

func NewBot(token, openRouterKey string, store *auth.Store, adminMgr *admin.Manager, log *logger.Logger) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}
	return &Bot{
		api:        api,
		ai:         ai.New(openRouterKey),
		store:      store,
		admin:      adminMgr,
		log:        log,
		limiter:    ratelimit.NewUserLimiter(),
		stopChan:   make(chan struct{}),
		adminState: make(map[int64]*adminWizard),
	}, nil
}

func (b *Bot) Start() {
	b.log.Info(fmt.Sprintf("Bot activo: @%s", b.api.Self.UserName))
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := b.api.GetUpdatesChan(u)
	jobs := make(chan tgbotapi.Update, 500)
	for i := 0; i < 20; i++ {
		go b.worker(jobs)
	}
	for {
		select {
		case <-b.stopChan:
			close(jobs)
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			jobs <- update
		}
	}
}

func (b *Bot) Stop() {
	close(b.stopChan)
	b.api.StopReceivingUpdates()
}

func (b *Bot) worker(jobs <-chan tgbotapi.Update) {
	for update := range jobs {
		b.handleUpdate(update)
	}
}

func (b *Bot) handleUpdate(update tgbotapi.Update) {
	if update.CallbackQuery != nil {
		b.handleCallback(update.CallbackQuery)
		return
	}
	if update.Message == nil {
		return
	}
	msg := update.Message
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	b.log.Request(chatID, msg.From.UserName, text)
	if msg.IsCommand() {
		b.handleCommand(msg)
		return
	}
	if !b.store.IsAuthorized(chatID) {
		b.handleAuth(chatID, msg.From.UserName, text)
		return
	}
	if wizard, ok := b.adminState[chatID]; ok && time.Now().Before(wizard.expires) {
		b.handleWizard(chatID, msg.From.UserName, text, wizard)
		return
	}
	if b.store.IsAdmin(chatID) && strings.HasPrefix(text, "/") {
		b.handleAdminCommand(chatID, msg.From.UserName, text)
		return
	}
	b.handleAIQuery(chatID, msg.From.UserName, text)
}

func (b *Bot) handleCommand(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	switch msg.Command() {
	case "start":
		b.sendStart(chatID)
	case "help":
		b.sendHelp(chatID)
	case "menu":
		if b.store.IsAuthorized(chatID) {
			b.sendMainMenu(chatID)
		} else {
			b.sendStart(chatID)
		}
	case "admin":
		if b.store.IsAdmin(chatID) {
			b.sendAdminMenu(chatID)
		} else {
			b.send(chatID, "Sin permisos de administrador.")
		}
	case "stats":
		if b.store.IsAdmin(chatID) {
			b.send(chatID, b.admin.Stats())
		}
	}
}

func (b *Bot) handleAuth(chatID int64, username, text string) {
	if b.store.IsAdminKey(text) {
		b.store.CreateSession(chatID, username, text, auth.RankAdmin)
		b.log.Auth(chatID, text, "ADMIN_ACCESS")
		b.sendAdminWelcome(chatID, username)
		return
	}
	entry, ok := b.store.ValidateKey(text)
	if ok {
		b.store.CreateSession(chatID, username, text, entry.Rank)
		b.log.Auth(chatID, text, "ACCESS_GRANTED rank="+string(entry.Rank))
		b.sendWelcome(chatID, username, entry.Rank)
		return
	}
	b.log.Auth(chatID, text, "DENIED")
	b.sendLocked(chatID)
}

func (b *Bot) handleAIQuery(chatID int64, username, text string) {
	if !b.limiter.Allow(chatID) {
		b.send(chatID, "Demasiado rapido! Espera un momento.")
		return
	}
	if b.limiter.IsInFlight(chatID) {
		b.send(chatID, "Procesando consulta anterior... espera.")
		return
	}
	b.limiter.SetInFlight(chatID, true)
	defer b.limiter.SetInFlight(chatID, false)
	b.store.IncrementMessages(chatID)
	b.api.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
	rank := "user"
	if sess, ok := b.store.GetSession(chatID); ok {
		rank = string(sess.Rank)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	response, err := b.ai.Ask(ctx, text, rank)
	if err != nil {
		b.log.Error(fmt.Sprintf("AI error for %d: %v", chatID, err))
		b.send(chatID, "Error de conexion con la IA. Intenta de nuevo.")
		return
	}
	b.sendLong(chatID, response)
}

func (b *Bot) handleCallback(cb *tgbotapi.CallbackQuery) {
	chatID := cb.Message.Chat.ID
	data := cb.Data
	b.api.Request(tgbotapi.NewCallback(cb.ID, ""))
	if !b.store.IsAuthorized(chatID) {
		return
	}
	switch data {
	case "menu_ai":
		b.send(chatID, "Escribe tu consulta y DarkMax IA respondera.\n\nEspecialidades:\n- Ciberseguridad\n- Programacion\n- OSINT\n- Vulnerabilidades\n- Redes")
	case "menu_help":
		b.sendHelp(chatID)
	case "admin_create_key":
		if b.store.IsAdmin(chatID) {
			b.startCreateKeyWizard(chatID)
		}
	case "admin_list_keys":
		if b.store.IsAdmin(chatID) {
			b.send(chatID, b.admin.ListAllKeys())
		}
	case "admin_stats":
		if b.store.IsAdmin(chatID) {
			b.send(chatID, b.admin.Stats())
		}
	case "admin_sessions":
		if b.store.IsAdmin(chatID) {
			b.send(chatID, b.admin.ActiveSessions())
		}
	case "admin_menu":
		if b.store.IsAdmin(chatID) {
			b.sendAdminMenu(chatID)
		}
	case "rank_user", "rank_vip", "rank_admin":
		if wiz, ok := b.adminState[chatID]; ok && wiz.step == "rank" {
			rankMap := map[string]auth.KeyRank{
				"rank_user":  auth.RankUser,
				"rank_vip":   auth.RankVIP,
				"rank_admin": auth.RankAdmin,
			}
			wiz.data["rank"] = string(rankMap[data])
			wiz.step = "owner"
			b.send(chatID, "Escribe el nombre o alias del dueno de la key:")
		}
	case "expire_never", "expire_7", "expire_30", "expire_90":
		if wiz, ok := b.adminState[chatID]; ok && wiz.step == "expire" {
			expireMap := map[string]string{
				"expire_never": "0",
				"expire_7":     "7",
				"expire_30":    "30",
				"expire_90":    "90",
			}
			wiz.data["days"] = expireMap[data]
			b.finishCreateKey(chatID, cb.From.UserName, wiz)
		}
	}
}

func (b *Bot) handleAdminCommand(chatID int64, username, text string) {
	parts := strings.Fields(text)
	cmd := parts[0]
	switch cmd {
	case "/deletekey":
		if len(parts) < 2 {
			b.send(chatID, "Uso: /deletekey KEY")
			return
		}
		if err := b.admin.DeleteKey(parts[1], username); err != nil {
			b.send(chatID, "Error: "+err.Error())
		} else {
			b.send(chatID, "Key eliminada: "+parts[1])
		}
	case "/keyinfo":
		if len(parts) < 2 {
			b.send(chatID, "Uso: /keyinfo KEY")
			return
		}
		info, ok := b.admin.GetKeyInfo(parts[1])
		if !ok {
			b.send(chatID, "Key no encontrada.")
		} else {
			b.send(chatID, info)
		}
	case "/setrank":
		if len(parts) < 3 {
			b.send(chatID, "Uso: /setrank KEY user|vip|admin")
			return
		}
		rankMap := map[string]auth.KeyRank{
			"user": auth.RankUser, "vip": auth.RankVIP,
			"admin": auth.RankAdmin, "owner": auth.RankOwner,
		}
		rank, ok := rankMap[parts[2]]
		if !ok {
			b.send(chatID, "Rango invalido. Usa: user, vip, admin, owner")
			return
		}
		if err := b.admin.SetRank(parts[1], rank, username); err != nil {
			b.send(chatID, "Error: "+err.Error())
		} else {
			b.send(chatID, "Rango actualizado.")
		}
	case "/togglekey":
		if len(parts) < 2 {
			b.send(chatID, "Uso: /togglekey KEY")
			return
		}
		if err := b.admin.ToggleKey(parts[1], username); err != nil {
			b.send(chatID, "Error: "+err.Error())
		} else {
			b.send(chatID, "Estado de key cambiado.")
		}
	case "/changeadminkey":
		if len(parts) < 2 {
			b.send(chatID, "Uso: /changeadminkey NUEVA_KEY")
			return
		}
		if err := b.admin.ChangeAdminKey(parts[1], username); err != nil {
			b.send(chatID, "Error: "+err.Error())
		} else {
			b.send(chatID, "Admin key actualizada.")
		}
	case "/listkeys":
		b.send(chatID, b.admin.ListAllKeys())
	case "/sessions":
		b.send(chatID, b.admin.ActiveSessions())
	default:
		b.handleAIQuery(chatID, username, text)
	}
}

func (b *Bot) startCreateKeyWizard(chatID int64) {
	b.adminState[chatID] = &adminWizard{
		step:    "rank",
		data:    make(map[string]string),
		expires: time.Now().Add(5 * time.Minute),
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("👤 User", "rank_user"),
			tgbotapi.NewInlineKeyboardButtonData("⭐ VIP", "rank_vip"),
			tgbotapi.NewInlineKeyboardButtonData("👑 Admin", "rank_admin"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, "Crear Nueva Key\n\nPaso 1: Selecciona el rango:")
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

func (b *Bot) handleWizard(chatID int64, username, text string, wiz *adminWizard) {
	switch wiz.step {
	case "owner":
		wiz.data["owner"] = text
		wiz.step = "expire"
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Sin limite", "expire_never"),
				tgbotapi.NewInlineKeyboardButtonData("7 dias", "expire_7"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("30 dias", "expire_30"),
				tgbotapi.NewInlineKeyboardButtonData("90 dias", "expire_90"),
			),
		)
		msg := tgbotapi.NewMessage(chatID, "Paso 3: Cuando expira la key?")
		msg.ReplyMarkup = keyboard
		b.api.Send(msg)
	}
}

func (b *Bot) finishCreateKey(chatID int64, username string, wiz *adminWizard) {
	delete(b.adminState, chatID)
	rankMap := map[string]auth.KeyRank{
		"user": auth.RankUser, "vip": auth.RankVIP, "admin": auth.RankAdmin,
	}
	rank := rankMap[wiz.data["rank"]]
	days := 0
	fmt.Sscanf(wiz.data["days"], "%d", &days)
	entry, err := b.admin.GenerateKey(wiz.data["owner"], username, rank, days)
	if err != nil {
		b.send(chatID, "Error creando key: "+err.Error())
		return
	}
	expStr := "Sin expiracion"
	if entry.ExpiresAt != nil {
		expStr = "Expira: " + entry.ExpiresAt.Format("02/01/2006")
	}
	b.send(chatID, fmt.Sprintf(
		"Key Creada!\n\nKey: %s\nDueno: %s\nRango: %s\n%s",
		entry.Key, entry.Owner, strings.ToUpper(string(rank)), expStr,
	))
}

func (b *Bot) sendStart(chatID int64) {
	text := "DarkMax IA\n\nSistema de IA para ciberseguridad y programacion.\nAcceso restringido.\n\nIntroduce tu keymaster para continuar:"
	b.send(chatID, text)
}

func (b *Bot) sendLocked(chatID int64) {
	text := "SISTEMA BLOQUEADO\n\nAcceso denegado.\nIntroduce una keymaster valida."
	b.send(chatID, text)
}

func (b *Bot) sendWelcome(chatID int64, username string, rank auth.KeyRank) {
	icons := map[auth.KeyRank]string{
		auth.RankUser:  "👤",
		auth.RankVIP:   "⭐",
		auth.RankAdmin: "👑",
		auth.RankOwner: "💎",
	}
	icon := icons[rank]
	text := fmt.Sprintf(
		"Acceso Concedido!\n\nBienvenido @%s\n%s Rango: %s\n\nDarkMax IA listo. Escribe tu consulta o usa /menu",
		username, icon, strings.ToUpper(string(rank)),
	)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Consultar IA", "menu_ai"),
			tgbotapi.NewInlineKeyboardButtonData("Ayuda", "menu_help"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

func (b *Bot) sendAdminWelcome(chatID int64, username string) {
	text := fmt.Sprintf("Panel de Administracion\n\nAcceso admin concedido\nUsuario: @%s\n\nBienvenido al panel de control de DarkMax IA.", username)
	b.send(chatID, text)
	b.sendAdminMenu(chatID)
}

func (b *Bot) sendAdminMenu(chatID int64) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Crear Key", "admin_create_key"),
			tgbotapi.NewInlineKeyboardButtonData("Listar Keys", "admin_list_keys"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Estadisticas", "admin_stats"),
			tgbotapi.NewInlineKeyboardButtonData("Sesiones", "admin_sessions"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Volver a IA", "menu_ai"),
		),
	)
	text := "Panel Admin DarkMax IA\n\nComandos:\n/deletekey KEY\n/keyinfo KEY\n/setrank KEY rango\n/togglekey KEY\n/listkeys\n/sessions\n/changeadminkey NUEVA"
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

func (b *Bot) sendMainMenu(chatID int64) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Consultar IA", "menu_ai"),
			tgbotapi.NewInlineKeyboardButtonData("Ayuda", "menu_help"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, "DarkMax IA - Menu Principal\n\nSelecciona una opcion:")
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

func (b *Bot) sendHelp(chatID int64) {
	text := "DarkMax IA - Ayuda\n\nEspecialidades:\n\nCiberseguridad - hacking etico, pentesting, CTF\nProgramacion - Go, Python, C, Bash\nOSINT - investigacion y recoleccion de info\nRedes - protocolos, trafico, configuracion\n\nComandos:\n/start - Inicio\n/menu - Menu principal\n/help - Esta ayuda"
	b.send(chatID, text)
}

func (b *Bot) send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	b.api.Send(msg)
}

func (b *Bot) sendLong(chatID int64, text string) {
	const maxLen = 4000
	if len(text) <= maxLen {
		b.send(chatID, text)
		return
	}
	parts := splitText(text, maxLen)
	for i, part := range parts {
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		b.send(chatID, part)
	}
}

func splitText(text string, maxLen int) []string {
	var parts []string
	for len(text) > maxLen {
		cutAt := maxLen
		for i := cutAt; i > maxLen-200 && i > 0; i-- {
			if text[i] == '\n' {
				cutAt = i
				break
			}
		}
		parts = append(parts, text[:cutAt])
		text = text[cutAt:]
	}
	if text != "" {
		parts = append(parts, text)
	}
	return parts
}

func rankIcon(rank auth.KeyRank) string {
	switch rank {
	case auth.RankOwner:
		return "💎"
	case auth.RankAdmin:
		return "👑"
	case auth.RankVIP:
		return "⭐"
	default:
		return "👤"
	}
}


func formatDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// maskKey oculta parte de la key por seguridad: DM-ABCD1234 → DM-****1234
func maskKey(key string) string {
	if len(key) <= 6 {
		return "****"
	}
	visible := 4
	masked := len(key) - visible
	if masked < 4 {
		masked = 4
	}
	return key[:len(key)-masked] + strings.Repeat("*", masked-visible) + key[len(key)-visible:]
}
