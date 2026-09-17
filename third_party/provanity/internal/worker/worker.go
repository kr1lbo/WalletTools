// Package worker implements the headless vanity generation command that ships
// as the standalone provanity-worker binary.
package worker

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/woomai/provanity/internal/cliexit"
	"github.com/woomai/provanity/internal/config"
	provanitycrypto "github.com/woomai/provanity/internal/crypto"
	"github.com/woomai/provanity/internal/gpu"
	"github.com/woomai/provanity/internal/local"
	"github.com/woomai/provanity/internal/vanity"
)

// Version is overridden at link time by cmd/build (-X
// github.com/woomai/provanity/internal/worker.Version=...).
var Version = "0.1.0-dev"

type runFunc func(context.Context, local.Options, local.EmitFunc) (local.Result, error)

var runSearch runFunc = local.Run

type flags struct {
	mode               string
	pattern            string
	devices            string
	batchMultiple      int
	workSize           int
	progressInterval   int
	wsProgressInterval int
	outputPath         string
	wsServerPort       int
	initPub            string
	initPubStdin       bool
	wsKey              string
	wsKeyStdin         bool
}

type stdinHandshake struct {
	InitPub string `json:"init_pub"`
	WSKey   string `json:"ws_key"`
}

// NewRootCommand returns a cobra command suitable for use as the root of the
// standalone provanity-worker binary.
func NewRootCommand() *cobra.Command {
	cmd := newCommand()
	cmd.Use = "provanity-worker"
	cmd.Short = "Headless vanity generation worker for orchestration"
	cmd.Version = Version
	cmd.SetVersionTemplate(versionInfo() + "\n")
	cmd.SilenceErrors = true
	cmd.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}
	return cmd
}

func newCommand() *cobra.Command {
	var f flags

	cmd := &cobra.Command{
		Use:          "provanity-worker",
		Short:        "Headless vanity generation worker for orchestration",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCommand(cmd, f)
		},
	}

	cmd.Flags().StringVar(&f.mode, "mode", "evm", "wallet mode: evm or tron")
	cmd.Flags().StringVar(&f.pattern, "pattern", "", "vanity pattern for selected mode; for example pattern:dead, leading:0:4, or prefix:ABC / suffix:xyz with --mode tron")
	cmd.Flags().StringVar(&f.devices, "devices", "all", "comma-separated CUDA device ids or all")
	cmd.Flags().IntVarP(&f.batchMultiple, "batch-multiple", "B", 0, "advanced raw GPU batch size; omit for profile/autotune")
	cmd.Flags().IntVar(&f.workSize, "work-size", 0, "CUDA threads per block; omit for profile/autotune")
	cmd.Flags().IntVar(&f.progressInterval, "progress-interval", 0, "milliseconds between stdout progress events; 0 disables stdout progress")
	cmd.Flags().IntVar(&f.wsProgressInterval, "ws-progress-interval", 0, "milliseconds between WebSocket progress broadcasts; 0 disables WebSocket progress")
	cmd.Flags().StringVarP(&f.outputPath, "output", "o", "", "write plaintext JSON result to this file")
	cmd.Flags().IntVar(&f.wsServerPort, "ws-server-port", 0, "start an encrypted WebSocket control server on 0.0.0.0:N; 0 disables it")
	cmd.Flags().StringVar(&f.initPub, "init-pub", "", "remote mode init public key as 128 hex chars")
	cmd.Flags().BoolVar(&f.initPubStdin, "init-pub-stdin", false, "read init_pub from one-line stdin JSON")
	cmd.Flags().StringVar(&f.wsKey, "ws-key", "", "32-byte WebSocket encryption key as 64 hex chars")
	cmd.Flags().BoolVar(&f.wsKeyStdin, "ws-key-stdin", false, "read ws_key from one-line stdin JSON")
	return cmd
}

func versionInfo() string {
	var b strings.Builder
	fmt.Fprintf(&b, "provanity-worker %s\n", Version)
	fmt.Fprintf(&b, "goos/goarch: %s/%s", runtime.GOOS, runtime.GOARCH)
	if paths, err := config.ResolvePaths(); err == nil {
		fmt.Fprintf(&b, "\nconfig dir: %s", paths.ConfigDir)
		fmt.Fprintf(&b, "\ncache dir: %s", paths.CacheDir)
	}
	return b.String()
}

