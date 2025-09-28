package generator

import (
	"WalletTools/internal/keystore"
	"WalletTools/internal/logsink"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"WalletTools/internal/crypto"
	"WalletTools/internal/mnemonic"
	"WalletTools/internal/patterns"
	"WalletTools/pkg/config"
	"WalletTools/pkg/logx"
)

type logPriv struct {
	Address    string `json:"address"`
	PrivateKey string `json:"private_key,omitempty"`
	Keystore   string `json:"keystore,omitempty"`
	Note       string `json:"note,omitempty"`
}

func Run(ctx context.Context, opt Options) error {
	cfg, err := config.Load(opt.PatternsPath)
	if err != nil {
		return fmt.Errorf("load patterns: %w", err)
	}

	module := string(opt.Source)
	keystoreUsage := opt.Source == SourcePrivKey && opt.Encrypt

	// logs/<module>/<DD.MM.YYYY>/<module_<HH-MM-SS>>
	dir, err := logsink.MakeModuleDirs(opt.LogsBase, module, keystoreUsage)
	if err != nil {
		return err
	}
	_ = logsink.WriteHint(dir, opt.PassHint)

	// app.log + консоль через logx
	logPath := filepath.Join(dir, "app.log")
	if err := logx.Init(logx.Config{
		Level:                "info",
		FilePath:             logPath,
		ConsoleOnly:          false,
		HideSecretsInConsole: opt.CaseMaskedOut,
	}); err != nil {
		return fmt.Errorf("logx init for module failed: %w", err)
	}

	app := logx.S()
	app.Infow("generation started",
		"module", module,
		"keystoreUsage", keystoreUsage,
		"patterns", opt.PatternsPath,
	)

	start := time.Now()
	showSecrets := !opt.CaseMaskedOut

	switch opt.Source {
	case SourcePrivKey:
		return runPriv(ctx, dir, cfg, keystoreUsage, opt.KeystorePassword, start, showSecrets)
	case SourceMnemonic:
		return runMnemonic(ctx, dir, cfg, opt.WordsStrength, opt.Passphrase, opt.DeriveN, start, showSecrets)
	default:
		return fmt.Errorf("unknown source: %s", opt.Source)
	}
}

// =============================== PRIVATE KEYS ===============================

func runPriv(ctx context.Context, dir string, cfg *config.PatternsConfig, encrypt bool, ksPwd string, start time.Time, showSecrets bool) error {
	log := logx.With("generator").With("mode", "priv")
	app := logx.S()

	var attempts uint64
	lastTick := time.Now()
	const tickEvery = 4 * time.Second

	progress := func(now time.Time) {
		elapsed := now.Sub(start)
		rate := 0.0
		if elapsed > 0 {
			rate = float64(attempts) / elapsed.Seconds()
		}
		app.Infow("progress",
			"attempts", attempts,
			"rate_addr_per_sec", fmt.Sprintf("%.2f", rate),
			"elapsed", humanDuration(elapsed),
		)
	}

	for {
		select {
		case <-ctx.Done():
			app.Infow("stopped",
				"elapsed", humanDuration(time.Since(start)),
				"attempts", attempts,
				"reason", "context canceled",
			)
			return ctx.Err()
		default:
		}

		priv, err := crypto.NewPrivKey()
		attempts++
		if err != nil {
			log.Errorw("generate priv failed", "err", err)
			continue
		}
		addr := crypto.AddressHex(priv)

		now := time.Now()
		if now.Sub(lastTick) >= tickEvery {
			progress(now)
			lastTick = now
		}

		mr := patterns.MatchAddress(cfg, addr)
		if mr == nil {
			continue
		}

		if encrypt {
			blob, err := crypto.KeystoreJSON(priv, ksPwd)
			if err != nil {
				log.Errorw("keystore encrypt failed", "addr", addr, "err", err)
				continue
			}
			if err := appendJSONL(dir, mr.Kind, blob); err != nil {
				log.Errorw("jsonl append failed", "addr", addr, "kind", mr.Kind, "err", err)
			}
		} else {
			rec := logPriv{
				Address:    addr,
				PrivateKey: crypto.PrivToHex(priv),
			}
			b, _ := json.Marshal(rec)
			if err := appendJSONL(dir, mr.Kind, b); err != nil {
				log.Errorw("jsonl append failed", "addr", addr, "kind", mr.Kind, "err", err)
			}
		}

		elapsed := time.Since(start)
		if !encrypt && showSecrets {
			app.Infow("FOUND",
				"kind", mr.Kind,
				"address", addr,
				"attempt", attempts,
				"elapsed", humanDuration(elapsed),
				"private_key", crypto.PrivToHex(priv),
			)
		} else {
			app.Infow("FOUND",
				"kind", mr.Kind,
				"address", addr,
				"attempt", attempts,
				"elapsed", humanDuration(elapsed),
			)
		}

		if mr.Final {
			app.Infow("final reached, stop")
			return nil
		}
	}
}

