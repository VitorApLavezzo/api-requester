package utils

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RateLimitClient struct {
	Client      *http.Client
	MaxRetries  int
	BaseBackoff time.Duration
	mu          sync.Mutex

	Limit     int
	Remaining int
	ResetTime time.Time

	AutoRateMode bool
	DynamicRate  int
	SafeRate     int

	LastRequest time.Time
}

func NewRateLimitClient() *RateLimitClient {
	return &RateLimitClient{
		Client:      &http.Client{},
		MaxRetries:  5,
		BaseBackoff: 1 * time.Second,
		DynamicRate: 1,
	}
}

func (rl *RateLimitClient) Do(req *http.Request) (*http.Response, error) {

	rl.applyDynamicWait()

	if rl.mustWaitBeforeNext() {
		wait := time.Until(rl.ResetTime)
		if wait < time.Second {
			wait = time.Second
		}
		fmt.Printf("⏳ Esperando reset por header: %v\n", wait)
		time.Sleep(wait)
	}

	var resp *http.Response
	var err error

	for attempt := 0; attempt <= rl.MaxRetries; attempt++ {

		resp, err = rl.Client.Do(req)
		if err != nil {
			return nil, err
		}

		rl.updateRateLimitTracking(resp)

		// Sucesso
		if resp.StatusCode != http.StatusTooManyRequests {
			rl.adjustDynamicRate(false)
			return resp, nil
		}

		// 429 detectado
		rl.adjustDynamicRate(true)
		wait := rl.getWaitTime(resp, attempt)

		fmt.Printf("⚠️ 429 detectado. Esperando %v...\n", wait)
		time.Sleep(wait)
	}

	return nil, errors.New("excedido número máximo de tentativas após rate limit")
}

func (rl *RateLimitClient) mustWaitBeforeNext() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.Limit == 0 {
		return false
	}
	if rl.Remaining > 0 {
		return false
	}
	if rl.ResetTime.IsZero() {
		return false
	}
	return time.Now().Before(rl.ResetTime)
}

func (rl *RateLimitClient) updateRateLimitTracking(resp *http.Response) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	h := resp.Header

	foundHeader := false

	if v := h.Get("X-RateLimit-Limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			rl.Limit = n
			foundHeader = true
		}
	}

	if v := h.Get("X-RateLimit-Remaining"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			rl.Remaining = n
			foundHeader = true
		}
	}

	if reset := h.Get("X-RateLimit-Reset"); reset != "" {
		if ts, err := strconv.ParseInt(reset, 10, 64); err == nil {
			rl.ResetTime = time.Unix(ts, 0)
			foundHeader = true
		}
	}

	// Se a API manda headers → entra em modo fixo
	if foundHeader {
		rl.AutoRateMode = false
		return
	}

	// Caso contrário → modo de exploração automática
	if rl.SafeRate == 0 {
		rl.AutoRateMode = true
	}
}

func (rl *RateLimitClient) getWaitTime(resp *http.Response, attempt int) time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	h := resp.Header

	if retry := h.Get("Retry-After"); retry != "" {

		if sec, err := strconv.Atoi(strings.TrimSpace(retry)); err == nil {
			d := time.Duration(sec) * time.Second
			if d <= 0 {
				d = rl.BaseBackoff
			}
			return d
		}

		if t, err := http.ParseTime(retry); err == nil {
			d := time.Until(t)
			if d <= 0 {
				d = rl.BaseBackoff
			}
			return d
		}
	}

	// Se já descobrimos o SafeRate, respeite a janela automática
	if rl.SafeRate > 0 {
		return time.Second
	}

	// Modo automático sem header → espera mínima
	if rl.AutoRateMode {
		return rl.BaseBackoff
	}

	wait := rl.BaseBackoff * time.Duration(1<<attempt)
	if wait > 2*time.Minute {
		wait = 2 * time.Minute
	}
	return wait
}

func (rl *RateLimitClient) applyDynamicWait() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Se já sabemos o SafeRate → usar ele sempre
	if rl.SafeRate > 0 {
		minInterval := time.Second / time.Duration(rl.SafeRate)
		if time.Since(rl.LastRequest) < minInterval {
			time.Sleep(minInterval - time.Since(rl.LastRequest))
		}
		rl.LastRequest = time.Now()
		return
	}

	if !rl.AutoRateMode || rl.DynamicRate <= 0 {
		return
	}

	minInterval := time.Second / time.Duration(rl.DynamicRate)

	if time.Since(rl.LastRequest) < minInterval {
		time.Sleep(minInterval - time.Since(rl.LastRequest))
	}

	rl.LastRequest = time.Now()
}

func (rl *RateLimitClient) adjustDynamicRate(hit429 bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	
	if rl.SafeRate > 0 {
		rl.DynamicRate = rl.SafeRate
		rl.AutoRateMode = false 
		return
	}

	if !rl.AutoRateMode {
		return
	}

	if hit429 {
		rl.SafeRate = rl.DynamicRate - 1
		if rl.SafeRate < 1 {
			rl.SafeRate = 1
		}

		fmt.Println("🔒 Limite seguro detectado:", rl.SafeRate, "req/s")

		rl.DynamicRate = rl.SafeRate
		return
	}

	nextRate := rl.DynamicRate + 1

	fmt.Println("⬆ Aumentando taxa automática para", nextRate, "req/s")
	rl.DynamicRate = nextRate
}
