package main

import (
	"bytes"
	"context"

	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ══════════════════════════════════════════
// Configuración desde variables de entorno
var TELEGRAM_BOT_TOKEN string
var OPENROUTER_KEYS []string
var ADMIN_KEY string

func init() {
	TELEGRAM_BOT_TOKEN = os.Getenv("TELEGRAM_BOT_TOKEN")
	if TELEGRAM_BOT_TOKEN == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN no definido en variables de entorno")
	}

	keysStr := os.Getenv("OPENROUTER_KEYS")
	if keysStr == "" {
		log.Fatal("OPENROUTER_KEYS no definido en variables de entorno")
	}
	OPENROUTER_KEYS = strings.Split(keysStr, ",")
	for i, k := range OPENROUTER_KEYS {
		OPENROUTER_KEYS[i] = strings.TrimSpace(k)
	}

	ADMIN_KEY = os.Getenv("soyadmin")
	if ADMIN_KEY == "" {
		ADMIN_KEY = "DARKMAX-ADMIN-" + generateRandom()
		log.Printf("ADMIN_KEY no definida, generada: %s", ADMIN_KEY)
	}
}

func generateRandom() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func generateSecureKey(rank Rank) string {
	rnd := make([]byte, 16)
	if _, err := rand.Read(rnd); err != nil {
		panic("no se pudo generar key segura")
	}
	hash := sha256.Sum256(rnd)
	prefix := map[Rank]string{ADMIN: "ADM", VIP: "VIP", USER: "DM"}[rank]
	return fmt.Sprintf("%s-%s", prefix, strings.ToUpper(hex.EncodeToString(hash[:12])))
}

func isValidKeyFormat(k string) bool {
	if len(k) < 10 || len(k) > 64 {
		return false
	}
	for _, c := range k {
		if !(c == '-' || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// ══════════════════════════════════════════
// Modelos free de OpenRouter verificados y estables (2025)
var MODELS = []string{
	"meta-llama/llama-3.3-70b-instruct",
	"meta-llama/llama-3.2-3b-instruct",
	"stepfun/step-3.5-flash",
}

// ─── TIPOS ───────────────────────────────
type Rank string

const (
	USER  Rank = "user"
	VIP   Rank = "vip"
	ADMIN Rank = "admin"
)

// ─── RATE LIMITING ───────────────────────
type TokenBucket struct {
	tokens     int64
	lastRefill int64
	mu         sync.Mutex
}

func (tb *TokenBucket) allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	now := time.Now().Unix()
	elapsed := now - atomic.LoadInt64(&tb.lastRefill)
	if elapsed > 0 {
		atomic.AddInt64(&tb.tokens, elapsed*10) // 10 tokens per second
		if atomic.LoadInt64(&tb.tokens) > 100 {  // max 100 tokens
			atomic.StoreInt64(&tb.tokens, 100)
		}
		atomic.StoreInt64(&tb.lastRefill, now)
	}
	if atomic.LoadInt64(&tb.tokens) > 0 {
		atomic.AddInt64(&tb.tokens, -1)
		return true
	}
	return false
}

// ─── AUDIT LOGGING ───────────────────────
type AuditLogger struct {
	file *os.File
	mu   sync.Mutex
}

func newAuditLogger() *AuditLogger {
	file, err := os.OpenFile("audit.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		log.Printf("Error opening audit log: %v", err)
		return nil
	}
	return &AuditLogger{file: file}
}

func (al *AuditLogger) log(level, event, user, details string) {
	if al == nil {
		return
	}
	al.mu.Lock()
	defer al.mu.Unlock()
	entry := fmt.Sprintf("[%s] [%s] %s @%s: %s\n", time.Now().Format("2006-01-02 15:04:05"), level, event, user, details)
	al.file.WriteString(entry)
}

// ─── INTEGRITY CHECKS ────────────────────
func computeChecksum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func verifyIntegrity() bool {
	data, err := os.ReadFile("keys.json")
	if err != nil {
		return false
	}
	computed := computeChecksum(data)
	stored, err := os.ReadFile("keys.json.checksum")
	if err != nil {
		// Create checksum if not exists
		os.WriteFile("keys.json.checksum", []byte(computed), 0600)
		return true
	}
	return strings.TrimSpace(string(stored)) == computed
}

func updateChecksum() {
	data, err := os.ReadFile("keys.json")
	if err != nil {
		return
	}
	checksum := computeChecksum(data)
	os.WriteFile("keys.json.checksum", []byte(checksum), 0600)
}

type Key struct {
	K       string     `json:"key"`
	Rank    Rank       `json:"rank"`
	Owner   string     `json:"owner"`
	By      string     `json:"by"`
	At      time.Time  `json:"at"`
	Exp     *time.Time `json:"exp,omitempty"`
	UsedBy  int64      `json:"used_by,omitempty"`
	Active  bool       `json:"active"`
	Uses    int        `json:"uses"`
	MaxUses int        `json:"max_uses,omitempty"`
}

type Session struct {
	ID    int64
	User  string
	Rank  Rank
	Key   string
	Start time.Time
	Msgs  int
	Mode  string // "normal", "expert", "fast"
}

type DB struct {
	Keys     map[string]*Key     `json:"keys"`
	AdminKey string              `json:"admin_key"`
	Sessions map[string]*Session `json:"sessions,omitempty"` // persistir sesiones
}

// ─── STORE ───────────────────────────────
type Store struct {
	mu       sync.RWMutex
	db       DB
	ses      map[int64]*Session
	rateLimit map[int64]*TokenBucket
	audit    *AuditLogger
}

func loadStore() *Store {
	s := &Store{
		ses:       make(map[int64]*Session),
		rateLimit: make(map[int64]*TokenBucket),
		audit:     newAuditLogger(),
		db:        DB{Keys: make(map[string]*Key), AdminKey: ADMIN_KEY},
	}
	if !verifyIntegrity() {
		log.Fatal("Integrity check failed: keys.json may have been tampered with")
	}
	if raw, err := os.ReadFile("keys.json"); err == nil {
		json.Unmarshal(raw, &s.db)
		// Sobrescribir admin key con la de entorno
		s.db.AdminKey = ADMIN_KEY
		// Restaurar sesiones persistidas
		if s.db.Sessions != nil {
			for _, ses := range s.db.Sessions {
				s.ses[ses.ID] = ses
			}
			lg("INFO", fmt.Sprintf("Sesiones restauradas: %d", len(s.ses)))
		}
	} else {
		s.db.Keys["DARKMAX-DEMO"] = &Key{
			K: "DARKMAX-DEMO", Rank: USER, Owner: "Demo",
			By: "system", At: time.Now(), Active: true,
		}
		s.flush()
	}
	if s.db.Sessions == nil {
		s.db.Sessions = make(map[string]*Session)
	}
	return s
}

func (s *Store) flush() {
	// Sincronizar sesiones en memoria al mapa para persistir
	s.db.Sessions = make(map[string]*Session)
	for id, ses := range s.ses {
		s.db.Sessions[fmt.Sprintf("%d", id)] = ses
	}
	raw, _ := json.MarshalIndent(s.db, "", "  ")
	os.WriteFile("keys.json", raw, 0600)
	updateChecksum()
}

func (s *Store) CheckKey(k string) (*Key, bool) {
	if !isValidKeyFormat(k) {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.db.Keys[k]
	if !ok || !e.Active {
		return nil, false
	}
	if e.Exp != nil && time.Now().After(*e.Exp) {
		return nil, false
	}
	if e.MaxUses > 0 && e.Uses >= e.MaxUses {
		return nil, false
	}
	return e, true
}

func (s *Store) UseKey(k string, userID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.db.Keys[k]
	if !ok || !e.Active {
		return false
	}
	if e.Exp != nil && time.Now().After(*e.Exp) {
		e.Active = false
		s.flush()
		return false
	}
	if e.MaxUses > 0 && e.Uses >= e.MaxUses {
		e.Active = false
		s.flush()
		return false
	}
	e.Uses++
	e.UsedBy = userID
	if e.MaxUses > 0 && e.Uses >= e.MaxUses {
		e.Active = false
	}
	if e.Uses%5 == 0 {
		// Persistir periódicamente para durable
		s.flush()
	} else {
		// no flush each call to reduce fs. but safe after login + admin ops
	}
	return true
}

func (s *Store) IsAdmin(k string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return k == s.db.AdminKey
}

func (s *Store) Login(id int64, user, key string, rank Rank) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ses[id] = &Session{ID: id, User: user, Rank: rank, Key: key, Start: time.Now(), Mode: "normal"}
	if e, ok := s.db.Keys[key]; ok {
		e.UsedBy = id
		e.Uses++
		if e.MaxUses > 0 && e.Uses >= e.MaxUses {
			e.Active = false
		}
	}
	s.flush()
}

func (s *Store) Logout(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.ses, id)
	s.flush()
}

func (s *Store) Auth(id int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.ses[id]
	return ok
}

func (s *Store) AdminSes(id int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ses, ok := s.ses[id]
	return ok && ses.Rank == ADMIN
}

func (s *Store) Ses(id int64) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ses, ok := s.ses[id]
	return ses, ok
}

