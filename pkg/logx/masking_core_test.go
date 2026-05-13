package logx

import (
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestDefaultMaskPatternRedactsFullPrefixedPrivateKey(t *testing.T) {
	secret := "0x" + strings.Repeat("a", 64)

	got := defaultMaskPattern().ReplaceAllString("private="+secret, "[REDACTED]")
	if strings.Contains(got, secret) || strings.Contains(got, secret[2:]) {
		t.Fatalf("private key leaked after masking: %q", got)
	}
}

func TestMaskingCoreWithKeepsFieldRedaction(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	masked := &maskingCore{
		Core:         core,
		sensitive:    defaultSensitiveKeys(),
		maskPattern:  defaultMaskPattern(),
		replaceValue: "[REDACTED]",
	}

	zap.New(masked).With(zap.String("private_key", "secret")).Info("test")

	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(entries))
	}
	if got := entries[0].ContextMap()["private_key"]; got != "[REDACTED]" {
		t.Fatalf("expected private_key to be redacted, got %#v", got)
	}
}
