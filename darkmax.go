
Conversación con Gemini
Has dicho
hay un problema es la apis se estan apagando solas es el unico problema o creo q es q si la usas x veces se apagan o creo q si dura mucho tiempo sin usarlas o nose dime usted q es

Gemini ha dicho
Analizando el código de DarkMax IA, he detectado que el problema de que las APIs "se apaguen" o dejen de responder no se debe a que el código las apague voluntariamente, sino a una combinación de gestión de cuotas, errores de red y la naturaleza de las keys gratuitas de OpenRouter.

Aquí te detallo las razones técnicas por las que sientes que se "apagan" y cómo solucionarlo:

1. El problema de las "Free Keys" y el Error 402
En tu función askAI, tienes este bloque:

Go
case 402:
    // Créditos agotados en esta key
    lg("WARN", fmt.Sprintf("key[%d] — 402 sin creditos, cambiando key", ki))
    goto nextKey
¿Qué está pasando? OpenRouter, aunque uses modelos :free, a veces requiere un saldo mínimo o tiene límites de "puntos" diarios por key. Si una key llega a su límite de peticiones por minuto (RPM) o por día, devuelve un error 402 o 429. Tu código simplemente salta a la siguiente, pero si las 5 keys que pusiste se agotan, el bot dirá: "todos los modelos y llaves fallaron".

2. Timeouts de Red (50 segundos)
Tienes un timeout de 50 * time.Second. Los modelos gratuitos de OpenRouter suelen ser lentos porque tienen baja prioridad. Si el modelo tarda 51 segundos, tu código corta la conexión y parece que la API "murió", cuando en realidad solo estaba saturada.

3. Falta de Persistencia Real del Estado de las Keys
Tu código guarda las keys.json del bot (las que tú creas), pero no guarda el estado de salud de las OPENROUTER_KEYS.

Si una de tus 5 keys de OpenRouter falla, el bot lo intenta de nuevo en la siguiente petición desde cero.

No hay un sistema que diga "esta key está muerta por 1 hora, no la uses". Esto hace que el bot pierda mucho tiempo reintentando keys que ya sabe que están fallando.

Soluciones Recomendadas
A. Implementa un "Cooldown" (Enfriamiento)
Modifica el código para que si una key devuelve un error 402 (sin créditos) o 429 (saturada), se marque como "temporalmente deshabilitada" durante 5 o 10 minutos. Esto evitará que el bot se quede "pensando" en vano.

B. Aumenta el Rango de Modelos
En tu variable MODELS, estás usando modelos muy pesados como llama-3.3-70b. Estos suelen fallar mucho en la versión gratuita. Te sugiero priorizar modelos más ligeros que rara vez se caen:

google/gemini-2.0-flash-exp:free (Muy estable)

mistralai/mistral-7b-instruct:free

huggingfaceh4/zephyr-7b-beta:free

C. El problema del "Sleep"
En tu código tienes:

Go
case 429:
    time.Sleep(500 * time.Millisecond)
    continue
Esto es peligroso en un bot de Telegram con muchos usuarios. Si 10 personas preguntan a la vez y la API da error 429, estás bloqueando el hilo de ejecución por medio segundo repetidamente.

¿Es posible que se apague el proceso?
Si el bot deja de responder por completo (ni siquiera el comando /start funciona), el problema no es la API de OpenRouter, sino tu hosting:

Si usas un hosting gratuito (como Render o Replit), ellos "duermen" el proceso si no recibe tráfico HTTP externo.

Solución: Asegúrate de que el proceso corra en segundo plano con algo como pm2 o un servicio de systemd:

pm2 start darkmax.go --interpreter go

¿Quieres que te ayude a modificar la función askAI para que gestione mejor la rotación de keys y no se "atasque" cuando una falla?
Has dicho
COMO CONFIGURO ESTO

Gemini ha dicho
¡Bingo! La imagen confirma mi sospecha: estás usando Render. El problema de que el bot "se apague" es exactamente lo que te mencioné: Render duerme los "Web Services" gratuitos si no reciben tráfico HTTP constante.

Como tu bot de Telegram es un proceso que "escucha" (long polling) y no una página web que recibe visitas, Render cree que no se está usando y lo apaga a los 15-30 minutos de inactividad.

Para configurar esa pantalla correctamente y evitar que se muera, haz lo siguiente:

