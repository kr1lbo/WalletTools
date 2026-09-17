package local

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	provanitycrypto "github.com/woomai/provanity/internal/crypto"
	"github.com/woomai/provanity/internal/cuda"
	"github.com/woomai/provanity/internal/gpu"
	"github.com/woomai/provanity/internal/vanity"
)

func TestCudaConfigTronPrefix(t *testing.T) {
	keypair, err := provanitycrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	pat, err := vanity.ParseTronPattern("prefix:AB")
	if err != nil {
		t.Fatalf("ParseTronPattern: %v", err)
	}
	cfg, err := cudaConfig(Options{Wallet: WalletTron, Pattern: pat}, keypair)
	if err != nil {
		t.Fatalf("cudaConfig: %v", err)
	}
	if cfg.Mode != cuda.ModeTronPrefix {
		t.Fatalf("mode = %d, want ModeTronPrefix", cfg.Mode)
	}
	los, his, ok := provanitycrypto.TronPrefixLadder("TAB")
	if !ok {
		t.Fatal("TronPrefixLadder(TAB) unreachable")
	}
	if int(cfg.TronPrefixLevels) != len(los) || len(los) != pat.TargetScore() {
		t.Fatalf("levels = %d, ladder = %d, target = %d", cfg.TronPrefixLevels, len(los), pat.TargetScore())
	}
	for j := range los {
		off := j * 2 * provanitycrypto.AddressSize
		if !bytes.Equal(cfg.TronPrefixLadder[off:off+provanitycrypto.AddressSize], los[j][:]) {
			t.Fatalf("level %d lo bound mismatch", j)
		}
		if !bytes.Equal(cfg.TronPrefixLadder[off+provanitycrypto.AddressSize:off+2*provanitycrypto.AddressSize], his[j][:]) {
			t.Fatalf("level %d hi bound mismatch", j)
		}
	}
	if int(cfg.StopScore) != pat.TargetScore() {
		t.Fatalf("StopScore = %d, want %d", cfg.StopScore, pat.TargetScore())
	}
}

func TestCudaConfigTronSuffix(t *testing.T) {
	keypair, err := provanitycrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	pat, err := vanity.ParseTronPattern("suffix:xyz")
	if err != nil {
		t.Fatalf("ParseTronPattern: %v", err)
	}
	cfg, err := cudaConfig(Options{Wallet: WalletTron, Pattern: pat}, keypair)
	if err != nil {
		t.Fatalf("cudaConfig: %v", err)
	}
	if cfg.Mode != cuda.ModeTronSuffix {
		t.Fatalf("mode = %d, want ModeTronSuffix", cfg.Mode)
	}
	if int(cfg.TronSuffixLen) != 3 {
		t.Fatalf("suffix len = %d, want 3", cfg.TronSuffixLen)
	}
	if cfg.TronSuffixMod != 58*58*58 {
		t.Fatalf("suffix mod = %d, want %d", cfg.TronSuffixMod, 58*58*58)
	}
	// digit[0] is the last character ('z'), then 'y', then 'x'.
	wantDigits := []byte{
		byte(provanitycrypto.Base58Index('z')),
		byte(provanitycrypto.Base58Index('y')),
		byte(provanitycrypto.Base58Index('x')),
	}
	for i, want := range wantDigits {
		if cfg.TronSuffixDigits[i] != want {
			t.Fatalf("digit[%d] = %d, want %d", i, cfg.TronSuffixDigits[i], want)
		}
	}
	if int(cfg.StopScore) != pat.TargetScore() {
		t.Fatalf("StopScore = %d, want %d", cfg.StopScore, pat.TargetScore())
	}
}

