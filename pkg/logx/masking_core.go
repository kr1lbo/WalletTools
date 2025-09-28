package logx

import (
	"regexp"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// maskingCore wraps a core and redacts sensitive structured fields and masks patterns in Entry.Message.
// It is intended to be used for console output only.
type maskingCore struct {
	zapcore.Core
	sensitive    map[string]struct{} // lowercased keys to redact
	maskPattern  *regexp.Regexp      // pattern to mask in messages (like 64-hex)
	replaceValue string
}

func (m *maskingCore) cloneFieldsWithRedaction(fields []zapcore.Field) []zapcore.Field {
	if len(fields) == 0 {
		return fields
	}
	out := make([]zapcore.Field, 0, len(fields))
	for _, f := range fields {
		key := strings.ToLower(f.Key)
		if _, ok := m.sensitive[key]; ok {
			out = append(out, zap.String(f.Key, m.replaceValue))
			continue
		}
		out = append(out, f)
	}
	return out
}

func (m *maskingCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// mask message text
	if m.maskPattern != nil && entry.Message != "" {
		entry.Message = m.maskPattern.ReplaceAllString(entry.Message, m.replaceValue)
	}
	// redact fields
	fields = m.cloneFieldsWithRedaction(fields)
	return m.Core.Write(entry, fields)
}

func defaultSensitiveKeys() map[string]struct{} {
	keys := []string{
		"private", "private_key", "privatekey",
		"priv", "secret", "mnemonic", "seed", "passphrase",
		"raw", "raw_key", "raw_private", "key",
	}
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[strings.ToLower(k)] = struct{}{}
	}
	return m
}

func defaultMaskPattern() *regexp.Regexp {
	// match 64 hex (likely raw private key) or 0x followed by 40 hex (address)
	pattern := `(?i)(0x[a-f0-9]{40}|[a-f0-9]{64})`
	return regexp.MustCompile(pattern)
}
