package redact

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

const marker = "<redacted>"

var sensitiveKeyValuePattern = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:PASSWORD|PASSWD|PWD|TOKEN|SECRET|PRIVATE[_-]?KEY|ACCESS[_-]?KEY|CLIENT[_-]?SECRET|SAS|SIGNATURE|SIG)[A-Z0-9_-]*)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;&|]+)`)

// Text redacts credentials and tokens from executor output before it is written
// to operator-visible logs, evidence files, or PR summaries.
func Text(s string) string {
	if s == "" {
		return ""
	}
	redacted := redactURLs(s)
	redacted = sensitiveKeyValuePattern.ReplaceAllString(redacted, `${1}${2}`+marker)
	return redacted
}

// Args returns a copy of args with sensitive values redacted while leaving the
// original slice untouched for command execution and audit metric parsing.
func Args(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	redacted := make([]string, len(args))
	for i := range args {
		redacted[i] = Text(args[i])
	}
	for i := 0; i < len(redacted); i++ {
		arg := redacted[i]
		if strings.HasPrefix(arg, "--") {
			name, value, hasValue := strings.Cut(arg, "=")
			if isSensitiveFlag(name) {
				if hasValue {
					redacted[i] = name + "=" + marker
				} else if i+1 < len(redacted) {
					redacted[i+1] = marker
					i++
				}
			} else if hasValue {
				redacted[i] = name + "=" + Text(value)
			}
			continue
		}
		if strings.HasPrefix(arg, "-") && isSensitiveFlag(arg) && i+1 < len(redacted) {
			redacted[i+1] = marker
			i++
		}
	}
	return redacted
}

func redactURLs(s string) string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '"' || r == '\'' || r == '`' || r == '<' || r == '>' || r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}'
	})
	redacted := s
	for _, field := range fields {
		candidate := strings.Trim(field, ",.;")
		if candidate == "" || !strings.Contains(candidate, "://") {
			continue
		}
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		changed := false
		if parsed.User != nil {
			username := parsed.User.Username()
			if _, hasPassword := parsed.User.Password(); hasPassword {
				parsed.User = url.UserPassword(username, marker)
			} else {
				parsed.User = url.User(marker)
			}
			changed = true
		}
		query := parsed.Query()
		for key, values := range query {
			if !isSensitiveKey(key) {
				continue
			}
			for i := range values {
				values[i] = marker
			}
			query[key] = values
			changed = true
		}
		if !changed {
			continue
		}
		parsed.RawQuery = query.Encode()
		redactedURL := strings.ReplaceAll(parsed.String(), "%3Credacted%3E", marker)
		redactedURL = strings.ReplaceAll(redactedURL, "%3credacted%3e", marker)
		redacted = strings.ReplaceAll(redacted, candidate, redactedURL)
	}
	return redacted
}

func isSensitiveFlag(flag string) bool {
	flag = strings.TrimLeft(strings.ToLower(strings.TrimSpace(flag)), "-")
	return isSensitiveKey(flag)
}

func isSensitiveKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "_", "-")
	switch key {
	case "password", "passwd", "pwd", "token", "access-token", "refresh-token", "secret", "client-secret", "sig", "signature", "sas", "private-key", "access-key", "secret-access-key":
		return true
	}
	return strings.Contains(key, "password") ||
		strings.Contains(key, "passwd") ||
		strings.Contains(key, "token") ||
		strings.Contains(key, "secret") ||
		strings.Contains(key, "private-key") ||
		strings.Contains(key, "access-key") ||
		strings.HasSuffix(key, "-sig") ||
		strings.HasSuffix(key, "-signature")
}