func TestIsCUDAOOMError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "cudaMalloc oom (raw backend format)", err: errors.New("cudaMalloc(state_inv): out of memory (2)"), want: true},
		{name: "wrapped oom", err: fmt.Errorf("autotune candidate failed: %w", errors.New("cudaMalloc(hashes): out of memory (2)")), want: true},
		{name: "launch failure (NOT oom)", err: errors.New("pv_iterate_init launch: invalid argument (1)"), want: false},
		{name: "ctx canceled (NOT oom)", err: errors.New("context canceled"), want: false},
		{name: "generic", err: errors.New("something else"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCUDAOOMError(tt.err); got != tt.want {
				t.Fatalf("isCUDAOOMError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestFinalizeCUDAResultZeroOffset(t *testing.T) {
	pattern, err := vanity.ParsePattern("pattern:XX")
	if err != nil {
		t.Fatalf("ParsePattern: %v", err)
	}
	keypair, err := provanitycrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	result, err := finalizeCUDAResult(keypair, Options{Pattern: pattern}, gpu.FoundEvent{
		Offset:     strings.Repeat("0", 64),
		Address:    hex.EncodeToString(keypair.Address),
		ElapsedSec: 2,
		Attempts:   2000,
	}, gpu.ProgressEvent{Hashrate: 1000})
	if err != nil {
		t.Fatalf("finalizeCUDAResult() error = %v", err)
	}
	if result.Address != "0x"+hex.EncodeToString(keypair.Address) {
		t.Fatalf("address = %s, want 0x%x", result.Address, keypair.Address)
	}
	if result.Offset != "0x"+strings.Repeat("0", 64) {
		t.Fatalf("offset = %s", result.Offset)
	}
	if result.Stats.Hashrate != 1000 {
		t.Fatalf("hashrate = %d", result.Stats.Hashrate)
	}
}

func TestFinalizeCUDAResultTronWallet(t *testing.T) {
	keypair, err := provanitycrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	tronAddress, err := provanitycrypto.TronAddressFromEVMAddress(keypair.Address)
	if err != nil {
		t.Fatalf("TronAddressFromEVMAddress: %v", err)
	}
	pattern, err := vanity.ParseTronPattern("prefix:" + tronAddress[1:4])
	if err != nil {
		t.Fatalf("ParseTronPattern: %v", err)
	}

	result, err := finalizeCUDAResult(keypair, Options{Wallet: WalletTron, Pattern: pattern}, gpu.FoundEvent{
		Offset:     strings.Repeat("0", 64),
		Address:    hex.EncodeToString(keypair.Address),
		ElapsedSec: 2,
		Attempts:   2000,
	}, gpu.ProgressEvent{Hashrate: 1000})
	if err != nil {
		t.Fatalf("finalizeCUDAResult() error = %v", err)
	}
	if result.Address != tronAddress {
		t.Fatalf("address = %s, want %s", result.Address, tronAddress)
	}
	if result.ScoreBase != 58 {
		t.Fatalf("score base = %d, want 58", result.ScoreBase)
	}
	if result.Score != pattern.TargetScore() {
		t.Fatalf("score = %d, want %d", result.Score, pattern.TargetScore())
	}
}

func TestFinalizeCUDAResultRemoteMode(t *testing.T) {
	pattern, err := vanity.ParsePattern("pattern:XX")
	if err != nil {
		t.Fatalf("ParsePattern: %v", err)
	}
	initPrivateKey := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000001")
	publicKey, err := provanitycrypto.PublicKeyFromPrivateKey(initPrivateKey)
	if err != nil {
		t.Fatalf("PublicKeyFromPrivateKey: %v", err)
	}
	offset := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000001")
	finalPrivateKey, err := provanitycrypto.ComposePrivateKey(initPrivateKey, offset)
	if err != nil {
		t.Fatalf("ComposePrivateKey: %v", err)
	}
	address, err := provanitycrypto.AddressFromPrivateKey(finalPrivateKey)
	if err != nil {
		t.Fatalf("AddressFromPrivateKey: %v", err)
	}

	result, err := finalizeCUDAResult(provanitycrypto.KeyPair{PublicKey: publicKey}, Options{Pattern: pattern}, gpu.FoundEvent{
		Offset:     hex.EncodeToString(offset),
		Address:    hex.EncodeToString(address),
		ElapsedSec: 2,
		Attempts:   2000,
	}, gpu.ProgressEvent{Hashrate: 1000})
	if err != nil {
		t.Fatalf("finalizeCUDAResult() error = %v", err)
	}
	if result.PrivateKey != "" {
		t.Fatalf("private key = %q, want omitted", result.PrivateKey)
	}
	if result.Address != "0x"+hex.EncodeToString(address) {
		t.Fatalf("address = %s, want 0x%x", result.Address, address)
	}
	if result.Offset != "0x"+hex.EncodeToString(offset) {
		t.Fatalf("offset = %s, want 0x%x", result.Offset, offset)
	}
	if result.PublicKey != "0x"+hex.EncodeToString(publicKey) {
		t.Fatalf("public key = %s, want 0x%x", result.PublicKey, publicKey)
	}
}

func TestBenchmarkResultUsesProgressWindow(t *testing.T) {
	first := gpu.ProgressEvent{
		ElapsedSec: 1,
		ElapsedMS:  1000,
		Attempts:   1000,
	}
	last := gpu.ProgressEvent{
		ElapsedSec: 4,
		ElapsedMS:  4000,
		Attempts:   7000,
		Hashrate:   1,
		Devices:    []gpu.Device{{ID: 0, Name: "GPU", Hashrate: 1}},
	}

	result := benchmarkResult(10*time.Second, time.Time{}, first, []gpu.Device{{ID: 0, Name: "GPU"}}, last, 2, 1500)
	if result.DurationSec != 3 {
		t.Fatalf("duration = %f", result.DurationSec)
	}
	if result.Attempts != 6000 {
		t.Fatalf("attempts = %d", result.Attempts)
	}
	if result.Hashrate != 2000 {
		t.Fatalf("hashrate = %d", result.Hashrate)
	}
	if result.PeakHashrate != 2000 {
		t.Fatalf("peak hashrate = %d", result.PeakHashrate)
	}
	if len(result.Devices) != 1 || result.Devices[0].Hashrate != result.Hashrate {
		t.Fatalf("devices = %#v", result.Devices)
	}
}

func TestBenchmarkResultKeepsMultiDevicePerDeviceHashrates(t *testing.T) {
	// Multi-GPU runs surface per-device hashrate in JSON/wizard so users can see
	// each card's contribution. Aggregate hashrate (top-level) is still computed
	// from total attempts/time; per-device values come from the last progress
	// event and may not sum exactly to aggregate due to differing time windows.
	first := gpu.ProgressEvent{
		ElapsedSec: 1,
		ElapsedMS:  1000,
		Attempts:   1000,
	}
	last := gpu.ProgressEvent{
		ElapsedSec: 4,
		ElapsedMS:  4000,
		Attempts:   7000,
		Hashrate:   3000,
		Devices: []gpu.Device{
			{ID: 0, Name: "GPU0", Hashrate: 1000},
			{ID: 1, Name: "GPU1", Hashrate: 2000},
		},
	}

	result := benchmarkResult(10*time.Second, time.Time{}, first, []gpu.Device{
		{ID: 0, Name: "GPU0"},
		{ID: 1, Name: "GPU1"},
	}, last, 2, 3000)

	if len(result.Devices) != 2 {
		t.Fatalf("devices = %#v", result.Devices)
	}
	if result.Devices[0].Hashrate != 1000 || result.Devices[1].Hashrate != 2000 {
		t.Fatalf("per-device hashrates not preserved from progress event: %#v", result.Devices)
	}
	if result.Hashrate != 2000 {
		t.Fatalf("top-level hashrate should reflect total attempts/time (=6000/3000ms=2000), got %d", result.Hashrate)
	}
}

func TestAttachBenchmarkTuningDoesNotChangeBenchmarkPeak(t *testing.T) {
	result := attachBenchmarkTuning(BenchmarkResult{PeakHashrate: 100}, []TuningSample{
		{Params: CUDAParams{BatchMultiple: 1, WorkSize: 1}, Hashrate: 90},
		{Params: CUDAParams{BatchMultiple: 2, WorkSize: 2}, Hashrate: 120},
	})

	if result.PeakHashrate != 100 {
		t.Fatalf("peak hashrate = %d, want 100", result.PeakHashrate)
	}
	if len(result.TopTuning) != 2 || result.TopTuning[0].Hashrate != 120 {
		t.Fatalf("top tuning = %#v", result.TopTuning)
	}
}

func TestStateFromProgressPreservesHashrateUncertain(t *testing.T) {
	state := stateFromProgress(gpu.ProgressEvent{
		ElapsedMS: 5000,
		Attempts:  1234,
	}, cudaRunState{HashrateUncertain: true})

	if !state.HashrateUncertain {
		t.Fatal("HashrateUncertain = false, want true")
	}
	if state.ElapsedMS != 5000 || state.Attempts != 1234 {
		t.Fatalf("state = %#v", state)
	}
}

func TestAdjustCUDAEventPreservesPerDeviceHashrates(t *testing.T) {
	event := adjustCUDAEvent(gpu.ProgressEvent{
		Type:      gpu.EventProgress,
		ElapsedMS: 1000,
		Attempts:  3000,
		Hashrate:  3000,
		Devices: []gpu.Device{
			{ID: 0, Hashrate: 1000},
			{ID: 1, Hashrate: 2000},
		},
	}, cudaRunState{ElapsedMS: 500, Attempts: 100})

	progress, ok := event.(gpu.ProgressEvent)
	if !ok {
		t.Fatalf("event = %#v", event)
	}
	if progress.ElapsedMS != 1500 || progress.Attempts != 3100 {
		t.Fatalf("progress = %#v", progress)
	}
	if progress.Devices[0].Hashrate != 1000 || progress.Devices[1].Hashrate != 2000 {
		t.Fatalf("device hashrates = %#v", progress.Devices)
	}
}

func fixedHex(t *testing.T, value string) []byte {
	t.Helper()
	out, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode hex %q: %v", value, err)
	}
	return out
}
