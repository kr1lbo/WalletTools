package generator

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"WalletTools/internal/crypto"
	"WalletTools/internal/cudagen"
	"WalletTools/internal/patterns"
	"WalletTools/pkg/config"
	"WalletTools/pkg/logx"
)

type gpuTarget struct {
	Pattern string
	Kind    string
	Final   bool
	Approx  bool
	Verify  *config.PatternsConfig
}

func gpuPatterns(cfg *config.PatternsConfig) ([]gpuTarget, error) {
	if cfg.CaseSensitive {
		return nil, fmt.Errorf("GPU mode does not support case_sensitive addresses yet")
	}
	if cfg.Edges.MinCount != 0 {
		return nil, fmt.Errorf("GPU mode does not support edges patterns yet")
	}
	var targets []gpuTarget
	for i := range cfg.Specific {
		p := cfg.Specific[i]
		middle := 40 - len(p.Prefix) - len(p.Suffix)
		if middle < 0 {
			return nil, fmt.Errorf("GPU pattern exceeds address length")
		}
		targets = append(targets, gpuTarget{Pattern: "pattern:" + strings.ToLower(p.Prefix) + strings.Repeat("X", middle) + strings.ToLower(p.Suffix), Kind: "specific", Final: p.Final, Verify: &config.PatternsConfig{Specific: []config.SpecificPattern{p}}})
	}
	for i := range cfg.Symmetric {
		p := cfg.Symmetric[i]
		middle := 40 - len(p.Prefix) - len(p.Suffix)
		if middle < 0 {
			return nil, fmt.Errorf("GPU pattern exceeds address length")
		}
		prefix := strings.ToUpper(p.Prefix)
		prefix = strings.ReplaceAll(prefix, "Y", "Z")
		prefix = strings.ReplaceAll(prefix, "X", "Y")
		suffix := strings.ToUpper(p.Suffix)
		suffix = strings.ReplaceAll(suffix, "Y", "Z")
		suffix = strings.ReplaceAll(suffix, "X", "Y")
		targets = append(targets, gpuTarget{Pattern: "pattern:" + prefix + strings.Repeat("X", middle) + suffix, Kind: "symmetric", Final: p.Final, Verify: &config.PatternsConfig{Symmetric: []config.SymmetricPattern{p}}})
	}
	for i := range cfg.Regexp {
		p := cfg.Regexp[i]
		compiled, err := compileGPURegexp(p.Pattern, cfg.CaseSensitive)
		if err != nil {
			return nil, err
		}
		targets = append(targets, gpuTarget{Pattern: compiled, Kind: "regexp", Final: p.Final, Approx: true, Verify: &config.PatternsConfig{CaseSensitive: cfg.CaseSensitive, Regexp: []config.RegexpPattern{p}}})
	}
	// Duplicate configured targets waste VRAM. Merge them and retain final=true.
	byPattern := make(map[string]int)
	unique := make([]gpuTarget, 0, len(targets))
	for _, target := range targets {
		key := target.Kind + "\x00" + target.Pattern
		if target.Kind == "regexp" {
			key += "\x00" + target.Verify.Regexp[0].Pattern
		}
		if index, ok := byPattern[key]; ok {
			unique[index].Final = unique[index].Final || target.Final
			continue
		}
		byPattern[key] = len(unique)
		unique = append(unique, target)
	}
	if len(unique) == 0 {
		return nil, fmt.Errorf("GPU mode has no specific or symmetric patterns")
	}
	return unique, nil
}

type gpuSearchResult struct {
	target    gpuTarget
	candidate cudagen.Candidate
	err       error
}

func workerGPUFull(
	ctx context.Context,
	opt Options,
	cfg *config.PatternsConfig,
	start time.Time,
	attempts *uint64,
	out chan<- foundEvent,
) error {
	targets, err := gpuPatterns(cfg)
	if err != nil {
		return err
	}
	searchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan gpuSearchResult, len(targets)*2)
	counts := make([]uint64, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		logx.S().Infow("full GPU target selected", "index", i, "kind", target.Kind, "pattern", target.Pattern, "final", target.Final)
		wg.Add(1)
		go func(index int, target gpuTarget) {
			defer wg.Done()
			for {
				candidate, err := cudagen.Search(searchCtx, cudagen.Options{
					Executable: opt.CUDAExecutable,
					Device:     opt.CUDADevice,
					BatchSize:  opt.CUDABatchSize,
				}, target.Pattern, func(n, rate uint64) {
					atomic.StoreUint64(&counts[index], n)
					var total uint64
					for j := range counts {
						total += atomic.LoadUint64(&counts[j])
					}
					atomic.StoreUint64(attempts, total)
					logx.S().Infow("gpu progress", "target", index, "attempts", n, "rate_addr_per_sec", rate)
				})
				select {
				case results <- gpuSearchResult{target: target, candidate: candidate, err: err}:
				case <-searchCtx.Done():
					return
				}
				if err != nil || target.Final {
					return
				}
			}
		}(i, target)
	}
	go func() { wg.Wait(); close(results) }()

	for found := range results {
		if found.err != nil {
			if errors.Is(found.err, context.Canceled) && searchCtx.Err() != nil {
				continue
			}
			cancel()
			return found.err
		}
		target, result := found.target, found.candidate

		raw, err := hex.DecodeString(strings.TrimPrefix(result.PrivateKey, "0x"))
		if err != nil {
			return fmt.Errorf("decode GPU private key: %w", err)
		}
		priv, err := crypto.PrivKeyFromBytes(raw)
		if err != nil {
			return fmt.Errorf("verify GPU private key: %w", err)
		}
		address := crypto.AddressHex(priv)
		if !strings.EqualFold(address, result.Address) {
			return fmt.Errorf("GPU result failed CPU address verification")
		}
		match := patterns.MatchAddress(target.Verify, address)
		if match == nil || match.Kind != target.Kind {
			if target.Approx {
				continue
			}
			return fmt.Errorf("GPU result failed CPU pattern verification")
		}

		ev := foundEvent{Kind: target.Kind, Address: address, Elapsed: time.Since(start), Attempt: result.Attempts, Final: target.Final}
		if opt.Encrypt {
			ev.KsJSON, err = crypto.KeystoreJSON(priv, opt.KeystorePassword)
			if err != nil {
				return fmt.Errorf("encrypt GPU result: %w", err)
			}
		} else {
			ev.PrivateHex = crypto.PrivToHex(priv)
		}
		select {
		case out <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
		if target.Final {
			cancel()
			wg.Wait()
			return nil
		}
	}
	return ctx.Err()
}
