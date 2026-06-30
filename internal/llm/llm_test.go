package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTokenSourcesSupportAPIKeyOAuthAndExternalCommand(t *testing.T) {
	env := map[string]string{
		"LLM_API_KEY":       "raw-api-key",
		"LLM_ACCESS_TOKEN":  "raw-access-token",
		"LLM_CLIENT_ID":     "client-a",
		"LLM_CLIENT_SECRET": "client-secret-a",
		"LLM_REFRESH_TOKEN": "refresh-token-a",
	}
	envLookup := func(key string) string { return env[key] }

	apiKey, err := NewTokenSource(AuthConfig{
		Mode:   AuthModeAPIKey,
		Env:    "LLM_API_KEY",
		Scheme: "Bearer",
	}, TokenSourceOptions{EnvLookup: envLookup})
	if err != nil {
		t.Fatalf("NewTokenSource(api_key) error = %v", err)
	}
	apiToken, err := apiKey.Token(context.Background())
	if err != nil {
		t.Fatalf("api key Token() error = %v", err)
	}
	if apiToken.Value != "raw-api-key" || apiToken.Type != "Bearer" {
		t.Fatalf("api token = %+v, want bearer api key", apiToken)
	}
	if strings.Contains(apiKey.RedactedSummary(), "raw-api-key") {
		t.Fatalf("RedactedSummary leaked API key: %s", apiKey.RedactedSummary())
	}

	tokenEnv, err := NewTokenSource(AuthConfig{
		Mode:     AuthModeOAuthTokenEnv,
		TokenEnv: "LLM_ACCESS_TOKEN",
	}, TokenSourceOptions{EnvLookup: envLookup})
	if err != nil {
		t.Fatalf("NewTokenSource(oauth_token_env) error = %v", err)
	}
	envToken, err := tokenEnv.Token(context.Background())
	if err != nil {
		t.Fatalf("oauth token env Token() error = %v", err)
	}
	if envToken.Value != "raw-access-token" || envToken.Type != "Bearer" {
		t.Fatalf("oauth token env token = %+v", envToken)
	}

	var grants []string
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		grants = append(grants, r.Form.Get("grant_type"))
		switch r.Form.Get("grant_type") {
		case "client_credentials":
			if r.Form.Get("client_id") != "client-a" || r.Form.Get("client_secret") != "client-secret-a" {
				t.Fatalf("client credentials form = %s", r.Form.Encode())
			}
			if r.Form.Get("scope") != "llm.invoke migration.read" {
				t.Fatalf("scope = %q", r.Form.Get("scope"))
			}
			io.WriteString(w, `{"access_token":"oauth-client-token","token_type":"Bearer","expires_in":3600}`)
		case "refresh_token":
			if r.Form.Get("refresh_token") != "refresh-token-a" {
				t.Fatalf("refresh token form = %s", r.Form.Encode())
			}
			io.WriteString(w, `{"access_token":"oauth-refresh-token","token_type":"Bearer","expires_in":3600}`)
		default:
			t.Fatalf("unexpected grant_type %q", r.Form.Get("grant_type"))
		}
	}))
	defer tokenServer.Close()

	clientCredentials, err := NewTokenSource(AuthConfig{
		Mode:            AuthModeOAuthClientCredentials,
		TokenURL:        tokenServer.URL,
		ClientIDEnv:     "LLM_CLIENT_ID",
		ClientSecretEnv: "LLM_CLIENT_SECRET",
		Scopes:          []string{"llm.invoke", "migration.read"},
	}, TokenSourceOptions{EnvLookup: envLookup, HTTPClient: tokenServer.Client()})
	if err != nil {
		t.Fatalf("NewTokenSource(oauth_client_credentials) error = %v", err)
	}
	clientToken, err := clientCredentials.Token(context.Background())
	if err != nil {
		t.Fatalf("client credentials Token() error = %v", err)
	}
	if clientToken.Value != "oauth-client-token" {
		t.Fatalf("client credentials token = %+v", clientToken)
	}

	refreshToken, err := NewTokenSource(AuthConfig{
		Mode:            AuthModeOAuthRefreshToken,
		TokenURL:        tokenServer.URL,
		ClientIDEnv:     "LLM_CLIENT_ID",
		ClientSecretEnv: "LLM_CLIENT_SECRET",
		RefreshTokenEnv: "LLM_REFRESH_TOKEN",
	}, TokenSourceOptions{EnvLookup: envLookup, HTTPClient: tokenServer.Client()})
	if err != nil {
		t.Fatalf("NewTokenSource(oauth_refresh_token) error = %v", err)
	}
	refreshed, err := refreshToken.Token(context.Background())
	if err != nil {
		t.Fatalf("refresh token Token() error = %v", err)
	}
	if refreshed.Value != "oauth-refresh-token" {
		t.Fatalf("refresh token = %+v", refreshed)
	}
	if strings.Join(grants, ",") != "client_credentials,refresh_token" {
		t.Fatalf("grants = %v", grants)
	}

	external, err := NewTokenSource(AuthConfig{
		Mode:                 AuthModeExternalCommand,
		Command:              []string{"vault", "read", "-field=token", "secret/sqlserver2tidb/llm"},
		AllowExternalCommand: true,
	}, TokenSourceOptions{
		EnvLookup: envLookup,
		CommandRunner: func(ctx context.Context, command []string) (string, error) {
			if strings.Join(command, " ") != "vault read -field=token secret/sqlserver2tidb/llm" {
				t.Fatalf("command = %v", command)
			}
			return "external-token\n", nil
		},
	})
	if err != nil {
		t.Fatalf("NewTokenSource(external_command) error = %v", err)
	}
	externalToken, err := external.Token(context.Background())
	if err != nil {
		t.Fatalf("external command Token() error = %v", err)
	}
	if externalToken.Value != "external-token" {
		t.Fatalf("external token = %+v", externalToken)
	}
}

