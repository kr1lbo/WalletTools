package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"WalletTools/internal/cli"
	"WalletTools/pkg/appcfg"
	"WalletTools/pkg/logx"
)

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
	workers := appConf.Cores
	maxCPU := runtime.NumCPU()
	logx.S().Info("MaxCpuNum: ", maxCPU)
	if workers <= 0 {
		workers = maxCPU
	} else if workers > maxCPU {
		workers = maxCPU
	}

	logx.S().Infow("wallettools started",
		"cwd", cwd,
		"lang", appConf.Language,
		"log_level", appConf.LogLevel,
		"hide_secrets_in_console", appConf.HideSecretsInConsole,
	)

	r := cli.NewRunner()
	r.HideSecretsInConsole = appConf.HideSecretsInConsole
	r.Workers = workers
	r.Run()
}
