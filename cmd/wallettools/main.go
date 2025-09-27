package main

import (
	"WalletTools/internal/cli"
	"WalletTools/pkg/appcfg"
	"WalletTools/pkg/config"
	"WalletTools/pkg/i18n"
	logx "WalletTools/pkg/log"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// map app LogLevel -> zap level
func parseZapLevel(lvl string) zapcore.Level {
	switch lvl {
	case "debug":
		return zapcore.DebugLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error", "err":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

func createModuleFileLoggers(baseDir, module string, level zapcore.Level) (fullLogger *zap.SugaredLogger, appLogger *zap.SugaredLogger, cleanup func(), err error) {
	now := time.Now()
	dateFolder := now.Format("02.01")
	timeFolder := now.Format("15-04-05")
	moduleDir := filepath.Join(baseDir, "logs", module, dateFolder, timeFolder)

	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		return nil, nil, nil, fmt.Errorf("create module log dir: %w", err)
	}

	fullPath := filepath.Join(moduleDir, module+"-full.log")
	appPath := filepath.Join(moduleDir, "app.logs")

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encCfg.EncodeLevel = zapcore.CapitalLevelEncoder
	encCfg.EncodeCaller = zapcore.ShortCallerEncoder
	fileEncoder := zapcore.NewConsoleEncoder(encCfg)

	fFull, err := os.OpenFile(fullPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open full log file: %w", err)
	}
	fApp, err := os.OpenFile(appPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		_ = fFull.Close()
		return nil, nil, nil, fmt.Errorf("open app log file: %w", err)
	}

	coreFull := zapcore.NewCore(fileEncoder, zapcore.AddSync(fFull), level)
	coreApp := zapcore.NewCore(fileEncoder, zapcore.AddSync(fApp), level)

	loggerFull := zap.New(coreFull, zap.AddCaller())
	loggerApp := zap.New(coreApp, zap.AddCaller())

	sugarFull := loggerFull.Sugar()
	sugarApp := loggerApp.Sugar()

	cleanup = func() {
		_ = loggerFull.Sync()
		_ = loggerApp.Sync()
		_ = fFull.Sync()
		_ = fFull.Close()
		_ = fApp.Sync()
		_ = fApp.Close()
	}

	return sugarFull, sugarApp, cleanup, nil
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "getwd: %v\n", err)
		os.Exit(2)
	}

	appConf, err := appcfg.Load(filepath.Join(cwd, "configs", "app.yaml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "load app config: %v (use defaults: ru/info)\n", err)
		appConf = &appcfg.Config{Language: "ru", LogLevel: "info"}
	}

	if err := logx.Init(logx.Config{
		Level:                appConf.LogLevel,
		FilePath:             "",
		ConsoleOnly:          true,
		HideSecretsInConsole: appConf.HideSecretsInConsole,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "log init: %v\n", err)
		os.Exit(1)
	}
	defer logx.Close()

	logx.S().Infow("wallettools started", "cwd", cwd, "lang", appConf.Language, "log_level",
		appConf.LogLevel, "hide_secrets_in_console", appConf.HideSecretsInConsole)

	pcfg, err := config.Load(filepath.Join(cwd, "configs", "patterns.yaml"))
	if err != nil {
		logx.S().Errorw("load patterns config", "err", err)
		os.Exit(1)
	}

	modLevel := parseZapLevel(appConf.LogLevel)
	fullLogger, appLogger, cleanup, err := createModuleFileLoggers(cwd, "mnemonic", modLevel)
	if err != nil {
		logx.S().Errorw("create module loggers failed", "module", "mnemonic", "err", err)
	} else {
		defer cleanup()
		logx.S().Infow("module logs created", "module", "mnemonic")
		if appLogger != nil {
			appLogger.Infow("mnemonic module started", "config_present", pcfg != nil)
		}
		if fullLogger != nil {
			fullLogger.Infow("mnemonic module full log created", "path", filepath.Join("logs", "mnemonic"))
		}
	}

	msgs := i18n.Get(appConf.Language)

	r := cli.New(pcfg, cwd, msgs, fullLogger, appLogger)
	if err := r.Run(); err != nil {
		logx.S().Errorw("runner finished with error", "err", err)
	}
}
