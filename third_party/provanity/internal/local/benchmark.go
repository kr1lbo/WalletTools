package local

import (
	"context"
	"encoding/hex"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	provanitycrypto "github.com/woomai/provanity/internal/crypto"
	"github.com/woomai/provanity/internal/cuda"
	"github.com/woomai/provanity/internal/gpu"
)

type BenchmarkOptions struct {
	DeviceIDs          []int
	BatchMultiple      int
	WorkSize           int
	ManualParams       bool
	ProgressIntervalMS int
	Duration           time.Duration
	// AbandonBelow, when >0, makes runBenchmarkFixed exit early once the
	// post-warmup hashrate is below this threshold. Used by autotune probes to
	// skip clearly-worse candidates. The returned BenchmarkResult has
	// Abandoned=true in that case.
	AbandonBelow uint64
}

type BenchmarkResult struct {
	System       BenchmarkSystemInfo `json:"system"`
	DurationSec  float64             `json:"duration_sec"`
	ElapsedSec   uint64              `json:"elapsed_sec"`
	Attempts     uint64              `json:"attempts"`
	Hashrate     uint64              `json:"hashrate"`
	PeakHashrate uint64              `json:"peak_hashrate"`
	Devices      []gpu.Device        `json:"devices,omitempty"`
	Samples      int                 `json:"samples"`
	Params       CUDAParams          `json:"params"`
	ParamSource  string              `json:"param_source,omitempty"`
	Tuning       []TuningSample      `json:"tuning,omitempty"`
	TopTuning    []TuningSample      `json:"top_tuning,omitempty"`
	Abandoned    bool                `json:"-"` // internal autotune signal; not serialized
}

type BenchmarkSystemInfo struct {
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	DriverVersion string `json:"driver_version,omitempty"`
}

type BenchmarkEvent struct {
	GPUEvent gpu.Event
	Tuning   *TuningEvent
}

type BenchmarkEmitFunc func(BenchmarkEvent)

func Benchmark(ctx context.Context, opts BenchmarkOptions, emit BenchmarkEmitFunc) (BenchmarkResult, error) {
	if opts.Duration <= 0 {
		return BenchmarkResult{}, fmt.Errorf("benchmark duration must be positive")
	}
	if opts.ManualParams {
		opts = completeBenchmarkCUDAParams(opts)
		result, err := runBenchmarkFixed(ctx, opts, emit)
		if err != nil {
			return BenchmarkResult{}, err
		}
		result.Params = CUDAParams{BatchMultiple: opts.BatchMultiple, WorkSize: opts.WorkSize}
		result.ParamSource = "manual"
		result.System = benchmarkSystemInfo(ctx)
		return result, nil
	}

	device, ok := benchmarkTuningDevice(ctx, opts)
	if !ok {
		opts = completeBenchmarkCUDAParams(opts)
		emitBenchmarkTuning(emit, TuningStateDefault, "", 0, 0, CUDAParams{BatchMultiple: opts.BatchMultiple, WorkSize: opts.WorkSize}, 0)
		result, err := runBenchmarkFixed(ctx, opts, emit)
		if err != nil {
			return BenchmarkResult{}, err
		}
		result.Params = CUDAParams{BatchMultiple: opts.BatchMultiple, WorkSize: opts.WorkSize}
		result.ParamSource = "default"
		result.System = benchmarkSystemInfo(ctx)
		return result, nil
	}
	params, samples, rate, ok, err := autotuneBenchmark(ctx, opts, device, emit)
	if err != nil {
		return BenchmarkResult{}, err
	}
	if !ok {
		opts = completeBenchmarkCUDAParams(opts)
		result, err := runBenchmarkFixed(ctx, opts, emit)
		if err != nil {
			return BenchmarkResult{}, err
		}
		result.Params = CUDAParams{BatchMultiple: opts.BatchMultiple, WorkSize: opts.WorkSize}
		result.ParamSource = "default"
		result = attachBenchmarkTuning(result, samples)
		result.System = benchmarkSystemInfo(ctx)
		return result, nil
	}
	opts.BatchMultiple = params.BatchMultiple
	opts.WorkSize = params.WorkSize
	result, err := runBenchmarkFixed(ctx, opts, emit)
	if err != nil {
		return BenchmarkResult{}, err
	}
	saveCUDAProfile(device, params, rate)
	result.Params = params
	result.ParamSource = "autotune"
	result = attachBenchmarkTuning(result, samples)
	result.System = benchmarkSystemInfo(ctx)
	return result, nil
}

