package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxAttempts    = 5
	requestTimeout = 30 * time.Second
)

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

	urlBase, err := loadEnvValues(envPath)
	if err != nil {
		log.Fatalf("Erro carregando .env: %v", err)
	}

	urlRequest := buildURL(urlBase)
	body, errors, err := doRequestWithRetry(urlRequest, maxAttempts)

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

func loadEnvValues(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("erro ao abrir .env: %w", err)
	}
	defer file.Close()

	var urlBase string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "URL=") {
			urlBase = strings.TrimPrefix(line, "URL=")
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	if urlBase == "" {
		return "", fmt.Errorf("URL não encontrada no .env")
	}

	return urlBase, nil
}

func buildURL(urlBase string) string {
	today := time.Now().Format("2006-01-02")
	return fmt.Sprintf("%s?dataBase=%sT00:00:00.000Z", urlBase, today)
}

func doRequestWithRetry(url string, attempts int) ([]byte, []ErrorResponse, error) {
	client := &http.Client{Timeout: requestTimeout}
	var errors []ErrorResponse

	for attempt := 1; attempt <= attempts; attempt++ {
		log.Printf("Tentativa %d de %d...", attempt, attempts)

		body, status, err := doSingleRequest(client, url)
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

func doSingleRequest(client *http.Client, url string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")

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
