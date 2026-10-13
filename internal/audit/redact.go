package audit

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Redaction is a safety net, not the primary control: callers record
// names and operation ids, never values. Record additionally scrubs the
// patterns below from Detail, OverrideReason, and Plan so a caller that
// echoes an upstream error cannot persist a credential. It is
// best-effort: it recognises known sensitive key names and well-known
// token shapes, not arbitrary secrets.
const redacted = "[redacted]"

var sensitiveKeys = []string{
	"password", "passwd", "passphrase", "secret", "token", "apikey",
	"api_key", "api-key", "private_key", "privatekey", "credential",
	"authorization", "cookie", "csrf",
}

var (
	// key=value pairs in free text.
	assignmentRE = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:password|passwd|passphrase|secret|token|api[_-]?key|private[_-]?key|credential|authorization|cookie)[a-z0-9_.-]*)=("[^"]*"|'[^']*'|\S+)`)
	bearerRE     = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	// age identities, and JWT-like tokens (Cloudflare tunnel tokens are
	// base64 JSON starting eyJ).
	ageRE = regexp.MustCompile(`AGE-SECRET-KEY-[A-Z0-9]+`)
	jwtRE = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{16,}(?:\.[A-Za-z0-9_-]+)*`)
)

// RedactText scrubs known credential shapes from free text.
func RedactText(s string) string {
	s = assignmentRE.ReplaceAllString(s, "${1}="+redacted)
	s = bearerRE.ReplaceAllString(s, "${1} "+redacted)
	s = ageRE.ReplaceAllString(s, redacted)
	s = jwtRE.ReplaceAllString(s, redacted)
	return s
}

func sensitiveKey(k string) bool {
	k = strings.ToLower(k)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// RedactPlan replaces the values of sensitive-named keys anywhere in a
// JSON plan and scrubs string values as text. Invalid JSON is returned
// scrubbed as text.
func RedactPlan(plan string) string {
	if plan == "" {
		return plan
	}
	var v any
	if err := json.Unmarshal([]byte(plan), &v); err != nil {
		return RedactText(plan)
	}
	before, err := json.Marshal(v)
	if err != nil {
		return RedactText(plan)
	}
	out, err := json.Marshal(redactValue(v))
	if err != nil {
		return RedactText(plan)
	}
	if string(out) == string(before) {
		// Nothing scrubbed: keep the caller's bytes untouched.
		return plan
	}
	return string(out)
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if sensitiveKey(k) && val != nil {
				t[k] = redacted
				continue
			}
			t[k] = redactValue(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = redactValue(val)
		}
		return t
	case string:
		return RedactText(t)
	default:
		return v
	}
}
