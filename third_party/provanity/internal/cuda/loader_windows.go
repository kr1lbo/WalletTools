//go:build windows

package cuda

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/woomai/provanity/internal/config"
	"github.com/woomai/provanity/internal/gpu"
	"golang.org/x/sys/windows"
)

const (
	envCUDADLL = "PROVANITY_CUDA_DLL"
)

type cudaDevice struct {
	ID              int32
	Name            [128]byte
	GlobalMem       uint64
	Multiprocessors int32
	ComputeMajor    int32
	ComputeMinor    int32
	Hashrate        uint64
}

type cudaEvent struct {
	Type         int32
	_            [4]byte
	ElapsedSec   uint64
	ElapsedMS    uint64
	Attempts     uint64
	Hashrate     uint64
	Score        int32
	DeviceCount  int32
	Devices      [MaxDevices]cudaDevice
	Offset       [65]byte
	Address      [41]byte
	ErrorCode    [64]byte
	ErrorMessage [256]byte
}

// cudaConfig mirrors provanity_cuda_config byte-for-byte. TronSuffixMod is
// placed while still 8-aligned (offset 136) so no interior padding is needed;
// the trailing [5]byte rounds the struct to 800 bytes to match the C layout.
type cudaConfig struct {
	PublicKeyHex       uintptr
	Mode               int32
	Contract           int32
	Pattern            [PatternLen]byte
	PatternMasks       [PatternLen]uint16
	DeviceIDs          [MaxDevices]int32
	DeviceCount        int32
	BatchMultiple      uint32
	ProgressIntervalMS uint32
	WorkSize           uint32
	TronSuffixMod      uint64
	StopScore          byte
	TronPrefixLevels   byte
	TronSuffixLen      byte
	TronSuffixDigits   [TronMaxSuffixLen]byte
	TronPrefixLadder   [TronPrefixLadderLen]byte
	_                  [5]byte
}

type cudaDLL struct {
	path        string
	dll         *windows.DLL
	version     *windows.Proc
	listDevices *windows.Proc
	run         *windows.Proc
}

type cudaLibraryCandidate struct {
	name string
	path string
}

type callbackState struct {
	ctx  context.Context
	emit EmitFunc
}

var (
	loadOnce    sync.Once
	loadedDLL   *cudaDLL
	loadErr     error
	callbackSeq atomic.Uintptr
	callbacks   sync.Map
)

func ListDevices() ([]gpu.Device, error) {
	dll, err := load()
	if err != nil {
		return nil, err
	}
	return listDevicesLoaded(dll)
}

func listDevicesLoaded(dll *cudaDLL) ([]gpu.Device, error) {
	var raw [MaxDevices]cudaDevice
	var errBuf [512]byte
	ret, _, callErr := dll.listDevices.Call(
		uintptr(unsafe.Pointer(&raw[0])),
		uintptr(MaxDevices),
		uintptr(unsafe.Pointer(&errBuf[0])),
		uintptr(len(errBuf)),
	)
	if int32(ret) < 0 {
		return nil, dllError(errBuf[:], callErr)
	}

	devices := make([]gpu.Device, 0, int(ret))
	for i := 0; i < int(ret); i++ {
		devices = append(devices, convertDevice(raw[i]))
	}
	return devices, nil
}

