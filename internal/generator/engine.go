package generator

import (
	"WalletTools/internal/keystore"
	"WalletTools/internal/logsink"
	"context"
	"encoding/json"
	"errors"
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
	return run(ctx, opt, func(dir string, ev foundEvent) error { return saveFoundEvent(dir, opt, ev) })
}

func run(ctx context.Context, opt Options, save func(string, foundEvent) error) error {
	if opt.Source != SourcePrivKey && opt.Source != SourceMnemonic {
		return fmt.Errorf("unknown source: %s", opt.Source)
	}
	if opt.Source == SourcePrivKey && opt.Encrypt && opt.KeystorePassword == "" {
		return fmt.Errorf("keystore password must not be empty")
	}
	if opt.Source == SourceMnemonic && opt.WordsStrength != 0 && (opt.WordsStrength < 128 || opt.WordsStrength > 256 || opt.WordsStrength%32 != 0) {
		return fmt.Errorf("mnemonic strength must be 128, 160, 192, 224 or 256")
	}

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
	if err := logsink.WriteHint(dir, opt.PassHint); err != nil {
		return fmt.Errorf("write hint: %w", err)
	}

	// app.log + консоль через logx
	logPath := filepath.Join(dir, "app.log")
	if err := logx.Init(logx.Config{
		Level:                opt.LogLevel,
		FilePath:             logPath,
		ConsoleOnly:          false,
		HideSecretsInConsole: opt.CaseMaskedOut,
	}); err != nil {
		return fmt.Errorf("logx init for module failed: %w", err)
	}
	defer logx.Close()
	app := logx.S()

	// workers
	workers := opt.Workers
	maxCPU := runtime.NumCPU()
	if workers <= 0 {
		workers = maxCPU
	} else if workers > maxCPU {
		workers = maxCPU
	}
	previousProcs := runtime.GOMAXPROCS(workers)
	defer runtime.GOMAXPROCS(previousProcs)

	app.Infow("generation started",
		"module", module,
		"keystoreUsage", keystoreUsage,
		"patterns", opt.PatternsPath,
		"workers", workers,
		"GOMAXPROCS", workers,
		"gpu_enabled", opt.GPUEnabled && opt.Source == SourcePrivKey,
	)

	start := time.Now()
	showSecrets := !opt.CaseMaskedOut

	events := make(chan foundEvent, workers*4)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var attempts uint64
	var stoppedByFinal atomic.Bool

	var finalOnce sync.Once
	var writeErr error // Only the writer modifies this; read after writerDone.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for ev := range events {
			if writeErr != nil {
				continue
			}
			writeErr = save(dir, ev)
			if writeErr != nil {
				cancel()
				continue
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
					stoppedByFinal.Store(true)
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
	var gpuDone chan error
	switch opt.Source {
	case SourcePrivKey:
		if opt.GPUEnabled {
			wg.Add(1)
			gpuDone = make(chan error, 1)
			go func() {
				defer wg.Done()
				gpuDone <- workerGPUFull(ctx, opt, cfg, start, &attempts, events)
			}()
		} else {
			wg.Add(workers)
			for i := 0; i < workers; i++ {
				go func() {
					defer wg.Done()
					workerPriv(ctx, cfg, opt.Encrypt, opt.KeystorePassword, start, &attempts, events)
				}()
			}
		}
	case SourceMnemonic:
		wg.Add(workers)
		for i := 0; i < workers; i++ {
			go func() {
				defer wg.Done()
				workerMnemonic(ctx, cfg, opt.WordsStrength, opt.Passphrase, opt.DeriveN, start, &attempts, events)
			}()
		}
	}

	wg.Wait()
	var gpuErr error
	if gpuDone != nil {
		gpuErr = <-gpuDone
	}
	close(events)
	<-writerDone
	cancel()
	<-statusDone

	logx.S().Infow("stopped",
		"elapsed", humanDuration(time.Since(start)),
		"attempts", atomic.LoadUint64(&attempts),
	)
	if writeErr != nil {
		return fmt.Errorf("save match: %w", writeErr)
	}
	if stoppedByFinal.Load() {
		return nil
	}
	if gpuErr != nil && !errors.Is(gpuErr, context.Canceled) {
		return gpuErr
	}
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

func workerGPU(
	ctx context.Context,
	keys <-chan [32]byte,
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
		case raw, ok := <-keys:
			if !ok {
				return
			}
			n := atomic.AddUint64(attempts, 1)
			priv, err := crypto.PrivKeyFromBytes(raw[:])
			if err != nil { // zero or >= secp256k1 order; discard safely
				continue
			}
			addr := crypto.AddressHex(priv)
			mr := patterns.MatchAddress(cfg, addr)
			if mr == nil {
				continue
			}

			ev := foundEvent{Kind: mr.Kind, Address: addr, Elapsed: time.Since(start), Attempt: n, Final: mr.Final}
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

func saveFoundEvent(dir string, opt Options, ev foundEvent) error {
	if opt.Source == SourceMnemonic {
		line := fmt.Sprintf("address=%s index=%d path=%s mnemonic=%q passphrase=%q priv=%s", ev.Address, ev.Index, ev.Path, ev.Mnemonic, ev.Pass, ev.PrivateHex)
		return logsink.WriteMatch(dir, ev.Kind, line, false)
	}
	if opt.Encrypt {
		return appendJSONL(dir, ev.Kind, ev.KsJSON)
	}
	blob, err := json.Marshal(logPriv{Address: ev.Address, PrivateKey: ev.PrivateHex})
	if err != nil {
		return err
	}
	return appendJSONL(dir, ev.Kind, blob)
}

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
