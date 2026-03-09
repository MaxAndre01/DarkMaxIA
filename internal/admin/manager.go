package admin

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"darkmax/internal/auth"
	"darkmax/internal/logger"
)

// Manager gestiona operaciones administrativas
type Manager struct {
	store  *auth.Store
	logger *logger.Logger
}

// NewManager crea un nuevo manager
func NewManager(store *auth.Store, log *logger.Logger) *Manager {
	return &Manager{store: store, logger: log}
}

// GenerateKey genera una key aleatoria
func (m *Manager) GenerateKey(owner, createdBy string, rank auth.KeyRank, days int) (*auth.KeyEntry, error) {
	rawBytes := make([]byte, 8)
	if _, err := rand.Read(rawBytes); err != nil {
		return nil, err
	}

	prefix := rankPrefix(rank)
	keyStr := fmt.Sprintf("%s-%s", prefix, strings.ToUpper(hex.EncodeToString(rawBytes)))

	entry := &auth.KeyEntry{
		Key:       keyStr,
		Rank:      rank,
		Owner:     owner,
		CreatedBy: createdBy,
		CreatedAt: time.Now(),
		Active:    true,
	}

	if days > 0 {
		exp := time.Now().Add(time.Duration(days) * 24 * time.Hour)
		entry.ExpiresAt = &exp
	}

	if err := m.store.AddKey(entry); err != nil {
		return nil, err
	}

	m.logger.Admin(createdBy, fmt.Sprintf("CREATE_KEY key=%s owner=%s rank=%s", keyStr, owner, rank))
	return entry, nil
}

// DeleteKey elimina una key
func (m *Manager) DeleteKey(key, adminUser string) error {
	err := m.store.DeleteKey(key)
	if err == nil {
		m.logger.Admin(adminUser, fmt.Sprintf("DELETE_KEY key=%s", key))
	}
	return err
}

// GetKeyInfo retorna información detallada de una key
func (m *Manager) GetKeyInfo(key string) (string, bool) {
	entry, ok := m.store.GetKey(key)
	if !ok {
		return "", false
	}

	expStr := "♾️ Sin expiración"
	if entry.ExpiresAt != nil {
		remaining := time.Until(*entry.ExpiresAt)
		if remaining < 0 {
			expStr = "❌ EXPIRADA"
		} else {
			expStr = fmt.Sprintf("⏳ Expira en %s", formatDuration(remaining))
		}
	}

	usedStr := "🔓 Sin usar"
	if entry.UsedBy != 0 {
		usedStr = fmt.Sprintf("👤 ChatID: %d", entry.UsedBy)
	}

	statusIcon := "✅"
	if !entry.Active {
		statusIcon = "🚫"
	}

	info := fmt.Sprintf(`🔑 *Información de Key*

━━━━━━━━━━━━━━━━━━━━
🪪 *Key:* %s
%s *Estado:* %s
%s *Rango:* %s
👤 *Dueño:* %s
🛠️ *Creada por:* %s
📅 *Fecha creación:* %s
%s
🔌 *Usada por:* %s
📊 *Total usos:* %d
━━━━━━━━━━━━━━━━━━━━`,
		entry.Key,
		statusIcon,
		statusActive(entry.Active),
		rankIcon(entry.Rank),
		strings.ToUpper(string(entry.Rank)),
		entry.Owner,
		entry.CreatedBy,
		entry.CreatedAt.Format("02/01/2006 15:04"),
		expStr,
		usedStr,
		entry.Uses,
	)

	return info, true
}

// ListAllKeys retorna listado formateado de keys
func (m *Manager) ListAllKeys() string {
	keys := m.store.ListKeys()
	if len(keys) == 0 {
		return "📭 No hay keys registradas."
	}

	var sb strings.Builder
	sb.WriteString("📋 *Lista de Keys*\n\n")

	for i, k := range keys {
		statusIcon := "✅"
		if !k.Active {
			statusIcon = "🚫"
		}
		sb.WriteString(fmt.Sprintf("%d\\. %s `%s` — %s %s — 👤 %s\n",
			i+1,
			statusIcon,
			k.Key,
			rankIcon(k.Rank),
			strings.ToUpper(string(k.Rank)),
			k.Owner,
		))
	}

	sb.WriteString(fmt.Sprintf("\n📊 *Total:* %d keys", len(keys)))
	return sb.String()
}

// SetRank cambia el rango de una key
func (m *Manager) SetRank(key string, rank auth.KeyRank, adminUser string) error {
	err := m.store.SetKeyRank(key, rank)
	if err == nil {
		m.logger.Admin(adminUser, fmt.Sprintf("SET_RANK key=%s rank=%s", key, rank))
	}
	return err
}

// ToggleKey activa/desactiva key
func (m *Manager) ToggleKey(key, adminUser string) error {
	err := m.store.ToggleKey(key)
	if err == nil {
		m.logger.Admin(adminUser, fmt.Sprintf("TOGGLE_KEY key=%s", key))
	}
	return err
}

// Stats retorna estadísticas del sistema
func (m *Manager) Stats() string {
	keys := m.store.ListKeys()
	sessions := m.store.GetActiveSessions()

	var active, inactive, admins, vips int
	for _, k := range keys {
		if k.Active {
			active++
		} else {
			inactive++
		}
		if k.Rank == auth.RankAdmin || k.Rank == auth.RankOwner {
			admins++
		}
		if k.Rank == auth.RankVIP {
			vips++
		}
	}

	totalMsgs := 0
	for _, s := range sessions {
		totalMsgs += s.Messages
	}

	return fmt.Sprintf(`📊 *Estadísticas DarkMax IA*

━━━━━━━━━━━━━━━━━━━━
🔑 *Keys totales:* %d
  ✅ Activas: %d
  🚫 Inactivas: %d
  👑 Admins: %d
  ⭐ VIPs: %d

👥 *Sesiones activas:* %d
💬 *Mensajes (sesión):* %d
━━━━━━━━━━━━━━━━━━━━`,
		len(keys), active, inactive, admins, vips,
		len(sessions), totalMsgs,
	)
}

// ActiveSessions retorna listado de sesiones activas
func (m *Manager) ActiveSessions() string {
	sessions := m.store.GetActiveSessions()
	if len(sessions) == 0 {
		return "📭 No hay sesiones activas."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("👥 *Sesiones Activas* (%d)\n\n", len(sessions)))

	for i, s := range sessions {
		sb.WriteString(fmt.Sprintf("%d\\. %s @%s — %s — 💬 %d msgs — ⏱️ %s\n",
			i+1,
			rankIcon(s.Rank),
			s.Username,
			strings.ToUpper(string(s.Rank)),
			s.Messages,
			formatDuration(time.Since(s.StartedAt)),
		))
	}

	return sb.String()
}

// ChangeAdminKey cambia la admin key
func (m *Manager) ChangeAdminKey(newKey, adminUser string) error {
	err := m.store.ChangeAdminKey(newKey)
	if err == nil {
		m.logger.Admin(adminUser, "CHANGE_ADMIN_KEY")
	}
	return err
}

// --- Helpers ---

func rankPrefix(rank auth.KeyRank) string {
	switch rank {
	case auth.RankOwner:
		return "OWNER"
	case auth.RankAdmin:
		return "ADMIN"
	case auth.RankVIP:
		return "VIP"
	default:
		return "DM"
	}
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

func statusActive(active bool) string {
	if active {
		return "Activa"
	}
	return "Desactivada"
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