package local

import (
	"encoding/json"
	"testing"

	"github.com/woomai/provanity/internal/gpu"
)

// Builtin profiles are intentionally empty until the kernels stabilize; known
// cards autotune (slower startup, always correct) and we'll pre-fill measured
// params once the algorithm is frozen. This guards against a malformed
// cuda_profiles.json, which builtinCUDAProfiles silently turns into nil.
func TestBuiltinCUDAProfilesAreValid(t *testing.T) {
	var profiles []CUDAProfile
	if err := json.Unmarshal(builtinCUDAProfilesJSON, &profiles); err != nil {
		t.Fatalf("cuda_profiles.json does not parse: %v", err)
	}
	for _, p := range profiles {
		if p.Name == "" || p.BatchMultiple <= 0 || p.WorkSize <= 0 {
			t.Fatalf("invalid builtin profile: %#v", p)
		}
	}
}

// Each known-card profile must match the exact device it was measured on, and
// not match any of the other known-card devices. Catches regressions where a
// loosely-bounded profile (e.g. a 3090 entry) would accidentally absorb a
// 3060/A6000 with the same compute capability but different SM count or VRAM.
func TestBuiltinCUDAProfilesAreExclusivePerKnownDevice(t *testing.T) {
	type knownDevice struct {
		gpu.Device
		profileName string
	}
	known := []knownDevice{
		{Device: gpu.Device{Name: "NVIDIA GeForce RTX 5090", ComputeMajor: 12, ComputeMinor: 0, Multiprocessors: 170, GlobalMem: 33670758400}, profileName: "NVIDIA GeForce RTX 5090"},
		{Device: gpu.Device{Name: "NVIDIA RTX PRO 6000 Blackwell Server Edition", ComputeMajor: 12, ComputeMinor: 0, Multiprocessors: 188, GlobalMem: 101975851008}, profileName: "NVIDIA RTX PRO 6000 Blackwell Server Edition"},
		{Device: gpu.Device{Name: "NVIDIA GeForce RTX 4090", ComputeMajor: 8, ComputeMinor: 9, Multiprocessors: 128, GlobalMem: 25252724736}, profileName: "NVIDIA GeForce RTX 4090"},
		{Device: gpu.Device{Name: "NVIDIA GeForce RTX 4070 Ti SUPER", ComputeMajor: 8, ComputeMinor: 9, Multiprocessors: 66, GlobalMem: 16715218944}, profileName: "NVIDIA GeForce RTX 4070 Ti SUPER"},
		{Device: gpu.Device{Name: "NVIDIA GeForce RTX 3090", ComputeMajor: 8, ComputeMinor: 6, Multiprocessors: 82, GlobalMem: 25296044032}, profileName: "NVIDIA GeForce RTX 3090"},
		{Device: gpu.Device{Name: "NVIDIA GeForce RTX 3080", ComputeMajor: 8, ComputeMinor: 6, Multiprocessors: 68, GlobalMem: 10354753536}, profileName: "NVIDIA GeForce RTX 3080"},
		{Device: gpu.Device{Name: "NVIDIA GeForce RTX 3060 Ti", ComputeMajor: 8, ComputeMinor: 6, Multiprocessors: 38, GlobalMem: 8222670848}, profileName: "NVIDIA GeForce RTX 3060 Ti"},
		{Device: gpu.Device{Name: "NVIDIA GeForce RTX 3060", ComputeMajor: 8, ComputeMinor: 6, Multiprocessors: 28, GlobalMem: 12490440704}, profileName: "NVIDIA GeForce RTX 3060"},
		{Device: gpu.Device{Name: "NVIDIA GeForce RTX 2080 Ti", ComputeMajor: 7, ComputeMinor: 5, Multiprocessors: 68, GlobalMem: 11347623936}, profileName: "NVIDIA GeForce RTX 2080 Ti"},
		{Device: gpu.Device{Name: "NVIDIA GeForce GTX 1660 SUPER", ComputeMajor: 7, ComputeMinor: 5, Multiprocessors: 22, GlobalMem: 6027608064}, profileName: "NVIDIA GeForce GTX 1660 SUPER"},
	}
	profiles := builtinCUDAProfiles()
	if len(profiles) == 0 {
		t.Skip("no builtin profiles to verify")
	}
	for _, kd := range known {
		var matched []string
		for _, p := range profiles {
			if profileMatchesDevice(p, kd.Device) {
				matched = append(matched, p.Name)
			}
		}
		if len(matched) != 1 || matched[0] != kd.profileName {
			t.Fatalf("device %q (sm=%d, mem=%d) matched profiles %v, want exactly [%q]", kd.Name, kd.Multiprocessors, kd.GlobalMem, matched, kd.profileName)
		}
	}
}

