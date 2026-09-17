package local

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	provanitycrypto "github.com/woomai/provanity/internal/crypto"
	"github.com/woomai/provanity/internal/cuda"
	"github.com/woomai/provanity/internal/estimate"
	"github.com/woomai/provanity/internal/gpu"
	"github.com/woomai/provanity/internal/vanity"
)

type Options struct {
	Wallet             WalletKind
	Pattern            vanity.Pattern
	DeviceIDs          []int
	BatchMultiple      int
	WorkSize           int
	ManualParams       bool
	ProgressIntervalMS int
	OutputPath         string
	InitialPublicKey   []byte
	initialKeyPair     *provanitycrypto.KeyPair
}

type Stats struct {
	ElapsedSec        uint64 `json:"elapsed_sec"`
	Attempts          uint64 `json:"attempts"`
	Hashrate          uint64 `json:"hashrate,omitempty"`
	HashrateUncertain bool   `json:"hashrate_uncertain,omitempty"`
}

type Result struct {
	Address     string `json:"address"`
	PrivateKey  string `json:"private_key,omitempty"`
	PublicKey   string `json:"public_key,omitempty"`
	Offset      string `json:"offset"`
	Pattern     string `json:"pattern"`
	Score       int    `json:"score,omitempty"`
	TargetScore int    `json:"target_score,omitempty"`
	ScoreBase   int    `json:"-"`
	Stats       Stats  `json:"stats"`
	Partial     bool   `json:"partial,omitempty"`
	OutputPath  string `json:"-"`
}

type Candidate struct {
	Address     string
	Offset      string
	Score       int
	TargetScore int
	ScoreBase   int
	Matched     bool
	ElapsedSec  uint64
	ElapsedMS   uint64
	Attempts    uint64
}

type WalletKind string

const (
	WalletEVM  WalletKind = "evm"
	WalletTron WalletKind = "tron"
)

const (
	TuningStateActive   = "active"
	TuningStateSampled  = "sampled"
	TuningStateSelected = "selected"
	TuningStateDefault  = "default"
	TuningStateSkipped  = "skipped"
)

type TuningEvent struct {
	State       string
	Phase       string
	Index       int
	Total       int
	Params      CUDAParams
	Hashrate    uint64
	ElapsedSec  uint64
	DurationSec float64
}

type RunEvent struct {
	GPUEvent  gpu.Event
	Tuning    *TuningEvent
	Candidate *Candidate
	Result    *Result
	Message   string
}

type EmitFunc func(RunEvent)

func Run(ctx context.Context, opts Options, emit EmitFunc) (Result, error) {
	opts.Wallet = normalizeWalletKind(opts.Wallet)
	keypair, err := initialKeyPair(opts)
	if err != nil {
		return Result{}, fmt.Errorf("generate initial keypair: %w", err)
	}
	if opts.ManualParams {
		opts = withCompleteCUDAParams(opts)
		return runCUDA(ctx, opts, keypair, emit)
	}
	devices, err := ProbeDevices(ctx)
	if err == nil {
		if device, ok := selectTuningDevice(devices, opts.DeviceIDs); ok {
			if resolution, ok := resolveCUDAProfile(device); ok {
				opts.BatchMultiple = resolution.Params.BatchMultiple
				opts.WorkSize = resolution.Params.WorkSize
				emitTuning(emit, TuningStateSelected, resolution.Source, 0, 0, resolution.Params, 0)
				return runCUDA(ctx, opts, keypair, emit)
			}
			if opts.ProgressIntervalMS > 0 && shouldOnlineTune(opts.Pattern) {
				return runCUDAAutotuned(ctx, opts, keypair, device, emit)
			}
		}
	}
	opts = withCompleteCUDAParams(opts)
	emitTuning(emit, TuningStateDefault, "", 0, 0, CUDAParams{BatchMultiple: opts.BatchMultiple, WorkSize: opts.WorkSize}, 0)
	return runCUDAWithState(ctx, opts, keypair, cudaRunState{HashrateUncertain: true}, emit, nil)
}

type cudaRunState struct {
	ElapsedMS         uint64
	Attempts          uint64
	HashrateUncertain bool
}

type cudaRunOutcome struct {
	Result       Result
	Found        bool
	Partial      bool
	Abandoned    bool
	LastProgress gpu.ProgressEvent
}

