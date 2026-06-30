package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BornChanger/sqlserver2tidb/internal/redact"
)

const (
	AuthModeAPIKey                 = "api_key"
	AuthModeOAuthClientCredentials = "oauth_client_credentials"
	AuthModeOAuthRefreshToken      = "oauth_refresh_token"
	AuthModeOAuthTokenEnv          = "oauth_token_env"
	AuthModeExternalCommand        = "external_command"
)

const ProviderTypeOpenAICompatible = "openai_compatible"

type Client interface {
	Generate(ctx context.Context, req Request) (Response, error)
}

type Request struct {
	Task        string
	System      string
	Inputs      []InputFile
	OutputStyle string
	JSONSchema  string
}

type InputFile struct {
	Path    string
	Content string
}

type Response struct {
	Text        string
	Model       string
	Usage       Usage
	GeneratedAt string
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type AccessToken struct {
	Type      string
	Value     string
	ExpiresAt time.Time
}

type TokenSource interface {
	Token(ctx context.Context) (AccessToken, error)
	RedactedSummary() string
}

type AuthConfig struct {
	Mode                 string
	Header               string
	Scheme               string
	Env                  string
	TokenEnv             string
	TokenURL             string
	ClientIDEnv          string
	ClientSecretEnv      string
	RefreshTokenEnv      string
	Scopes               []string
	Command              []string
	AllowExternalCommand bool
}

type TokenSourceOptions struct {
	EnvLookup     func(string) string
	HTTPClient    *http.Client
	CommandRunner func(context.Context, []string) (string, error)
	Now           func() time.Time
}

func NewTokenSource(config AuthConfig, options TokenSourceOptions) (TokenSource, error) {
	config.Mode = strings.ToLower(strings.TrimSpace(config.Mode))
	if config.Mode == "" {
		config.Mode = AuthModeAPIKey
	}
	if options.EnvLookup == nil {
		options.EnvLookup = os.Getenv
	}
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	switch config.Mode {
	case AuthModeAPIKey:
		env := strings.TrimSpace(config.Env)
		if env == "" {
			return nil, fmt.Errorf("api_key auth env is required")
		}
		return envTokenSource{
			mode:      config.Mode,
			env:       env,
			scheme:    normalizeTokenType(config.Scheme),
			envLookup: options.EnvLookup,
		}, nil
	case AuthModeOAuthTokenEnv:
		env := strings.TrimSpace(config.TokenEnv)
		if env == "" {
			env = strings.TrimSpace(config.Env)
		}
		if env == "" {
			return nil, fmt.Errorf("oauth_token_env auth token_env is required")
		}
		return envTokenSource{
			mode:      config.Mode,
			env:       env,
			scheme:    normalizeTokenType(config.Scheme),
			envLookup: options.EnvLookup,
		}, nil
	case AuthModeOAuthClientCredentials:
		if strings.TrimSpace(config.TokenURL) == "" {
			return nil, fmt.Errorf("oauth_client_credentials auth token_url is required")
		}
		if strings.TrimSpace(config.ClientIDEnv) == "" || strings.TrimSpace(config.ClientSecretEnv) == "" {
			return nil, fmt.Errorf("oauth_client_credentials auth client_id_env and client_secret_env are required")
		}
		return &oauthTokenSource{
			mode:       config.Mode,
			config:     config,
			envLookup:  options.EnvLookup,
			httpClient: options.HTTPClient,
			now:        options.Now,
		}, nil
	case AuthModeOAuthRefreshToken:
		if strings.TrimSpace(config.TokenURL) == "" {
			return nil, fmt.Errorf("oauth_refresh_token auth token_url is required")
		}
		if strings.TrimSpace(config.RefreshTokenEnv) == "" {
			return nil, fmt.Errorf("oauth_refresh_token auth refresh_token_env is required")
		}
		return &oauthTokenSource{
			mode:       config.Mode,
			config:     config,
			envLookup:  options.EnvLookup,
			httpClient: options.HTTPClient,
			now:        options.Now,
		}, nil
	case AuthModeExternalCommand:
		if !config.AllowExternalCommand {
			return nil, fmt.Errorf("external_command auth is disabled; set allow_external_command to true")
		}
		if len(config.Command) == 0 {
			return nil, fmt.Errorf("external_command auth command is required")
		}
		runner := options.CommandRunner
		if runner == nil {
			runner = runExternalTokenCommand
		}
		return externalCommandTokenSource{command: append([]string(nil), config.Command...), runner: runner}, nil
	default:
		return nil, fmt.Errorf("unsupported auth mode %q", config.Mode)
	}
}

type envTokenSource struct {
	mode      string
	env       string
	scheme    string
	envLookup func(string) string
}

func (source envTokenSource) Token(context.Context) (AccessToken, error) {
	value := strings.TrimSpace(source.envLookup(source.env))
	if value == "" {
		return AccessToken{}, fmt.Errorf("%s env %s is not set", source.mode, source.env)
	}
	return AccessToken{Type: normalizeTokenType(source.scheme), Value: value}, nil
}

func (source envTokenSource) RedactedSummary() string {
	return fmt.Sprintf("mode=%s env=%s", source.mode, source.env)
}

type oauthTokenSource struct {
	mode       string
	config     AuthConfig
	envLookup  func(string) string
	httpClient *http.Client
	now        func() time.Time
	mu         sync.Mutex
	cached     AccessToken
}

func (source *oauthTokenSource) Token(ctx context.Context) (AccessToken, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.cached.Value != "" && source.cached.ExpiresAt.After(source.now().Add(time.Minute)) {
		return source.cached, nil
	}
	form := url.Values{}
	switch source.mode {
	case AuthModeOAuthClientCredentials:
		clientID := strings.TrimSpace(source.envLookup(source.config.ClientIDEnv))
		clientSecret := strings.TrimSpace(source.envLookup(source.config.ClientSecretEnv))
		if clientID == "" || clientSecret == "" {
			return AccessToken{}, fmt.Errorf("oauth_client_credentials client id or secret env is not set")
		}
		form.Set("grant_type", "client_credentials")
		form.Set("client_id", clientID)
		form.Set("client_secret", clientSecret)
		if len(source.config.Scopes) > 0 {
			form.Set("scope", strings.Join(source.config.Scopes, " "))
		}
	case AuthModeOAuthRefreshToken:
		refreshToken := strings.TrimSpace(source.envLookup(source.config.RefreshTokenEnv))
		if refreshToken == "" {
			return AccessToken{}, fmt.Errorf("oauth_refresh_token refresh token env is not set")
		}
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", refreshToken)
		if strings.TrimSpace(source.config.ClientIDEnv) != "" {
			form.Set("client_id", strings.TrimSpace(source.envLookup(source.config.ClientIDEnv)))
		}
		if strings.TrimSpace(source.config.ClientSecretEnv) != "" {
			form.Set("client_secret", strings.TrimSpace(source.envLookup(source.config.ClientSecretEnv)))
		}
		if len(source.config.Scopes) > 0 {
			form.Set("scope", strings.Join(source.config.Scopes, " "))
		}
	default:
		return AccessToken{}, fmt.Errorf("unsupported OAuth auth mode %q", source.mode)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(source.config.TokenURL), strings.NewReader(form.Encode()))
	if err != nil {
		return AccessToken{}, fmt.Errorf("create OAuth token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := source.httpClient.Do(request)
	if err != nil {
		return AccessToken{}, fmt.Errorf("request OAuth token: %w", err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return AccessToken{}, fmt.Errorf("OAuth token endpoint returned %s: %s", response.Status, redact.Text(string(data)))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return AccessToken{}, fmt.Errorf("parse OAuth token response: %w", err)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return AccessToken{}, fmt.Errorf("OAuth token response missing access_token")
	}
	expiresAt := source.now().Add(time.Hour)
	if payload.ExpiresIn > 0 {
		expiresAt = source.now().Add(time.Duration(payload.ExpiresIn) * time.Second)
	}
	source.cached = AccessToken{
		Type:      normalizeTokenType(payload.TokenType),
		Value:     strings.TrimSpace(payload.AccessToken),
		ExpiresAt: expiresAt,
	}
	return source.cached, nil
}

func (source *oauthTokenSource) RedactedSummary() string {
	return fmt.Sprintf("mode=%s token_url=%s", source.mode, source.config.TokenURL)
}

type externalCommandTokenSource struct {
	command []string
	runner  func(context.Context, []string) (string, error)
}

func (source externalCommandTokenSource) Token(ctx context.Context) (AccessToken, error) {
	output, err := source.runner(ctx, append([]string(nil), source.command...))
	if err != nil {
		return AccessToken{}, fmt.Errorf("external token command failed: %w", err)
	}
	value := strings.TrimSpace(output)
	if value == "" {
		return AccessToken{}, fmt.Errorf("external token command returned empty token")
	}
	return AccessToken{Type: "Bearer", Value: value}, nil
}

func (source externalCommandTokenSource) RedactedSummary() string {
	return "mode=external_command command=" + strings.Join(redact.Args(source.command), " ")
}

func runExternalTokenCommand(ctx context.Context, command []string) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("external token command is empty")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	data, err := cmd.Output()
	return string(data), err
}

type staticTokenSource struct {
	token AccessToken
}

func StaticTokenSource(token AccessToken) TokenSource {
	token.Type = normalizeTokenType(token.Type)
	return staticTokenSource{token: token}
}

func (source staticTokenSource) Token(context.Context) (AccessToken, error) {
	if strings.TrimSpace(source.token.Value) == "" {
		return AccessToken{}, fmt.Errorf("static token is empty")
	}
	return source.token, nil
}

func (source staticTokenSource) RedactedSummary() string {
	return "mode=static"
}

func normalizeTokenType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Bearer"
	}
	return value
}

type ProviderConfigFile struct {
	DefaultProvider string
	Providers       []ProviderConfig
}

type ProviderConfig struct {
	ID      string
	Type    string
	BaseURL string
	Model   string
	Auth    AuthConfig
}

func (config ProviderConfigFile) Provider(id string) (ProviderConfig, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		id = strings.TrimSpace(config.DefaultProvider)
	}
	for _, provider := range config.Providers {
		if provider.ID == id {
			return provider, true
		}
	}
	return ProviderConfig{}, false
}

