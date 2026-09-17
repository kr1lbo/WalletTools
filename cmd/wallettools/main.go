package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"WalletTools/internal/cli"
	"WalletTools/internal/portable"
	"WalletTools/pkg/appcfg"
	"WalletTools/pkg/logx"
)

var version = "dev"

func main() {
	dataDir := flag.String("data-dir", "", "Directory for configs, inputs and logs (default: executable directory)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("WalletTools " + version)
		return
	}
	if *dataDir == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		*dataDir = filepath.Dir(exe)
	}
	if err := portable.Init(*dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "Initialize data directory: %v\nMove WalletTools to a writable folder or use --data-dir.\n", err)
		os.Exit(1)
	}
	if err := os.Chdir(*dataDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "getwd: %v\n", err)
		os.Exit(2)
	}

	appConf, err := appcfg.Load(filepath.Join(cwd, "configs", "app.yaml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "load app config: %v\n", err)
		os.Exit(1)
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
	r.LogLevel = appConf.LogLevel
	r.GPUEnabled = appConf.GPUEnabled
	r.CUDAExecutable = appConf.CUDAExecutable
	r.CUDADevice = appConf.CUDADevice
	r.CUDABatchSize = appConf.CUDABatchSize
	r.Run()
}
