package utils

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
	"strconv"
	"strings"
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
		LastRequest: time.Now().Add(-1 * time.Hour),
	}
}

func (rl *RateLimitClient) Do(req *http.Request) (*http.Response, error) {

	rl.applyDynamicWait()

	if rl.mustWaitBeforeNext() {
		wait := time.Until(rl.ResetTime)
		if wait < time.Second {
			wait = time.Second
		}
		fmt.Printf("Esperando reset por header oficial: %v\n", wait)
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

		if resp.StatusCode != http.StatusTooManyRequests {
			rl.adjustDynamicRate(false)
			
			rl.mu.Lock()
			rl.LastRequest = time.Now()
			rl.mu.Unlock()
			
			return resp, nil
		}

		resp.Body.Close()
		rl.adjustDynamicRate(true)               
		wait := rl.getWaitTime(resp, attempt)

		fmt.Printf("429 detectado. Tentativa %d/%d. Esperando %v...\n", attempt+1, rl.MaxRetries, wait)
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

	if foundHeader {
		rl.AutoRateMode = false
		return
	}

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

	if rl.SafeRate > 0 {
		return 1 * time.Second
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

	currentRate := rl.DynamicRate
	if rl.SafeRate > 0 {
		currentRate = rl.SafeRate
	}

	if currentRate <= 0 {
		currentRate = 1
	}

	minInterval := time.Second / time.Duration(currentRate)
	elapsed := time.Since(rl.LastRequest)

	if elapsed < minInterval {
		sleepTime := minInterval - elapsed
		time.Sleep(sleepTime)
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
		newSafe := rl.DynamicRate - 1
		if newSafe < 1 {
			newSafe = 1
		}

		rl.SafeRate = newSafe
		rl.DynamicRate = newSafe
		fmt.Printf("Limite seguro encontrado e travado em: %d req/s\n", rl.SafeRate)
		return
	}

	nextRate := rl.DynamicRate + 1
	fmt.Printf("Aumentando taxa de exploração para %d req/s\n", nextRate)
	rl.DynamicRate = nextRate
}