// isCUDAOOMError reports whether err corresponds to a CUDA memory-allocation
// failure (cudaErrorMemoryAllocation, surfaced by the backend as the substring
// "out of memory"). Autotune treats these candidates as unusable on the current
// device and skips them instead of aborting the whole run, so users with VRAM
// occupied by other processes still get a working session at a smaller batch.
func isCUDAOOMError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "out of memory")
}

func runCUDAAutotuned(ctx context.Context, opts Options, keypair provanitycrypto.KeyPair, device gpu.Device, emit EmitFunc) (Result, error) {
	state := cudaRunState{HashrateUncertain: true}
	emit = suppressRepeatedReady(emit)
	var samples []TuningSample
	var bestPartial Result
	mergePartial := func(o cudaRunOutcome) {
		if o.Partial && o.Result.Score > bestPartial.Score {
			bestPartial = o.Result
		}
	}
	finishWith := func(latest Result, latestErr error) (Result, error) {
		if latest.Address != "" && !latest.Partial {
			return latest, nil
		}
		if latest.Address != "" && latest.Score >= bestPartial.Score {
			return latest, nil
		}
		if bestPartial.Address != "" {
			return bestPartial, nil
		}
		if latest.Address != "" {
			return latest, nil
		}
		return Result{}, latestErr
	}
	probeParams := onlineTuningCandidates(device)
	var bestProbeHashrate uint64
	const probeAbandonRatioNum, probeAbandonRatioDen = 80, 100 // skip probes scoring below 80% of running best
	for i, params := range probeParams {
		emitTuning(emit, TuningStateActive, "probe", i+1, len(probeParams), params, 0)
		tunedOpts := opts
		tunedOpts.BatchMultiple = params.BatchMultiple
		tunedOpts.WorkSize = params.WorkSize
		abandonBelow := bestProbeHashrate * probeAbandonRatioNum / probeAbandonRatioDen
		outcome, err := runTimedCUDAWithAbandon(ctx, tunedOpts, keypair, state, emit, 5*time.Second, abandonBelow)
		if err != nil && !outcome.Partial {
			if isCUDAOOMError(err) {
				emitTuning(emit, TuningStateSkipped, "probe", i+1, len(probeParams), params, 0)
				continue
			}
			return finishWith(Result{}, err)
		}
		if outcome.Found {
			return outcome.Result, nil
		}
		mergePartial(outcome)
		state = stateFromProgress(outcome.LastProgress, state)
		if outcome.Abandoned {
			// Probe terminated early because it was clearly slower than the
			// running best. Don't pollute samples with the partial reading.
			emitTuning(emit, TuningStateSkipped, "probe", i+1, len(probeParams), params, 0)
			if ctx.Err() != nil {
				return finishWith(Result{}, ctx.Err())
			}
			continue
		}
		if outcome.LastProgress.Hashrate > 0 {
			samples = append(samples, TuningSample{Params: params, Hashrate: outcome.LastProgress.Hashrate, DurationSec: 5, Round: "probe"})
			if outcome.LastProgress.Hashrate > bestProbeHashrate {
				bestProbeHashrate = outcome.LastProgress.Hashrate
			}
		}
		if ctx.Err() != nil {
			return finishWith(Result{}, ctx.Err())
		}
	}
	confirmParams := topTuningParams(samples, 2)
	for i, params := range confirmParams {
		emitTuning(emit, TuningStateActive, "confirm", i+1, len(confirmParams), params, 0)
		tunedOpts := opts
		tunedOpts.BatchMultiple = params.BatchMultiple
		tunedOpts.WorkSize = params.WorkSize
		outcome, err := runTimedCUDA(ctx, tunedOpts, keypair, state, emit, 8*time.Second)
		if err != nil && !outcome.Partial {
			if isCUDAOOMError(err) {
				emitTuning(emit, TuningStateSkipped, "confirm", i+1, len(confirmParams), params, 0)
				continue
			}
			return finishWith(Result{}, err)
		}
		if outcome.Found {
			return outcome.Result, nil
		}
		mergePartial(outcome)
		state = stateFromProgress(outcome.LastProgress, state)
		if outcome.LastProgress.Hashrate > 0 {
			samples = append(samples, TuningSample{Params: params, Hashrate: outcome.LastProgress.Hashrate, DurationSec: 8, Round: "confirm"})
		}
		if ctx.Err() != nil {
			return finishWith(Result{}, ctx.Err())
		}
	}
	params, rate, ok := bestTuningParams(samples)
	if !ok {
		opts = withCompleteCUDAParams(opts)
		emitTuning(emit, TuningStateDefault, "", 0, 0, CUDAParams{BatchMultiple: opts.BatchMultiple, WorkSize: opts.WorkSize}, 0)
		final, err := runCUDAWithState(ctx, opts, keypair, state, emit, nil)
		return finishWith(final, err)
	}
	saveCUDAProfile(device, params, rate)
	opts.BatchMultiple = params.BatchMultiple
	opts.WorkSize = params.WorkSize
	state.HashrateUncertain = false
	emitTuning(emit, TuningStateSelected, "", 0, 0, params, rate)
	final, err := runCUDAWithState(ctx, opts, keypair, state, emit, nil)
	return finishWith(final, err)
}