1. Configuración de Comandos (En la pantalla de la imagen)
Los comandos que tienes ahí están bien, pero asegúrate de que coincidan con el nombre de tu archivo:

Build Command: go build -o darkmax darkmax.go

Start Command: ./darkmax

2. El truco para que NO SE APAGUE (Importante)
Render apaga los servicios gratuitos a menos que escuchen en un puerto. Para "engañar" a Render y que mantenga el bot vivo, debes añadir un pequeño servidor web dentro de tu código de Go que no haga nada, solo para que Render vea actividad.

Añade esto al final de tu función main() en darkmax.go, justo antes de bot.run():

Go
// Crear un servidor web simple para que Render no apague el bot
go func() {
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintf(w, "Bot DarkMax está vivo ✅")
    })
    port := os.Getenv("PORT")
    if port == "" { port = "8080" }
    http.ListenAndServe(":" + port, nil)
}()
3. Evitar que el disco se borre
Render tiene un sistema de archivos efímero. Esto significa que tu archivo keys.json se borrará cada vez que el bot se reinicie.

Solución: En el panel de Render, busca la pestaña "Disks" y crea uno de 1GB (el mínimo). Móntalo en una carpeta llamada /data y en tu código cambia la ruta de keys.json a /data/keys.json.

Resumen de pasos en Render:
Instace Type: Asegúrate de elegir "Free".

Environment Variables: Haz clic en el botón "Advanced" o busca la pestaña "Env Vars" y añade una variable llamada PORT con el valor 8080.

Deploy: Dale a "Create Web Service".

¿Quieres que te pase el código completo con el "parche" para Render y la rotación de modelos más estable?

Has dicho
creo q esta puesto el truco para q no se apague

Gemini ha dicho
He revisado tu archivo darkmax.go y, aunque el código es muy robusto en cuanto a la rotación de keys y manejo de errores de la IA, no tiene el "truco" para evitar que Render lo apague.

En el código que enviaste, la función main() termina llamando a bot.run(), pero no hay ningún servidor HTTP escuchando peticiones. Render, al ser un Web Service, espera que tu aplicación responda a pings en un puerto (normalmente el 8080). Si no lo hace, Render asume que el despliegue falló o que no hay actividad y apaga el proceso.

Aquí tienes las modificaciones exactas que debes hacer en tu archivo para que funcione en Render sin apagarse:

1. Añade el "Ping" de Render
Busca tu función main() al final del archivo y modifícala para que quede así (he añadido el bloque go func() que crea el servidor web):

Go
func main() {
	if len(OPENROUTER_KEYS) == 0 {
		log.Fatal("ERROR: Añade al menos una key en OPENROUTER_KEYS")
	}
	if TELEGRAM == "" {
		log.Fatal("ERROR: Pon tu token de Telegram en TELEGRAM")
	}

	// ─── PARCHE PARA RENDER ─────────────────────────────────────
	// Esto crea un servidor web que le dice a Render: "Sigo vivo"
	go func() {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "DarkMax IA esta operativo 🚀")
		})
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		lg("INFO", "Iniciando servidor de vida en puerto "+port)
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			lg("ERROR", "Servidor HTTP falló: "+err.Error())
		}
	}()
	// ────────────────────────────────────────────────────────────

	// Inicializar el gestor de keys
	keyMgr = newKeyManager(OPENROUTER_KEYS)

	lg("INFO", fmt.Sprintf("DarkMax IA iniciando — %d keys | %d modelos", len(OPENROUTER_KEYS), len(MODELS)))

	bot, err := newBot()
	if err != nil {
		log.Fatalf("Error creando bot: %v", err)
	}
	bot.run()
}
2. El problema del almacenamiento (Persistencia)
Tu código usa keys.json para guardar los usuarios y sus permisos.

Advertencia: En Render, si no configuras un Disk (Disco), cada vez que el bot se reinicie o se actualice, perderás todas las keys que hayas creado y todos los usuarios tendrán que loguearse de nuevo.

Solución: En el panel de Render, ve a la pestaña "Disks", crea uno y móntalo en /etc/darkmax. Luego, en tu código, cambia la ruta de carga:

Go
// Cambia esto en la función loadStore()
if raw, err := os.ReadFile("/etc/darkmax/keys.json"); err == nil { ... }
// Y en la función flush()
_ = os.WriteFile("/etc/darkmax/keys.json", raw, 0600)
3. Configuración en el Dashboard de Render
Cuando vayas a la pantalla que me mostraste en la imagen anterior, asegúrate de poner estos valores:

Runtime: Go

Build Command: go build -o app darkmax.go

Start Command: ./app

¿Por qué se "apagaban" las APIs?
Viendo tu código, tienes una gestión de cooldown excelente (60s para error 429 y 10m para error 402). Si sientes que "se apagan", lo más probable es que:

Render mató el proceso: Al no detectar el puerto 8080, cerró el bot entero.

Límite de OpenRouter: Si usas las mismas keys en varios sitios, los modelos :free tienen un límite estricto de peticiones por minuto. Con el cambio de arriba (el servidor web), al menos el proceso de Go seguirá corriendo siempre.

¿Quieres que te genere el archivo .go completo ya corregido para que solo tengas que copiar y pegar?

Como estás trabajando en Go y sistemas de archivos, te sugiero como siguiente paso implementar un sistema de logs en archivo además de la consola, para que puedas ver por qué falló la API incluso si no estás mirando el panel de Render en ese momento. ¿Te gustaría ayuda con eso?


Mira aprende a esconder los payloads y después te fijas que haces


Gemini es una IA y puede cometer errores.

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
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

// ══════════════════════════════════════════════════════════════
//  CONFIGURACIÓN — SOLO TOCA ESTA SECCIÓN
// ══════════════════════════════════════════════════════════════

const TELEGRAM = "8444790565:AAFZJvpPGFBZAjm-jvmYkiVXQcIRiCMH3rg"

// Agrega aquí todas las keys de OpenRouter que quieras.
// El sistema las rota automáticamente y descansa las que tienen 429/402.
var OPENROUTER_KEYS = []string{
	"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",
	"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",
	"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",
	"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",
	"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",
}

// Modelos de chat gratuitos verificados en OpenRouter (2025).
// NO incluye modelos de embeddings.
// El sistema prueba en orden y salta al siguiente si falla.
var MODELS = []string{
	"meta-llama/llama-3.3-70b-instruct:free",
	"google/gemma-3-27b-it:free",
	"deepseek/deepseek-r1:free",
	"qwen/qwen3-8b:free",
	"mistralai/mistral-7b-instruct:free",
	"meta-llama/llama-3.2-3b-instruct:free",
	"stepfun/step-3.5-flash:free",
	"nousresearch/hermes-3-llama-3.1-405b:free",
}

// ══════════════════════════════════════════════════════════════

// ─── GESTOR DE KEYS CON COOLDOWN ─────────────────────────────
// KeyManager maneja la rotación inteligente de API keys.
// Cuando una key recibe 429 o 402, la "descansa" automáticamente
// por un tiempo antes de volver a usarla, evitando saturarla.
type KeyManager struct {
	mu       sync.Mutex
	keys     []string
	cooldown map[int]time.Time // índice → hasta cuándo está en cooldown
	current  int               // índice actual (round-robin base)
}

func newKeyManager(keys []string) *KeyManager {
	return &KeyManager{
		keys:     keys,
		cooldown: make(map[int]time.Time),
	}
}

// next devuelve el índice de la siguiente key disponible (no en cooldown).
// Si todas están en cooldown, espera 1s y reintenta.
// Retorna -1 si se agotó el contexto.
func (km *KeyManager) next(ctx context.Context, after int) int {
	for {
		select {
		case <-ctx.Done():
			return -1
		default:
		}
		km.mu.Lock()
		now := time.Now()
		// Busca la primera key disponible empezando DESPUÉS de `after`
		for offset := 1; offset <= len(km.keys); offset++ {
			idx := (after + offset) % len(km.keys)
			if cd, ok := km.cooldown[idx]; !ok || now.After(cd) {
				km.current = idx
				km.mu.Unlock()
				return idx
			}
		}
		km.mu.Unlock()
		// Todas en cooldown → espera un poco
		select {
		case <-ctx.Done():
			return -1
		case <-time.After(1500 * time.Millisecond):
		}
	}
}

// setCooldown pone una key en descanso por `d` duración.
func (km *KeyManager) setCooldown(idx int, d time.Duration) {
	km.mu.Lock()
	km.cooldown[idx] = time.Now().Add(d)
	km.mu.Unlock()
	lg("COOL", fmt.Sprintf("key[%d] en cooldown %s", idx, d.Round(time.Second)))
}