func ParseProviderConfig(r io.Reader) (ProviderConfigFile, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return ProviderConfigFile{}, err
	}
	var result ProviderConfigFile
	var current *ProviderConfig
	section := ""
	listKey := ""
	lines := strings.Split(string(data), "\n")
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 {
			section = ""
			listKey = ""
		}
		if indent == 0 && strings.HasPrefix(trimmed, "default_provider:") {
			result.DefaultProvider = trimScalar(strings.TrimPrefix(trimmed, "default_provider:"))
			continue
		}
		if indent == 0 && trimmed == "providers:" {
			section = "providers"
			continue
		}
		if section == "providers" && indent == 2 && strings.HasPrefix(trimmed, "- ") {
			provider := ProviderConfig{Type: ProviderTypeOpenAICompatible}
			result.Providers = append(result.Providers, provider)
			current = &result.Providers[len(result.Providers)-1]
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if strings.HasPrefix(rest, "id:") {
				current.ID = trimScalar(strings.TrimPrefix(rest, "id:"))
			}
			listKey = ""
			continue
		}
		if current == nil {
			continue
		}
		if indent == 4 {
			listKey = ""
			if trimmed == "auth:" {
				continue
			}
			key, value, ok := strings.Cut(trimmed, ":")
			if !ok {
				continue
			}
			switch strings.TrimSpace(key) {
			case "id":
				current.ID = trimScalar(value)
			case "type":
				current.Type = trimScalar(value)
			case "base_url":
				current.BaseURL = trimScalar(value)
			case "model":
				current.Model = trimScalar(value)
			}
			continue
		}
		if indent == 6 {
			key, value, ok := strings.Cut(trimmed, ":")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			listKey = ""
			if value == "" && (key == "scopes" || key == "command") {
				listKey = key
				continue
			}
			switch key {
			case "mode":
				current.Auth.Mode = trimScalar(value)
			case "header":
				current.Auth.Header = trimScalar(value)
			case "scheme":
				current.Auth.Scheme = trimScalar(value)
			case "env":
				current.Auth.Env = trimScalar(value)
			case "token_env":
				current.Auth.TokenEnv = trimScalar(value)
			case "token_url":
				current.Auth.TokenURL = trimScalar(value)
			case "client_id_env":
				current.Auth.ClientIDEnv = trimScalar(value)
			case "client_secret_env":
				current.Auth.ClientSecretEnv = trimScalar(value)
			case "refresh_token_env":
				current.Auth.RefreshTokenEnv = trimScalar(value)
			case "allow_external_command":
				current.Auth.AllowExternalCommand = parseBool(value)
			}
			continue
		}
		if indent == 8 && strings.HasPrefix(trimmed, "- ") {
			value := trimScalar(strings.TrimPrefix(trimmed, "- "))
			switch listKey {
			case "scopes":
				current.Auth.Scopes = append(current.Auth.Scopes, value)
			case "command":
				current.Auth.Command = append(current.Auth.Command, value)
			}
		}
	}
	return result, nil
}