func emitTuning(emit EmitFunc, state, phase string, index, total int, params CUDAParams, hashrate uint64) {
	if emit == nil {
		return
	}
	emit(RunEvent{Tuning: &TuningEvent{
		State:    state,
		Phase:    phase,
		Index:    index,
		Total:    total,
		Params:   params,
		Hashrate: hashrate,
	}})
}

func suppressRepeatedReady(emit EmitFunc) EmitFunc {
	if emit == nil {
		return nil
	}
	seenReady := false
	return func(event RunEvent) {
		if event.GPUEvent != nil {
			if _, ok := event.GPUEvent.(gpu.ReadyEvent); ok {
				if seenReady {
					return
				}
				seenReady = true
			}
		}
		emit(event)
	}
}

func shouldOnlineTune(pattern vanity.Pattern) bool {
	estimateResult, err := estimate.ForPattern(pattern, 500_000_000)
	if err != nil {
		return true
	}
	return estimateResult.P50 > 2*time.Minute
}

func stateFromProgress(progress gpu.ProgressEvent, previous cudaRunState) cudaRunState {
	if progress.ElapsedMS == 0 && progress.Attempts == 0 {
		return previous
	}
	return cudaRunState{
		ElapsedMS:         progress.ElapsedMS,
		Attempts:          progress.Attempts,
		HashrateUncertain: previous.HashrateUncertain,
	}
}

func withCompleteCUDAParams(opts Options) Options {
	params := completeCUDAParams(CUDAParams{BatchMultiple: opts.BatchMultiple, WorkSize: opts.WorkSize})
	opts.BatchMultiple = params.BatchMultiple
	opts.WorkSize = params.WorkSize
	return opts
}

func runCUDA(ctx context.Context, opts Options, keypair provanitycrypto.KeyPair, emit EmitFunc) (Result, error) {
	return runCUDAWithState(ctx, opts, keypair, cudaRunState{}, emit, nil)
}

func runTimedCUDA(ctx context.Context, opts Options, keypair provanitycrypto.KeyPair, state cudaRunState, emit EmitFunc, duration time.Duration) (cudaRunOutcome, error) {
	return runTimedCUDAWithAbandon(ctx, opts, keypair, state, emit, duration, 0)
}

// runTimedCUDAWithAbandon is like runTimedCUDA but bails out early when the
// post-warmup hashrate is clearly worse than `abandonBelow` (set to 0 to
// disable). Used by autotune probes to skip slow candidates without paying the
// full probe duration. The returned outcome's Abandoned flag tells the caller
// that the result is not comparable to a full sample.
func runTimedCUDAWithAbandon(ctx context.Context, opts Options, keypair provanitycrypto.KeyPair, state cudaRunState, emit EmitFunc, duration time.Duration, abandonBelow uint64) (cudaRunOutcome, error) {
	var (
		firstProgress     gpu.ProgressEvent
		firstProgressTime time.Time
		samples           int
		abandoned         bool
	)
	outcome, err := runCUDAAttempt(ctx, opts, keypair, state, emit, func(progress gpu.ProgressEvent) bool {
		if firstProgressTime.IsZero() {
			firstProgress = progress
			firstProgressTime = time.Now()
			return false
		}
		samples++
		// Wait for at least 2 progress samples after the first so the kernel
		// is past its warm-up ramp before we judge it. Then abandon if its
		// instantaneous hashrate is still well under the running best — the
		// candidate is clearly not worth the rest of its 5s budget.
		if abandonBelow > 0 && samples >= 2 && progress.Hashrate > 0 && progress.Hashrate < abandonBelow {
			abandoned = true
			return true
		}
		return benchmarkWindowElapsed(firstProgress, progress, firstProgressTime) >= duration
	})
	outcome.Abandoned = abandoned
	return outcome, err
}

