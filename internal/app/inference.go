package app

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

func isInferenceNamespace(pathname string) bool {
	return hasPrefixPath(pathname, "/v1") || hasPrefixPath(pathname, "/v1beta") || hasPrefixPath(pathname, "/backend-api/codex")
}

func validInferenceRequest(r *http.Request, sessionToken string) bool {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.Header.Values("Upgrade")) != 0 {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		switch r.URL.Path {
		case "/v1/models", "/v1beta/models":
			if !onlyQueryValues(query, "alt", "sse") {
				return false
			}
		case "/v1/codex/models", "/backend-api/codex/models":
			if !validClientVersionQuery(query) {
				return false
			}
		default:
			return false
		}
	case http.MethodPost:
		switch r.URL.Path {
		case "/v1/responses", "/v1/chat/completions", "/v1/messages", "/v1/messages/count_tokens",
			"/backend-api/codex/responses", "/backend-api/codex/responses/compact":
			if !onlyQueryValues(query, "alt", "sse") {
				return false
			}
		default:
			if !validGeminiInferencePath(r.URL.Path) || !onlyQueryValues(query, "alt", "sse") {
				return false
			}
		}
	default:
		return false
	}
	authorizations := r.Header.Values("Authorization")
	if len(authorizations) != 1 || !strings.HasPrefix(authorizations[0], "Bearer ") {
		return false
	}
	token := authorizations[0][len("Bearer "):]
	if len(token) < 8 || len(token) > 4096 || subtle.ConstantTimeCompare([]byte(token), []byte(sessionToken)) == 1 {
		return false
	}
	for _, char := range token {
		if char <= 0x20 || char >= 0x7f {
			return false
		}
	}
	return true
}

func onlyQueryValues(values url.Values, key, expected string) bool {
	for name, entries := range values {
		if name != key {
			return false
		}
		for _, value := range entries {
			if value != expected {
				return false
			}
		}
	}
	return true
}

func validClientVersionQuery(values url.Values) bool {
	for name, entries := range values {
		if name != "client_version" {
			return false
		}
		for _, value := range entries {
			if len(value) == 0 || len(value) > 100 {
				return false
			}
			for _, char := range value {
				if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._+-", char)) {
					return false
				}
			}
		}
	}
	return true
}

func validGeminiInferencePath(pathname string) bool {
	const prefix = "/v1beta/models/"
	tail := strings.TrimPrefix(pathname, prefix)
	if tail == pathname {
		return false
	}
	for _, suffix := range []string{":generateContent", ":streamGenerateContent"} {
		if !strings.HasSuffix(tail, suffix) {
			continue
		}
		model := strings.TrimSuffix(tail, suffix)
		if len(model) == 0 || len(model) > 500 {
			return false
		}
		for _, char := range model {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("_.:/-", char)) {
				return false
			}
		}
		for _, segment := range strings.Split(model, "/") {
			if segment == "." || segment == ".." {
				return false
			}
		}
		return true
	}
	return false
}
