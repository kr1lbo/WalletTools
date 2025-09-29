package cli

import (
	"WalletTools/internal/ops/encdec"
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"WalletTools/internal/generator"
	"WalletTools/pkg/logx"
)

type Runner struct {
	in                   *bufio.Reader
	HideSecretsInConsole bool
	Workers              int
}

func NewRunner() *Runner {
	return &Runner{in: bufio.NewReader(os.Stdin)}
}

func (r *Runner) prompt() string {
	text, _ := r.in.ReadString('\n')
	return strings.TrimSpace(text)
}

func (r *Runner) Run() {
	for {
		fmt.Println()
		fmt.Println("WalletTools — Vanity generator")
		fmt.Println("1) Generate by Private Keys")
		fmt.Println("2) Generate by Mnemonic")
		fmt.Println("3) Encrypt raw → keystore")
		fmt.Println("4) Decrypt keystore → raw")
		fmt.Println("Press enter to exit")
		fmt.Print("> ")
		choice := strings.ToLower(r.prompt())
		switch choice {
		case "1":
			r.handleGenPriv()
		case "2":
			r.handleGenMnemonic()
		case "3":
			r.handleEncrypt()
		case "4":
			r.handleDecrypt()
		case "":
			return
		default:
			fmt.Println("Unknown choice")
		}
	}
}

func (r *Runner) handleGenPriv() {
	fmt.Println("Encrypt to keystore? (y/n)")
	yn := strings.ToLower(r.prompt())
	encrypt := yn == "y" || yn == "yes"

	var pwd string
	var hint string
	if encrypt {
		fmt.Print("Keystore password: ")
		pwd = r.prompt()
		if pwd == "" {
			fmt.Println("Empty password, encryption disabled.")
			encrypt = false
		} else {
			fmt.Print("Optional password hint (will be saved to hint.txt, e.g. DSf...): ")
			hint = r.prompt()
		}
	}

	opt := generator.Options{
		Source:           generator.SourcePrivKey,
		Encrypt:          encrypt,
		KeystorePassword: pwd,
		LogsBase:         "logs",
		PassHint:         hint,
		PatternsPath:     "configs/patterns.yaml",
		CaseMaskedOut:    r.HideSecretsInConsole,
		Workers:          r.Workers,
	}
	ctx := withInterrupt(context.Background())
	logx.S().Infow("start generation", "mode", "private", "encrypt", encrypt)
	if err := generator.Run(ctx, opt); err != nil {
		logx.S().Errorw("generation error", "err", err)
	} else {
		logx.S().Infow("generation done")
	}
}

func (r *Runner) handleGenMnemonic() {
	fmt.Print("Use BIP-39 passphrase? (y/n): ")
	yn := strings.ToLower(r.prompt())
	usePP := yn == "y" || yn == "yes"

	var pass string
	var hint string
	if usePP {
		fmt.Print("Enter BIP-39 passphrase: ")
		pass = r.prompt()
		fmt.Print("Optional passphrase hint (saved to folder): ")
		hint = r.prompt()
	}

	fmt.Print("Derive N addresses (default 5): ")
	deriveStr := strings.TrimSpace(r.prompt())
	deriveN := 5
	if deriveStr != "" {
		if n := atoiSafe(deriveStr); n > 0 {
			deriveN = n
		}
	}

	opt := generator.Options{
		Source:        generator.SourceMnemonic,
		WordsStrength: 128,
		DeriveN:       deriveN,
		Passphrase:    pass,
		LogsBase:      "logs",
		PassHint:      hint,
		PatternsPath:  "configs/patterns.yaml",
		CaseMaskedOut: r.HideSecretsInConsole,
		Workers:       r.Workers,
	}
	ctx := withInterrupt(context.Background())
	logx.S().Infow("start generation", "mode", "mnemonic", "derive_n", deriveN, "use_passphrase", usePP)
	if err := generator.Run(ctx, opt); err != nil {
		logx.S().Errorw("generation error", "err", err)
	} else {
		logx.S().Infow("generation done")
	}
}

func (r *Runner) handleEncrypt() {
	fmt.Print("Keystore password: ")
	pwd := strings.TrimSpace(r.prompt())
	fmt.Print("Optional hint: ")
	hint := strings.TrimSpace(r.prompt())
	_ = encdec.EncryptPrivates(withInterrupt(context.Background()), encdec.EncryptOptions{
		InputsBaseDir: "inputs", LogsBase: "logs",
		Password: pwd, PassHint: hint,
		HideSecretsInConsole: r.HideSecretsInConsole,
	})
}

func (r *Runner) handleDecrypt() {
	fmt.Print("Keystore password: ")
	pwd := strings.TrimSpace(r.prompt())
	_ = encdec.DecryptKeystores(withInterrupt(context.Background()), encdec.DecryptOptions{
		InputsBaseDir: "inputs", LogsBase: "logs",
		Password: pwd, HideSecretsInConsole: r.HideSecretsInConsole,
	})
}

func atoiSafe(s string) int {
	var n int
	_, _ = fmt.Sscan(s, &n)
	return n
}

func withInterrupt(parent context.Context) context.Context {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
	}()
	return ctx
}
