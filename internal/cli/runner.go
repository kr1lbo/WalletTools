package cli

import (
	"WalletTools/pkg/config"
	"WalletTools/pkg/i18n"
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// Runner encapsulates CLI states and dependencies.
type Runner struct {
	Config        *config.PatternsConfig
	in            *bufio.Reader
	baseDir       string
	fileLogger    *zap.SugaredLogger // detailed file logger for module (may be nil)
	appFileLogger *zap.SugaredLogger // app.logs file logger (may be nil)
	msgs          i18n.Messages
}

// New creates a Runner. baseDir is used to search for configs/patterns.yaml by default.
func New(cfg *config.PatternsConfig, baseDir string, msgs i18n.Messages, moduleFileLogger, moduleAppLogger *zap.SugaredLogger) *Runner {
	return &Runner{
		Config:        cfg,
		in:            bufio.NewReader(os.Stdin),
		baseDir:       baseDir,
		fileLogger:    moduleFileLogger,
		appFileLogger: moduleAppLogger,
		msgs:          msgs,
	}
}

func (r *Runner) prompt() string {
	fmt.Print("> ")
	s, _ := r.in.ReadString('\n')
	return strings.TrimSpace(s)
}

func (r *Runner) showMenu() {
	fmt.Println()
	fmt.Println(r.msgs.MenuTitle)
	fmt.Println(r.msgs.MenuGenPrivKeys)
	fmt.Println(r.msgs.MenuGenMnemonics)
	fmt.Println(r.msgs.MenuEncryptRaw)
	fmt.Println(r.msgs.MenuDecryptKeystore)
	fmt.Println(r.msgs.MenuShowPatterns)
	fmt.Println(r.msgs.MenuExit)
}

func (r *Runner) logInfo(msg string, kv ...interface{}) {
	zap.S().Infow(msg, kv...)
	if r.appFileLogger != nil {
		r.appFileLogger.Infow(msg, kv...)
	}
	if r.fileLogger != nil {
		r.fileLogger.Infow(msg, kv...)
	}
}

func (r *Runner) Run() error {
	if r.Config == nil {
		return fmt.Errorf("config is not loaded")
	}

	for {
		r.showMenu()
		choice := r.prompt()
		switch choice {
		case "0":
			r.logInfo(r.msgs.ExitSelected)
			fmt.Println(r.msgs.ExitText)
			return nil
		case "1":
			r.handleGenPrivKeys()
		case "2":
			r.handleGenMnemonics()
		case "3":
			r.handleEncryptRaw()
		case "4":
			r.handleDecryptKeystore()
		case "5":
			r.showConfig()
		default:
			fmt.Println(r.msgs.UnknownCommand, choice)
		}
	}
}

func (r *Runner) handleGenPrivKeys() {
	fmt.Println(r.msgs.GenPrivPrompt)
	yn := strings.ToLower(r.prompt())
	encrypt := yn == "y" || yn == "yes"
	r.logInfo(r.msgs.GenPrivStarted, "encrypt_keystore", encrypt, "case_sensitive", r.getCase())
	fmt.Printf(r.msgs.GenPrivStub, encrypt)
}

func (r *Runner) handleGenMnemonics() {
	fmt.Println(r.msgs.GenMnemPrompt)
	yn := strings.ToLower(r.prompt())
	usePP := yn == "y" || yn == "yes"
	r.logInfo(r.msgs.GenMnemStarted, "use_passphrase", usePP, "case_sensitive", r.getCase())
	fmt.Printf(r.msgs.GenMnemStub, usePP)
}

func (r *Runner) handleEncryptRaw() {
	fmt.Println(r.msgs.EncryptPrompt)
	p := r.prompt()
	if p == "" {
		fmt.Println(r.msgs.EncryptStdin)
		return
	}
	path := filepath.Clean(p)
	r.logInfo("encryptRaw requested", "path", path)
	fmt.Printf(r.msgs.EncryptPlanned, path)
}

func (r *Runner) handleDecryptKeystore() {
	fmt.Println(r.msgs.DecryptPrompt)
	p := r.prompt()
	if p == "" {
		fmt.Println("Path is empty.")
		return
	}
	path := filepath.Clean(p)
	r.logInfo("decryptKeystore requested", "path", path)
	fmt.Printf(r.msgs.DecryptPlanned, path)
}

func (r *Runner) showConfig() {
	if r.Config == nil {
		fmt.Println(r.msgs.ConfigNotLoaded)
		return
	}
	fmt.Println(r.msgs.ConfigHeader)
	fmt.Printf(r.msgs.ConfigSymbols, r.Config.Symbols)
	fmt.Printf(r.msgs.ConfigCaseSensitive, r.Config.CaseSensitive)

	if len(r.Config.Symmetric) > 0 {
		fmt.Println(r.msgs.ConfigSymmetric)
		for i, s := range r.Config.Symmetric {
			fmt.Printf("  %d) prefix=%s suffix=%s final=%v\n", i+1, s.Prefix, s.Suffix, s.Final)
		}
	}
	if len(r.Config.Specific) > 0 {
		fmt.Println(r.msgs.ConfigSpecific)
		for i, s := range r.Config.Specific {
			fmt.Printf("  %d) prefix=%s suffix=%s final=%v\n", i+1, s.Prefix, s.Suffix, s.Final)
		}
	}
	fmt.Printf(r.msgs.ConfigEdges, r.Config.Edges.MinCount, r.Config.Edges.Side, r.Config.Edges.Final)

	if len(r.Config.Regexp) > 0 {
		fmt.Println(r.msgs.ConfigRegexp)
		for i, rr := range r.Config.Regexp {
			fmt.Printf("  %d) pattern=%s final=%v\n", i+1, rr.Pattern, rr.Final)
		}
	}
}

func (r *Runner) getCase() string {
	if r.Config == nil {
		return "n/a"
	}
	if r.Config.CaseSensitive {
		return "sensitive"
	}
	return "insensitive"
}
