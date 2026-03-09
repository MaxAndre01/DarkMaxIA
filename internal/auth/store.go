package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// KeyRank define el rango de una key
type KeyRank string

const (
	RankUser    KeyRank = "user"
	RankVIP     KeyRank = "vip"
	RankAdmin   KeyRank = "admin"
	RankOwner   KeyRank = "owner"
)

// KeyEntry representa una key con metadatos
type KeyEntry struct {
	Key       string    `json:"key"`
	Rank      KeyRank   `json:"rank"`
	Owner     string    `json:"owner"`       // Nombre del dueño
	CreatedBy string    `json:"created_by"`  // Admin que la creó
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	UsedBy    int64     `json:"used_by,omitempty"` // Telegram ChatID
	UsedAt    *time.Time `json:"used_at,omitempty"`
	Active    bool      `json:"active"`
	Uses      int       `json:"uses"` // Contador de sesiones
}

// SessionInfo info de sesión activa
type SessionInfo struct {
	ChatID    int64
	Username  string
	Rank      KeyRank
	KeyUsed   string
	StartedAt time.Time
	Messages  int
}

// Store gestiona keys y sesiones
type Store struct {
	mu       sync.RWMutex
	filePath string
	Keys     map[string]*KeyEntry   `json:"keys"`
	AdminKey string                 `json:"admin_key"`
	sessions map[int64]*SessionInfo // en memoria
}

// NewStore carga o crea el almacén
func NewStore(path string) (*Store, error) {
	s := &Store{
		filePath: path,
		Keys:     make(map[string]*KeyEntry),
		sessions: make(map[int64]*SessionInfo),
	}

	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, s); err != nil {
			return nil, fmt.Errorf("error parsing store: %w", err)
		}
	} else {
		// Archivo nuevo: crear key de admin por defecto
		s.AdminKey = "DARKMAX-ADMIN-2024"
		s.Keys["DARKMAX-DEMO"] = &KeyEntry{
			Key:       "DARKMAX-DEMO",
			Rank:      RankUser,
			Owner:     "Demo",
			CreatedBy: "system",
			CreatedAt: time.Now(),
			Active:    true,
		}
		s.save()
	}

	return s, nil
}

// ValidateKey verifica si una key existe y está activa
func (s *Store) ValidateKey(key string) (*KeyEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.Keys[key]
	if !ok || !entry.Active {
		return nil, false
	}

	// Verificar expiración
	if entry.ExpiresAt != nil && time.Now().After(*entry.ExpiresAt) {
		return nil, false
	}

	return entry, true
}

// IsAdminKey verifica si es la key de administrador
func (s *Store) IsAdminKey(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return key == s.AdminKey
}

// CreateSession registra una sesión activa
func (s *Store) CreateSession(chatID int64, username, key string, rank KeyRank) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[chatID] = &SessionInfo{
		ChatID:    chatID,
		Username:  username,
		Rank:      rank,
		KeyUsed:   key,
		StartedAt: time.Now(),
	}

	// Actualizar uso de key
	if entry, ok := s.Keys[key]; ok {
		entry.UsedBy = chatID
		now := time.Now()
		entry.UsedAt = &now
		entry.Uses++
	}
	s.save()
}

// GetSession retorna la sesión activa
func (s *Store) GetSession(chatID int64) (*SessionInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[chatID]
	return sess, ok
}

// IsAuthorized verifica si el usuario tiene sesión
func (s *Store) IsAuthorized(chatID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.sessions[chatID]
	return ok
}

// IsAdmin verifica si el usuario es admin
func (s *Store) IsAdmin(chatID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[chatID]
	if !ok {
		return false
	}
	return sess.Rank == RankAdmin || sess.Rank == RankOwner
}

// IncrementMessages incrementa contador de mensajes de sesión
func (s *Store) IncrementMessages(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[chatID]; ok {
		sess.Messages++
	}
}

// AddKey agrega una nueva key
func (s *Store) AddKey(entry *KeyEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Keys[entry.Key]; exists {
		return fmt.Errorf("la key %s ya existe", entry.Key)
	}
	s.Keys[entry.Key] = entry
	return s.save()
}

// DeleteKey elimina una key
func (s *Store) DeleteKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Keys[key]; !exists {
		return fmt.Errorf("key %s no encontrada", key)
	}
	delete(s.Keys, key)
	return s.save()
}

// GetKey obtiene info de una key
func (s *Store) GetKey(key string) (*KeyEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.Keys[key]
	return entry, ok
}

// ListKeys lista todas las keys
func (s *Store) ListKeys() []*KeyEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*KeyEntry, 0, len(s.Keys))
	for _, v := range s.Keys {
		list = append(list, v)
	}
	return list
}

// SetKeyRank cambia el rango de una key
func (s *Store) SetKeyRank(key string, rank KeyRank) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.Keys[key]
	if !ok {
		return fmt.Errorf("key %s no encontrada", key)
	}
	entry.Rank = rank
	return s.save()
}

// ToggleKey activa/desactiva una key
func (s *Store) ToggleKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.Keys[key]
	if !ok {
		return fmt.Errorf("key %s no encontrada", key)
	}
	entry.Active = !entry.Active
	return s.save()
}

// GetActiveSessions retorna sesiones activas
func (s *Store) GetActiveSessions() []*SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*SessionInfo, 0, len(s.sessions))
	for _, v := range s.sessions {
		list = append(list, v)
	}
	return list
}

// GetSessionCount retorna cantidad de sesiones activas
func (s *Store) GetSessionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// ChangeAdminKey cambia la key de admin
func (s *Store) ChangeAdminKey(newKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.AdminKey = newKey
	return s.save()
}

func (s *Store) Logout(chatID int64) bool {
    s.mu.Lock()
    defer s.mu.Unlock()
    _, exists := s.sessions[chatID]
    if exists {
        delete(s.sessions, chatID)
    }
    return exists
}

// save escribe el JSON (sin lock, llamar con lock ya tomado)
func (s *Store) save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0600)
}