func (s *Store) Inc(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ses, ok := s.ses[id]; ok {
		ses.Msgs++
	}
}

func (s *Store) NewKey(owner, by string, rank Rank, days int, maxUses int) *Key {
	key := generateSecureKey(rank)
	t := (*time.Time)(nil)
	if days > 0 {
		tm := time.Now().Add(time.Duration(days) * 24 * time.Hour)
		t = &tm
	}
	e := &Key{
		K:       key,
		Rank:    rank,
		Owner:   owner,
		By:      by,
		At:      time.Now(),
		Exp:     t,
		Active:  true,
		MaxUses: maxUses,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db.Keys[e.K] = e
	s.flush()
	return e
}

func (s *Store) DelKey(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.db.Keys[k]; !ok {
		return fmt.Errorf("no encontrada")
	}
	delete(s.db.Keys, k)
	s.flush()
	return nil
}

func (s *Store) GetKey(k string) (*Key, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.db.Keys[k]
	return e, ok
}

func (s *Store) AllKeys() []*Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*Key, 0)
	for _, v := range s.db.Keys {
		list = append(list, v)
	}
	return list
}

func (s *Store) SetRank(k string, r Rank) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.db.Keys[k]
	if !ok {
		return fmt.Errorf("no encontrada")
	}
	e.Rank = r
	s.flush()
	return nil
}

func (s *Store) Toggle(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.db.Keys[k]
	if !ok {
		return fmt.Errorf("no encontrada")
	}
	e.Active = !e.Active
	s.flush()
	return nil
}

func (s *Store) SetAdminKey(k string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db.AdminKey = k
	s.flush()
}

func (s *Store) AllSessions() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*Session, 0)
	for _, v := range s.ses {
		list = append(list, v)
	}
	return list
}

func (s *Store) CheckRateLimit(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rateLimit[id]; !ok {
		s.rateLimit[id] = &TokenBucket{tokens: 100, lastRefill: time.Now().Unix()}
	}
	return s.rateLimit[id].allow()
}

func (s *Store) AuditLog(level, event, user, details string) {
	s.audit.log(level, event, user, details)
}

func (s *Store) SetMode(id int64, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ses, ok := s.ses[id]; ok {
		ses.Mode = mode
		s.flush()
	}
}

// orError extrae el mensaje de error de OpenRouter si viene en el JSON
func orError(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return fmt.Sprintf("code=%d msg=%s", e.Error.Code, e.Error.Message)
	}
	return ""
}