func runCUDAWithState(ctx context.Context, opts Options, keypair provanitycrypto.KeyPair, state cudaRunState, emit EmitFunc, stopProgress func(gpu.ProgressEvent) bool) (Result, error) {
	outcome, err := runCUDAAttempt(ctx, opts, keypair, state, emit, stopProgress)
	if outcome.Found || outcome.Partial {
		return outcome.Result, nil
	}
	if err != nil {
		return Result{}, err
	}
	return Result{}, fmt.Errorf("cuda search stopped before target pattern was found")
}

func runCUDAAttempt(ctx context.Context, opts Options, keypair provanitycrypto.KeyPair, state cudaRunState, emit EmitFunc, stopProgress func(gpu.ProgressEvent) bool) (cudaRunOutcome, error) {
	cfg, err := cudaConfig(opts, keypair)
	if err != nil {
		return cudaRunOutcome{}, err
	}

	var (
		lastProgress gpu.ProgressEvent
		bestScore    int
		bestEvent    *gpu.FoundEvent
		result       Result
		runErr       error
	)
	err = cuda.Run(ctx, cfg, func(event gpu.Event) bool {
		event = adjustCUDAEvent(event, state)
		if emit != nil {
			emit(RunEvent{GPUEvent: event})
		}
		switch e := event.(type) {
		case gpu.ProgressEvent:
			lastProgress = e
			if stopProgress != nil && stopProgress(e) {
				return true
			}
		case gpu.PhaseEvent:
			// Setup-lifecycle event; already forwarded via the unconditional
			// emit above. No special handling needed here.
		case gpu.FoundEvent:
			address := candidateAddress(opts, e.Address)
			score := scoreAddress(opts, address, e.Address)
			candidate := Candidate{
				Address:     address,
				Offset:      e.Offset,
				Score:       score,
				TargetScore: opts.Pattern.TargetScore(),
				ScoreBase:   scoreBase(opts),
				Matched:     matchesAddress(opts, address, e.Address),
				ElapsedSec:  e.ElapsedSec,
				ElapsedMS:   e.ElapsedMS,
				Attempts:    e.Attempts,
			}
			if score > bestScore || candidate.Matched {
				if emit != nil {
					emit(RunEvent{Candidate: &candidate})
				}
				bestScore = score
				eventCopy := e
				bestEvent = &eventCopy
			}
			if !candidate.Matched {
				return false
			}
			if lastProgress.Type == "" {
				lastProgress = gpu.ProgressEvent{
					Type:              gpu.EventProgress,
					ElapsedSec:        e.ElapsedSec,
					ElapsedMS:         e.ElapsedMS,
					Attempts:          e.Attempts,
					HashrateUncertain: state.HashrateUncertain,
				}
			}
			result, runErr = finalizeCUDAResult(keypair, opts, e, lastProgress)
			if runErr != nil {
				return true
			}
			if opts.OutputPath != "" {
				if err := writeResult(opts.OutputPath, result); err != nil {
					runErr = err
					return true
				}
				result.OutputPath = opts.OutputPath
			}
			if emit != nil {
				emit(RunEvent{Result: &result})
			}
			return true
		case gpu.ErrorEvent:
			runErr = fmt.Errorf("cuda error %s: %s", e.Code, e.Message)
			return true
		}
		return false
	})
	if runErr != nil {
		return cudaRunOutcome{LastProgress: lastProgress}, runErr
	}
	if result.Address != "" {
		return cudaRunOutcome{Result: result, Found: true, LastProgress: lastProgress}, nil
	}
	if bestEvent != nil {
		progress := lastProgress
		if progress.Type == "" {
			progress = gpu.ProgressEvent{
				Type:              gpu.EventProgress,
				ElapsedSec:        bestEvent.ElapsedSec,
				ElapsedMS:         bestEvent.ElapsedMS,
				Attempts:          bestEvent.Attempts,
				HashrateUncertain: state.HashrateUncertain,
			}
		}
		partial, perr := finalizeCUDAResult(keypair, opts, *bestEvent, progress)
		if perr == nil {
			partial.Partial = true
			if opts.OutputPath != "" {
				if werr := writeResult(opts.OutputPath, partial); werr == nil {
					partial.OutputPath = opts.OutputPath
				}
			}
			if emit != nil {
				emit(RunEvent{Result: &partial})
			}
			return cudaRunOutcome{Result: partial, Partial: true, LastProgress: lastProgress}, nil
		}
	}
	if err != nil {
		return cudaRunOutcome{LastProgress: lastProgress}, err
	}
	return cudaRunOutcome{LastProgress: lastProgress}, nil
}