func runBenchmarkFixed(ctx context.Context, opts BenchmarkOptions, emit BenchmarkEmitFunc) (BenchmarkResult, error) {
	opts = completeBenchmarkCUDAParams(opts)
	progressInterval := opts.ProgressIntervalMS
	if progressInterval <= 0 {
		progressInterval = 1000
	}

	keypair, err := provanitycrypto.GenerateKeyPair()
	if err != nil {
		return BenchmarkResult{}, fmt.Errorf("generate benchmark keypair: %w", err)
	}

	cfg := cuda.Config{
		PublicKeyHex:       hex.EncodeToString(keypair.PublicKey),
		Mode:               cuda.ModeLeading,
		DeviceIDs:          append([]int(nil), opts.DeviceIDs...),
		BatchMultiple:      uint32(opts.BatchMultiple),
		WorkSize:           uint32(opts.WorkSize),
		ProgressIntervalMS: uint32(progressInterval),
	}
	// Benchmark mode no longer exists in the CUDA ABI. We re-use the
	// leading-nibble kernel with a target value that can never appear in a
	// nibble (valid nibbles are 0..15), guaranteeing the kernel always
	// returns score 0 and never publishes a candidate.
	cfg.Pattern[0] = 0xff

	var (
		readyDevices      []gpu.Device
		firstProgress     gpu.ProgressEvent
		lastProgress      gpu.ProgressEvent
		peakHashrate      uint64
		samples           int
		firstProgressTime time.Time
		runErr            error
		abandoned         bool
	)
	err = cuda.Run(ctx, cfg, func(event gpu.Event) bool {
		if emit != nil {
			emit(BenchmarkEvent{GPUEvent: event})
		}
		switch e := event.(type) {
		case gpu.ReadyEvent:
			readyDevices = e.Devices
		case gpu.ProgressEvent:
			if e.Hashrate > peakHashrate {
				peakHashrate = e.Hashrate
			}
			lastProgress = e
			samples++
			if firstProgressTime.IsZero() {
				firstProgress = e
				firstProgressTime = time.Now()
				return false
			}
			// Autotune early-exit: bail once we've seen a couple of post-warmup
			// samples and we're clearly below the running best. See the matching
			// path in run.go runTimedCUDAWithAbandon for the rationale.
			if opts.AbandonBelow > 0 && samples >= 3 && e.Hashrate > 0 && e.Hashrate < opts.AbandonBelow {
				abandoned = true
				return true
			}
			return benchmarkWindowElapsed(firstProgress, lastProgress, firstProgressTime) >= opts.Duration
		case gpu.ErrorEvent:
			runErr = fmt.Errorf("cuda benchmark error %s: %s", e.Code, e.Message)
			return true
		}
		return false
	})
	if runErr != nil {
		return BenchmarkResult{}, runErr
	}
	if err != nil {
		return BenchmarkResult{}, err
	}
	if samples == 0 {
		return BenchmarkResult{}, fmt.Errorf("cuda benchmark stopped before reporting progress")
	}
	result := benchmarkResult(opts.Duration, firstProgressTime, firstProgress, readyDevices, lastProgress, samples, peakHashrate)
	result.Abandoned = abandoned
	return result, nil
}

func completeBenchmarkCUDAParams(opts BenchmarkOptions) BenchmarkOptions {
	params := completeCUDAParams(CUDAParams{BatchMultiple: opts.BatchMultiple, WorkSize: opts.WorkSize})
	opts.BatchMultiple = params.BatchMultiple
	opts.WorkSize = params.WorkSize
	return opts
}

func benchmarkTuningDevice(ctx context.Context, opts BenchmarkOptions) (gpu.Device, bool) {
	devices, err := ProbeDevices(ctx)
	if err != nil {
		return gpu.Device{}, false
	}
	return selectTuningDevice(devices, opts.DeviceIDs)
}

