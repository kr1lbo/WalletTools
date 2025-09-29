package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Config struct {
	Level                string // debug|info|warn|error
	FilePath             string // path template, e.g. "logs/{start}.log" or "" (no file)
	ConsoleOnly          bool   // if true, do not write to the file
	HideSecretsInConsole bool   // if true, we mask the private data in the console
}

var StartTime = time.Now()

var (
	global  *zap.Logger
	sugar   *zap.SugaredLogger
	fileOut *os.File
)

var moscowTZ = time.FixedZone("MSK", 3*60*60)

// Init initializes the global logger.
// Cfg.FilePath — the path to the file (may contain {start} and {pid}); if empty, or cfg.ConsoleOnly=true — the file is not in use.
// Cfg.HideSecretsInConsole controls the masking in the console.
func Init(cfg Config) error {
	level := parseLevel(cfg.Level)

	// encoder config base
	encCfg := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "lvl",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stack",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeTime:     timeEncoderRFC3339,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// console encoder (with color)
	consoleEncCfg := encCfg
	consoleEncCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	consoleEncoder := zapcore.NewConsoleEncoder(consoleEncCfg)

	// file encoder (no color)
	fileEncCfg := encCfg
	fileEncCfg.EncodeLevel = zapcore.CapitalLevelEncoder
	fileEncoder := zapcore.NewConsoleEncoder(fileEncCfg)

	var cores []zapcore.Core

	// console core: possibly wrapped to redact secrets
	consoleCore := zapcore.NewCore(consoleEncoder, zapcore.Lock(os.Stdout), level)
	if cfg.HideSecretsInConsole {
		consoleCore = &maskingCore{
			Core:         consoleCore,
			sensitive:    defaultSensitiveKeys(),
			maskPattern:  defaultMaskPattern(),
			replaceValue: "[REDACTED]",
		}
	}
	cores = append(cores, consoleCore)

	// file core: if requested and not console-only
	if cfg.FilePath != "" && !cfg.ConsoleOnly {
		resolved := resolvePath(cfg.FilePath)
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return fmt.Errorf("create logs dir: %w", err)
		}
		f, err := os.OpenFile(resolved, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		fileOut = f
		cores = append(cores, zapcore.NewCore(fileEncoder, zapcore.AddSync(f), level))
	}

	core := zapcore.NewTee(cores...)
	logger := zap.New(core,
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.PanicLevel),
	)
	zap.ReplaceGlobals(logger)

	global = logger
	sugar = logger.Sugar()
	return nil
}

// Close syncs and closes the file (if open).
func Close() {
	if global != nil {
		_ = global.Sync()
	}
	if fileOut != nil {
		_ = fileOut.Sync()
		_ = fileOut.Close()
		fileOut = nil
	}
}

func L() *zap.Logger        { return global }
func S() *zap.SugaredLogger { return sugar }

func With(name string) *zap.SugaredLogger     { return sugar.Named(name) }
func WithFields(kv ...any) *zap.SugaredLogger { return sugar.With(kv...) }

func resolvePath(tmpl string) string {
	startLocal := StartTime.In(moscowTZ).Format("2006-01-02_15-04-05")
	repl := map[string]string{
		"{start}": startLocal,
		"{pid}":   fmt.Sprintf("%d", os.Getpid()),
	}
	path := tmpl
	for k, v := range repl {
		path = strings.ReplaceAll(path, k, v)
	}
	return path
}

func parseLevel(lvl string) zapcore.LevelEnabler {
	switch strings.ToLower(strings.TrimSpace(lvl)) {
	case "debug":
		return zapcore.DebugLevel
	case "info", "":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error", "err":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

func timeEncoderRFC3339(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.In(moscowTZ).Format(time.RFC3339))
}