func runCommand(cmd *cobra.Command, f flags) error {
	cfg, err := resolveConfig(cmd, f)
	if err != nil {
		return commandError(cmd, err)
	}

	runCtx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stopSignals()
	runCtx, cancelRun := context.WithCancel(runCtx)
	defer cancelRun()

	var ws *wsServer
	if cfg.wsServerPort > 0 {
		ws, err = startWSServer(cfg.wsServerPort, cfg.wsKey, cancelRun)
		if err != nil {
			return commandError(cmd, err)
		}
		defer ws.Close()
	}

	em := newEmitter(cmd.OutOrStdout(), ws, cfg.progressInterval, cfg.wsProgressInterval)
	result, runErr := runSearch(runCtx, cfg.options, em.Emit)
	if result.Address != "" && !em.ResultEmitted() {
		em.EmitResult(result)
	}
	if runErr != nil {
		if result.Address != "" && errors.Is(runErr, context.Canceled) {
			return nil
		}
		if !em.ErrorEmitted() {
			em.EmitError(runErr.Error())
		}
		return cliexit.WithCode(1, runErr)
	}
	return nil
}

type resolvedConfig struct {
	options            local.Options
	progressInterval   time.Duration
	wsProgressInterval time.Duration
	wsServerPort       int
	wsKey              []byte
}

func resolveConfig(cmd *cobra.Command, f flags) (resolvedConfig, error) {
	if f.initPub != "" && f.initPubStdin {
		return resolvedConfig{}, fmt.Errorf("--init-pub and --init-pub-stdin are mutually exclusive")
	}
	if f.wsKey != "" && f.wsKeyStdin {
		return resolvedConfig{}, fmt.Errorf("--ws-key and --ws-key-stdin are mutually exclusive")
	}
	if f.batchMultiple < 0 {
		return resolvedConfig{}, fmt.Errorf("batch multiple cannot be negative")
	}
	if f.workSize < 0 {
		return resolvedConfig{}, fmt.Errorf("work size cannot be negative")
	}
	if f.progressInterval < 0 {
		return resolvedConfig{}, fmt.Errorf("progress interval cannot be negative")
	}
	if f.wsProgressInterval < 0 {
		return resolvedConfig{}, fmt.Errorf("WebSocket progress interval cannot be negative")
	}
	if f.wsServerPort < 0 || f.wsServerPort > 65535 {
		return resolvedConfig{}, fmt.Errorf("WebSocket server port must be between 0 and 65535")
	}
	if (f.wsKey != "" || f.wsKeyStdin) && f.wsServerPort == 0 {
		return resolvedConfig{}, fmt.Errorf("--ws-key requires --ws-server-port > 0")
	}
	if f.wsServerPort > 0 && f.wsKey == "" && !f.wsKeyStdin {
		return resolvedConfig{}, fmt.Errorf("--ws-server-port requires --ws-key or --ws-key-stdin")
	}

	var handshake stdinHandshake
	if f.initPubStdin || f.wsKeyStdin {
		parsed, err := readStdinHandshake(cmd.InOrStdin())
		closeStdin(cmd.InOrStdin())
		if err != nil {
			return resolvedConfig{}, err
		}
		handshake = parsed
	}

	var initPublicKey []byte
	if f.initPub != "" {
		parsed, err := parsePublicKey(f.initPub)
		if err != nil {
			return resolvedConfig{}, err
		}
		initPublicKey = parsed
	} else if f.initPubStdin {
		if strings.TrimSpace(handshake.InitPub) == "" {
			return resolvedConfig{}, fmt.Errorf("stdin JSON is missing init_pub")
		}
		parsed, err := parsePublicKey(handshake.InitPub)
		if err != nil {
			return resolvedConfig{}, err
		}
		initPublicKey = parsed
	}

	var wsKey []byte
	if f.wsKey != "" {
		parsed, err := parseFixedHex("ws_key", f.wsKey, 32)
		if err != nil {
			return resolvedConfig{}, err
		}
		wsKey = parsed
	} else if f.wsKeyStdin {
		if strings.TrimSpace(handshake.WSKey) == "" {
			return resolvedConfig{}, fmt.Errorf("stdin JSON is missing ws_key")
		}
		parsed, err := parseFixedHex("ws_key", handshake.WSKey, 32)
		if err != nil {
			return resolvedConfig{}, err
		}
		wsKey = parsed
	}

	wallet, parsedPattern, err := parseModePattern(f.mode, f.pattern)
	if err != nil {
		return resolvedConfig{}, err
	}
	deviceIDs, requestedSelection, err := local.ParseDeviceIDs(f.devices)
	if err != nil {
		return resolvedConfig{}, err
	}
	if requestedSelection {
		return resolvedConfig{}, fmt.Errorf("worker does not support interactive GPU selection; pass --devices instead")
	}

	progressMS := minPositive(f.progressInterval, f.wsProgressInterval)
	return resolvedConfig{
		options: local.Options{
			Wallet:             wallet,
			Pattern:            parsedPattern,
			DeviceIDs:          deviceIDs,
			BatchMultiple:      f.batchMultiple,
			WorkSize:           f.workSize,
			ManualParams:       explicitCUDAParams(cmd, f.batchMultiple, f.workSize),
			ProgressIntervalMS: progressMS,
			OutputPath:         f.outputPath,
			InitialPublicKey:   initPublicKey,
		},
		progressInterval:   time.Duration(f.progressInterval) * time.Millisecond,
		wsProgressInterval: time.Duration(f.wsProgressInterval) * time.Millisecond,
		wsServerPort:       f.wsServerPort,
		wsKey:              wsKey,
	}, nil
}

