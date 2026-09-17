package local

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/woomai/provanity/internal/config"
	"github.com/woomai/provanity/internal/gpu"
)

const (
	cudaProfileVersion          = "cuda-v2"
	envIgnoreCUDAProfiles       = "PROVANITY_CUDA_IGNORE_PROFILES"
	defaultTuneProgressInterval = 500
)

var baselineCUDAParams = CUDAParams{BatchMultiple: 16384, WorkSize: 256}

//go:embed cuda_profiles.json
var builtinCUDAProfilesJSON []byte

type CUDAParams struct {
	BatchMultiple int `json:"batch_multiple"`
	WorkSize      int `json:"work_size"`
}

type CUDAProfile struct {
	Name              string `json:"name"`
	ComputeMajor      int    `json:"compute_major,omitempty"`
	ComputeMinor      int    `json:"compute_minor,omitempty"`
	MinMultiprocessor int    `json:"min_multiprocessors,omitempty"`
	MaxMultiprocessor int    `json:"max_multiprocessors,omitempty"`
	MinGlobalMem      uint64 `json:"min_global_mem,omitempty"`
	MaxGlobalMem      uint64 `json:"max_global_mem,omitempty"`
	BatchMultiple     int    `json:"batch_multiple"`
	WorkSize          int    `json:"work_size"`
}

type CUDAProfileResolution struct {
	Params CUDAParams `json:"params"`
	Source string     `json:"source"`
	Key    string     `json:"key,omitempty"`
}

type TuningSample struct {
	Params      CUDAParams `json:"params"`
	Hashrate    uint64     `json:"hashrate"`
	DurationSec float64    `json:"duration_sec,omitempty"`
	Round       string     `json:"round,omitempty"`
}

type cudaProfileCache struct {
	Version string                      `json:"version"`
	Entries map[string]cudaProfileEntry `json:"entries"`
}

type cudaProfileEntry struct {
	Name            string     `json:"name"`
	ComputeMajor    int        `json:"compute_major,omitempty"`
	ComputeMinor    int        `json:"compute_minor,omitempty"`
	Multiprocessors int        `json:"multiprocessors,omitempty"`
	GlobalMem       uint64     `json:"global_mem,omitempty"`
	Params          CUDAParams `json:"params"`
	Hashrate        uint64     `json:"hashrate,omitempty"`
	UpdatedUnix     int64      `json:"updated_unix"`
	AutotuneVersion string     `json:"autotune_version"`
}

func resolveCUDAProfile(device gpu.Device) (CUDAProfileResolution, bool) {
	if ignoreCUDAProfiles() {
		return CUDAProfileResolution{}, false
	}
	key := cudaProfileKey(device)
	if entry, ok := loadCUDAProfileCacheEntry(key); ok {
		return CUDAProfileResolution{Params: entry.Params, Source: "cache", Key: key}, true
	}
	for _, profile := range builtinCUDAProfiles() {
		if profileMatchesDevice(profile, device) {
			return CUDAProfileResolution{
				Params: CUDAParams{BatchMultiple: profile.BatchMultiple, WorkSize: profile.WorkSize},
				Source: "known",
				Key:    key,
			}, true
		}
	}
	return CUDAProfileResolution{}, false
}

func completeCUDAParams(params CUDAParams) CUDAParams {
	if params.BatchMultiple <= 0 {
		params.BatchMultiple = baselineCUDAParams.BatchMultiple
	}
	if params.WorkSize <= 0 {
		params.WorkSize = baselineCUDAParams.WorkSize
	}
	return params
}

func saveCUDAProfile(device gpu.Device, params CUDAParams, hashrate uint64) {
	if params.BatchMultiple <= 0 || params.WorkSize <= 0 {
		return
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return
	}
	if err := os.MkdirAll(paths.ProfilesDir, 0o700); err != nil {
		return
	}
	path := cudaProfileCachePath(paths)
	cache := loadCUDAProfileCache(path)
	if cache.Entries == nil {
		cache.Entries = make(map[string]cudaProfileEntry)
	}
	key := cudaProfileKey(device)
	cache.Version = cudaProfileVersion
	cache.Entries[key] = cudaProfileEntry{
		Name:            device.Name,
		ComputeMajor:    device.ComputeMajor,
		ComputeMinor:    device.ComputeMinor,
		Multiprocessors: device.Multiprocessors,
		GlobalMem:       device.GlobalMem,
		Params:          params,
		Hashrate:        hashrate,
		UpdatedUnix:     time.Now().Unix(),
		AutotuneVersion: cudaProfileVersion,
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(data, '\n'), 0o600)
}