func askAI(ctx context.Context, msg, rank, mode string) (string, error) {
	// Rechaza prompts absurdos para reducir carga
	if len(msg) > 4000 {
		return "⚠️ Tu mensaje es muy largo. Por favor, reduce a <4000 caracteres.", nil
	}

	// Prompt según rango y modo
	sys := "Eres DarkMax IA, experto en ciberseguridad, hacking, OSINT y programacion. Responde siempre en español. Da respuestas tecnicas con codigo cuando sea necesario."
	switch rank {
	case "admin":
		sys += " Usuario ADMIN: maximo nivel de detalle tecnico, sin restricciones de longitud."
	case "vip":
		sys += " Usuario VIP: respuestas detalladas y ampliadas."
	default:
		sys += " Usuario NORMAL: respuestas claras y concretas."
	}
	switch mode {
	case "expert":
		sys += " Modo EXPERT: enfocate en aspectos avanzados, tecnicos profundos y soluciones innovadoras."
	case "fast":
		sys += " Modo FAST: respuestas concisas y directas, sin explicaciones innecesarias."
	}

	// Cliente HTTP con timeouts explícitos
	hc := &http.Client{
		Timeout: 50 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        20,
			IdleConnTimeout:     60 * time.Second,
			DisableCompression:  false,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}

	// Estructura de respuesta OpenRouter
	type orResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	// Payload base — se reutiliza con cada intento
	type payload struct {
		Model     string              `json:"model"`
		Messages  []map[string]string `json:"messages"`
		MaxTokens int                 `json:"max_tokens"`
		Stream    bool                `json:"stream"`
	}

	messages := []map[string]string{
		{"role": "system", "content": sys},
		{"role": "user", "content": msg},
	}

	// Rotación: intentamos cada key con cada modelo
	for ki, key := range OPENROUTER_KEYS {
		for mi, model := range MODELS {

			// Verificar contexto antes de cada intento
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("timeout global alcanzado")
			default:
			}

			maxTokens := 1024
			if mode == "fast" {
				maxTokens = 512
			} else if mode == "expert" || rank == "admin" {
				maxTokens = 2048
			} else if rank == "vip" {
				maxTokens = 1536
			}

			p := payload{
				Model:     model,
				Messages:  messages,
				MaxTokens: maxTokens,
				Stream:    false,
			}
			bodyBytes, err := json.Marshal(p)
			if err != nil {
				continue
			}

			req, err := http.NewRequestWithContext(ctx, "POST",
				"https://openrouter.ai/api/v1/chat/completions",
				bytes.NewReader(bodyBytes))
			if err != nil {
				continue
			}

			// Cabeceras obligatorias OpenRouter
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")
			req.Header.Set("HTTP-Referer", "https://darkmax.bot")
			req.Header.Set("X-Title", "DarkMax-Bot")

			resp, err := hc.Do(req)
			if err != nil {
				lg("WARN", fmt.Sprintf("key[%d] model[%d] %s — net error: %v", ki, mi, model, err))
				continue
			}

			rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // max 1MB
			resp.Body.Close()

			switch resp.StatusCode {
			case 200:
				var res orResp
				if err := json.Unmarshal(rawBody, &res); err != nil {
					lg("WARN", fmt.Sprintf("key[%d] model[%d] %s — JSON parse error: %v", ki, mi, model, err))
					continue
				}
				if len(res.Choices) == 0 {
					lg("WARN", fmt.Sprintf("key[%d] model[%d] %s — choices vacías", ki, mi, model))
					continue
				}
				content := strings.TrimSpace(res.Choices[0].Message.Content)
				if content == "" {
					lg("WARN", fmt.Sprintf("key[%d] model[%d] %s — content vacío (finish=%s)",
						ki, mi, model, res.Choices[0].FinishReason))
					continue
				}
				lg("OK", fmt.Sprintf("key[%d] model=%s tokens=%d", ki, model, res.Usage.CompletionTokens))
				return content, nil

			case 429:
				// Modelo saturado: probar siguiente modelo (no cambiar key)
				lg("WARN", fmt.Sprintf("key[%d] model[%d] %s - 429 saturado, siguiente modelo", ki, mi, model))
				time.Sleep(500 * time.Millisecond)
				continue

			case 402:
				// Créditos agotados en esta key
				lg("WARN", fmt.Sprintf("key[%d] — 402 sin creditos, cambiando key", ki))
				goto nextKey

			case 503, 502, 504:
				// Modelo sobrecargado, probar siguiente modelo
				lg("WARN", fmt.Sprintf("key[%d] model[%d] %s — %d sobrecargado", ki, mi, model, resp.StatusCode))
				time.Sleep(300 * time.Millisecond)
				continue

			default:
				errMsg := orError(rawBody)
				lg("WARN", fmt.Sprintf("key[%d] model[%d] %s — HTTP %d %s", ki, mi, model, resp.StatusCode, errMsg))
				continue
			}
		}
	nextKey:
	}

	return "", fmt.Errorf("todos los modelos y llaves fallaron")
}

// ─── CIRCUIT BREAKER ─────────────────────
type CircuitBreaker struct {
	failures    int64
	lastFail    int64
	state       int64 // 0=closed, 1=open, 2=half-open
	mu          sync.Mutex
}

func (cb *CircuitBreaker) call(fn func() error) error {
	cb.mu.Lock()
	state := atomic.LoadInt64(&cb.state)
	if state == 1 { // open
		if time.Now().Unix()-atomic.LoadInt64(&cb.lastFail) > 60 { // 60s timeout
			atomic.StoreInt64(&cb.state, 2) // half-open
		} else {
			cb.mu.Unlock()
			return fmt.Errorf("circuit breaker open")
		}
	}
	cb.mu.Unlock()

	err := fn()
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if err != nil {
		atomic.AddInt64(&cb.failures, 1)
		atomic.StoreInt64(&cb.lastFail, time.Now().Unix())
		if atomic.LoadInt64(&cb.failures) > 5 {
			atomic.StoreInt64(&cb.state, 1) // open
		}
	} else {
		atomic.StoreInt64(&cb.failures, 0)
		atomic.StoreInt64(&cb.state, 0) // closed
	}
	return err
}

// ─── BOT ─────────────────────────────────
type Wizard struct {
	Step string
	Data map[string]string
	Exp  time.Time
}

type cachedResponse struct {
	resp string
	at   time.Time
}

