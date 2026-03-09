package ratelimit

import (
	"sync"
	"time"
)

// UserLimiter gestiona rate limiting por usuario
type UserLimiter struct {
	mu      sync.Mutex
	buckets map[int64]*bucket
}

type bucket struct {
	tokens   float64
	maxToken float64
	lastFill time.Time
	rate     float64 // tokens por segundo
	inFlight bool    // si hay una request en proceso
}

// NewUserLimiter crea un nuevo rate limiter
// maxReq: máximo de requests por intervalo
// interval: ventana de tiempo
func NewUserLimiter() *UserLimiter {
	ul := &UserLimiter{
		buckets: make(map[int64]*bucket),
	}
	// Limpiar buckets viejos cada 5 minutos
	go ul.cleanup()
	return ul
}

// Allow verifica si el usuario puede hacer una request
// Retorna true si está permitido, false si debe esperar
func (ul *UserLimiter) Allow(chatID int64) bool {
	ul.mu.Lock()
	defer ul.mu.Unlock()

	b, ok := ul.buckets[chatID]
	if !ok {
		b = &bucket{
			tokens:   5,
			maxToken: 5,
			lastFill: time.Now(),
			rate:     0.5, // 1 token cada 2 segundos
		}
		ul.buckets[chatID] = b
	}

	// Rellenar tokens según tiempo transcurrido
	now := time.Now()
	elapsed := now.Sub(b.lastFill).Seconds()
	b.tokens = min(b.maxToken, b.tokens+elapsed*b.rate)
	b.lastFill = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// SetInFlight marca que hay una petición en vuelo
func (ul *UserLimiter) SetInFlight(chatID int64, v bool) {
	ul.mu.Lock()
	defer ul.mu.Unlock()
	if b, ok := ul.buckets[chatID]; ok {
		b.inFlight = v
	}
}

// IsInFlight verifica si hay petición en proceso
func (ul *UserLimiter) IsInFlight(chatID int64) bool {
	ul.mu.Lock()
	defer ul.mu.Unlock()
	if b, ok := ul.buckets[chatID]; ok {
		return b.inFlight
	}
	return false
}

func (ul *UserLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		ul.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for id, b := range ul.buckets {
			if b.lastFill.Before(cutoff) {
				delete(ul.buckets, id)
			}
		}
		ul.mu.Unlock()
	}
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}