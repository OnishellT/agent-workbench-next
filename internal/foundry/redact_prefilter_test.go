// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"math/rand"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// redactSpansUnfiltered is the reference: every pattern runs on every string.
func redactSpansUnfiltered(text string) []redactionSpan {
	spans := []redactionSpan{}
	add := func(pattern *regexp.Regexp, group int, kind string) {
		for _, loc := range pattern.FindAllStringSubmatchIndex(text, -1) {
			idx := 2 * group
			if idx+1 >= len(loc) || loc[idx] < 0 || loc[idx+1] <= loc[idx] {
				continue
			}
			start, end := loc[idx], loc[idx+1]
			if placeholderPattern.MatchString(text[start:end]) {
				continue
			}
			if kind == "opaque_token" {
				token := text[start:end]
				if regexp.MustCompile(`^[0-9a-fA-F]{40,}$`).MatchString(token) || !strings.ContainsAny(token, "abcdefghijklmnopqrstuvwxyz") || !strings.ContainsAny(token, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") || !strings.ContainsAny(token, "0123456789") {
					continue
				}
			}
			spans = append(spans, redactionSpan{start, end, kind})
		}
	}
	add(pemPattern, 0, "pem")
	add(credentialPattern, 2, "credential")
	add(jwtPattern, 2, "jwt")
	add(authorizationPattern, 1, "authorization")
	add(urlCredentialPattern, 1, "url_credentials")
	add(assignmentPattern, 2, "assignment")
	add(opaquePattern, 0, "opaque_token")
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	return spans
}

func filteredSpans(text string) []redactionSpan {
	_, redactions, _ := redactString(text, false)
	out := make([]redactionSpan, 0, len(redactions))
	for _, r := range redactions {
		out = append(out, redactionSpan{r["start"].(int), r["end"].(int), r["kind"].(string)})
	}
	return out
}

// mergedSpans mirrors redactString's merge so the two can be compared.
func mergedSpans(spans []redactionSpan) []redactionSpan {
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	merged := []redactionSpan{}
	for _, span := range spans {
		if len(merged) > 0 && span.start < merged[len(merged)-1].end {
			if span.end > merged[len(merged)-1].end {
				merged[len(merged)-1].end = span.end
			}
			continue
		}
		merged = append(merged, span)
	}
	return merged
}

func TestRedactionPrefilterMatchesUnfilteredPatterns(t *testing.T) {
	pieces := []string{"password", "PASSWORD", "Token", "api_key", "API KEY", "=", ":", " is ", "\"", "'", "secret", "ſecret", "K", "Bearer ", "basic ", "BEARER", "eyJhbGciOi.abcdef.ghijkl", "sk-abcdefgh12345", "ghp_abcdefgh1234", "github_pat_abcdefgh12", "xoxb-abcdefgh1234", "AKIAABCDEFGHIJKLMNOP", "AIza" + strings.Repeat("a1", 16), "https://user:pw@host/", "://", "-----BEGIN PRIVATE KEY-----", "-----END PRIVATE KEY-----", strings.Repeat("aB3", 15), strings.Repeat("0123456789abcdef", 3), "[REDACTED:pem]", "hello world", " ", "\n", "x", "é", "日本", "private key", "connection string", "pwd", "Authorization", "credentials"}
	rng := rand.New(rand.NewSource(7))
	matched := 0
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := rng.Intn(8); n >= 0; n-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		text := b.String()
		if !utf8.ValidString(text) {
			continue
		}
		want := mergedSpans(redactSpansUnfiltered(text))
		got := filteredSpans(text)
		if len(want) == 0 && len(got) == 0 {
			continue
		}
		matched++
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("spans differ for %q\nwant %v\n got %v", text, want, got)
		}
	}
	if matched < 5000 {
		t.Fatalf("corpus too weak: only %d strings produced spans", matched)
	}
}

func TestTruncateRunesMatchesRuneSlice(t *testing.T) {
	for _, value := range []string{"", "abc", "añb", "日本語テキスト", "bad\xffbytes\xfe", strings.Repeat("é", 50)} {
		for limit := 0; limit < 60; limit++ {
			want := value
			if runes := []rune(value); len(runes) > limit {
				want = string(runes[:limit])
			}
			if got := truncateRunes(value, limit); got != want {
				t.Fatalf("truncateRunes(%q,%d)=%q want %q", value, limit, got, want)
			}
		}
	}
}

func secretKeyUnfiltered(key string) bool {
	value := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	if secretKeyPattern.MatchString(value) {
		return true
	}
	for _, suffix := range []string{"_api_key", "_access_token", "_refresh_token", "_auth_token", "_client_secret", "_private_key", "_secret_access_key", "_connection_string", "_password", "_passwd", "_credentials", "_authorization"} {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}

func TestSecretKeyPrefilterMatchesUnfiltered(t *testing.T) {
	pieces := []string{"api", "key", "KEY", "_", "-", " ", "token", "access", "refresh", "auth", "client", "secret", "password", "passwd", "pwd", "credential", "credentials", "authorization", "private", "connection", "string", "type", "uuid", "id", "x", "ſ", "K", "é"}
	rng := rand.New(rand.NewSource(11))
	hits := 0
	for i := 0; i < 30000; i++ {
		var b strings.Builder
		for n := rng.Intn(5); n >= 0; n-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		key := b.String()
		want := secretKeyUnfiltered(key)
		if got := secretKey(key); got != want {
			t.Fatalf("secretKey(%q)=%v want %v", key, got, want)
		}
		if want {
			hits++
		}
	}
	if hits < 1000 {
		t.Fatalf("corpus too weak: %d secret keys", hits)
	}
}