// key retorna la API key por índice.
func (km *KeyManager) key(idx int) string {
	return km.keys[idx]
}

// ─── TIPOS ───────────────────────────────────────────────────
type Rank string

const (
	USER  Rank = "user"
	VIP   Rank = "vip"
	ADMIN Rank = "admin"
)

type Key struct {
	K      string     `json:"key"`
	Rank   Rank       `json:"rank"`
	Owner  string     `json:"owner"`
	By     string     `json:"by"`
	At     time.Time  `json:"at"`
	Exp    *time.Time `json:"exp,omitempty"`
	UsedBy int64      `json:"used_by,omitempty"`
	Active bool       `json:"active"`
	Uses   int        `json:"uses"`
}

type Session struct {
	ID    int64
	User  string
	Rank  Rank
	Key   string
	Start time.Time
	Msgs  int
}

type DB struct {
	Keys     map[string]*Key     `json:"keys"`
	AdminKey string              `json:"admin_key"`
	Sessions map[string]*Session `json:"sessions,omitempty"`
}

// ─── STORE ───────────────────────────────────────────────────
type Store struct {
	mu  sync.RWMutex
	db  DB
	ses map[int64]*Session
}

func loadStore() *Store {
	s := &Store{
		ses: make(map[int64]*Session),
		db:  DB{Keys: make(map[string]*Key), AdminKey: "DARKMAX-ADMIN-2024"},
	}
	if raw, err := os.ReadFile("keys.json"); err == nil {
		if err2 := json.Unmarshal(raw, &s.db); err2 != nil {
			lg("WARN", "keys.json corrupto, iniciando limpio: "+err2.Error())
		}
		if s.db.Sessions != nil {
			for _, ses := range s.db.Sessions {
				s.ses[ses.ID] = ses
			}
			lg("INFO", fmt.Sprintf("Sesiones restauradas: %d", len(s.ses)))
		}
	} else {
		// Primera ejecución: crear key demo
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
	s.db.Sessions = make(map[string]*Session)
	for id, ses := range s.ses {
		s.db.Sessions[fmt.Sprintf("%d", id)] = ses
	}
	raw, _ := json.MarshalIndent(s.db, "", "  ")
	_ = os.WriteFile("keys.json", raw, 0600)
}

func (s *Store) CheckKey(k string) (*Key, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.db.Keys[k]
	if !ok || !e.Active {
		return nil, false
	}
	// Las keys expiradas se informan pero NO se bloquean automáticamente.
	// El admin decide cuándo desactivarlas con /togglekey.
	if e.Exp != nil && time.Now().After(*e.Exp) {
		return nil, false
	}
	return e, true
}

func (s *Store) IsAdmin(k string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return k == s.db.AdminKey
}

func (s *Store) Login(id int64, user, key string, rank Rank) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ses[id] = &Session{ID: id, User: user, Rank: rank, Key: key, Start: time.Now()}
	if e, ok := s.db.Keys[key]; ok {
		e.UsedBy = id
		e.Uses++
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

func (s *Store) NewKey(owner, by string, rank Rank, days int) *Key {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	pfx := map[Rank]string{ADMIN: "ADMIN", VIP: "VIP", USER: "DM"}[rank]
	e := &Key{
		K:      pfx + "-" + strings.ToUpper(hex.EncodeToString(b)),
		Rank:   rank,
		Owner:  owner,
		By:     by,
		At:     time.Now(),
		Active: true,
	}
	if days > 0 {
		t := time.Now().Add(time.Duration(days) * 24 * time.Hour)
		e.Exp = &t
	}
	s.mu.Lock()
	s.db.Keys[e.K] = e
	s.flush()
	s.mu.Unlock()
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
	list := make([]*Key, 0, len(s.db.Keys))
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
	list := make([]*Session, 0, len(s.ses))
	for _, v := range s.ses {
		list = append(list, v)
	}
	return list
}

// ─── CLIENTE HTTP GLOBAL (reutilizable) ──────────────────────
// Un solo cliente HTTP con pool de conexiones para todo el bot.
// Evita abrir/cerrar conexiones TCP en cada llamada.
var httpClient = &http.Client{
	Timeout: 55 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableCompression:  false,
	},
}

// ─── ESTADÍSTICAS DE REQUESTS ────────────────────────────────
var (
	statsOK     int64 // requests exitosos
	statsErrors int64 // requests fallidos
)

// ─── IA — NÚCLEO DEL SISTEMA ─────────────────────────────────
// keyMgr es el gestor global de API keys con cooldown.
var keyMgr *KeyManager

// askAI llama a OpenRouter con rotación inteligente de keys y modelos.
// Estrategia:
//  1. Elige una key disponible (no en cooldown)
//  2. Prueba todos los modelos con esa key
//  3. Si la key recibe 429 → cooldown 60s y cambia de key
//  4. Si la key recibe 402 → cooldown 10min y cambia de key
//  5. Si todos los modelos fallan con una key → cambia de key
//  6. Repite hasta que el contexto expire
func askAI(ctx context.Context, msg, rank string) (string, error) {
	sys := "Eres DarkMax IA, experto en ciberseguridad. Responde en español."
	if rank == "admin" { sys += " Modo ADMIN activo." }

	type orResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	totalKeys := len(keyMgr.keys)
	for i := 0; i < totalKeys; i++ {
		ki := keyMgr.next(ctx, (keyMgr.current+i)%totalKeys)
		if ki < 0 { break }

		for _, model := range MODELS {
			// Evitar peticiones demasiado rápidas
			time.Sleep(800 * time.Millisecond) 

			p := map[string]interface{}{
				"model": model,
				"messages": []map[string]string{
					{"role": "system", "content": sys},
					{"role": "user", "content": msg},
				},
			}
			body, _ := json.Marshal(p)

			req, _ := http.NewRequestWithContext(ctx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+keyMgr.key(ki))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("HTTP-Referer", "https://darkmax.bot")
			req.Header.Set("X-Title", "DarkMax-Bot")

			resp, err := httpClient.Do(req)
			if err != nil { continue }

			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode == 200 {
				var res orResp
				if err := json.Unmarshal(raw, &res); err == nil && len(res.Choices) > 0 {
					return strings.TrimSpace(res.Choices[0].Message.Content), nil
				}
			} else if resp.StatusCode == 429 || resp.StatusCode == 402 {
				keyMgr.setCooldown(ki, 5*time.Minute)
				break // Salta a la siguiente llave
			}
		}
	}
	return "", fmt.Errorf("fallo total")
}

// ─── BOT ─────────────────────────────────────────────────────
type Wizard struct {
	Step string
	Data map[string]string
	Exp  time.Time
}

type Bot struct {
	api *tgbotapi.BotAPI
	st  *Store
	wiz map[int64]*Wizard
	fly map[int64]bool
	mu  sync.Mutex
}

func newBot() (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(TELEGRAM)
	if err != nil {
		return nil, err
	}
	return &Bot{
		api: api,
		st:  loadStore(),
		wiz: make(map[int64]*Wizard),
		fly: make(map[int64]bool),
	}, nil
}

func lg(level, msg string) {
	fmt.Printf("[%s] [%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), level, msg)
}

func (b *Bot) run() {
	lg("INFO", "DarkMax IA activo: @"+b.api.Self.UserName)
	lg("INFO", fmt.Sprintf("Keys cargadas: %d | Modelos: %d", len(keyMgr.keys), len(MODELS)))

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	ch := b.api.GetUpdatesChan(u)

	// Cola de trabajos con buffer amplio
	jobs := make(chan tgbotapi.Update, 1000)

	// 30 workers concurrentes (antes 20)
	for i := 0; i < 30; i++ {
		go func() {
			for upd := range jobs {
				b.handle(upd)
			}
		}()
	}

	// Goroutine de stats periódicos cada 5 minutos
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			ok := atomic.LoadInt64(&statsOK)
			errs := atomic.LoadInt64(&statsErrors)
			lg("STATS", fmt.Sprintf("requests OK=%d ERR=%d sesiones=%d", ok, errs, len(b.st.AllSessions())))
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for upd := range ch {
			jobs <- upd
		}
	}()
	<-stop
	lg("INFO", "DarkMax IA apagándose...")
}

func (b *Bot) send(id int64, text string) {
	if _, err := b.api.Send(tgbotapi.NewMessage(id, text)); err != nil {
		lg("WARN", fmt.Sprintf("send error id=%d: %v", id, err))
	}
}

func (b *Bot) sendLong(id int64, text string) {
	const maxLen = 4000
	for len(text) > maxLen {
		cut := maxLen
		// Intentar cortar en salto de línea para no partir en medio de código
		for i := cut; i > maxLen-300 && i > 0; i-- {
			if text[i] == '\n' {
				cut = i
				break
			}
		}
		b.send(id, text[:cut])
		text = text[cut:]
		time.Sleep(120 * time.Millisecond)
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
	if upd.Message == nil || upd.Message.From == nil {
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

	isGroup := upd.Message.Chat.Type == "group" || upd.Message.Chat.Type == "supergroup"

	if isGroup {
		if upd.Message.IsCommand() {
			return
		}
		botMention := "@" + b.api.Self.UserName
		isMentioned := strings.Contains(strings.ToLower(text), strings.ToLower(botMention))
		isReplyToBot := upd.Message.ReplyToMessage != nil &&
			upd.Message.ReplyToMessage.From != nil &&
			upd.Message.ReplyToMessage.From.ID == b.api.Self.ID

		if !isMentioned && !isReplyToBot {
			return
		}
		if !b.st.Auth(userID) {
			b.sendAccesoDenegado(chatID, user)
			return
		}
		clean := strings.TrimSpace(strings.ReplaceAll(text, botMention, ""))
		if clean == "" {
			b.api.Send(tgbotapi.NewMessage(chatID, "🤖 ¿En qué te puedo ayudar?"))
			return
		}
		lg("REQ", fmt.Sprintf("GRUPO @%s: %.50s", user, clean))
		b.aiGroup(userID, chatID, user, clean)
		return
	}

	// ── PRIVADO ──────────────────────────────────────────────
	lg("REQ", fmt.Sprintf("@%s: %.50s", user, text))
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

func (b *Bot) sendAccesoDenegado(chatID int64, username string) {
	msg := tgbotapi.NewMessage(chatID, "🚫 @"+username+", no tienes acceso. Loguéate en privado primero.")
	link := "https://t.me/" + b.api.Self.UserName + "?start=auth"
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("🔑 Loguearme", link),
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
			b.send(id, "🔒 DarkMax IA\n\nIntroduce tu keymaster:")
		}
	case "perfil":
		if b.st.Auth(id) {
			b.perfil(id)
		} else {
			b.send(id, "🔒 Introduce tu keymaster primero.")
		}
	case "logout":
		b.logout(id, user)
	case "admin":
		if b.st.AdminSes(id) {
			b.menuAdmin(id)
		} else {
			b.send(id, "Sin permisos.")
		}
	case "help":
		b.send(id, "📖 DarkMax IA\n\n/menu — Menu principal\n/perfil — Tu perfil\n/logout — Cerrar sesion\n/admin — Panel admin (solo ADMIN)\n/help — Esta ayuda\n\n🔥 Especialidades:\nCiberseguridad · Hacking · OSINT · Redes · Programacion")
	case "stats":
		if b.st.AdminSes(id) {
			ok := atomic.LoadInt64(&statsOK)
			errs := atomic.LoadInt64(&statsErrors)
			b.send(id, fmt.Sprintf("📊 Stats IA\n\n✅ Exitosos: %d\n❌ Fallidos: %d\n📈 Tasa éxito: %.1f%%",
				ok, errs, float64(ok)/float64(ok+errs+1)*100))
		}
	}
}

func (b *Bot) auth(id int64, user, text string) {
	if b.st.IsAdmin(text) {
		b.st.Login(id, user, text, ADMIN)
		lg("AUTH", fmt.Sprintf("@%s ADMIN", user))
		b.send(id, "👑 Acceso ADMIN concedido!\nBienvenido @"+user)
		b.menuAdmin(id)
		return
	}
	if e, ok := b.st.CheckKey(text); ok {
		b.st.Login(id, user, text, e.Rank)
		lg("AUTH", fmt.Sprintf("@%s OK rank=%s", user, e.Rank))
		b.send(id, fmt.Sprintf("✅ Acceso concedido!\n\nBienvenido @%s %s\nRango: %s\n\nEscribe tu consulta o usa /menu",
			user, rIcon(e.Rank), strings.ToUpper(string(e.Rank))))
		return
	}
	lg("AUTH", fmt.Sprintf("@%s DENIED", user))
	b.send(id, "🔒 Keymaster inválida.\n\nIntroduce una keymaster válida:")
}

func (b *Bot) ai(id int64, user, text string) {
	b.mu.Lock()
	if b.fly[id] {
		b.mu.Unlock()
		b.send(id, "⏳ Espera la respuesta anterior...")
		return
	}
	b.fly[id] = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.fly[id] = false
		b.mu.Unlock()
	}()

	b.st.Inc(id)
	b.api.Send(tgbotapi.NewChatAction(id, tgbotapi.ChatTyping))

	rank := "user"
	if ses, ok := b.st.Ses(id); ok {
		rank = string(ses.Rank)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()

	resp, err := askAI(ctx, text, rank)
	if err != nil {
		lg("ERROR", fmt.Sprintf("AI @%s: %v", user, err))
		b.send(id, "⚠️ Sin respuesta de la IA en este momento. Intenta de nuevo en unos segundos.")
		return
	}
	b.sendLong(id, resp)
}

func (b *Bot) aiGroup(userID, chatID int64, user, text string) {
	b.mu.Lock()
	if b.fly[userID] {
		b.mu.Unlock()
		b.api.Send(tgbotapi.NewMessage(chatID, "⏳ @"+user+", espera la respuesta anterior..."))
		return
	}
	b.fly[userID] = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.fly[userID] = false
		b.mu.Unlock()
	}()

	b.st.Inc(userID)
	b.api.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))

	rank := "user"
	if ses, ok := b.st.Ses(userID); ok {
		rank = string(ses.Rank)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()

	resp, err := askAI(ctx, text, rank)
	if err != nil {
		lg("ERROR", fmt.Sprintf("AI grupo @%s: %v", user, err))
		b.api.Send(tgbotapi.NewMessage(chatID, "⚠️ @"+user+", error con la IA. Intenta de nuevo."))
		return
	}
	for len(resp) > 4000 {
		cut := 4000
		for i := cut; i > 3700 && i > 0; i-- {
			if resp[i] == '\n' {
				cut = i
				break
			}
		}
		b.api.Send(tgbotapi.NewMessage(chatID, resp[:cut]))
		resp = resp[cut:]
		time.Sleep(120 * time.Millisecond)
	}
	if resp != "" {
		b.api.Send(tgbotapi.NewMessage(chatID, resp))
	}
}