func Run(ctx context.Context, cfg Config, emit EmitFunc) error {
	dll, err := load()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	publicKey, err := windows.BytePtrFromString(cfg.PublicKeyHex)
	if err != nil {
		return fmt.Errorf("encode public key: %w", err)
	}
	raw, err := toCUDAConfig(cfg, publicKey)
	if err != nil {
		return err
	}

	id := callbackSeq.Add(1)
	if id == 0 {
		id = callbackSeq.Add(1)
	}
	callbacks.Store(id, callbackState{ctx: ctx, emit: emit})
	defer callbacks.Delete(id)

	var errBuf [512]byte
	ret, _, callErr := dll.run.Call(
		uintptr(unsafe.Pointer(&raw)),
		windows.NewCallback(cudaCallback),
		id,
		uintptr(unsafe.Pointer(&errBuf[0])),
		uintptr(len(errBuf)),
	)
	if int32(ret) < 0 {
		return dllError(errBuf[:], callErr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func toCUDAConfig(cfg Config, publicKey *byte) (cudaConfig, error) {
	if cfg.PublicKeyHex == "" {
		return cudaConfig{}, fmt.Errorf("public key is required")
	}
	if len(cfg.DeviceIDs) > MaxDevices {
		return cudaConfig{}, fmt.Errorf("at most %d CUDA devices are supported", MaxDevices)
	}
	raw := cudaConfig{
		PublicKeyHex:       uintptr(unsafe.Pointer(publicKey)),
		Mode:               int32(cfg.Mode),
		Pattern:            cfg.Pattern,
		PatternMasks:       cfg.PatternMasks,
		BatchMultiple:      cfg.BatchMultiple,
		ProgressIntervalMS: cfg.ProgressIntervalMS,
		WorkSize:           cfg.WorkSize,
		TronSuffixMod:      cfg.TronSuffixMod,
		StopScore:          cfg.StopScore,
		TronPrefixLevels:   cfg.TronPrefixLevels,
		TronSuffixLen:      cfg.TronSuffixLen,
		TronSuffixDigits:   cfg.TronSuffixDigits,
		TronPrefixLadder:   cfg.TronPrefixLadder,
	}
	if cfg.Contract {
		raw.Contract = 1
	}
	raw.DeviceCount = int32(len(cfg.DeviceIDs))
	for i, id := range cfg.DeviceIDs {
		if id < 0 {
			return cudaConfig{}, fmt.Errorf("device id cannot be negative: %d", id)
		}
		raw.DeviceIDs[i] = int32(id)
	}
	return raw, nil
}

func cudaCallback(event *cudaEvent, userData uintptr) uintptr {
	stateValue, ok := callbacks.Load(userData)
	if !ok {
		return 1
	}
	state := stateValue.(callbackState)
	if err := state.ctx.Err(); err != nil {
		return 1
	}
	converted := convertEvent(event)
	if state.emit != nil && state.emit(converted) {
		return 1
	}
	if err := state.ctx.Err(); err != nil {
		return 1
	}
	return 0
}

func convertEvent(event *cudaEvent) gpu.Event {
	switch event.Type {
	case 1:
		return gpu.ReadyEvent{Type: gpu.EventReady, Devices: convertDevices(event)}
	case 2:
		return gpu.ProgressEvent{
			Type:       gpu.EventProgress,
			ElapsedSec: event.ElapsedSec,
			ElapsedMS:  event.ElapsedMS,
			Attempts:   event.Attempts,
			Hashrate:   event.Hashrate,
			Devices:    convertDevices(event),
		}
	case 3:
		return gpu.FoundEvent{
			Type:       gpu.EventFound,
			Offset:     cString(event.Offset[:]),
			Address:    cString(event.Address[:]),
			ElapsedSec: event.ElapsedSec,
			ElapsedMS:  event.ElapsedMS,
			Attempts:   event.Attempts,
		}
	case 4:
		return gpu.ErrorEvent{
			Type:    gpu.EventError,
			Code:    cString(event.ErrorCode[:]),
			Message: cString(event.ErrorMessage[:]),
		}
	case 5:
		deviceID := 0
		if event.DeviceCount > 0 {
			deviceID = int(event.Devices[0].ID)
		}
		return gpu.PhaseEvent{
			Type:     gpu.EventPhase,
			DeviceID: deviceID,
			Phase:    cString(event.ErrorCode[:]),
			Message:  cString(event.ErrorMessage[:]),
			Value:    event.Attempts,
		}
	default:
		return gpu.ErrorEvent{Type: gpu.EventError, Code: "unknown_cuda_event", Message: "unknown CUDA event"}
	}
}

func convertDevices(event *cudaEvent) []gpu.Device {
	count := int(event.DeviceCount)
	if count < 0 {
		count = 0
	}
	if count > MaxDevices {
		count = MaxDevices
	}
	devices := make([]gpu.Device, 0, count)
	for i := 0; i < count; i++ {
		devices = append(devices, convertDevice(event.Devices[i]))
	}
	return devices
}

func convertDevice(device cudaDevice) gpu.Device {
	return gpu.Device{
		ID:              int(device.ID),
		Name:            cString(device.Name[:]),
		GlobalMem:       device.GlobalMem,
		Multiprocessors: int(device.Multiprocessors),
		ComputeMajor:    int(device.ComputeMajor),
		ComputeMinor:    int(device.ComputeMinor),
		Hashrate:        device.Hashrate,
	}
}

func load() (*cudaDLL, error) {
	loadOnce.Do(func() {
		loadedDLL, loadErr = loadDLL()
	})
	return loadedDLL, loadErr
}

func loadDLL() (*cudaDLL, error) {
	if override := strings.TrimSpace(os.Getenv(envCUDADLL)); override != "" {
		return openDLL(cudaLibraryCandidate{name: filepath.Base(override), path: override}, false)
	}

	var errs []error
	for _, candidate := range cudaLibraryCandidates() {
		if _, err := os.Stat(candidate.path); err != nil {
			continue
		}
		dll, err := openDLL(candidate, true)
		if err == nil {
			return dll, nil
		}
		errs = append(errs, err)
	}

	for _, cacheRoot := range embeddedDLLCacheRoots() {
		path, err := Extract(cacheRoot, StandardDLLName)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", cacheRoot, StandardDLLName, err))
			continue
		}
		dll, err := openDLL(cudaLibraryCandidate{name: StandardDLLName, path: path}, true)
		if err == nil {
			return dll, nil
		}
		errs = append(errs, err)
	}

	return nil, fmt.Errorf("load CUDA DLL: %v", errs)
}

func openDLL(candidate cudaLibraryCandidate, probe bool) (*cudaDLL, error) {
	dll, err := windows.LoadDLL(candidate.path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", candidate.path, err)
	}
	loaded := &cudaDLL{
		path: candidate.path,
		dll:  dll,
	}
	loaded.version, err = dll.FindProc("provanity_cuda_version")
	if err != nil {
		dll.Release()
		return nil, fmt.Errorf("load %s: %w", candidate.path, err)
	}
	loaded.listDevices, err = dll.FindProc("provanity_cuda_list_devices")
	if err != nil {
		dll.Release()
		return nil, fmt.Errorf("load %s: %w", candidate.path, err)
	}
	loaded.run, err = dll.FindProc("provanity_cuda_run")
	if err != nil {
		dll.Release()
		return nil, fmt.Errorf("load %s: %w", candidate.path, err)
	}
	if err := ensureBackendVersion(loaded); err != nil {
		dll.Release()
		return nil, err
	}
	if probe {
		devices, err := listDevicesLoaded(loaded)
		if err != nil {
			dll.Release()
			return nil, fmt.Errorf("probe %s: %w", candidate.path, err)
		}
		if err := ensureBackendSupportsDevices(devices); err != nil {
			dll.Release()
			return nil, fmt.Errorf("probe %s: %w", candidate.path, err)
		}
	}
	return loaded, nil
}

func ensureBackendVersion(dll *cudaDLL) error {
	var versionBuf [64]byte
	ret, _, _ := dll.version.Call(
		uintptr(unsafe.Pointer(&versionBuf[0])),
		uintptr(len(versionBuf)),
	)
	if int32(ret) < 0 {
		return fmt.Errorf("load %s: CUDA backend version is empty", dll.path)
	}
	version := cString(versionBuf[:])
	if version == cudaBackendVersion {
		return nil
	}
	return fmt.Errorf("load %s: CUDA backend version %q does not match %q; rebuild or update CUDA backend assets", dll.path, version, cudaBackendVersion)
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

	addLocations(StandardDLLName)
	addLocations(CompatibilityDLLName)
	return candidates
}

func embeddedDLLCacheRoots() []string {
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

func dllError(buf []byte, callErr error) error {
	msg := cString(buf)
	if msg != "" {
		return fmt.Errorf("%s", msg)
	}
	if callErr != nil && callErr != windows.ERROR_SUCCESS {
		return callErr
	}
	return fmt.Errorf("CUDA backend call failed")
}

func cString(buf []byte) string {
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}