func TestProviderConfigParsesAuthModes(t *testing.T) {
	config := `
default_provider: enterprise-gateway
providers:
  - id: openai
    type: openai_compatible
    base_url: https://api.openai.com/v1
    model: gpt-4.1
    auth:
      mode: api_key
      env: OPENAI_API_KEY
      scheme: Bearer
  - id: enterprise-gateway
    type: openai_compatible
    base_url: https://llm-gateway.example.com/v1
    model: migration-advisor
    auth:
      mode: oauth_client_credentials
      token_url: https://idp.example.com/oauth2/token
      client_id_env: SQLSERVER2TIDB_LLM_CLIENT_ID
      client_secret_env: SQLSERVER2TIDB_LLM_CLIENT_SECRET
      scopes:
        - llm.invoke
        - migration.read
  - id: workload-identity
    type: openai_compatible
    base_url: https://llm-gateway.example.com/v1
    model: migration-advisor
    auth:
      mode: external_command
      allow_external_command: true
      command:
        - vault
        - read
        - -field=token
        - secret/sqlserver2tidb/llm-token
`
	loaded, err := ParseProviderConfig(strings.NewReader(config))
	if err != nil {
		t.Fatalf("ParseProviderConfig() error = %v", err)
	}
	if loaded.DefaultProvider != "enterprise-gateway" {
		t.Fatalf("DefaultProvider = %q", loaded.DefaultProvider)
	}
	enterprise, ok := loaded.Provider("enterprise-gateway")
	if !ok {
		t.Fatal("Provider(enterprise-gateway) missing")
	}
	if enterprise.Auth.Mode != AuthModeOAuthClientCredentials {
		t.Fatalf("enterprise auth mode = %q", enterprise.Auth.Mode)
	}
	if strings.Join(enterprise.Auth.Scopes, ",") != "llm.invoke,migration.read" {
		t.Fatalf("enterprise scopes = %v", enterprise.Auth.Scopes)
	}
	workload, ok := loaded.Provider("workload-identity")
	if !ok {
		t.Fatal("Provider(workload-identity) missing")
	}
	if !workload.Auth.AllowExternalCommand {
		t.Fatal("external_command provider did not parse allow_external_command")
	}
	if strings.Join(workload.Auth.Command, " ") != "vault read -field=token secret/sqlserver2tidb/llm-token" {
		t.Fatalf("external command = %v", workload.Auth.Command)
	}
}

func TestOpenAICompatibleClientSendsBearerTokenAndParsesResponse(t *testing.T) {
	var authHeader string
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		body = string(data)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"# Compatibility Advice\n\nReview XML columns."}}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`)
	}))
	defer server.Close()

	client, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		BaseURL: server.URL + "/v1",
		Model:   "migration-advisor",
		TokenSource: StaticTokenSource(AccessToken{
			Type:  "Bearer",
			Value: "oauth-access-token",
		}),
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleClient() error = %v", err)
	}
	response, err := client.Generate(context.Background(), Request{
		Task:   "compatibility_advice",
		System: "You are a migration advisor.",
		Inputs: []InputFile{{
			Path:    "clusters/prod-sqlserver-a/inventory/compatibility-report.md",
			Content: "SQLSERVER_TYPE_XML",
		}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if authHeader != "Bearer oauth-access-token" {
		t.Fatalf("Authorization = %q", authHeader)
	}
	if !strings.Contains(body, `"model":"migration-advisor"`) || !strings.Contains(body, "SQLSERVER_TYPE_XML") {
		t.Fatalf("request body = %s", body)
	}
	if response.Text != "# Compatibility Advice\n\nReview XML columns." {
		t.Fatalf("response text = %q", response.Text)
	}
	if response.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v", response.Usage)
	}
}

func TestOpenAICompatibleClientRejectsInvalidBaseURL(t *testing.T) {
	_, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		BaseURL: "://bad-url",
		Model:   "migration-advisor",
		TokenSource: StaticTokenSource(AccessToken{
			Type:  "Bearer",
			Value: "token",
		}),
	})
	if err == nil {
		t.Fatal("NewOpenAICompatibleClient() expected invalid base URL error")
	}
	if strings.TrimSpace(err.Error()) == "" {
		t.Fatalf("error = %v", err)
	}
}