func adjustCUDAEvent(event gpu.Event, state cudaRunState) gpu.Event {
	switch e := event.(type) {
	case gpu.ProgressEvent:
		if e.ElapsedMS == 0 && e.ElapsedSec > 0 {
			e.ElapsedMS = e.ElapsedSec * 1000
		}
		e.ElapsedMS += state.ElapsedMS
		e.ElapsedSec = e.ElapsedMS / 1000
		e.Attempts += state.Attempts
		e.HashrateUncertain = state.HashrateUncertain
		if len(e.Devices) == 1 {
			e.Devices[0].Hashrate = e.Hashrate
		}
		return e
	case gpu.FoundEvent:
		if e.ElapsedMS == 0 && e.ElapsedSec > 0 {
			e.ElapsedMS = e.ElapsedSec * 1000
		}
		e.ElapsedMS += state.ElapsedMS
		e.ElapsedSec = e.ElapsedMS / 1000
		e.Attempts += state.Attempts
		return e
	default:
		return event
	}
}

func ProbeDevices(ctx context.Context) ([]gpu.Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return cuda.ListDevices()
}

func normalizeWalletKind(kind WalletKind) WalletKind {
	switch kind {
	case "", WalletEVM:
		return WalletEVM
	case WalletTron:
		return WalletTron
	default:
		return kind
	}
}