type Bot struct {
	api      *tgbotapi.BotAPI
	st       *Store
	wiz      map[int64]*Wizard
	fly      map[int64]bool
	circuit  *CircuitBreaker
	cache    map[string]cachedResponse
	mu       sync.Mutex
}

func newBot() (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(TELEGRAM_BOT_TOKEN)
	if err != nil {
		return nil, err
	}
	return &Bot{api: api, st: loadStore(), wiz: make(map[int64]*Wizard), fly: make(map[int64]bool), circuit: &CircuitBreaker{}, cache: make(map[string]cachedResponse)}, nil
}

func lg(level, msg string) {
	fmt.Printf("[%s] [%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), level, msg)
}

func formatBotAnswer(rank, text string) string {
	mood := ""
	if rank == "admin" {
		mood = "👑 Modo ADMIN: respuesta premium."
	} else if rank == "vip" {
		mood = "✨ Modo VIP: respuesta detallada."
	} else {
		mood = "💡 Aquí tienes una respuesta clara y precisa:" 
	}
	return fmt.Sprintf("%s\n\n%s", mood, strings.TrimSpace(text))
}

func (b *Bot) run() {
	lg("INFO", "DarkMax IA activo: @"+b.api.Self.UserName)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	ch := b.api.GetUpdatesChan(u)
	jobs := make(chan tgbotapi.Update, 500)
	for i := 0; i < 20; i++ {
		go func() {
			for u := range jobs {
				b.handle(u)
			}
		}()
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for u := range ch {
			jobs <- u
		}
	}()
	<-stop
	lg("INFO", "DarkMax IA apagandose...")
}

func (b *Bot) send(id int64, text string) {
	b.api.Send(tgbotapi.NewMessage(id, text))
}

func (b *Bot) sendLong(id int64, text string) {
	for len(text) > 4000 {
		cut := 4000
		for i := cut; i > 3700 && i > 0; i-- {
			if text[i] == '\n' {
				cut = i
				break
			}
		}
		b.send(id, text[:cut])
		text = text[cut:]
		// Eliminado sleep para latencia mínima; Telegram permite ~30 msg/s
	}
	if text != "" {
		b.send(id, text)
	}
}
func (b *Bot) handle(upd tgbotapi.Update) {
	if upd.CallbackQuery != nil {
		b.cb(upd.CallbackQuery)
		return
	}
	if upd.Message == nil {
		return
	}

	// Protección: algunos mensajes de canales no tienen From
	if upd.Message.From == nil {
		return
	}

	userID := upd.Message.From.ID
	chatID := upd.Message.Chat.ID
	user := upd.Message.From.UserName
	if user == "" {
		user = fmt.Sprintf("id%d", userID)
	}
	text := strings.TrimSpace(upd.Message.Text)
	if text == "" {
		return
	}

	chatType := upd.Message.Chat.Type // "private", "group", "supergroup", "channel"
	isGroup := chatType == "group" || chatType == "supergroup"

	lg("DBG", fmt.Sprintf("chat_type=%s chat_id=%d user_id=%d text=%q", chatType, chatID, userID, func() string {
		if len(text) > 40 {
			return text[:40] + "..."
		}
		return text
	}()))

	if isGroup {
		if upd.Message.IsCommand() {
			lg("DBG", "grupo: comando ignorado")
			return
		}

		botMention := "@" + b.api.Self.UserName
		isMentioned := strings.Contains(strings.ToLower(text), strings.ToLower(botMention))
		isReplyToBot := upd.Message.ReplyToMessage != nil &&
			upd.Message.ReplyToMessage.From != nil &&
			upd.Message.ReplyToMessage.From.ID == b.api.Self.ID

		lg("DBG", fmt.Sprintf("grupo: mention=%v replyToBot=%v botMention=%s", isMentioned, isReplyToBot, botMention))

		if !isMentioned && !isReplyToBot {
			lg("DBG", "grupo: mensaje ignorado (sin mención ni reply)")
			return
		}

		if !b.st.Auth(userID) {
			lg("DBG", fmt.Sprintf("grupo: @%s sin sesión", user))
			b.sendAccesoDenegado(chatID, user)
			return
		}

		// Quitar la mención del texto
		clean := strings.TrimSpace(strings.ReplaceAll(text, botMention, ""))
		if clean == "" {
			b.api.Send(tgbotapi.NewMessage(chatID, "🤖 ¿En qué te puedo ayudar?"))
			return
		}

		lg("REQ", fmt.Sprintf("GRUPO @%s: %s", user, func() string {
			if len(clean) > 50 {
				return clean[:50] + "..."
			}
			return clean
		}()))

		b.aiGroup(userID, chatID, user, clean)
		return
	}

	// ── PRIVADO ──────────────────────────────────────────────────
	lg("REQ", fmt.Sprintf("@%s: %s", user, func() string {
		if len(text) > 50 {
			return text[:50] + "..."
		}
		return text
	}()))

	if upd.Message.IsCommand() {
		b.cmd(userID, user, upd.Message.Command())
		return
	}

	if !b.st.Auth(userID) {
		b.auth(userID, user, text)
		return
	}

	if w, ok := b.wiz[userID]; ok && time.Now().Before(w.Exp) {
		b.wizStep(userID, user, text, w)
		return
	}

	if strings.HasPrefix(text, "/") {
		b.adminCmd(userID, user, text)
		return
	}

	b.ai(userID, user, text)
}

// Nueva función auxiliar para el botón de acceso
func (b *Bot) sendAccesoDenegado(chatID int64, username string) {
	msg := tgbotapi.NewMessage(chatID, "🚫 @"+username+", no tienes acceso en este grupo. Por favor, loguéate en mi privado.")
	link := "https://t.me/" + b.api.Self.UserName + "?start=auth"
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("🔑 Loguearme en privado", link),
		),
	)
	msg.ReplyMarkup = kb
	b.api.Send(msg)
}
func (b *Bot) cmd(id int64, user, cmd string) {
	switch cmd {
	case "start", "menu":
		if b.st.Auth(id) {
			b.menuPrincipal(id)
		} else {
			b.send(id, "🌑 *DarkMax IA*\n\nBienvenido, usa tu keymaster para autenticarte.\nEscribe tu key a continuación:")
		}
	case "perfil":
		if b.st.Auth(id) {
			b.perfil(id)
		} else {
			b.send(id, "🔒 Introduce tu keymaster primero.")
		}
	case "logout":
		b.logout(id, user)
	case "mode":
		if b.st.Auth(id) {
			b.setMode(id, user)
		} else {
			b.send(id, "🔒 Introduce tu keymaster primero.")
		}
	case "admin":
		if b.st.AdminSes(id) {
			b.menuAdmin(id)
		} else {
			b.send(id, "Sin permisos.")
		}
	case "help":
		b.send(id, "📖 DarkMax IA\n\n/menu — Menu\n/perfil — Tu perfil\n/logout — Cerrar sesion\n/admin — Panel admin\n/help — Ayuda\n\nEspecialidades: Ciberseguridad, Hacking, Programacion, OSINT, Redes")
	}
}