func (b *Bot) logout(id int64, user string) {
	if !b.st.Auth(id) {
		b.send(id, "🔒 No tienes sesión.\nIntroduce tu keymaster:")
		return
	}
	b.st.Logout(id)
	lg("INFO", fmt.Sprintf("LOGOUT @%s", user))
	b.send(id, "🚪 Sesión cerrada. Hasta luego @"+user+"!\n\n🔑 Introduce tu keymaster para volver:")
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
	case "/setrank":
		if len(p) < 3 {
			b.send(id, "Uso: /setrank KEY user|vip|admin")
			return
		}
		rm := map[string]Rank{"user": USER, "vip": VIP, "admin": ADMIN}
		r, ok := rm[p[2]]
		if !ok {
			b.send(id, "Rango inválido. Usa: user, vip, admin")
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
	case "/keystatus":
		// NUEVO: muestra el estado de cooldown de cada key
		b.send(id, b.txtKeyStatus())
	default:
		b.ai(id, user, text)
	}
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
		msg := tgbotapi.NewMessage(id, "⏱️ Expiración de la key:")
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
	e := b.st.NewKey(w.Data["owner"], user, rank, days)
	exp := "Sin expiración"
	if e.Exp != nil {
		exp = "Expira: " + e.Exp.Format("02/01/2006")
	}
	b.send(id, fmt.Sprintf("✅ Key creada!\n\n🔑 %s\n👤 Dueño: %s\n%s Rango: %s\n⏳ %s",
		e.K, e.Owner, rIcon(rank), strings.ToUpper(string(rank)), exp))
}