func trimScalar(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"`)
	value = strings.Trim(value, `'`)
	return value
}

func parseBool(value string) bool {
	parsed, _ := strconv.ParseBool(trimScalar(value))
	return parsed
}

type OpenAICompatibleConfig struct {
	BaseURL     string
	Model       string
	TokenSource TokenSource
	HTTPClient  *http.Client
}

type openAICompatibleClient struct {
	baseURL     string
	model       string
	tokenSource TokenSource
	httpClient  *http.Client
}

func NewOpenAICompatibleClient(config OpenAICompatibleConfig) (Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		if err != nil {
			return nil, fmt.Errorf("parse base URL: %w", err)
		}
		return nil, fmt.Errorf("base URL must be absolute")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if config.TokenSource == nil {
		return nil, fmt.Errorf("token source is required")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return openAICompatibleClient{
		baseURL:     baseURL,
		model:       strings.TrimSpace(config.Model),
		tokenSource: config.TokenSource,
		httpClient:  httpClient,
	}, nil
}

func (client openAICompatibleClient) Generate(ctx context.Context, req Request) (Response, error) {
	token, err := client.tokenSource.Token(ctx)
	if err != nil {
		return Response{}, err
	}
	payload := openAIChatCompletionRequest{
		Model:    client.model,
		Messages: buildOpenAIMessages(req),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(token.Value) != "" {
		httpReq.Header.Set("Authorization", normalizeTokenType(token.Type)+" "+token.Value)
	}
	httpResp, err := client.httpClient.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("call LLM provider: %w", err)
	}
	defer httpResp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4<<20))
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return Response{}, fmt.Errorf("LLM provider returned %s: %s", httpResp.Status, redact.Text(string(data)))
	}
	var decoded openAIChatCompletionResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Response{}, fmt.Errorf("parse LLM provider response: %w", err)
	}
	if len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return Response{}, fmt.Errorf("LLM provider response missing message content")
	}
	return Response{
		Text:  decoded.Choices[0].Message.Content,
		Model: client.model,
		Usage: Usage{
			PromptTokens:     decoded.Usage.PromptTokens,
			CompletionTokens: decoded.Usage.CompletionTokens,
			TotalTokens:      decoded.Usage.TotalTokens,
		},
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

type openAIChatCompletionRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatCompletionResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func buildOpenAIMessages(req Request) []openAIMessage {
	messages := []openAIMessage(nil)
	if strings.TrimSpace(req.System) != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: strings.TrimSpace(req.System)})
	}
	var b strings.Builder
	if strings.TrimSpace(req.Task) != "" {
		fmt.Fprintf(&b, "Task: %s\n\n", strings.TrimSpace(req.Task))
	}
	if strings.TrimSpace(req.OutputStyle) != "" {
		fmt.Fprintf(&b, "Output style:\n%s\n\n", strings.TrimSpace(req.OutputStyle))
	}
	if strings.TrimSpace(req.JSONSchema) != "" {
		fmt.Fprintf(&b, "JSON schema:\n%s\n\n", strings.TrimSpace(req.JSONSchema))
	}
	for _, input := range req.Inputs {
		fmt.Fprintf(&b, "Input file: %s\n", strings.TrimSpace(input.Path))
		b.WriteString("```text\n")
		b.WriteString(input.Content)
		if !strings.HasSuffix(input.Content, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("```\n\n")
	}
	messages = append(messages, openAIMessage{Role: "user", Content: strings.TrimSpace(b.String())})
	return messages
}
