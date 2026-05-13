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

func (m *maskingCore) With(fields []zapcore.Field) zapcore.Core {
	return &maskingCore{
		Core:         m.Core.With(m.cloneFieldsWithRedaction(fields)),
		sensitive:    m.sensitive,
		maskPattern:  m.maskPattern,
		replaceValue: m.replaceValue,
	}
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
		"password", "pwd", "pass", "keystore_password",
		"raw", "raw_key", "raw_private", "key",
	}
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[strings.ToLower(k)] = struct{}{}
	}
	return m
}

func defaultMaskPattern() *regexp.Regexp {
	// Match raw EVM private keys. The 0x-prefixed form must be checked before
	// shorter hex-like values so message masking never leaves the tail visible.
	pattern := `(?i)(0x[a-f0-9]{64}|\b[a-f0-9]{64}\b)`
	return regexp.MustCompile(pattern)
}