func builtinCUDAProfiles() []CUDAProfile {
	var profiles []CUDAProfile
	if err := json.Unmarshal(builtinCUDAProfilesJSON, &profiles); err != nil {
		return nil
	}
	return profiles
}

func loadCUDAProfileCacheEntry(key string) (cudaProfileEntry, bool) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return cudaProfileEntry{}, false
	}
	cache := loadCUDAProfileCache(cudaProfileCachePath(paths))
	entry, ok := cache.Entries[key]
	if !ok || entry.AutotuneVersion != cudaProfileVersion {
		return cudaProfileEntry{}, false
	}
	if entry.Params.BatchMultiple <= 0 || entry.Params.WorkSize <= 0 {
		return cudaProfileEntry{}, false
	}
	return entry, true
}

func loadCUDAProfileCache(path string) cudaProfileCache {
	data, err := os.ReadFile(path)
	if err != nil {
		return cudaProfileCache{Version: cudaProfileVersion, Entries: map[string]cudaProfileEntry{}}
	}
	var cache cudaProfileCache
	if err := json.Unmarshal(data, &cache); err != nil || cache.Version != cudaProfileVersion {
		return cudaProfileCache{Version: cudaProfileVersion, Entries: map[string]cudaProfileEntry{}}
	}
	if cache.Entries == nil {
		cache.Entries = make(map[string]cudaProfileEntry)
	}
	return cache
}

func cudaProfileCachePath(paths config.Paths) string {
	return filepath.Join(paths.ProfilesDir, "cuda-tuning.json")
}

func profileMatchesDevice(profile CUDAProfile, device gpu.Device) bool {
	if normalizeCUDADeviceName(profile.Name) != normalizeCUDADeviceName(device.Name) {
		return false
	}
	if profile.ComputeMajor != 0 && profile.ComputeMajor != device.ComputeMajor {
		return false
	}
	if profile.ComputeMinor != 0 && profile.ComputeMinor != device.ComputeMinor {
		return false
	}
	if profile.MinMultiprocessor != 0 && device.Multiprocessors < profile.MinMultiprocessor {
		return false
	}
	if profile.MaxMultiprocessor != 0 && device.Multiprocessors > profile.MaxMultiprocessor {
		return false
	}
	if profile.MinGlobalMem != 0 && device.GlobalMem < profile.MinGlobalMem {
		return false
	}
	if profile.MaxGlobalMem != 0 && device.GlobalMem > profile.MaxGlobalMem {
		return false
	}
	return profile.BatchMultiple > 0 && profile.WorkSize > 0
}

func cudaProfileKey(device gpu.Device) string {
	return fmt.Sprintf("%s|cc%d.%d|sm%d|mem%d|%s",
		normalizeCUDADeviceName(device.Name),
		device.ComputeMajor,
		device.ComputeMinor,
		device.Multiprocessors,
		memoryBucketMiB(device.GlobalMem),
		cudaProfileVersion,
	)
}

func normalizeCUDADeviceName(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(name))), " ")
}

func memoryBucketMiB(bytes uint64) uint64 {
	if bytes == 0 {
		return 0
	}
	mib := bytes / (1024 * 1024)
	return ((mib + 255) / 256) * 256
}