func TestCompleteCUDAParamsUsesBaseline(t *testing.T) {
	tests := []struct {
		name string
		in   CUDAParams
		want CUDAParams
	}{
		{name: "empty", want: baselineCUDAParams},
		{name: "missing batch", in: CUDAParams{WorkSize: 512}, want: CUDAParams{BatchMultiple: baselineCUDAParams.BatchMultiple, WorkSize: 512}},
		{name: "missing work size", in: CUDAParams{BatchMultiple: 65536}, want: CUDAParams{BatchMultiple: 65536, WorkSize: baselineCUDAParams.WorkSize}},
		{name: "complete", in: CUDAParams{BatchMultiple: 245760, WorkSize: 128}, want: CUDAParams{BatchMultiple: 245760, WorkSize: 128}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := completeCUDAParams(tt.in); got != tt.want {
				t.Fatalf("completeCUDAParams(%#v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestTuningCandidatesIncludeKnownFastProfiles(t *testing.T) {
	device := gpu.Device{
		Name:         "NVIDIA GeForce RTX 5070",
		GlobalMem:    12 * 1024 * 1024 * 1024,
		ComputeMajor: 12,
	}
	candidates := tuningCandidates(device)
	want := CUDAParams{BatchMultiple: 245760, WorkSize: 128}
	for _, candidate := range candidates {
		if candidate == want {
			return
		}
	}
	t.Fatalf("candidates missing %#v: %#v", want, candidates)
}

func TestTuningCandidatesIncludeHighMemoryProbes(t *testing.T) {
	device := gpu.Device{
		Name:         "NVIDIA GeForce RTX 5090",
		GlobalMem:    32 * 1024 * 1024 * 1024,
		ComputeMajor: 12,
	}
	want := CUDAParams{BatchMultiple: 983040, WorkSize: 256}
	foundCold := false
	for _, candidate := range tuningCandidates(device) {
		if candidate == want {
			foundCold = true
			break
		}
	}
	if !foundCold {
		t.Fatalf("candidates missing high-memory probe %#v", want)
	}
	for _, candidate := range onlineTuningCandidates(device) {
		if candidate == want {
			return
		}
	}
	t.Fatalf("online candidates missing high-memory probe %#v", want)
}

func TestTuningCandidatesRespectMemoryBudget(t *testing.T) {
	device := gpu.Device{
		Name:         "NVIDIA GeForce GTX 1660 SUPER",
		GlobalMem:    6 * 1024 * 1024 * 1024,
		ComputeMajor: 7,
	}
	maxBatch := maxQuickTuneBatchForMemory(device.GlobalMem)
	wantHighProbe := CUDAParams{BatchMultiple: maxBatch, WorkSize: 256}
	foundHighProbe := false
	for _, candidate := range tuningCandidates(device) {
		if candidate.BatchMultiple > maxBatch {
			t.Fatalf("candidate exceeds quick-tune budget: %#v", candidate)
		}
		if candidate == wantHighProbe {
			foundHighProbe = true
		}
	}
	if !foundHighProbe {
		t.Fatalf("candidates missing low-memory high probe %#v", wantHighProbe)
	}
	for _, candidate := range onlineTuningCandidates(device) {
		if candidate.BatchMultiple > maxBatch {
			t.Fatalf("online candidate exceeds quick-tune budget: %#v", candidate)
		}
	}
}

func TestBestTuningParamsUsesMedian(t *testing.T) {
	a := CUDAParams{BatchMultiple: 16384, WorkSize: 256}
	b := CUDAParams{BatchMultiple: 245760, WorkSize: 128}
	params, rate, ok := bestTuningParams([]TuningSample{
		{Params: a, Hashrate: 100},
		{Params: a, Hashrate: 300},
		{Params: b, Hashrate: 190},
		{Params: b, Hashrate: 200},
	})
	if !ok {
		t.Fatal("bestTuningParams ok = false")
	}
	if params != b || rate != 190 {
		t.Fatalf("best = %#v %d, want %#v 190", params, rate, b)
	}
}

func TestBestConfirmedTuningParamsIgnoresProbeOnlyCandidates(t *testing.T) {
	probeOnly := CUDAParams{BatchMultiple: 32768, WorkSize: 128}
	confirmed := CUDAParams{BatchMultiple: 16384, WorkSize: 256}
	params, rate, ok := bestConfirmedTuningParams([]TuningSample{
		{Params: probeOnly, Hashrate: 700, Round: "probe"},
		{Params: confirmed, Hashrate: 600, Round: "probe"},
		{Params: confirmed, Hashrate: 620, Round: "confirm"},
	})
	if !ok {
		t.Fatal("bestConfirmedTuningParams ok = false")
	}
	if params != confirmed || rate != 600 {
		t.Fatalf("best = %#v %d, want %#v 600", params, rate, confirmed)
	}
}

func TestBestConfirmedTuningParamsUsesProbeAndConfirmForConfirmedCandidates(t *testing.T) {
	a := CUDAParams{BatchMultiple: 16384, WorkSize: 128}
	b := CUDAParams{BatchMultiple: 32768, WorkSize: 256}
	params, rate, ok := bestConfirmedTuningParams([]TuningSample{
		{Params: a, Hashrate: 490, Round: "probe"},
		{Params: a, Hashrate: 484, Round: "confirm"},
		{Params: a, Hashrate: 471, Round: "confirm"},
		{Params: b, Hashrate: 487, Round: "probe"},
		{Params: b, Hashrate: 483, Round: "confirm"},
		{Params: b, Hashrate: 478, Round: "confirm"},
	})
	if !ok {
		t.Fatal("bestConfirmedTuningParams ok = false")
	}
	if params != a || rate != 484 {
		t.Fatalf("best = %#v %d, want %#v 484", params, rate, a)
	}
}

func TestTopTuningSamplesLimitsAndSortsByHashrate(t *testing.T) {
	var samples []TuningSample
	for i := 0; i < 12; i++ {
		samples = append(samples, TuningSample{
			Params:   CUDAParams{BatchMultiple: 16384 + i, WorkSize: 128},
			Hashrate: uint64(i + 1),
		})
	}
	samples = append(samples, TuningSample{
		Params: CUDAParams{BatchMultiple: 1, WorkSize: 1},
	})

	top := topTuningSamples(samples, 10)
	if len(top) != 10 {
		t.Fatalf("top length = %d, want 10", len(top))
	}
	if top[0].Hashrate != 12 || top[9].Hashrate != 3 {
		t.Fatalf("top hash rates = %#v", top)
	}
}

func TestTopTuningSamplesAggregatesByParams(t *testing.T) {
	a := CUDAParams{BatchMultiple: 16384, WorkSize: 128}
	b := CUDAParams{BatchMultiple: 32768, WorkSize: 256}

	top := topTuningSamples([]TuningSample{
		{Params: a, Hashrate: 100, Round: "probe", DurationSec: 3},
		{Params: a, Hashrate: 150, Round: "confirm", DurationSec: 5},
		{Params: a, Hashrate: 200, Round: "confirm", DurationSec: 5},
		{Params: b, Hashrate: 140, Round: "probe", DurationSec: 3},
		{Params: b, Hashrate: 160, Round: "confirm", DurationSec: 5},
	}, 10)

	if len(top) != 2 {
		t.Fatalf("top length = %d, want 2: %#v", len(top), top)
	}
	if top[0].Params != a || top[0].Hashrate != 150 {
		t.Fatalf("top[0] = %#v, want params a median 150", top[0])
	}
	if top[0].Round != "" || top[0].DurationSec != 0 {
		t.Fatalf("top[0] keeps sample fields = %#v", top[0])
	}
}