func (b *Bot) auth(id int64, user, text string) {
	if b.st.IsAdmin(text) {
		b.st.Login(id, user, text, ADMIN)
		lg("AUTH", fmt.Sprintf("@%s ADMIN", user))
		b.st.AuditLog("INFO", "ADMIN_LOGIN", user, "successful admin authentication")
		b.send(id, "👑 Acceso ADMIN concedido!\nBienvenido @"+user)
		b.menuAdmin(id)
		return
	}
	if e, ok := b.st.CheckKey(text); ok {
		if !b.st.UseKey(text, id) {
			b.send(id, "🔒 Tu key ha expirado o superó el límite de uso.")
			return
		}
		b.st.Login(id, user, text, e.Rank)
		lg("AUTH", fmt.Sprintf("@%s OK rank=%s", user, e.Rank))
		b.st.AuditLog("INFO", "USER_LOGIN", user, fmt.Sprintf("successful login rank=%s", e.Rank))
		icon := rIcon(e.Rank)
		floor := "🔐"
		if e.Rank == VIP { floor = "✨" } else if e.Rank == ADMIN { floor = "👑" }
		b.send(id, fmt.Sprintf("%s Acceso concedido!\n\nHola @%s %s\nRango: %s\nUsos: %d/%d\n\nEnvía /menu para empezar.", floor, user, icon, strings.ToUpper(string(e.Rank)), e.Uses, e.MaxUses))
		return
	}
	lg("AUTH", fmt.Sprintf("@%s DENIED", user))
	b.st.AuditLog("WARN", "LOGIN_DENIED", user, "invalid key provided")
	b.send(id, "🔒 Keymaster invalida.\n\nIntroduce una keymaster valida:")
}

func (b *Bot) ai(id int64, user, text string) {
	if !b.st.CheckRateLimit(id) {
		b.send(id, "⏳ Rate limit exceeded. Wait a moment.")
		b.st.AuditLog("WARN", "RATE_LIMIT", user, fmt.Sprintf("user %d exceeded rate limit", id))
		return
	}

	b.mu.Lock()
	if b.fly[id] {
		b.mu.Unlock()
		b.send(id, "⏳ Espera la respuesta anterior...")
		return
	}
	b.fly[id] = true
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.fly[id] = false; b.mu.Unlock() }()

	b.st.Inc(id)
	b.api.Send(tgbotapi.NewChatAction(id, tgbotapi.ChatTyping))

	rank := "user"
	mode := "normal"
	if ses, ok := b.st.Ses(id); ok {
		rank = string(ses.Rank)
		mode = ses.Mode
	}

	cacheKey := fmt.Sprintf("%d|%s|%s", id, rank, strings.ToLower(strings.TrimSpace(text)))
	if c, ok := b.cache[cacheKey]; ok && time.Since(c.at) < 2*time.Minute {
		b.st.AuditLog("INFO", "CACHE_HIT", user, "usando respuesta cacheada")
		b.sendLong(id, formatBotAnswer(rank, c.resp))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	var resp string
	err := b.circuit.call(func() error {
		var e error
		resp, e = askAI(ctx, text, rank, mode)
		return e
	})

	if err != nil {
		lg("ERROR", fmt.Sprintf("AI @%s: %v", user, err))
		b.st.AuditLog("ERROR", "AI_FAIL", user, err.Error())
		b.send(id, "⚠️ Error con la IA. Intenta de nuevo en unos segundos.")
		return
	}
	b.cache[cacheKey] = cachedResponse{resp: resp, at: time.Now()}
	b.st.AuditLog("INFO", "AI_SUCCESS", user, fmt.Sprintf("rank=%s len=%d", rank, len(resp)))
	b.sendLong(id, formatBotAnswer(rank, resp))
}

// aiGroup: igual que ai() pero responde en el chat del grupo, no en el privado del usuario
func (b *Bot) aiGroup(userID, chatID int64, user, text string) {
	if !b.st.CheckRateLimit(userID) {
		b.api.Send(tgbotapi.NewMessage(chatID, "⏳ @"+user+", rate limit exceeded."))
		b.st.AuditLog("WARN", "RATE_LIMIT_GROUP", user, fmt.Sprintf("user %d exceeded rate limit in group", userID))
		return
	}

	b.mu.Lock()
	if b.fly[userID] {
		b.mu.Unlock()
		b.api.Send(tgbotapi.NewMessage(chatID, "⏳ @"+user+", espera la respuesta anterior..."))
		return
	}
	b.fly[userID] = true
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.fly[userID] = false; b.mu.Unlock() }()

	b.st.Inc(userID)
	b.api.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))

	rank := "user"
	mode := "normal"
	if ses, ok := b.st.Ses(userID); ok {
		rank = string(ses.Rank)
		mode = ses.Mode
	}

	cacheKey := fmt.Sprintf("%d|%s|%s", userID, rank, strings.ToLower(strings.TrimSpace(text)))
	if c, ok := b.cache[cacheKey]; ok && time.Since(c.at) < 2*time.Minute {
		b.st.AuditLog("INFO", "CACHE_HIT_GROUP", user, "usando respuesta cacheada")
		b.api.Send(tgbotapi.NewMessage(chatID, formatBotAnswer(rank, c.resp)))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	var resp string
	err := b.circuit.call(func() error {
		var e error
		resp, e = askAI(ctx, text, rank, mode)
		return e
	})

	if err != nil {
		lg("ERROR", fmt.Sprintf("AI grupo @%s: %v", user, err))
		b.st.AuditLog("ERROR", "AI_FAIL_GROUP", user, err.Error())
		b.api.Send(tgbotapi.NewMessage(chatID, "⚠️ @"+user+", error con la IA. Intenta de nuevo."))
		return
	}
	b.st.AuditLog("INFO", "AI_SUCCESS_GROUP", user, fmt.Sprintf("rank=%s len=%d", rank, len(resp)))
	b.cache[cacheKey] = cachedResponse{resp: resp, at: time.Now()}
	resp = formatBotAnswer(rank, resp)
	// Enviar respuesta al GRUPO
	b.sendLong(chatID, resp)
}