func parseModePattern(mode, rawPattern string) (local.WalletKind, vanity.Pattern, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", string(local.WalletEVM):
		pattern, err := vanity.ParsePattern(rawPattern)
		return local.WalletEVM, pattern, err
	case string(local.WalletTron):
		pattern, err := vanity.ParseTronPattern(rawPattern)
		return local.WalletTron, pattern, err
	default:
		return "", vanity.Pattern{}, fmt.Errorf("--mode must be evm or tron")
	}
}

func explicitCUDAParams(cmd *cobra.Command, batchMultiple, workSize int) bool {
	return (cmd.Flags().Changed("batch-multiple") && batchMultiple > 0) ||
		(cmd.Flags().Changed("work-size") && workSize > 0)
}

func commandError(cmd *cobra.Command, err error) error {
	message := err.Error()
	writeEvent(cmd.OutOrStdout(), errorEvent(message))
	return cliexit.Printed(cmd, 1, message)
}

func readStdinHandshake(in io.Reader) (stdinHandshake, error) {
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return stdinHandshake{}, fmt.Errorf("read stdin handshake: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return stdinHandshake{}, fmt.Errorf("stdin handshake is empty")
	}
	var handshake stdinHandshake
	if err := json.Unmarshal([]byte(line), &handshake); err != nil {
		return stdinHandshake{}, fmt.Errorf("parse stdin handshake JSON: %w", err)
	}
	return handshake, nil
}

func closeStdin(in io.Reader) {
	if closer, ok := in.(io.Closer); ok {
		_ = closer.Close()
	}
}

func parsePublicKey(raw string) ([]byte, error) {
	value, err := parseFixedHex("init_pub", raw, provanitycrypto.PublicKeySize)
	if err != nil {
		return nil, err
	}
	if _, err := provanitycrypto.ParsePublicKeyXY(value); err != nil {
		return nil, fmt.Errorf("invalid init_pub: %w", err)
	}
	return value, nil
}

func parseFixedHex(name, raw string, wantBytes int) ([]byte, error) {
	value := strings.TrimPrefix(strings.TrimSpace(raw), "0x")
	if len(value) != wantBytes*2 {
		return nil, fmt.Errorf("%s must be %d hex chars", name, wantBytes*2)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	if len(decoded) != wantBytes {
		return nil, fmt.Errorf("%s must be %d bytes", name, wantBytes)
	}
	return decoded, nil
}

func minPositive(values ...int) int {
	out := 0
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if out == 0 || value < out {
			out = value
		}
	}
	return out
}