func autotuneBenchmark(ctx context.Context, opts BenchmarkOptions, device gpu.Device, emit BenchmarkEmitFunc) (CUDAParams, []TuningSample, uint64, bool, error) {
	tuneOpts := opts
	tuneOpts.ProgressIntervalMS = defaultTuneProgressInterval
	var samples []TuningSample
	probeParams := tuningCandidates(device)
	var bestProbeHashrate uint64
	const probeAbandonRatioNum, probeAbandonRatioDen = 80, 100 // skip probes scoring below 80% of running best
	for i, params := range probeParams {
		tuneOpts.BatchMultiple = params.BatchMultiple
		tuneOpts.WorkSize = params.WorkSize
		tuneOpts.Duration = 3 * time.Second
		tuneOpts.AbandonBelow = bestProbeHashrate * probeAbandonRatioNum / probeAbandonRatioDen
		result, ok, err := runAutotuneSample(ctx, tuneOpts, emit, "probe", i+1, len(probeParams), params)
		if err != nil {
			return CUDAParams{}, samples, 0, false, err
		}
		if !ok || result.Abandoned {
			continue
		}
		samples = append(samples, TuningSample{Params: params, Hashrate: result.Hashrate, DurationSec: result.DurationSec, Round: "probe"})
		if result.Hashrate > bestProbeHashrate {
			bestProbeHashrate = result.Hashrate
		}
	}
	// Confirm round runs the top candidates to their full duration — these are
	// already known to be near-best, no early-exit.
	tuneOpts.AbandonBelow = 0
	topParams := topTuningParams(samples, 3)
	confirmTotal := len(topParams) * 2
	confirmIndex := 0
	for _, params := range topParams {
		confirmIndex++
		tuneOpts.BatchMultiple = params.BatchMultiple
		tuneOpts.WorkSize = params.WorkSize
		tuneOpts.Duration = 5 * time.Second
		result, ok, err := runAutotuneSample(ctx, tuneOpts, emit, "confirm", confirmIndex, confirmTotal, params)
		if err != nil {
			return CUDAParams{}, samples, 0, false, err
		}
		if !ok {
			continue
		}
		samples = append(samples, TuningSample{Params: params, Hashrate: result.Hashrate, DurationSec: result.DurationSec, Round: "confirm"})
	}
	for i := len(topParams) - 1; i >= 0; i-- {
		confirmIndex++
		params := topParams[i]
		tuneOpts.BatchMultiple = params.BatchMultiple
		tuneOpts.WorkSize = params.WorkSize
		tuneOpts.Duration = 5 * time.Second
		result, ok, err := runAutotuneSample(ctx, tuneOpts, emit, "confirm", confirmIndex, confirmTotal, params)
		if err != nil {
			return CUDAParams{}, samples, 0, false, err
		}
		if !ok {
			continue
		}
		samples = append(samples, TuningSample{Params: params, Hashrate: result.Hashrate, DurationSec: result.DurationSec, Round: "confirm"})
	}
	params, rate, ok := bestConfirmedTuningParams(samples)
	if ok {
		emitBenchmarkTuning(emit, TuningStateSelected, "", 0, 0, params, rate)
	} else {
		defaultOpts := completeBenchmarkCUDAParams(opts)
		defaultParams := CUDAParams{BatchMultiple: defaultOpts.BatchMultiple, WorkSize: defaultOpts.WorkSize}
		emitBenchmarkTuning(emit, TuningStateDefault, "", 0, 0, defaultParams, 0)
	}
	return params, samples, rate, ok, nil
}

func bestConfirmedTuningParams(samples []TuningSample) (CUDAParams, uint64, bool) {
	confirmedParams := make(map[CUDAParams]bool)
	for _, sample := range samples {
		if sample.Round == "confirm" {
			confirmedParams[sample.Params] = true
		}
	}
	if len(confirmedParams) == 0 {
		return bestTuningParams(samples)
	}
	var confirmedSamples []TuningSample
	for _, sample := range samples {
		if confirmedParams[sample.Params] {
			confirmedSamples = append(confirmedSamples, sample)
		}
	}
	return bestTuningParams(confirmedSamples)
}

func runAutotuneSample(ctx context.Context, opts BenchmarkOptions, emit BenchmarkEmitFunc, phase string, index, total int, params CUDAParams) (BenchmarkResult, bool, error) {
	emitBenchmarkTuningProgress(emit, TuningStateActive, phase, index, total, params, 0, 0, opts.Duration)
	sampleCtx, cancel := context.WithTimeout(ctx, opts.Duration+60*time.Second)
	defer cancel()
	result, err := runBenchmarkFixed(sampleCtx, opts, benchmarkAutotuneEmitter(emit, phase, index, total, params, opts.Duration))
	if err == nil {
		emitBenchmarkTuningProgress(emit, TuningStateSampled, phase, index, total, params, result.Hashrate, uint64(result.DurationSec), opts.Duration)
		return result, true, nil
	}
	if ctx.Err() != nil {
		return BenchmarkResult{}, false, ctx.Err()
	}
	emitBenchmarkTuning(emit, TuningStateSkipped, phase, index, total, params, 0)
	return BenchmarkResult{}, false, nil
}

func benchmarkResult(requested time.Duration, firstProgressTime time.Time, firstProgress gpu.ProgressEvent, readyDevices []gpu.Device, progress gpu.ProgressEvent, samples int, peakHashrate uint64) BenchmarkResult {
	duration := requested.Seconds()
	attempts := progress.Attempts
	elapsedMS := progressElapsedMS(firstProgress, progress)
	if elapsedMS > 0 && progress.Attempts > firstProgress.Attempts {
		duration = float64(elapsedMS) / 1000
		attempts = progress.Attempts - firstProgress.Attempts
	} else if !firstProgressTime.IsZero() {
		duration = time.Since(firstProgressTime).Seconds()
	}

	hashrate := progress.Hashrate
	if elapsedMS > 0 && attempts > 0 {
		hashrate = uint64((attempts * 1000) / elapsedMS)
	}
	devices := mergeBenchmarkDevices(readyDevices, progress.Devices)
	if len(devices) == 1 {
		devices[0].Hashrate = hashrate
	}
	// For multi-GPU, per-device hashrates come from the progress event via
	// mergeBenchmarkDevices and are kept so JSON/wizard consumers can see each
	// card's contribution. The top-level `hashrate` is the true aggregate;
	// sum(devices.hashrate) may differ slightly because per-device values use
	// the GPU's last reporting window while the aggregate uses total attempts.
	if hashrate > peakHashrate {
		peakHashrate = hashrate
	}
	return BenchmarkResult{
		DurationSec:  duration,
		ElapsedSec:   progress.ElapsedSec,
		Attempts:     attempts,
		Hashrate:     hashrate,
		PeakHashrate: peakHashrate,
		Devices:      devices,
		Samples:      samples,
	}
}

