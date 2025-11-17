# 📌 Rotina de Requisição com Retry, Log de Erros e Geração de Arquivo --- Go

Desenvolvido para estudo da linguagem de programação Golang

Este projeto realiza uma requisição HTTP para uma API, utilizando um
token carregado de um arquivo `.env`.\
A rotina inclui:

-   🔄 Requisições com múltiplas tentativas (retry)
-   📝 Salvamento estruturado de erros em JSON
-   📄 Geração de arquivo de resposta
-   ⚠️ Criação de arquivo vazio em caso de falha total
-   ⏱️ Timeout configurado
-   ✔️ Validação dos campos do `.env`

------------------------------------------------------------------------

## 🗂️ Estrutura do Projeto

    api-requester/
    │   ├── main.go
    │   ├── .env
    │   ├── response.json
    │   └── errors.json

------------------------------------------------------------------------

## ⚙️ Configuração do `.env`

O arquivo `.env` deve conter:

    URL=https://sua_api_aqui.com/endpoint
    ACCESS_TOKEN=seu_token_de_acesso

A rotina valida:

-   Se os campos existem
-   Se não estão vazios

------------------------------------------------------------------------

## 🚀 Funcionamento da Rotina

### 1. 🔧 Carregamento do `.env`

A função `loadEnvValues()`:

-   Lê o arquivo `.env`
-   Extrai `URL` e `ACCESS_TOKEN`
-   Valida ambos
-   Retorna erro detalhado caso algo esteja errado

------------------------------------------------------------------------

### 2. 🔗 Construção da URL com data atual

A função `buildURL()` acrescenta automaticamente o parâmetro:

    dataBase=YYYY-MM-DDT00:00:00.000Z

Exemplo:

    https://api.com/data?dataBase=2025-11-14T00:00:00.000Z

------------------------------------------------------------------------

### 3. 🔄 Requisição com Tentativas (Retry)

A função `doRequestWithRetry()`:

-   Realiza até **5 tentativas**
-   Apenas repete a tentativa se o status for **500**
-   Registra erros não-500 e encerra imediatamente
-   Aguarda 2s entre tentativas
-   Salva erros acumulados em `errors.json`

------------------------------------------------------------------------

### 4. 🌐 Execução da Requisição

`doSingleRequest()`:

-   Cria contexto com timeout
-   Envia requisição GET com headers:
    -   `User-Agent`
    -   `Authorization: Bearer <token>`
-   Retorna body e status code

------------------------------------------------------------------------

### 5. 📁 Escrita dos Arquivos

#### ✔ Caso sucesso

-   Cria `response.json` com o retorno da API.

#### ❌ Caso falha de todas as tentativas

-   Cria `response.json` vazio com `[]`.
-   Cria `errors.json` com os detalhes das falhas.

------------------------------------------------------------------------

## 📄 Exemplo de `errors.json`

``` json
[
  {
    "attempt": 1,
    "error": "Status code: 500"
  },
  {
    "attempt": 2,
    "error": "timeout waiting for response"
  }
]
```

------------------------------------------------------------------------

## ▶️ Como Executar

No diretório do projeto:

``` bash
go run main.go
```

------------------------------------------------------------------------

## 🔧 Constantes Configuráveis

  Constante          Descrição
  ------------------ ------------------------------------
  `envPath`          Caminho para o `.env`
  `responsePath`     Caminho onde a resposta será salva
  `errorLogPath`     Caminho para arquivo de erros
  `maxAttempts`      Quantidade de tentativas
  `requestTimeout`   Timeout por requisição

------------------------------------------------------------------------

## 🧩 Fluxo Completo

1.  Lê variáveis do `.env`
2.  Valida campos
3.  Monta URL com data atual
4.  Realiza requisições com retry
5.  Salva erros
6.  Salva resposta ou cria arquivo vazio