type emitter struct {
	out                io.Writer
	ws                 *wsServer
	stdoutProgress     time.Duration
	wsProgress         time.Duration
	mu                 sync.Mutex
	lastStdoutProgress uint64
	lastWSProgress     uint64
	bestScore          int
	resultEmitted      bool
	errorEmitted       bool
}

func newEmitter(out io.Writer, ws *wsServer, stdoutProgress, wsProgress time.Duration) *emitter {
	return &emitter{
		out:            out,
		ws:             ws,
		stdoutProgress: stdoutProgress,
		wsProgress:     wsProgress,
	}
}

func (e *emitter) Emit(event local.RunEvent) {
	if event.Tuning != nil {
		e.emitBoth(tuningEvent(*event.Tuning))
	}
	if event.GPUEvent != nil {
		switch gpuEvent := event.GPUEvent.(type) {
		case gpu.ReadyEvent:
			e.emitBoth(readyEvent(gpuEvent))
		case gpu.ProgressEvent:
			e.emitProgress(gpuEvent)
		case gpu.PhaseEvent:
			e.emitBoth(phaseEvent(gpuEvent))
		case gpu.ErrorEvent:
			e.EmitError(gpuEvent.Message)
		}
	}
	if event.Candidate != nil {
		e.mu.Lock()
		if event.Candidate.Score > e.bestScore {
			e.bestScore = event.Candidate.Score
		}
		e.mu.Unlock()
		e.emitBoth(candidateEvent(*event.Candidate))
	}
	if event.Result != nil {
		e.EmitResult(*event.Result)
	}
}

func (e *emitter) EmitResult(result local.Result) {
	e.mu.Lock()
	e.resultEmitted = true
	e.mu.Unlock()
	e.emitBoth(resultEvent(result))
}

func (e *emitter) EmitError(message string) {
	e.mu.Lock()
	e.errorEmitted = true
	e.mu.Unlock()
	e.emitBoth(errorEvent(message))
}

func (e *emitter) ResultEmitted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resultEmitted
}

func (e *emitter) ErrorEmitted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.errorEmitted
}

func (e *emitter) emitProgress(progress gpu.ProgressEvent) {
	pe := progressEvent(progress, e.currentBestScore())
	elapsedMS := progress.ElapsedMS
	if elapsedMS == 0 {
		elapsedMS = progress.ElapsedSec * 1000
	}

	emitStdout := e.shouldEmitStdoutProgress(elapsedMS)
	emitWS := e.shouldEmitWSProgress(elapsedMS)
	if emitStdout {
		writeEvent(e.out, pe)
	}
	if emitWS && e.ws != nil {
		e.ws.Broadcast(pe)
	}
}

func (e *emitter) emitBoth(event map[string]any) {
	writeEvent(e.out, event)
	if e.ws != nil {
		e.ws.Broadcast(event)
	}
}

func (e *emitter) currentBestScore() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.bestScore
}

func (e *emitter) shouldEmitStdoutProgress(elapsedMS uint64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return shouldEmitProgress(elapsedMS, uint64(e.stdoutProgress/time.Millisecond), &e.lastStdoutProgress)
}

func (e *emitter) shouldEmitWSProgress(elapsedMS uint64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return shouldEmitProgress(elapsedMS, uint64(e.wsProgress/time.Millisecond), &e.lastWSProgress)
}

func shouldEmitProgress(elapsedMS, intervalMS uint64, last *uint64) bool {
	if intervalMS == 0 {
		return false
	}
	if *last == 0 || elapsedMS < *last || elapsedMS-*last >= intervalMS {
		*last = elapsedMS
		return true
	}
	return false
}

func writeEvent(out io.Writer, event map[string]any) {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(event)
}

func baseEvent(name string) map[string]any {
	return map[string]any{
		"event": name,
		"ts":    time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
	}
}