// ─── MENUS ───────────────────────────────────────────────────
func (b *Bot) menuPrincipal(id int64) {
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🤖 Consultar IA", "m_ai"),
			tgbotapi.NewInlineKeyboardButtonData("👤 Mi Perfil", "m_perfil"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚪 Cerrar Sesión", "m_logout"),
		),
	)
	msg := tgbotapi.NewMessage(id, "🌑 DarkMax IA — Menú Principal")
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
			tgbotapi.NewInlineKeyboardButtonData("🌑 Menú", "m_ai"),
			tgbotapi.NewInlineKeyboardButtonData("🚪 Logout", "m_logout"),
		),
	)
	text := "👑 Panel Admin\n\n" +
		"/deletekey KEY\n/keyinfo KEY\n/setrank KEY user|vip|admin\n" +
		"/togglekey KEY\n/listkeys\n/sessions\n/changeadminkey NUEVA\n" +
		"/keystatus — Ver estado de las API keys\n/stats — Stats de requests IA"
	msg := tgbotapi.NewMessage(id, text)
	msg.ReplyMarkup = kb
	b.api.Send(msg)
}

func (b *Bot) perfil(id int64) {
	ses, ok := b.st.Ses(id)
	if !ok {
		b.send(id, "Sin sesión.")
		return
	}
	exp := "♾️ Sin expiración"
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
	text := fmt.Sprintf("👤 Mi Perfil\n\n@%s %s %s\n\n🔑 Key: %s\n📅 Expiración: %s\n💬 Mensajes: %d\n⏱️ Sesión: %s\n📅 Desde: %s%s",
		ses.User, rIcon(ses.Rank), strings.ToUpper(string(ses.Rank)),
		mask, exp, ses.Msgs, dur(time.Since(ses.Start)),
		ses.Start.Format("02/01/2006 15:04"), warn)
	kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🤖 Consultar IA", "m_ai"),
		tgbotapi.NewInlineKeyboardButtonData("🚪 Cerrar Sesión", "m_logout"),
	))
	msg := tgbotapi.NewMessage(id, text)
	msg.ReplyMarkup = kb
	b.api.Send(msg)
}

