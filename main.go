package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"regexp"
)

const (
	maxAttempts    = 5
	requestTimeout = 30 * time.Second
)

/* --------------------- RATE LIMIT STRUCTS --------------------- */

type RateLimit struct {
	URL       string
	Value     int
	Type      string // "seconds" ou "minutes"
	Count     int
	ResetTime time.Time
	mu        sync.Mutex
}

var rateLimits []*RateLimit

/* -------------------------------------------------------------- */

type ErrorResponse struct {
	Attempt int    `json:"attempt"`
	Error   string `json:"error"`
}

func main() {

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("Erro ao obter diretório atual: %v", err)
	}

	envPath := cwd + "/.env"
	responsePath := cwd + "/response.json"
	errorLogPath := cwd + "/errors.json"

	/* ----------- CARREGA RATE LIMIT DINÂMICO DO ENV ----------- */
	loadRateLimitsFromEnv(envPath)
	/* ----------------------------------------------------------- */

	urlBase, tokenURL, token, err := loadEnvValues(envPath)
	if err != nil || strings.TrimSpace(token) == "" {
		log.Println("ACCESS_TOKEN ausente ou inválido. Gerando novo token...")
		token, err = getAccessToken(envPath, tokenURL)
		if err != nil {
			log.Fatalf("Erro ao gerar token: %v", err)
		}
	}

	urlRequest := buildURL(urlBase)
	body, errors, err := doRequestWithRetry(urlRequest, token, maxAttempts)

	if len(errors) > 0 {
		saveErrors(errorLogPath, errors)
	}

	if err != nil {
		log.Printf("Falha final na requisição: %v", err)
		createEmptyResponseFile(responsePath)
		return
	}

	writeFile(responsePath, body)
	log.Println("Arquivo response.json criado com sucesso.")
}


func loadRateLimitsFromEnv(envPath string) {
	content, err := os.ReadFile(envPath)
	if err != nil {
		log.Fatalf("Erro ao abrir .env para rate limit: %v", err)
	}

	re := regexp.MustCompile(`(?m)^RATE_LIMIT_(\d+)_(URL|VALUE|TYPE)=(.+)$`)
	matches := re.FindAllStringSubmatch(string(content), -1)

	temp := make(map[string]map[string]string)

	for _, m := range matches {
		group := m[1]
		field := m[2]
		value := strings.TrimSpace(m[3])

		if _, ok := temp[group]; !ok {
			temp[group] = make(map[string]string)
		}

		temp[group][field] = value
	}

	for _, data := range temp {
		if data["URL"] == "" || data["VALUE"] == "" || data["TYPE"] == "" {
			continue
		}

		valueInt := 0
		fmt.Sscanf(data["VALUE"], "%d", &valueInt)

		rl := &RateLimit{
			URL:       data["URL"],
			Value:     valueInt,
			Type:      strings.ToLower(data["TYPE"]),
			ResetTime: time.Now(),
		}

        rateLimits = append(rateLimits, rl)
	}

	log.Println("Rate limits carregados:", len(rateLimits))
}

func applyRateLimit(requestURL string) {
	for _, rl := range rateLimits {

		if strings.HasPrefix(requestURL, rl.URL) {

			rl.mu.Lock()

			now := time.Now()

			// RESET automático baseado no tipo
			if rl.Type == "seconds" {
				if now.After(rl.ResetTime.Add(1 * time.Second)) {
					rl.Count = 0
					rl.ResetTime = now
				}
			} else if rl.Type == "minutes" {
				if now.After(rl.ResetTime.Add(1 * time.Minute)) {
					rl.Count = 0
					rl.ResetTime = now
				}
			}

			if rl.Count >= rl.Value {
				waitDuration := rl.ResetTime.Add(
					map[string]time.Duration{
						"seconds": 1 * time.Second,
						"minutes": 1 * time.Minute,
					}[rl.Type],
				).Sub(now)

				rl.mu.Unlock()

				log.Printf("[RATE LIMIT] Aguardando %v para URL %s\n", waitDuration, requestURL)
				time.Sleep(waitDuration)

				applyRateLimit(requestURL)
				return
			}

			rl.Count++
			rl.mu.Unlock()

			return
		}
	}
}

/* -------------------------------------------------------------- */