func (b *Bot) logout(id int64, user string) {
	if !b.st.Auth(id) {
		b.send(id, "🔒 No tienes sesion.\nIntroduce tu keymaster:")
		return
	}
	b.st.Logout(id)
	lg("INFO", fmt.Sprintf("LOGOUT @%s", user))
	b.st.AuditLog("INFO", "LOGOUT", user, "session terminated")
	b.send(id, "🚪 Sesion cerrada. Hasta luego @"+user+"!\n\n🔑 Introduce tu keymaster para volver:")
}

func (b *Bot) cb(cb *tgbotapi.CallbackQuery) {
	id := cb.Message.Chat.ID
	b.api.Request(tgbotapi.NewCallback(cb.ID, ""))
	if !b.st.Auth(id) {
		return
	}
	switch cb.Data {
	case "m_ai":
		b.send(id, "🤖 Listo. Escribe tu consulta:")
	case "m_perfil":
		b.perfil(id)
	case "m_logout":
		b.logout(id, cb.From.UserName)
	case "m_admin":
		if b.st.AdminSes(id) {
			b.menuAdmin(id)
		}
	case "a_create":
		if b.st.AdminSes(id) {
			b.wizStart(id)
		}
	case "a_list":
		if b.st.AdminSes(id) {
			b.send(id, b.txtKeys())
		}
	case "a_stats":
		if b.st.AdminSes(id) {
			b.send(id, b.txtStats())
		}
	case "a_ses":
		if b.st.AdminSes(id) {
			b.send(id, b.txtSessions())
		}
	case "mode_normal", "mode_expert", "mode_fast":
		mode := strings.TrimPrefix(cb.Data, "mode_")
		b.st.SetMode(id, mode)
		b.send(id, fmt.Sprintf("✅ Modo cambiado a: %s", strings.ToUpper(mode)))
	case "r_user", "r_vip", "r_admin":
		if w, ok := b.wiz[id]; ok && w.Step == "rank" {
			w.Data["rank"] = strings.TrimPrefix(cb.Data, "r_")
			w.Step = "owner"
			b.send(id, "👤 Escribe el nombre del dueño:")
		}
	case "e_never", "e_7", "e_30", "e_90":
		if w, ok := b.wiz[id]; ok && w.Step == "exp" {
			w.Data["days"] = strings.TrimPrefix(cb.Data, "e_")
			if w.Data["days"] == "never" {
				w.Data["days"] = "0"
			}
			b.wizFinish(id, cb.From.UserName, w)
		}
	}
}

func (b *Bot) adminCmd(id int64, user, text string) {
	if !b.st.AdminSes(id) {
		b.ai(id, user, text)
		return
	}
	p := strings.Fields(text)
	switch p[0] {
	case "/deletekey":
		if len(p) < 2 {
			b.send(id, "Uso: /deletekey KEY")
			return
		}
		if err := b.st.DelKey(p[1]); err != nil {
			b.send(id, "Error: "+err.Error())
		} else {
			b.send(id, "✅ Key eliminada.")
		}
	case "/keyinfo":
		if len(p) < 2 {
			b.send(id, "Uso: /keyinfo KEY")
			return
		}
		b.send(id, b.txtKeyInfo(p[1]))
	case "/createkey":
		// /createkey vip 30 500 ideaOwner
		if len(p) < 3 {
			b.send(id, "Uso: /createkey user|vip|admin <dias> <max_uses> <owner>")
			return
		}
		rankMap := map[string]Rank{"user": USER, "vip": VIP, "admin": ADMIN}
		rank, ok := rankMap[strings.ToLower(p[1])]
		if !ok {
			b.send(id, "Rango inválido. user/vip/admin")
			return
		}
		days := 0
		if n, err := fmt.Sscan(p[2], &days); err != nil || n != 1 {
			b.send(id, "Dias inválidos")
			return
		}
		maxUses := 0
		if len(p) >= 4 {
			if n, err := fmt.Sscan(p[3], &maxUses); err != nil || n != 1 {
				b.send(id, "Max usos inválido")
				return
			}
		}
		owner := "admin"
		if len(p) >= 5 {
			owner = p[4]
		}
		k := b.st.NewKey(owner, user, rank, days, maxUses)
		b.send(id, fmt.Sprintf("✅ Key creada:\n🔑 %s\n👤 %s\nRango: %s\nExpira: %s\nMax usos: %d", k.K, k.Owner, strings.ToUpper(string(k.Rank)), func() string { if k.Exp == nil { return "∞" }; return k.Exp.Format("02/01/2006") }(), k.MaxUses))
	case "/editkey":
		if len(p) < 4 {
			b.send(id, "Uso: /editkey KEY field value (active|rank|exp|maxuses)")
			return
		}
		key := p[1]
		field := strings.ToLower(p[2])
		value := p[3]
		if err := b.editKey(key, field, value); err != nil {
			b.send(id, "Error: "+err.Error())
		} else {
			b.send(id, "✅ Key actualizada")
		}
	case "/setrank":
		if len(p) < 3 {
			b.send(id, "Uso: /setrank KEY user|vip|admin")
			return
		}
		rm := map[string]Rank{"user": USER, "vip": VIP, "admin": ADMIN}
		r, ok := rm[p[2]]
		if !ok {
			b.send(id, "Rango invalido. Usa: user, vip, admin")
			return
		}
		if err := b.st.SetRank(p[1], r); err != nil {
			b.send(id, "Error: "+err.Error())
		} else {
			b.send(id, "✅ Rango actualizado.")
		}
	case "/togglekey":
		if len(p) < 2 {
			b.send(id, "Uso: /togglekey KEY")
			return
		}
		if err := b.st.Toggle(p[1]); err != nil {
			b.send(id, "Error: "+err.Error())
		} else {
			b.send(id, "✅ Key toggled.")
		}
	case "/changeadminkey":
		if len(p) < 2 {
			b.send(id, "Uso: /changeadminkey NUEVA")
			return
		}
		b.st.SetAdminKey(p[1])
		b.send(id, "✅ Admin key cambiada.")
	case "/listkeys":
		b.send(id, b.txtKeys())
	case "/sessions":
		b.send(id, b.txtSessions())
	case "/integrity":
		if verifyIntegrity() {
			b.send(id, "✅ Integrity check passed.")
		} else {
			b.send(id, "❌ Integrity check failed! File may be tampered.")
		}
	default:
		b.ai(id, user, text)
	}
}