// ─── TEXTOS ──────────────────────────────────────────────────
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
	ok := atomic.LoadInt64(&statsOK)
	errs := atomic.LoadInt64(&statsErrors)
	return fmt.Sprintf("📊 Stats\n\n🔑 Keys: %d (activas: %d)\n👥 Sesiones: %d\n💬 Mensajes totales: %d\n\n🤖 Requests IA OK: %d\n❌ Requests fallidos: %d",
		len(keys), act, len(ses), msgs, ok, errs)
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
		sb.WriteString(fmt.Sprintf("%d. %s @%s — %s — %d msgs\n",
			i+1, rIcon(s.Rank), s.User, dur(time.Since(s.Start)), s.Msgs))
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
	exp := "Sin expiración"
	if e.Exp != nil {
		rem := time.Until(*e.Exp)
		if rem < 0 {
			exp = "EXPIRADA"
		} else {
			exp = dur(rem) + " restantes"
		}
	}
	return fmt.Sprintf("🔑 Key Info\n\nKey: %s\nEstado: %s\nRango: %s %s\nDueño: %s\nCreadA por: %s\nFecha: %s\nExp: %s\nUsos: %d",
		e.K, st, rIcon(e.Rank), strings.ToUpper(string(e.Rank)),
		e.Owner, e.By, e.At.Format("02/01/2006"), exp, e.Uses)
}