func cudaConfig(opts Options, keypair provanitycrypto.KeyPair) (cuda.Config, error) {
	opts = withCompleteCUDAParams(opts)
	cfg := cuda.Config{
		PublicKeyHex:       hex.EncodeToString(keypair.PublicKey),
		DeviceIDs:          append([]int(nil), opts.DeviceIDs...),
		BatchMultiple:      uint32(opts.BatchMultiple),
		WorkSize:           uint32(opts.WorkSize),
		ProgressIntervalMS: uint32(opts.ProgressIntervalMS),
	}
	switch {
	case opts.Wallet == WalletTron && opts.Pattern.Kind == vanity.PatternTronPrefix:
		// Each prefix length maps to a nested [lo,hi] address interval; the
		// kernel reports the deepest interval that contains a candidate as the
		// matched-character count (graded progress, no base58/SHA per candidate).
		los, his, ok := provanitycrypto.TronPrefixLadder(opts.Pattern.Value)
		if !ok || len(los) == 0 {
			return cuda.Config{}, fmt.Errorf("Tron prefix %q is not a reachable address prefix", opts.Pattern.Value)
		}
		if len(los) > cuda.TronMaxPrefixLevels {
			return cuda.Config{}, fmt.Errorf("Tron prefix has %d levels, exceeds %d", len(los), cuda.TronMaxPrefixLevels)
		}
		cfg.Mode = cuda.ModeTronPrefix
		cfg.StopScore = byte(opts.Pattern.TargetScore())
		cfg.TronPrefixLevels = byte(len(los))
		for j := range los {
			off := j * 2 * provanitycrypto.AddressSize
			copy(cfg.TronPrefixLadder[off:off+provanitycrypto.AddressSize], los[j][:])
			copy(cfg.TronPrefixLadder[off+provanitycrypto.AddressSize:off+2*provanitycrypto.AddressSize], his[j][:])
		}
	case opts.Wallet == WalletTron && opts.Pattern.Kind == vanity.PatternTronSuffix:
		// The suffix is checksum-dependent, so the kernel computes the real
		// base58check tail per candidate. We pass the target digit values
		// (index 0 = last character) and the modulus 58^N used to extract the
		// address tail via value mod 58^N.
		value := opts.Pattern.Value
		n := len(value)
		if n == 0 || n > cuda.TronMaxSuffixLen {
			return cuda.Config{}, fmt.Errorf("Tron suffix length %d is out of range (1..%d)", n, cuda.TronMaxSuffixLen)
		}
		cfg.Mode = cuda.ModeTronSuffix
		cfg.StopScore = byte(opts.Pattern.TargetScore())
		cfg.TronSuffixLen = byte(n)
		mod := uint64(1)
		for i := 0; i < n; i++ {
			idx := provanitycrypto.Base58Index(value[n-1-i])
			if idx < 0 {
				return cuda.Config{}, fmt.Errorf("Tron suffix has invalid base58 character %q", value[n-1-i])
			}
			cfg.TronSuffixDigits[i] = byte(idx)
			mod *= 58
		}
		cfg.TronSuffixMod = mod
	case opts.Wallet == WalletTron:
		return cuda.Config{}, fmt.Errorf("Tron only supports prefix or suffix mode")
	case opts.Pattern.Kind == vanity.PatternPattern:
		cfg.Mode = cuda.ModePattern
		// Each nibble position is either a target value 0..15 or
		// cuda.PatternWildcard when the user wrote X / x / * / ?.
		for i := range cfg.Pattern {
			cfg.Pattern[i] = cuda.PatternWildcard
		}
		value := opts.Pattern.Value
		limit := len(value)
		if limit > cuda.PatternLen {
			limit = cuda.PatternLen
		}
		for i := 0; i < limit; i++ {
			if value[i] == 'Y' {
				cfg.Pattern[i] = cuda.PatternGroupX
				continue
			}
			if value[i] == 'Z' {
				cfg.Pattern[i] = cuda.PatternGroupY
				continue
			}
			n := hexNibbleNoError(value[i])
			if n == 0xff {
				continue
			}
			cfg.Pattern[i] = n
		}
		cfg.StopScore = byte(opts.Pattern.TargetScore())
	case opts.Pattern.Kind == vanity.PatternLeading:
		cfg.Mode = cuda.ModeLeading
		cfg.Pattern[0] = hexNibbleNoError(opts.Pattern.Value[0])
		cfg.StopScore = byte(opts.Pattern.TargetScore())
	case opts.Pattern.Kind == vanity.PatternMask:
		cfg.Mode = cuda.ModeMask
		cfg.PatternMasks = opts.Pattern.Masks
		cfg.StopScore = byte(opts.Pattern.TargetScore())
	default:
		return cuda.Config{}, fmt.Errorf("unsupported CUDA pattern kind %q", opts.Pattern.Kind)
	}
	return cfg, nil
}

func hexNibbleNoError(ch byte) byte {
	switch {
	case ch >= '0' && ch <= '9':
		return ch - '0'
	case ch >= 'a' && ch <= 'f':
		return ch - 'a' + 10
	case ch >= 'A' && ch <= 'F':
		return ch - 'A' + 10
	default:
		return 0xff
	}
}

func initialKeyPair(opts Options) (provanitycrypto.KeyPair, error) {
	if opts.initialKeyPair != nil && len(opts.InitialPublicKey) > 0 {
		return provanitycrypto.KeyPair{}, fmt.Errorf("initial private key and initial public key are mutually exclusive")
	}
	if len(opts.InitialPublicKey) > 0 {
		publicKey := append([]byte(nil), opts.InitialPublicKey...)
		if _, err := provanitycrypto.ParsePublicKeyXY(publicKey); err != nil {
			return provanitycrypto.KeyPair{}, err
		}
		return provanitycrypto.KeyPair{PublicKey: publicKey}, nil
	}
	if opts.initialKeyPair == nil {
		return provanitycrypto.GenerateKeyPair()
	}
	keypair := *opts.initialKeyPair
	keypair.PrivateKey = append([]byte(nil), keypair.PrivateKey...)
	keypair.PublicKey = append([]byte(nil), keypair.PublicKey...)
	keypair.Address = append([]byte(nil), keypair.Address...)
	return keypair, nil
}

