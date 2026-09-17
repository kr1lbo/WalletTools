package cli

import (
	"WalletTools/internal/generator"
	"WalletTools/internal/ops/encdec"
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"
)

type Runner struct {
	in                   *bufio.Reader
	HideSecretsInConsole bool
	Workers              int
	LogLevel             string
	GPUEnabled           bool
	CUDAExecutable       string
	CUDADevice           int
	CUDABatchSize        int
}

func NewRunner() *Runner {
	return &Runner{in: bufio.NewReader(os.Stdin)}
}

// prompt reads a string from stdin, truncating spaces and newlines.
func (r *Runner) prompt() string {
	text, _ := r.in.ReadString('\n')
	return strings.TrimSpace(text)
}

// readPassword — hidden input of a single value (password/passphrase).
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	defer wipeBytes(pw)
	return string(pw), nil
}

// readPasswordWithConfirmOrSkip — hidden confirmation input.
func readPasswordWithConfirmOrSkip(prompt, confirmPrompt string) (pwd string, set bool, err error) {
	for {
		p, err := readPassword(prompt)
		if err != nil {
			return "", false, err
		}
		if p == "" {
			return "", false, nil
		}
		c, err := readPassword(confirmPrompt)
		if err != nil {
			return "", false, err
		}
		if p != c {
			fmt.Fprintln(os.Stderr, "Passwords do not match. Try again.")
			continue
		}
		return p, true, nil
	}
}

func readNonEmptyPasswordLoop(prompt string) (string, error) {
	for {
		p, err := readPassword(prompt)
		if err != nil {
			return "", err
		}
		if p == "" {
			fmt.Fprintln(os.Stderr, "Password cannot be empty. Try again.")
			continue
		}
		return p, nil
	}
}

func wipeBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
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

		switch strings.ToLower(r.prompt()) {
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

// handleGenPriv — private key generation.
func (r *Runner) handleGenPriv() {
	fmt.Print("Encrypt to keystore? (y/n): ")
	yn := strings.ToLower(r.prompt())
	encrypt := yn == "y" || yn == "yes"

	var pwd string
	var hint string
	if encrypt {
		p, set, err := readPasswordWithConfirmOrSkip(
			"Keystore password (Enter to skip): ",
			"Repeat password: ",
		)
		if err != nil {
			fmt.Println("Error:", err)
			return
		}
		if !set {
			encrypt = false
		} else {
			pwd = p
			fmt.Print("Optional password hint (saved to hint.txt): ")
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
		LogLevel:         r.LogLevel,
		GPUEnabled:       r.GPUEnabled,
		CUDAExecutable:   r.CUDAExecutable,
		CUDADevice:       r.CUDADevice,
		CUDABatchSize:    r.CUDABatchSize,
	}
	ctx, stop := withInterrupt(context.Background())
	defer stop()
	if err := generator.Run(ctx, opt); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Println("Generation stopped.")
		} else {
			fmt.Println("Generation error:", err)
		}
	} else {
		fmt.Println("Generation done.")
	}
}

// handleGenMnemonic — mnemonic generation.
func (r *Runner) handleGenMnemonic() {
	fmt.Print("Use BIP-39 passphrase? (y/n): ")
	yn := strings.ToLower(r.prompt())
	usePP := yn == "y" || yn == "yes"

	var hint string
	var passStr string

	if usePP {
		p, set, err := readPasswordWithConfirmOrSkip(
			"Enter BIP-39 passphrase (Enter to skip): ",
			"Repeat passphrase: ",
		)
		if err != nil {
			fmt.Println("Error:", err)
			return
		}
		if set {
			tmp := []byte(p)
			passStr = string(tmp)
			wipeBytes(tmp)

			fmt.Print("Optional passphrase hint (saved to folder): ")
			hint = r.prompt()
		} else {
			usePP = false
		}
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
		Passphrase:    passStr,
		LogsBase:      "logs",
		PassHint:      hint,
		PatternsPath:  "configs/patterns.yaml",
		CaseMaskedOut: r.HideSecretsInConsole,
		Workers:       r.Workers,
		LogLevel:      r.LogLevel,
	}

	ctx, stop := withInterrupt(context.Background())
	defer stop()

	if err := generator.Run(ctx, opt); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Println("Generation stopped.")
		} else {
			fmt.Println("Generation error:", err)
		}
	} else {
		fmt.Println("Generation done.")
	}

	passStr = ""
}

// handleEncrypt — manual encryption of private keys in the keystore.
func (r *Runner) handleEncrypt() {
	p, set, err := readPasswordWithConfirmOrSkip(
		"Keystore password (Enter to skip): ",
		"Repeat password: ",
	)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	if !set {
		fmt.Println("Password skipped — encryption canceled.")
		return
	}

	fmt.Print("Optional hint: ")
	hint := strings.TrimSpace(r.prompt())

	ctx, stop := withInterrupt(context.Background())
	defer stop()

	if err := encdec.EncryptPrivates(
		ctx,
		encdec.EncryptOptions{
			InputsBaseDir:        "inputs",
			LogsBase:             "logs",
			Password:             p,
			PassHint:             hint,
			HideSecretsInConsole: r.HideSecretsInConsole,
			LogLevel:             r.LogLevel,
		},
	); err != nil {
		fmt.Println("Encryption error:", err)
	}
}

// handleDecrypt — decryption keystore → raw. An empty password is prohibited.
func (r *Runner) handleDecrypt() {
	pwd, err := readNonEmptyPasswordLoop("Keystore password for decryption: ")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	ctx, stop := withInterrupt(context.Background())
	defer stop()

	if err := encdec.DecryptKeystores(
		ctx,
		encdec.DecryptOptions{
			InputsBaseDir:        "inputs",
			LogsBase:             "logs",
			Password:             pwd,
			HideSecretsInConsole: r.HideSecretsInConsole,
			LogLevel:             r.LogLevel,
		},
	); err != nil {
		fmt.Println("Decryption error:", err)
	}
}

func atoiSafe(s string) int {
	var n int
	_, _ = fmt.Sscan(s, &n)
	return n
}

func withInterrupt(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
}
