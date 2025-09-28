package generator

import (
	"WalletTools/internal/keystore"
	"WalletTools/internal/logsink"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
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

type foundEvent struct {
	Kind       string
	Address    string
	PrivateHex string
	KsJSON     []byte
	Note       string
	Elapsed    time.Duration
	Attempt    uint64
	Final      bool

	Mnemonic string
	Pass     string
	Path     string
	Index    int
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

	// workers
	workers := opt.Workers
	runtime.GOMAXPROCS(workers)

	app.Infow("generation started",
		"module", module,
		"keystoreUsage", keystoreUsage,
		"patterns", opt.PatternsPath,
		"workers", workers,
		"GOMAXPROCS", workers,
	)

	start := time.Now()
	showSecrets := !opt.CaseMaskedOut

	events := make(chan foundEvent, workers*4)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var attempts uint64

	var finalOnce sync.Once
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for ev := range events {
			switch {
			case opt.Source == SourcePrivKey && opt.Encrypt:
				if err := appendJSONL(dir, ev.Kind, ev.KsJSON); err != nil {
					logx.S().Errorw("jsonl append failed", "addr", ev.Address, "kind", ev.Kind, "err", err)
				}
			case opt.Source == SourcePrivKey && !opt.Encrypt:
				rec := logPriv{Address: ev.Address, PrivateKey: ev.PrivateHex}
				b, _ := json.Marshal(rec)
				if err := appendJSONL(dir, ev.Kind, b); err != nil {
					logx.S().Errorw("jsonl append failed", "addr", ev.Address, "kind", ev.Kind, "err", err)
				}
			case opt.Source == SourceMnemonic:
				line := fmt.Sprintf(
					"address=%s index=%d path=%s mnemonic=%q passphrase=%q priv=%s",
					ev.Address, ev.Index, ev.Path, ev.Mnemonic, ev.Pass, ev.PrivateHex,
				)
				_ = logsink.WriteMatch(dir, ev.Kind, line, false)
			}

			if showSecrets {
				if opt.Source == SourceMnemonic {
					logx.S().Infow("FOUND",
						"kind", ev.Kind,
						"address", ev.Address,
						"attempt", ev.Attempt,
						"elapsed", humanDuration(ev.Elapsed),
						"mnemonic", ev.Mnemonic,
						"passphrase", ev.Pass,
						"private_key", ev.PrivateHex,
					)
				} else {
					logx.S().Infow("FOUND",
						"kind", ev.Kind,
						"address", ev.Address,
						"attempt", ev.Attempt,
						"elapsed", humanDuration(ev.Elapsed),
						"private_key", ev.PrivateHex,
					)
				}
			} else {
				logx.S().Infow("FOUND",
					"kind", ev.Kind,
					"address", ev.Address,
					"attempt", ev.Attempt,
					"elapsed", humanDuration(ev.Elapsed),
				)
			}

			if ev.Final {
				finalOnce.Do(func() {
					logx.S().Infow("final reached, stop all workers")
					cancel()
				})
			}
		}
	}()

	statusDone := make(chan struct{})
	go func() {
		defer close(statusDone)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				elapsed := now.Sub(start)
				rate := 0.0
				n := atomic.LoadUint64(&attempts)
				if elapsed > 0 {
					rate = float64(n) / elapsed.Seconds()
				}
				logx.S().Infow("progress",
					"attempts", n,
					"rate_addr_per_sec", fmt.Sprintf("%.2f", rate),
					"elapsed", humanDuration(elapsed),
				)
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(workers)
	switch opt.Source {
	case SourcePrivKey:
		for i := 0; i < workers; i++ {
			go func() {
				defer wg.Done()
				workerPriv(ctx, cfg, opt.Encrypt, opt.KeystorePassword, start, &attempts, events)
			}()
		}
	case SourceMnemonic:
		for i := 0; i < workers; i++ {
			go func() {
				defer wg.Done()
				workerMnemonic(ctx, cfg, opt.WordsStrength, opt.Passphrase, opt.DeriveN, start, &attempts, events)
			}()
		}
	default:
		cancel()
		wg.Done()
		return fmt.Errorf("unknown source: %s", opt.Source)
	}

	wg.Wait()
	close(events)
	<-writerDone
	<-statusDone

	logx.S().Infow("stopped",
		"elapsed", humanDuration(time.Since(start)),
		"attempts", atomic.LoadUint64(&attempts),
	)
	return ctx.Err()
}

// =============================== WORKERS ===============================

func workerPriv(
	ctx context.Context,
	cfg *config.PatternsConfig,
	encrypt bool,
	ksPwd string,
	start time.Time,
	attempts *uint64,
	out chan<- foundEvent,
) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		priv, err := crypto.NewPrivKey()
		n := atomic.AddUint64(attempts, 1)
		if err != nil {
			logx.S().Errorw("generate priv failed", "err", err)
			continue
		}
		addr := crypto.AddressHex(priv)

		mr := patterns.MatchAddress(cfg, addr)
		if mr == nil {
			continue
		}

		ev := foundEvent{
			Kind:    mr.Kind,
			Address: addr,
			Elapsed: time.Since(start),
			Attempt: n,
			Final:   mr.Final,
		}

		if encrypt {
			blob, err := crypto.KeystoreJSON(priv, ksPwd)
			if err != nil {
				logx.S().Errorw("keystore encrypt failed", "addr", addr, "err", err)
				continue
			}
			ev.KsJSON = blob
		} else {
			ev.PrivateHex = crypto.PrivToHex(priv)
		}

		select {
		case <-ctx.Done():
			return
		case out <- ev:
		}
	}
}

func workerMnemonic(
	ctx context.Context,
	cfg *config.PatternsConfig,
	strength int,
	pass string,
	deriveN int,
	start time.Time,
	attempts *uint64,
	out chan<- foundEvent,
) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		mn, err := mnemonic.NewMnemonic(strength)
		if err != nil {
			logx.S().Errorw("mnemonic generate failed", "err", err)
			continue
		}
		derived, err := mnemonic.Derive(mn, pass, deriveN)
		if err != nil {
			logx.S().Errorw("mnemonic derive failed", "err", err)
			continue
		}

		for _, d := range derived {
			select {
			case <-ctx.Done():
				return
			default:
			}

			n := atomic.AddUint64(attempts, 1)
			addr := d.Address
			mr := patterns.MatchAddress(cfg, addr)
			if mr == nil {
				continue
			}

			ev := foundEvent{
				Kind:       mr.Kind,
				Address:    addr,
				PrivateHex: crypto.PrivToHex(d.Priv),
				Mnemonic:   d.Mnemonic,
				Pass:       pass,
				Path:       d.Path,
				Index:      d.Index,
				Elapsed:    time.Since(start),
				Attempt:    n,
				Final:      mr.Final,
			}

			select {
			case <-ctx.Done():
				return
			case out <- ev:
			}

			if mr.Final {
				return
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