// ================================ MNEMONICS =================================

func runMnemonic(ctx context.Context, dir string, cfg *config.PatternsConfig, strength int, pass string, deriveN int, start time.Time, showSecrets bool) error {
	log := logx.With("generator").With("mode", "mnemonic")
	app := logx.S()

	var attempts uint64
	lastTick := time.Now()
	const tickEvery = 4 * time.Second

	progress := func(now time.Time) {
		elapsed := now.Sub(start)
		rate := 0.0
		if elapsed > 0 {
			rate = float64(attempts) / elapsed.Seconds()
		}
		app.Infow("progress",
			"attempts", attempts,
			"rate_addr_per_sec", fmt.Sprintf("%.2f", rate),
			"elapsed", humanDuration(elapsed),
		)
	}

	for {
		select {
		case <-ctx.Done():
			app.Infow("stopped",
				"elapsed", humanDuration(time.Since(start)),
				"attempts", attempts,
				"reason", "context canceled",
			)
			return ctx.Err()
		default:
		}

		mn, err := mnemonic.NewMnemonic(strength)
		if err != nil {
			log.Errorw("mnemonic generate failed", "err", err)
			continue
		}
		derived, err := mnemonic.Derive(mn, pass, deriveN)
		if err != nil {
			log.Errorw("mnemonic derive failed", "err", err)
			continue
		}

		for _, d := range derived {
			select {
			case <-ctx.Done():
				app.Infow("stopped",
					"elapsed", humanDuration(time.Since(start)),
					"attempts", attempts,
					"reason", "context canceled",
				)
				return ctx.Err()
			default:
			}

			attempts++
			addr := d.Address

			now := time.Now()
			if now.Sub(lastTick) >= tickEvery {
				progress(now)
				lastTick = now
			}

			mr := patterns.MatchAddress(cfg, addr)
			if mr == nil {
				continue
			}

			line := fmt.Sprintf(
				"address=%s index=%d path=%s mnemonic=%q passphrase=%q priv=%s",
				addr, d.Index, d.Path, d.Mnemonic, pass, crypto.PrivToHex(d.Priv),
			)
			_ = logsink.WriteMatch(dir, mr.Kind, line, false)

			elapsed := time.Since(start)

			if showSecrets {
				app.Infow("FOUND",
					"kind", mr.Kind,
					"address", addr,
					"attempt", attempts,
					"elapsed", humanDuration(elapsed),
					"mnemonic", d.Mnemonic,
					"passphrase", pass,
					"private_key", crypto.PrivToHex(d.Priv),
				)
			} else {
				app.Infow("FOUND",
					"kind", mr.Kind,
					"address", addr,
					"attempt", attempts,
					"elapsed", humanDuration(elapsed),
				)
			}

			if mr.Final {
				app.Infow("final reached, stop")
				return nil
			}
		}
	}
}

// ------------------------------- helpers ------------------------------------

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
}

func appendJSONL(dir, kind string, blob []byte) error {
	path := filepath.Join(dir, kind+".jsonl")
	return keystore.AppendJSONL(path, blob)
}