func finalizeCUDAResult(keypair provanitycrypto.KeyPair, opts Options, found gpu.FoundEvent, progress gpu.ProgressEvent) (Result, error) {
	offset, err := hex.DecodeString(found.Offset)
	if err != nil {
		return Result{}, fmt.Errorf("decode cuda offset: %w", err)
	}
	address, err := hex.DecodeString(found.Address)
	if err != nil {
		return Result{}, fmt.Errorf("decode cuda address: %w", err)
	}
	if len(keypair.PrivateKey) == 0 {
		return remoteCUDAResult(keypair, opts, found, progress, offset, address)
	}
	finalized, err := provanitycrypto.Finalize(keypair.PrivateKey, offset, address)
	if err != nil {
		return Result{}, fmt.Errorf("finalize split key: %w", err)
	}

	hashrate := progress.Hashrate
	if hashrate == 0 && found.ElapsedSec > 0 {
		hashrate = found.Attempts / found.ElapsedSec
	}

	displayAddress, err := displayAddress(opts, finalized.Address)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Address:     displayAddress,
		PrivateKey:  "0x" + hex.EncodeToString(finalized.PrivateKey),
		PublicKey:   "0x" + hex.EncodeToString(finalized.PublicKey),
		Offset:      "0x" + found.Offset,
		Pattern:     opts.Pattern.String(),
		Score:       scoreAddress(opts, displayAddress, hex.EncodeToString(finalized.Address)),
		TargetScore: opts.Pattern.TargetScore(),
		ScoreBase:   scoreBase(opts),
		Stats: Stats{
			ElapsedSec:        found.ElapsedSec,
			Attempts:          found.Attempts,
			Hashrate:          hashrate,
			HashrateUncertain: progress.HashrateUncertain,
		},
	}, nil
}

func remoteCUDAResult(keypair provanitycrypto.KeyPair, opts Options, found gpu.FoundEvent, progress gpu.ProgressEvent, offset, address []byte) (Result, error) {
	if len(offset) != provanitycrypto.PrivateKeySize {
		return Result{}, fmt.Errorf("offset must be %d bytes", provanitycrypto.PrivateKeySize)
	}
	if len(address) != provanitycrypto.AddressSize {
		return Result{}, fmt.Errorf("address must be %d bytes", provanitycrypto.AddressSize)
	}

	hashrate := progress.Hashrate
	if hashrate == 0 && found.ElapsedSec > 0 {
		hashrate = found.Attempts / found.ElapsedSec
	}

	publicKey := ""
	if len(keypair.PublicKey) > 0 {
		publicKey = "0x" + hex.EncodeToString(keypair.PublicKey)
	}

	displayAddress, err := displayAddress(opts, address)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Address:     displayAddress,
		PublicKey:   publicKey,
		Offset:      "0x" + hex.EncodeToString(offset),
		Pattern:     opts.Pattern.String(),
		Score:       scoreAddress(opts, displayAddress, hex.EncodeToString(address)),
		TargetScore: opts.Pattern.TargetScore(),
		ScoreBase:   scoreBase(opts),
		Stats: Stats{
			ElapsedSec:        found.ElapsedSec,
			Attempts:          found.Attempts,
			Hashrate:          hashrate,
			HashrateUncertain: progress.HashrateUncertain,
		},
	}, nil
}

func writeResult(path string, result Result) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create result directory: %w", err)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write plaintext result: %w", err)
	}
	return nil
}

func candidateAddress(opts Options, addressHex string) string {
	if opts.Wallet != WalletTron {
		return "0x" + strings.TrimPrefix(addressHex, "0x")
	}
	address, err := hex.DecodeString(strings.TrimPrefix(addressHex, "0x"))
	if err != nil {
		return addressHex
	}
	tronAddress, err := provanitycrypto.TronAddressFromEVMAddress(address)
	if err != nil {
		return addressHex
	}
	return tronAddress
}

func displayAddress(opts Options, address []byte) (string, error) {
	if opts.Wallet == WalletTron {
		return provanitycrypto.TronAddressFromEVMAddress(address)
	}
	return "0x" + hex.EncodeToString(address), nil
}

func scoreAddress(opts Options, display, evmHex string) int {
	if opts.Wallet == WalletTron {
		return opts.Pattern.ScoreAddress(display)
	}
	return opts.Pattern.ScoreAddressHex(evmHex)
}

func matchesAddress(opts Options, display, evmHex string) bool {
	if opts.Wallet == WalletTron {
		return opts.Pattern.MatchesAddress(display)
	}
	return opts.Pattern.MatchesAddressHex(evmHex)
}

func scoreBase(opts Options) int {
	if opts.Wallet == WalletTron {
		return 58
	}
	return 16
}