func (b *Bot) editKey(key, field, value string) error {
	if !isValidKeyFormat(key) {
		return fmt.Errorf("key inválida")
	}
	k, ok := b.st.GetKey(key)
	if !ok {
		return fmt.Errorf("key no encontrada")
	}
	switch field {
	case "active":
		if value == "1" || strings.EqualFold(value, "true") || strings.EqualFold(value, "on") {
			k.Active = true
		} else if value == "0" || strings.EqualFold(value, "false") || strings.EqualFold(value, "off") {
			k.Active = false
		} else {
			return fmt.Errorf("valor inválido para active")
		}
	case "rank":
		rm := map[string]Rank{"user": USER, "vip": VIP, "admin": ADMIN}
		r, ok := rm[strings.ToLower(value)]
		if !ok {
			return fmt.Errorf("rango inválido")
		}
		k.Rank = r
	case "exp":
		if value == "0" || strings.EqualFold(value, "none") {
			k.Exp = nil
		} else {
			days := 0
			if _, err := fmt.Sscan(value, &days); err == nil {
				newExp := time.Now().Add(time.Duration(days) * 24 * time.Hour)
				k.Exp = &newExp
			} else {
				parsed, err := time.Parse("2006-01-02", value)
				if err != nil {
					return fmt.Errorf("formato exp inválido (dias o AAAA-MM-DD)")
				}
				k.Exp = &parsed
			}
		}
	case "maxuses":
		maxUses := 0
		if _, err := fmt.Sscan(value, &maxUses); err != nil {
			return fmt.Errorf("maxuses inválido")
		}
		k.MaxUses = maxUses
	default:
		return fmt.Errorf("campo no soportado")
	}
	b.st.flush()
	return nil
}

func (b *Bot) wizStart(id int64) {
	b.wiz[id] = &Wizard{Step: "rank", Data: make(map[string]string), Exp: time.Now().Add(5 * time.Minute)}
	kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("👤 User", "r_user"),
		tgbotapi.NewInlineKeyboardButtonData("⭐ VIP", "r_vip"),
		tgbotapi.NewInlineKeyboardButtonData("👑 Admin", "r_admin"),
	))
	msg := tgbotapi.NewMessage(id, "🔑 Crear Key — Elige el rango:")
	msg.ReplyMarkup = kb
	b.api.Send(msg)
}

func (b *Bot) wizStep(id int64, user, text string, w *Wizard) {
	if w.Step == "owner" {
		w.Data["owner"] = text
		w.Step = "exp"
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("♾️ Sin limite", "e_never"),
				tgbotapi.NewInlineKeyboardButtonData("7 dias", "e_7"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("30 dias", "e_30"),
				tgbotapi.NewInlineKeyboardButtonData("90 dias", "e_90"),
			),
		)
		msg := tgbotapi.NewMessage(id, "⏱️ Expiracion de la key:")
		msg.ReplyMarkup = kb
		b.api.Send(msg)
	}
}

func (b *Bot) wizFinish(id int64, user string, w *Wizard) {
	delete(b.wiz, id)
	rm := map[string]Rank{"user": USER, "vip": VIP, "admin": ADMIN}
	rank := rm[w.Data["rank"]]
	days := 0
	fmt.Sscanf(w.Data["days"], "%d", &days)
	e := b.st.NewKey(w.Data["owner"], user, rank, days, 0)
	exp := "Sin expiracion"
	if e.Exp != nil {
		exp = "Expira: " + e.Exp.Format("02/01/2006")
	}
	maxUsesText := "∞"
	if e.MaxUses > 0 {
		maxUsesText = fmt.Sprintf("%d", e.MaxUses)
	}
	b.send(id, fmt.Sprintf("✅ Key creada!\n\n🔑 %s\n👤 Dueño: %s\n%s Rango: %s\n⏳ %s\n🧩 Max usos: %s", e.K, e.Owner, rIcon(rank), strings.ToUpper(string(rank)), exp, maxUsesText))
}

// ─── MENUS ───────────────────────────────
func (b *Bot) menuPrincipal(id int64) {
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🤖 Consultar IA", "m_ai"),
			tgbotapi.NewInlineKeyboardButtonData("👤 Mi Perfil", "m_perfil"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚪 Cerrar Sesion", "m_logout"),
		),
	)
	msg := tgbotapi.NewMessage(id, "🌑 DarkMax IA — Menu Principal")
	msg.ReplyMarkup = kb
	b.api.Send(msg)
}