func ignoreCUDAProfiles() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(envIgnoreCUDAProfiles)))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func tuningCandidates(device gpu.Device) []CUDAParams {
	memoryBudget := defaultBatchMultipleForMemory(device.GlobalMem)
	maxBatch := maxQuickTuneBatchForMemory(device.GlobalMem)
	// 512 threads/block exceeds the kernel's __launch_bounds__(256, 2) hint and
	// always fails to launch on Turing+; probing it just wastes wallclock.
	workSizes := []int{128, 256}
	seen := make(map[CUDAParams]bool)
	var out []CUDAParams
	add := func(candidate CUDAParams) {
		if candidate.BatchMultiple <= 0 || candidate.BatchMultiple > maxBatch || candidate.WorkSize <= 0 || seen[candidate] {
			return
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	batches := []int{16384, 32768, 65536}
	if !isLowMemoryCUDADevice(device.GlobalMem) {
		batches = append(batches, 131072, memoryBudget)
		if memoryBudget >= 245760 {
			batches = append(batches, 245760)
		}
	}
	for _, batch := range batches {
		for _, workSize := range workSizes {
			add(CUDAParams{BatchMultiple: batch, WorkSize: workSize})
		}
	}
	for _, candidate := range highMemoryTuningCandidates(maxBatch) {
		add(candidate)
	}
	if isLowMemoryCUDADevice(device.GlobalMem) && maxBatch > 65536 {
		add(CUDAParams{BatchMultiple: maxBatch, WorkSize: 256})
	}
	return out
}

func onlineTuningCandidates(device gpu.Device) []CUDAParams {
	memoryBudget := defaultBatchMultipleForMemory(device.GlobalMem)
	maxBatch := maxQuickTuneBatchForMemory(device.GlobalMem)
	// work_size 512 exceeds __launch_bounds__(256, 2) and always fails to
	// launch; only probe 128 and 256.
	candidates := []CUDAParams{
		{BatchMultiple: 16384, WorkSize: 128},
		{BatchMultiple: 16384, WorkSize: 256},
		{BatchMultiple: 65536, WorkSize: 256},
	}
	if isLowMemoryCUDADevice(device.GlobalMem) && maxBatch > 65536 {
		candidates = append(candidates, CUDAParams{BatchMultiple: maxBatch, WorkSize: 256})
	} else if memoryBudget <= maxBatch {
		candidates = append(candidates,
			CUDAParams{BatchMultiple: memoryBudget, WorkSize: 128},
			CUDAParams{BatchMultiple: memoryBudget, WorkSize: 256},
		)
	}
	if maxBatch >= 131072 {
		candidates = append(candidates, CUDAParams{BatchMultiple: 131072, WorkSize: 256})
	}
	if maxBatch >= 245760 && memoryBudget >= 245760 {
		candidates = append(candidates,
			CUDAParams{BatchMultiple: 245760, WorkSize: 128},
			CUDAParams{BatchMultiple: 245760, WorkSize: 256},
		)
	}
	candidates = append(candidates, highMemoryTuningCandidates(maxBatch)...)
	return uniqueCUDAParams(candidates)
}

func highMemoryTuningCandidates(maxBatch int) []CUDAParams {
	var candidates []CUDAParams
	for _, batch := range []int{491520, 737280, 983040} {
		if maxBatch < batch {
			continue
		}
		candidates = append(candidates,
			CUDAParams{BatchMultiple: batch, WorkSize: 128},
			CUDAParams{BatchMultiple: batch, WorkSize: 256},
		)
	}
	return candidates
}

func uniqueCUDAParams(values []CUDAParams) []CUDAParams {
	seen := make(map[CUDAParams]bool)
	out := make([]CUDAParams, 0, len(values))
	for _, value := range values {
		if value.BatchMultiple <= 0 || value.WorkSize <= 0 || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func defaultBatchMultipleForMemory(globalMem uint64) int {
	const (
		batchSize           = 255
		mpNumberBytes       = 32
		deviceBuffers       = 3
		step                = 16384
		minDefault          = 32768
		maxDefault          = 245760
		memoryBudgetPercent = 55
		bytesPerBatchBucket = batchSize * mpNumberBytes * deviceBuffers
	)
	if globalMem == 0 {
		return step
	}
	value := int(((globalMem * memoryBudgetPercent) / 100) / bytesPerBatchBucket)
	value = (value / step) * step
	if value < minDefault {
		value = minDefault
	}
	if value > maxDefault {
		value = maxDefault
	}
	return value
}

func maxQuickTuneBatchForMemory(globalMem uint64) int {
	const (
		inverseStep       = 16384
		highMemoryTuneCap = 30 * 1024 * 1024 * 1024
		maxHighMemoryTune = 983040
	)
	if globalMem >= highMemoryTuneCap {
		return maxHighMemoryTune
	}
	budget := defaultBatchMultipleForMemory(globalMem)
	if isLowMemoryCUDADevice(globalMem) && budget > 65536 {
		return budget - inverseStep/2
	}
	return budget
}

func isLowMemoryCUDADevice(globalMem uint64) bool {
	const lowMemoryCap = 8 * 1024 * 1024 * 1024
	return globalMem > 0 && globalMem <= lowMemoryCap
}

func selectTuningDevice(devices []gpu.Device, ids []int) (gpu.Device, bool) {
	if len(devices) == 0 {
		return gpu.Device{}, false
	}
	if len(ids) == 0 {
		return devices[0], true
	}
	for _, id := range ids {
		for _, device := range devices {
			if device.ID == id {
				return device, true
			}
		}
	}
	return gpu.Device{}, false
}

func bestTuningParams(samples []TuningSample) (CUDAParams, uint64, bool) {
	type aggregate struct {
		params CUDAParams
		rates  []uint64
	}
	byParams := make(map[CUDAParams]*aggregate)
	for _, sample := range samples {
		if sample.Hashrate == 0 {
			continue
		}
		agg := byParams[sample.Params]
		if agg == nil {
			agg = &aggregate{params: sample.Params}
			byParams[sample.Params] = agg
		}
		agg.rates = append(agg.rates, sample.Hashrate)
	}
	var (
		bestParams CUDAParams
		bestRate   uint64
		ok         bool
	)
	for _, agg := range byParams {
		rate := medianUint64(agg.rates)
		if !ok || rate > bestRate {
			bestParams = agg.params
			bestRate = rate
			ok = true
		}
	}
	return bestParams, bestRate, ok
}

func topTuningParams(samples []TuningSample, limit int) []CUDAParams {
	type ranked struct {
		params CUDAParams
		rate   uint64
	}
	seen := make(map[CUDAParams]bool)
	var rankedParams []ranked
	for _, sample := range samples {
		if seen[sample.Params] || sample.Hashrate == 0 {
			continue
		}
		seen[sample.Params] = true
		var rates []uint64
		for _, other := range samples {
			if other.Params == sample.Params && other.Hashrate > 0 {
				rates = append(rates, other.Hashrate)
			}
		}
		rankedParams = append(rankedParams, ranked{params: sample.Params, rate: medianUint64(rates)})
	}
	sort.SliceStable(rankedParams, func(i, j int) bool {
		return rankedParams[i].rate > rankedParams[j].rate
	})
	if len(rankedParams) > limit {
		rankedParams = rankedParams[:limit]
	}
	out := make([]CUDAParams, 0, len(rankedParams))
	for _, rankedParam := range rankedParams {
		out = append(out, rankedParam.params)
	}
	return out
}

func topTuningSamples(samples []TuningSample, limit int) []TuningSample {
	if limit <= 0 {
		return nil
	}
	type aggregate struct {
		params CUDAParams
		rates  []uint64
	}
	byParams := make(map[CUDAParams]*aggregate)
	for _, sample := range samples {
		if sample.Hashrate == 0 {
			continue
		}
		agg := byParams[sample.Params]
		if agg == nil {
			agg = &aggregate{params: sample.Params}
			byParams[sample.Params] = agg
		}
		agg.rates = append(agg.rates, sample.Hashrate)
	}
	out := make([]TuningSample, 0, len(byParams))
	for _, agg := range byParams {
		out = append(out, TuningSample{
			Params:   agg.params,
			Hashrate: medianUint64(agg.rates),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Hashrate > out[j].Hashrate
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func medianUint64(values []uint64) uint64 {
	if len(values) == 0 {
		return 0
	}
	values = append([]uint64(nil), values...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[(len(values)-1)/2]
}