func attachBenchmarkTuning(result BenchmarkResult, samples []TuningSample) BenchmarkResult {
	result.Tuning = samples
	result.TopTuning = topTuningSamples(samples, 3)
	return result
}

func emitBenchmarkTuning(emit BenchmarkEmitFunc, state, phase string, index, total int, params CUDAParams, hashrate uint64) {
	emitBenchmarkTuningProgress(emit, state, phase, index, total, params, hashrate, 0, 0)
}

func emitBenchmarkTuningProgress(emit BenchmarkEmitFunc, state, phase string, index, total int, params CUDAParams, hashrate uint64, elapsedSec uint64, duration time.Duration) {
	if emit == nil {
		return
	}
	emit(BenchmarkEvent{Tuning: &TuningEvent{
		State:       state,
		Phase:       phase,
		Index:       index,
		Total:       total,
		Params:      params,
		Hashrate:    hashrate,
		ElapsedSec:  elapsedSec,
		DurationSec: duration.Seconds(),
	}})
}

func benchmarkAutotuneEmitter(emit BenchmarkEmitFunc, phase string, index, total int, params CUDAParams, duration time.Duration) BenchmarkEmitFunc {
	if emit == nil {
		return nil
	}
	return func(event BenchmarkEvent) {
		// Forward the original GPU event so the live renderer keeps its
		// attempts / hashrate gauges ticking during autotune probes — the
		// work is real (kernel evaluates the user pattern too), not wasted.
		if event.GPUEvent != nil {
			emit(event)
		}
		progress, ok := event.GPUEvent.(gpu.ProgressEvent)
		if !ok {
			return
		}
		emitBenchmarkTuningProgress(emit, TuningStateActive, phase, index, total, params, progress.Hashrate, progress.ElapsedSec, duration)
	}
}

func benchmarkSystemInfo(ctx context.Context) BenchmarkSystemInfo {
	return BenchmarkSystemInfo{
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		DriverVersion: detectNVIDIADriverVersion(ctx),
	}
}

func detectNVIDIADriverVersion(ctx context.Context) string {
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "nvidia-smi", "--query-gpu=driver_version", "--format=csv,noheader,nounits")
	data, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.FieldsFunc(string(data), func(r rune) bool {
		return r == '\n' || r == '\r'
	}) {
		if value := strings.TrimSpace(line); value != "" {
			return value
		}
	}
	return ""
}

func benchmarkWindowElapsed(firstProgress, lastProgress gpu.ProgressEvent, firstProgressTime time.Time) time.Duration {
	if elapsedMS := progressElapsedMS(firstProgress, lastProgress); elapsedMS > 0 {
		return time.Duration(elapsedMS) * time.Millisecond
	}
	if firstProgressTime.IsZero() {
		return 0
	}
	return time.Since(firstProgressTime)
}

func progressElapsedMS(firstProgress, lastProgress gpu.ProgressEvent) uint64 {
	if lastProgress.ElapsedMS > firstProgress.ElapsedMS {
		return lastProgress.ElapsedMS - firstProgress.ElapsedMS
	}
	if lastProgress.ElapsedSec > firstProgress.ElapsedSec {
		return (lastProgress.ElapsedSec - firstProgress.ElapsedSec) * 1000
	}
	return 0
}

func mergeBenchmarkDevices(readyDevices, progressDevices []gpu.Device) []gpu.Device {
	if len(readyDevices) == 0 {
		return append([]gpu.Device(nil), progressDevices...)
	}

	merged := append([]gpu.Device(nil), readyDevices...)
	index := make(map[int]int, len(merged))
	for i, device := range merged {
		index[device.ID] = i
	}
	for _, progressDevice := range progressDevices {
		if i, ok := index[progressDevice.ID]; ok {
			merged[i].Hashrate = progressDevice.Hashrate
			if merged[i].Name == "" {
				merged[i].Name = progressDevice.Name
			}
			continue
		}
		index[progressDevice.ID] = len(merged)
		merged = append(merged, progressDevice)
	}
	return merged
}