func (b *Bot) menuAdmin(id int64) {
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔑 Crear Key", "a_create"),
			tgbotapi.NewInlineKeyboardButtonData("📋 Listar Keys", "a_list"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📊 Stats", "a_stats"),
			tgbotapi.NewInlineKeyboardButtonData("👥 Sesiones", "a_ses"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🌑 Menu", "m_ai"),
			tgbotapi.NewInlineKeyboardButtonData("🚪 Logout", "m_logout"),
		),
	)
	text := "👑 Panel Admin\n\n/createkey user|vip|admin <dias> <max_uses> <owner>\n/editkey KEY field value (active|rank|exp|maxuses)\n/deletekey KEY\n/keyinfo KEY\n/setrank KEY user|vip|admin\n/togglekey KEY\n/listkeys\n/sessions\n/integrity\n/changeadminkey NUEVA"
	msg := tgbotapi.NewMessage(id, text)
	msg.ReplyMarkup = kb
	b.api.Send(msg)
}

func (b *Bot) perfil(id int64) {
	ses, ok := b.st.Ses(id)
	if !ok {
		b.send(id, "Sin sesion.")
		return
	}
	exp := "♾️ Sin expiracion"
	warn := ""
	if e, ok := b.st.GetKey(ses.Key); ok && e.Exp != nil {
		rem := time.Until(*e.Exp)
		if rem < 0 {
			exp = "❌ EXPIRADA"
		} else {
			exp = "⏳ " + dur(rem)
			if rem < 72*time.Hour {
				warn = "\n⚠️ Tu acceso expira pronto!"
			}
		}
	}
	mask := ses.Key
	if len(mask) > 8 {
		mask = mask[:4] + strings.Repeat("*", len(mask)-8) + mask[len(mask)-4:]
	}
	text := fmt.Sprintf("👤 Mi Perfil\n\n@%s %s %s\n\n🔑 Key: %s\n📅 Expiracion: %s\n💬 Mensajes: %d\n⏱️ Sesion: %s\n📅 Desde: %s%s",
		ses.User, rIcon(ses.Rank), strings.ToUpper(string(ses.Rank)),
		mask, exp, ses.Msgs, dur(time.Since(ses.Start)),
		ses.Start.Format("02/01/2006 15:04"), warn)
	kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🤖 Consultar IA", "m_ai"),
		tgbotapi.NewInlineKeyboardButtonData("🚪 Cerrar Sesion", "m_logout"),
	))
	msg := tgbotapi.NewMessage(id, text)
	msg.ReplyMarkup = kb
	b.api.Send(msg)
}

func (b *Bot) setMode(id int64, user string) {
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🤖 Normal", "mode_normal"),
			tgbotapi.NewInlineKeyboardButtonData("🧠 Expert", "mode_expert"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚡ Fast", "mode_fast"),
		),
	)
	msg := tgbotapi.NewMessage(id, "🎭 Selecciona el modo de IA:")
	msg.ReplyMarkup = kb
	b.api.Send(msg)
}

// ─── TEXTOS ──────────────────────────────
func (b *Bot) txtStats() string {
	keys := b.st.AllKeys()
	ses := b.st.AllSessions()
	act := 0
	for _, k := range keys {
		if k.Active {
			act++
		}
	}
	msgs := 0
	for _, s := range ses {
		msgs += s.Msgs
	}
	return fmt.Sprintf("📊 Stats\n\n🔑 Keys: %d (activas: %d)\n👥 Sesiones: %d\n💬 Mensajes totales: %d", len(keys), act, len(ses), msgs)
}

func (b *Bot) txtKeys() string {
	keys := b.st.AllKeys()
	if len(keys) == 0 {
		return "No hay keys."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 Keys (%d)\n\n", len(keys)))
	for i, k := range keys {
		st := "✅"
		if !k.Active {
			st = "🚫"
		}
		sb.WriteString(fmt.Sprintf("%d. %s %s %s — %s\n", i+1, st, rIcon(k.Rank), k.K, k.Owner))
	}
	return sb.String()
}

func (b *Bot) txtSessions() string {
	ses := b.st.AllSessions()
	if len(ses) == 0 {
		return "No hay sesiones activas."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("👥 Sesiones (%d)\n\n", len(ses)))
	for i, s := range ses {
		sb.WriteString(fmt.Sprintf("%d. %s @%s — %s — %d msgs\n", i+1, rIcon(s.Rank), s.User, dur(time.Since(s.Start)), s.Msgs))
	}
	return sb.String()
}

func (b *Bot) txtKeyInfo(k string) string {
	e, ok := b.st.GetKey(k)
	if !ok {
		return "Key no encontrada."
	}
	st := "✅ Activa"
	if !e.Active {
		st = "🚫 Desactivada"
	}
	exp := "Sin expiracion"
	if e.Exp != nil {
		rem := time.Until(*e.Exp)
		if rem < 0 {
			exp = "EXPIRADA"
		} else {
			exp = dur(rem) + " restantes"
		}
	}
	left := "∞"
	if e.MaxUses > 0 {
		left = fmt.Sprintf("%d", e.MaxUses-e.Uses)
	}
	return fmt.Sprintf("🔑 Key Info\n\nKey: %s\nEstado: %s\nRango: %s %s\nDueño: %s\nCrea: %s\nCreada: %s\nExp: %s\nUsos: %d/%d (restan %s)",
		e.K, st, rIcon(e.Rank), strings.ToUpper(string(e.Rank)), e.Owner, e.By, e.At.Format("02/01/2006"), exp, e.Uses, e.MaxUses, left)
}

// ─── HELPERS ─────────────────────────────
func rIcon(r Rank) string {
	switch r {
	case ADMIN:
		return "👑"
	case VIP:
		return "⭐"
	default:
		return "👤"
	}
}

func dur(d time.Duration) string {
	d = d.Round(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// ─── MAIN ────────────────────────────────
func main() {
	// Verificación: Aseguramos que la lista no esté vacía
	if len(OPENROUTER_KEYS) == 0 {
		log.Fatal("ERROR: Debes añadir al menos una llave en la lista OPENROUTER_KEYS")
	}

	bot, err := newBot()
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	bot.run()
}