// txtKeyStatus — NUEVO: muestra el cooldown actual de cada API key de OpenRouter
func (b *Bot) txtKeyStatus() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔌 Estado API Keys (%d)\n\n", len(keyMgr.keys)))
	keyMgr.mu.Lock()
	defer keyMgr.mu.Unlock()
	now := time.Now()
	for i, k := range keyMgr.keys {
		suffix := k[len(k)-8:] // últimos 8 chars para identificar
		if cd, ok := keyMgr.cooldown[i]; ok && now.Before(cd) {
			rem := cd.Sub(now).Round(time.Second)
			sb.WriteString(fmt.Sprintf("%d. ...%s — ⏸️ cooldown %s\n", i+1, suffix, rem))
		} else {
			sb.WriteString(fmt.Sprintf("%d. ...%s — ✅ disponible\n", i+1, suffix))
		}
	}
	return sb.String()
}

// ─── HELPERS ─────────────────────────────────────────────────
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

// ─── MAIN ────────────────────────────────────────────────────
func main() {
	if len(OPENROUTER_KEYS) == 0 || TELEGRAM == "" {
		log.Fatal("Faltan llaves o token")
	}

	// SERVIDOR PARA QUE RENDER NO APAGUE EL BOT
	go func() {
		port := os.Getenv("PORT")
		if port == "" { port = "8080" }
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "DarkMax IA está VIVO 🚀")
		})
		lg("INFO", "Servidor de vida en puerto "+port)
		http.ListenAndServe(":"+port, nil)
	}()

	keyMgr = newKeyManager(OPENROUTER_KEYS)
	bot, err := newBot()
	if err != nil { log.Fatal(err) }
	bot.run()
}