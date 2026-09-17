//go:build linux && cgo

package cuda

/*
#cgo CFLAGS: -I${SRCDIR}/native
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "provanity_cuda.h"

extern int32_t provanityCudaCallback(provanity_cuda_event *event, void *user_data);

typedef int32_t (*provanity_cuda_list_devices_fn)(provanity_cuda_device *devices, int32_t max_devices, char *error, uint32_t error_len);
typedef int32_t (*provanity_cuda_run_fn)(const provanity_cuda_config *config, provanity_cuda_callback callback, void *user_data, char *error, uint32_t error_len);
typedef int32_t (*provanity_cuda_version_fn)(char *version, uint32_t version_len);

static char *provanity_strdup_dlerror() {
	const char *err = dlerror();
	if (err == NULL) {
		return NULL;
	}
	return strdup(err);
}

static void *provanity_dlopen(const char *path, char **error) {
	dlerror();
	void *handle = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (handle == NULL && error != NULL) {
		*error = provanity_strdup_dlerror();
	}
	return handle;
}

static void *provanity_dlsym(void *handle, const char *name, char **error) {
	dlerror();
	void *symbol = dlsym(handle, name);
	const char *err = dlerror();
	if (err != NULL) {
		if (error != NULL) {
			*error = strdup(err);
		}
		return NULL;
	}
	return symbol;
}

static void provanity_dlclose(void *handle) {
	if (handle != NULL) {
		dlclose(handle);
	}
}

static int32_t provanity_call_list_devices(void *fn, provanity_cuda_device *devices, int32_t max_devices, char *error, uint32_t error_len) {
	return ((provanity_cuda_list_devices_fn)fn)(devices, max_devices, error, error_len);
}

static int32_t provanity_call_run(void *fn, const provanity_cuda_config *config, uintptr_t user_data, char *error, uint32_t error_len) {
	return ((provanity_cuda_run_fn)fn)(config, (provanity_cuda_callback)provanityCudaCallback, (void *)user_data, error, error_len);
}

static int32_t provanity_call_version(void *fn, char *version, uint32_t version_len) {
	return ((provanity_cuda_version_fn)fn)(version, version_len);
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/cgo"
	"strings"
	"sync"
	"unsafe"

	"github.com/woomai/provanity/internal/config"
	"github.com/woomai/provanity/internal/gpu"
)

const (
	envCUDASO  = "PROVANITY_CUDA_SO"
	envCUDALib = "PROVANITY_CUDA_LIB"
	envCUDADLL = "PROVANITY_CUDA_DLL"
)

type cudaSO struct {
	path        string
	handle      unsafe.Pointer
	version     unsafe.Pointer
	listDevices unsafe.Pointer
	run         unsafe.Pointer
}

type cudaLibraryCandidate struct {
	name string
	path string
}

var (
	loadOnce sync.Once
	loadedSO *cudaSO
	loadErr  error
)

func ListDevices() ([]gpu.Device, error) {
	so, err := load()
	if err != nil {
		return nil, err
	}
	return listDevicesLoaded(so)
}

func listDevicesLoaded(so *cudaSO) ([]gpu.Device, error) {
	var raw [MaxDevices]C.provanity_cuda_device
	var errBuf [512]C.char
	ret := C.provanity_call_list_devices(
		so.listDevices,
		&raw[0],
		C.int32_t(MaxDevices),
		&errBuf[0],
		C.uint32_t(len(errBuf)),
	)
	if ret < 0 {
		return nil, soError(errBuf[:])
	}

	count := int(ret)
	if count > MaxDevices {
		count = MaxDevices
	}
	devices := make([]gpu.Device, 0, count)
	for i := 0; i < count; i++ {
		devices = append(devices, convertCDevice(raw[i]))
	}
	return devices, nil
}

func Run(ctx context.Context, cfg Config, emit EmitFunc) error {
	so, err := load()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runCfg, devices, ok, err := multiDeviceRunConfig(so, cfg); err != nil {
		return err
	} else if ok {
		return runMulti(ctx, so, runCfg, devices, emit)
	}
	return runSingle(ctx, so, cfg, emit)
}

func runSingle(ctx context.Context, so *cudaSO, cfg Config, emit EmitFunc) error {
	raw, publicKey, err := toCUDAConfig(cfg)
	if err != nil {
		return err
	}
	defer C.free(unsafe.Pointer(publicKey))

	handle := cgo.NewHandle(callbackState{ctx: ctx, emit: emit})
	defer handle.Delete()

	var errBuf [512]C.char
	ret := C.provanity_call_run(
		so.run,
		&raw,
		C.uintptr_t(handle),
		&errBuf[0],
		C.uint32_t(len(errBuf)),
	)
	if ret < 0 {
		return soError(errBuf[:])
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func multiDeviceRunConfig(so *cudaSO, cfg Config) (Config, []gpu.Device, bool, error) {
	if len(cfg.DeviceIDs) == 1 {
		return Config{}, nil, false, nil
	}
	if len(cfg.DeviceIDs) > MaxDevices {
		return Config{}, nil, false, fmt.Errorf("at most %d CUDA devices are supported", MaxDevices)
	}
	available, err := listDevicesLoaded(so)
	if err != nil {
		return Config{}, nil, false, err
	}
	if len(available) <= 1 && len(cfg.DeviceIDs) == 0 {
		return Config{}, nil, false, nil
	}

	byID := make(map[int]gpu.Device, len(available))
	for _, device := range available {
		byID[device.ID] = device
	}

	runCfg := cfg
	var selected []gpu.Device
	if len(cfg.DeviceIDs) == 0 {
		runCfg.DeviceIDs = make([]int, 0, len(available))
		for _, device := range available {
			runCfg.DeviceIDs = append(runCfg.DeviceIDs, device.ID)
			selected = append(selected, device)
		}
	} else {
		for _, id := range cfg.DeviceIDs {
			device, ok := byID[id]
			if !ok {
				return Config{}, nil, false, fmt.Errorf("CUDA device %d is not available", id)
			}
			selected = append(selected, device)
		}
	}
	if len(selected) <= 1 {
		return Config{}, nil, false, nil
	}
	return runCfg, selected, true, nil
}

func runMulti(ctx context.Context, so *cudaSO, cfg Config, devices []gpu.Device, emit EmitFunc) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	agg := newMultiRunAggregator(devices, emit, cancel)
	if agg.emitReady() {
		return nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(devices))
	for _, device := range devices {
		deviceID := device.ID
		deviceCfg := cfg
		deviceCfg.DeviceIDs = []int{deviceID}
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := runSingle(runCtx, so, deviceCfg, func(event gpu.Event) bool {
				return agg.emitDeviceEvent(deviceID, event)
			})
			if err != nil {
				agg.recordRunError(err)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := agg.err(); err != nil {
		return err
	}
	for err := range errs {
		if err != nil && !(agg.stopped() && errors.Is(err, context.Canceled)) {
			return err
		}
	}
	return nil
}

type multiRunAggregator struct {
	mu         sync.Mutex
	callbackMu sync.Mutex
	devices    []gpu.Device
	indexByID  map[int]int
	attempts   []uint64
	hashrates  []uint64
	elapsedMS  []uint64
	emit       EmitFunc
	cancel     context.CancelFunc
	stop       bool
	firstErr   error
}

func newMultiRunAggregator(devices []gpu.Device, emit EmitFunc, cancel context.CancelFunc) *multiRunAggregator {
	copied := append([]gpu.Device(nil), devices...)
	indexByID := make(map[int]int, len(copied))
	for i, device := range copied {
		indexByID[device.ID] = i
	}
	return &multiRunAggregator{
		devices:   copied,
		indexByID: indexByID,
		attempts:  make([]uint64, len(copied)),
		hashrates: make([]uint64, len(copied)),
		elapsedMS: make([]uint64, len(copied)),
		emit:      emit,
		cancel:    cancel,
	}
}

func (a *multiRunAggregator) emitReady() bool {
	return a.call(gpu.ReadyEvent{Type: gpu.EventReady, Devices: a.snapshotDevices()})
}

func (a *multiRunAggregator) emitDeviceEvent(deviceID int, event gpu.Event) bool {
	switch e := event.(type) {
	case gpu.ReadyEvent:
		a.mergeReady(deviceID, e)
		return false
	case gpu.ProgressEvent:
		return a.emitProgress(deviceID, e)
	case gpu.FoundEvent:
		return a.emitFound(deviceID, e)
	case gpu.ErrorEvent:
		a.setErr(fmt.Errorf("cuda error %s: %s", e.Code, e.Message))
		return a.call(e)
	case gpu.PhaseEvent:
		// Pass through verbatim. Multiple GPUs racing through the same phase
		// will cause the status line to flicker between devices; that's
		// acceptable for v1 since each phase event already carries its
		// originating DeviceID, and the host UI shows the latest message.
		return a.call(e)
	default:
		return a.call(event)
	}
}

func (a *multiRunAggregator) mergeReady(deviceID int, event gpu.ReadyEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	idx, ok := a.indexByID[deviceID]
	if !ok || len(event.Devices) == 0 {
		return
	}
	a.devices[idx] = mergeDeviceInfo(a.devices[idx], event.Devices[0])
}

func (a *multiRunAggregator) emitProgress(deviceID int, event gpu.ProgressEvent) bool {
	a.mu.Lock()
	a.updateDeviceLocked(deviceID, event)
	progress := gpu.ProgressEvent{
		Type:      gpu.EventProgress,
		ElapsedMS: maxUint64(a.elapsedMS),
		Attempts:  sumUint64(a.attempts),
		Hashrate:  sumUint64(a.hashrates),
		Devices:   append([]gpu.Device(nil), a.devices...),
	}
	progress.ElapsedSec = progress.ElapsedMS / 1000
	a.mu.Unlock()
	return a.call(progress)
}

func (a *multiRunAggregator) emitFound(deviceID int, event gpu.FoundEvent) bool {
	a.mu.Lock()
	if event.ElapsedMS == 0 && event.ElapsedSec > 0 {
		event.ElapsedMS = event.ElapsedSec * 1000
	}
	if idx, ok := a.indexByID[deviceID]; ok {
		a.attempts[idx] = event.Attempts
		a.elapsedMS[idx] = event.ElapsedMS
	}
	event.Attempts = sumUint64(a.attempts)
	elapsedMS := maxUint64(a.elapsedMS)
	if elapsedMS > 0 {
		event.ElapsedMS = elapsedMS
		event.ElapsedSec = elapsedMS / 1000
	}
	a.mu.Unlock()
	return a.call(event)
}

func (a *multiRunAggregator) updateDeviceLocked(deviceID int, event gpu.ProgressEvent) {
	idx, ok := a.indexByID[deviceID]
	if !ok {
		return
	}
	if event.ElapsedMS == 0 && event.ElapsedSec > 0 {
		event.ElapsedMS = event.ElapsedSec * 1000
	}
	a.attempts[idx] = event.Attempts
	a.hashrates[idx] = event.Hashrate
	a.elapsedMS[idx] = event.ElapsedMS
	if len(event.Devices) > 0 {
		a.devices[idx] = mergeDeviceInfo(a.devices[idx], event.Devices[0])
	}
	a.devices[idx].Hashrate = event.Hashrate
}

func (a *multiRunAggregator) snapshotDevices() []gpu.Device {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]gpu.Device(nil), a.devices...)
}

func (a *multiRunAggregator) call(event gpu.Event) bool {
	if a.emit == nil {
		return false
	}
	a.callbackMu.Lock()
	stop := a.emit(event)
	a.callbackMu.Unlock()
	if stop {
		a.stopRuns()
	}
	return stop
}

func (a *multiRunAggregator) recordRunError(err error) {
	if err == nil || (a.stopped() && errors.Is(err, context.Canceled)) {
		return
	}
	a.setErr(err)
	a.stopRuns()
}

func (a *multiRunAggregator) setErr(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.firstErr == nil {
		a.firstErr = err
	}
}

func (a *multiRunAggregator) err() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.firstErr
}

func (a *multiRunAggregator) stopped() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stop
}

func (a *multiRunAggregator) stopRuns() {
	a.mu.Lock()
	if a.stop {
		a.mu.Unlock()
		return
	}
	a.stop = true
	a.mu.Unlock()
	a.cancel()
}

func mergeDeviceInfo(base, update gpu.Device) gpu.Device {
	if update.ID != 0 || base.ID == 0 {
		base.ID = update.ID
	}
	if update.Name != "" {
		base.Name = update.Name
	}
	if update.GlobalMem != 0 {
		base.GlobalMem = update.GlobalMem
	}
	if update.Multiprocessors != 0 {
		base.Multiprocessors = update.Multiprocessors
	}
	if update.ComputeMajor != 0 {
		base.ComputeMajor = update.ComputeMajor
	}
	if update.ComputeMinor != 0 {
		base.ComputeMinor = update.ComputeMinor
	}
	if update.Hashrate != 0 {
		base.Hashrate = update.Hashrate
	}
	return base
}

func sumUint64(values []uint64) uint64 {
	var out uint64
	for _, value := range values {
		out += value
	}
	return out
}

func maxUint64(values []uint64) uint64 {
	var out uint64
	for _, value := range values {
		if value > out {
			out = value
		}
	}
	return out
}

func toCUDAConfig(cfg Config) (C.provanity_cuda_config, *C.char, error) {
	if cfg.PublicKeyHex == "" {
		return C.provanity_cuda_config{}, nil, fmt.Errorf("public key is required")
	}
	if len(cfg.DeviceIDs) > MaxDevices {
		return C.provanity_cuda_config{}, nil, fmt.Errorf("at most %d CUDA devices are supported", MaxDevices)
	}

	publicKey := C.CString(cfg.PublicKeyHex)
	raw := C.provanity_cuda_config{
		public_key_hex:       publicKey,
		mode:                 C.int32_t(cfg.Mode),
		batch_multiple:       C.uint32_t(cfg.BatchMultiple),
		progress_interval_ms: C.uint32_t(cfg.ProgressIntervalMS),
		work_size:            C.uint32_t(cfg.WorkSize),
		stop_score:           C.uint8_t(cfg.StopScore),
	}
	if cfg.Contract {
		raw.contract = 1
	}
	for i, value := range cfg.Pattern {
		raw.pattern[i] = C.uint8_t(value)
	}
	for i, value := range cfg.PatternMasks {
		raw.pattern_masks[i] = C.uint16_t(value)
	}
	raw.device_count = C.int32_t(len(cfg.DeviceIDs))
	for i, id := range cfg.DeviceIDs {
		if id < 0 {
			C.free(unsafe.Pointer(publicKey))
			return C.provanity_cuda_config{}, nil, fmt.Errorf("device id cannot be negative: %d", id)
		}
		raw.device_ids[i] = C.int32_t(id)
	}
	raw.tron_suffix_mod = C.uint64_t(cfg.TronSuffixMod)
	raw.tron_prefix_levels = C.uint8_t(cfg.TronPrefixLevels)
	raw.tron_suffix_len = C.uint8_t(cfg.TronSuffixLen)
	for i, value := range cfg.TronSuffixDigits {
		raw.tron_suffix_digits[i] = C.uint8_t(value)
	}
	for i, value := range cfg.TronPrefixLadder {
		raw.tron_prefix_ladder[i] = C.uint8_t(value)
	}
	return raw, publicKey, nil
}

func load() (*cudaSO, error) {
	loadOnce.Do(func() {
		loadedSO, loadErr = loadSharedLibrary()
	})
	return loadedSO, loadErr
}

func loadSharedLibrary() (*cudaSO, error) {
	if override := cudaLibraryOverride(); override != "" {
		so, err := openSharedLibrary(cudaLibraryCandidate{name: filepath.Base(override), path: override}, false)
		if err != nil {
			return nil, err
		}
		return so, nil
	}

	var errs []error
	for _, candidate := range cudaLibraryCandidates() {
		if _, err := os.Stat(candidate.path); err != nil {
			continue
		}
		so, err := openSharedLibrary(candidate, true)
		if err == nil {
			return so, nil
		}
		errs = append(errs, err)
	}

	for _, cacheRoot := range embeddedSOCacheRoots() {
		path, err := Extract(cacheRoot, StandardSOName)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", cacheRoot, StandardSOName, err))
			continue
		}
		so, err := openSharedLibrary(cudaLibraryCandidate{name: StandardSOName, path: path}, true)
		if err == nil {
			return so, nil
		}
		errs = append(errs, err)
	}

	return nil, fmt.Errorf("load CUDA shared library: %v", errs)
}

func cudaLibraryOverride() string {
	for _, env := range []string{envCUDASO, envCUDALib, envCUDADLL} {
		if value := strings.TrimSpace(os.Getenv(env)); value != "" {
			return value
		}
	}
	return ""
}

func cudaLibraryCandidates() []cudaLibraryCandidate {
	var candidates []cudaLibraryCandidate
	add := func(name, path string) {
		if path == "" {
			return
		}
		for _, existing := range candidates {
			if existing.path == path {
				return
			}
		}
		candidates = append(candidates, cudaLibraryCandidate{name: name, path: path})
	}

	addLocations := func(name string) {
		add(name, name)
		if exe, err := os.Executable(); err == nil {
			exeDir := filepath.Dir(exe)
			add(name, filepath.Join(exeDir, name))
			add(name, filepath.Join(exeDir, "cuda", name))
		}
		if cwd, err := os.Getwd(); err == nil {
			add(name, filepath.Join(cwd, name))
			add(name, filepath.Join(cwd, "cuda", name))
			add(name, filepath.Join(cwd, ".tmp", "cuda-backend", name))
			add(name, filepath.Join(cwd, "internal", "cuda", "assets", name))
		}
	}

	addLocations(StandardSOName)
	addLocations(CompatibilitySOName)
	return candidates
}

func embeddedSOCacheRoots() []string {
	var roots []string
	if paths, err := config.ResolvePaths(); err == nil {
		roots = append(roots, paths.CacheDir)
	}
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Join(filepath.Dir(exe), ".provanity"))
	}
	roots = append(roots, filepath.Join(os.TempDir(), "provanity"))
	return roots
}

func openSharedLibrary(candidate cudaLibraryCandidate, probe bool) (*cudaSO, error) {
	cPath := C.CString(candidate.path)
	defer C.free(unsafe.Pointer(cPath))

	var cErr *C.char
	handle := C.provanity_dlopen(cPath, &cErr)
	if handle == nil {
		return nil, dlError(cErr)
	}

	listDevices, err := lookupSymbol(handle, "provanity_cuda_list_devices")
	if err != nil {
		C.provanity_dlclose(handle)
		return nil, err
	}
	version, err := lookupSymbol(handle, "provanity_cuda_version")
	if err != nil {
		C.provanity_dlclose(handle)
		return nil, err
	}
	if err := ensureBackendVersion(candidate.path, version); err != nil {
		C.provanity_dlclose(handle)
		return nil, err
	}
	run, err := lookupSymbol(handle, "provanity_cuda_run")
	if err != nil {
		C.provanity_dlclose(handle)
		return nil, err
	}

	so := &cudaSO{
		path:        candidate.path,
		handle:      handle,
		version:     version,
		listDevices: listDevices,
		run:         run,
	}
	if probe {
		devices, err := listDevicesLoaded(so)
		if err != nil {
			C.provanity_dlclose(handle)
			return nil, fmt.Errorf("probe %s: %w", candidate.path, err)
		}
		if err := ensureBackendSupportsDevices(devices); err != nil {
			C.provanity_dlclose(handle)
			return nil, fmt.Errorf("probe %s: %w", candidate.path, err)
		}
	}
	return so, nil
}

func ensureBackendVersion(path string, versionFn unsafe.Pointer) error {
	var versionBuf [64]C.char
	ret := C.provanity_call_version(versionFn, &versionBuf[0], C.uint32_t(len(versionBuf)))
	if int32(ret) < 0 {
		return fmt.Errorf("load %s: CUDA backend version is empty", path)
	}
	version := C.GoString(&versionBuf[0])
	if version == cudaBackendVersion {
		return nil
	}
	return fmt.Errorf("load %s: CUDA backend version %q does not match %q; rebuild or update CUDA backend assets", path, version, cudaBackendVersion)
}

func ensureBackendSupportsDevices(devices []gpu.Device) error {
	for _, device := range devices {
		if device.ComputeMajor*10+device.ComputeMinor >= 75 {
			continue
		}
		return fmt.Errorf("provanity requires a Turing (sm_75) or newer GPU with driver >= 580; device %d is sm_%d%d — use 1inch/profanity2 for older hardware", device.ID, device.ComputeMajor, device.ComputeMinor)
	}
	return nil
}

func lookupSymbol(handle unsafe.Pointer, name string) (unsafe.Pointer, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.char
	symbol := C.provanity_dlsym(handle, cName, &cErr)
	if symbol == nil {
		return nil, fmt.Errorf("load symbol %s: %w", name, dlError(cErr))
	}
	return symbol, nil
}

func dlError(cErr *C.char) error {
	if cErr == nil {
		return fmt.Errorf("dynamic loader error")
	}
	defer C.free(unsafe.Pointer(cErr))
	return fmt.Errorf("%s", C.GoString(cErr))
}

func soError(buf []C.char) error {
	msg := C.GoString(&buf[0])
	if msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return fmt.Errorf("CUDA backend call failed")
}