func loadEnvValues(path string) (string, string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", "", fmt.Errorf("erro ao abrir .env: %w", err)
	}
	defer file.Close()

	var urlBase, accessToken, tokenURL string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "URL=") {
			urlBase = strings.TrimPrefix(line, "URL=")
			continue
		}

		if strings.HasPrefix(line, "TOKEN_URL=") {
			tokenURL = strings.TrimPrefix(line, "TOKEN_URL=")
			continue
		}

		if strings.HasPrefix(line, "ACCESS_TOKEN=") {
			accessToken = strings.TrimPrefix(line, "ACCESS_TOKEN=")
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return "", "", "", err
	}

	if urlBase == "" {
		return "", "", "", fmt.Errorf("URL não encontrada no .env")
	}
	if tokenURL == "" {
		return "", "", "", fmt.Errorf("TOKEN_URL não encontrada no .env")
	}

	return urlBase, tokenURL, accessToken, nil
}

func loadCredentials(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", fmt.Errorf("erro ao abrir .env: %w", err)
	}
	defer file.Close()

	var clientID, clientSecret string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "CLIENT_ID=") {
			clientID = strings.TrimPrefix(line, "CLIENT_ID=")
			continue
		}

		if strings.HasPrefix(line, "CLIENT_SECRET=") {
			clientSecret = strings.TrimPrefix(line, "CLIENT_SECRET=")
			continue
		}
	}

	if clientID == "" || clientSecret == "" {
		return "", "", fmt.Errorf("CLIENT_ID ou CLIENT_SECRET ausente no .env")
	}

	return clientID, clientSecret, nil
}

func getAccessToken(envPath, tokenURL string) (string, error) {

	clientID, clientSecret, err := loadCredentials(envPath)
	if err != nil {
		return "", err
	}

	log.Println("Solicitando novo ACCESS_TOKEN...")

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)

	resp, err := http.Post(tokenURL, "application/x-www-form-urlencoded", bytes.NewBufferString(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("erro na requisição do token: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("erro ao obter token (%d): %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("erro ao decodificar token: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("token vazio ou inválido recebido")
	}

	updateEnvToken(envPath, tokenResp.AccessToken)
	return tokenResp.AccessToken, nil
}

func updateEnvToken(path, token string) {
	input, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("Erro lendo .env: %v", err)
	}

	lines := strings.Split(string(input), "\n")

	for i, line := range lines {
		if strings.HasPrefix(line, "ACCESS_TOKEN=") {
			lines[i] = "ACCESS_TOKEN=" + token
		}
	}

	err = os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
	if err != nil {
		log.Fatalf("Erro escrevendo .env: %v", err)
	}

	log.Println("ACCESS_TOKEN atualizado no .env")
}

func buildURL(urlBase string) string {
	today := time.Now().Format("2006-01-02")
	return fmt.Sprintf("%s?dataBase=%sT00:00:00.000Z", urlBase, today)
}

func doRequestWithRetry(url, token string, attempts int) ([]byte, []ErrorResponse, error) {
	client := &http.Client{Timeout: requestTimeout}
	var errors []ErrorResponse

	for attempt := 1; attempt <= attempts; attempt++ {

		/* -------- APLICA RATE LIMIT AQUI -------- */
		applyRateLimit(url)
		/* ---------------------------------------- */

		log.Printf("Tentativa %d de %d...", attempt, attempts)

		body, status, err := doSingleRequest(client, url, token)
		if err == nil && status == 200 {
			return body, errors, nil
		}

		msg := fmt.Sprintf("Status %d - %v", status, err)
		errors = append(errors, ErrorResponse{Attempt: attempt, Error: msg})
		log.Println("Erro:", msg)

		time.Sleep(2 * time.Second)
	}

	return nil, errors, fmt.Errorf("todas as tentativas falharam")
}

func doSingleRequest(client *http.Client, url, token string) ([]byte, int, error) {

	/* -------- APLICA RATE LIMIT EM TODA REQUISIÇÃO -------- */
	applyRateLimit(url)
	/* ------------------------------------------------------- */

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

func writeFile(path string, data []byte) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "tmp-*.tmp")
	if err != nil {
		log.Fatalf("Erro ao criar arquivo temporário: %v", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	if _, err := tmp.Write(data); err != nil {
		log.Fatalf("Erro ao escrever arquivo temporário: %v", err)
	}
	if err := tmp.Sync(); err != nil {
		log.Fatalf("Erro ao sincronizar arquivo temporário: %v", err)
	}

	if err := tmp.Close(); err != nil {
		log.Fatalf("Erro ao fechar arquivo temporário: %v", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		log.Fatalf("Erro ao mover arquivo temporário: %v", err)
	}
}

func saveErrors(path string, errors []ErrorResponse) {
	file, err := os.Create(path)
	if err != nil {
		log.Printf("Erro ao criar arquivo de erros: %v", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encoder.Encode(errors)
}

func createEmptyResponseFile(path string) {
	writeFile(path, []byte("[]"))
}