type deviceJSON struct {
	ID        int    `json:"id"`
	Name      string `json:"name,omitempty"`
	MemoryMiB uint64 `json:"memory_mib,omitempty"`
	Hashrate  uint64 `json:"hashrate,omitempty"`
}

type cudaParamsJSON struct {
	BatchMultiple int `json:"batch_multiple"`
	WorkSize      int `json:"work_size"`
}

func readyEvent(event gpu.ReadyEvent) map[string]any {
	out := baseEvent("ready")
	out["devices"] = devicesJSON(event.Devices)
	return out
}

func phaseEvent(event gpu.PhaseEvent) map[string]any {
	out := baseEvent("phase")
	out["device_id"] = event.DeviceID
	out["phase"] = event.Phase
	out["message"] = event.Message
	if event.Value > 0 {
		out["value"] = event.Value
	}
	return out
}

func devicesJSON(gpuDevices []gpu.Device) []deviceJSON {
	devices := make([]deviceJSON, 0, len(gpuDevices))
	for _, device := range gpuDevices {
		converted := deviceJSON{ID: device.ID, Name: device.Name}
		if device.GlobalMem > 0 {
			converted.MemoryMiB = device.GlobalMem / 1024 / 1024
		}
		if device.Hashrate > 0 {
			converted.Hashrate = device.Hashrate
		}
		devices = append(devices, converted)
	}
	return devices
}

func tuningEvent(event local.TuningEvent) map[string]any {
	out := baseEvent("tuning")
	out["state"] = event.State
	out["phase"] = event.Phase
	out["index"] = event.Index
	out["total"] = event.Total
	out["params"] = cudaParamsJSON{
		BatchMultiple: event.Params.BatchMultiple,
		WorkSize:      event.Params.WorkSize,
	}
	if event.Hashrate > 0 {
		out["hashrate"] = event.Hashrate
	}
	if event.ElapsedSec > 0 {
		out["elapsed_sec"] = event.ElapsedSec
	}
	if event.DurationSec > 0 {
		out["duration_sec"] = event.DurationSec
	}
	return out
}

func progressEvent(event gpu.ProgressEvent, bestScore int) map[string]any {
	out := baseEvent("progress")
	out["elapsed_sec"] = event.ElapsedSec
	out["attempts"] = event.Attempts
	out["hashrate"] = event.Hashrate
	out["hashrate_uncertain"] = event.HashrateUncertain
	out["best_score"] = bestScore
	if len(event.Devices) > 0 {
		out["devices"] = devicesJSON(event.Devices)
	}
	return out
}

func candidateEvent(candidate local.Candidate) map[string]any {
	out := baseEvent("candidate")
	out["address"] = ensureAddressPrefix(candidate.Address)
	out["offset"] = ensureHexPrefix(candidate.Offset)
	out["score"] = candidate.Score
	out["target_score"] = candidate.TargetScore
	out["matched"] = candidate.Matched
	out["elapsed_sec"] = candidate.ElapsedSec
	out["attempts"] = candidate.Attempts
	return out
}

func resultEvent(result local.Result) map[string]any {
	out := baseEvent("result")
	out["partial"] = result.Partial
	out["address"] = result.Address
	out["offset"] = result.Offset
	if result.PrivateKey != "" {
		out["private_key"] = result.PrivateKey
	}
	out["score"] = result.Score
	out["target_score"] = result.TargetScore
	out["stats"] = map[string]any{
		"elapsed_sec":        result.Stats.ElapsedSec,
		"attempts":           result.Stats.Attempts,
		"hashrate":           result.Stats.Hashrate,
		"hashrate_uncertain": result.Stats.HashrateUncertain,
	}
	if result.OutputPath != "" {
		out["output_path"] = result.OutputPath
	}
	return out
}

func errorEvent(message string) map[string]any {
	out := baseEvent("error")
	out["message"] = message
	return out
}

func ensureHexPrefix(value string) string {
	if value == "" || strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		return value
	}
	return "0x" + value
}

func ensureAddressPrefix(value string) string {
	if value == "" || strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		return value
	}
	if !isHexString(value) {
		return value
	}
	return "0x" + value
}

func isHexString(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') {
			continue
		}
		return false
	}
	return true
}
