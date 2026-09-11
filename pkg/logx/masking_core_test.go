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

func TestMaskingCoreRedactsNormalLoggerCalls(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	masked := &maskingCore{Core: core, sensitive: defaultSensitiveKeys(), maskPattern: defaultMaskPattern(), replaceValue: "[REDACTED]"}
	secret := "0x" + strings.Repeat("a", 64)
	logger := zap.New(masked)
	logger.Info("private="+secret, zap.String("private_key", secret))
	logger.Sugar().Infow("found", "mnemonic", "test mnemonic", "password", "test password")
	entries := observed.All()
	if len(entries) != 2 {
		t.Fatalf("got %d entries", len(entries))
	}
	if strings.Contains(entries[0].Message, secret) || entries[0].ContextMap()["private_key"] != "[REDACTED]" {
		t.Fatal("normal logger call bypassed masking")
	}
	if entries[1].ContextMap()["mnemonic"] != "[REDACTED]" || entries[1].ContextMap()["password"] != "[REDACTED]" {
		t.Fatal("sugared logger call bypassed masking")
	}
}
