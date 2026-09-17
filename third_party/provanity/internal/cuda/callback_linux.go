//go:build linux && cgo

package cuda

/*
#cgo CFLAGS: -I${SRCDIR}/native
#include <stdint.h>
#include "provanity_cuda.h"
*/
import "C"

import (
	"context"
	"runtime/cgo"
	"unsafe"

	"github.com/woomai/provanity/internal/gpu"
)

type callbackState struct {
	ctx  context.Context
	emit EmitFunc
}

//export provanityCudaCallback
func provanityCudaCallback(event *C.provanity_cuda_event, userData unsafe.Pointer) C.int32_t {
	if event == nil {
		return 1
	}
	handle := cgo.Handle(uintptr(userData))
	stateValue := handle.Value()
	state, ok := stateValue.(callbackState)
	if !ok {
		return 1
	}
	if err := state.ctx.Err(); err != nil {
		return 1
	}
	converted := convertCEvent(event)
	if state.emit != nil && state.emit(converted) {
		return 1
	}
	if err := state.ctx.Err(); err != nil {
		return 1
	}
	return 0
}

func convertCEvent(event *C.provanity_cuda_event) gpu.Event {
	switch event._type {
	case 1:
		return gpu.ReadyEvent{Type: gpu.EventReady, Devices: convertCDevices(event)}
	case 2:
		return gpu.ProgressEvent{
			Type:       gpu.EventProgress,
			ElapsedSec: uint64(event.elapsed_sec),
			ElapsedMS:  uint64(event.elapsed_ms),
			Attempts:   uint64(event.attempts),
			Hashrate:   uint64(event.hashrate),
			Devices:    convertCDevices(event),
		}
	case 3:
		return gpu.FoundEvent{
			Type:       gpu.EventFound,
			Offset:     C.GoString(&event.offset[0]),
			Address:    C.GoString(&event.address[0]),
			ElapsedSec: uint64(event.elapsed_sec),
			ElapsedMS:  uint64(event.elapsed_ms),
			Attempts:   uint64(event.attempts),
		}
	case 4:
		return gpu.ErrorEvent{
			Type:    gpu.EventError,
			Code:    C.GoString(&event.error_code[0]),
			Message: C.GoString(&event.error_message[0]),
		}
	case 5:
		deviceID := 0
		if event.device_count > 0 {
			deviceID = int(event.devices[0].id)
		}
		return gpu.PhaseEvent{
			Type:     gpu.EventPhase,
			DeviceID: deviceID,
			Phase:    C.GoString(&event.error_code[0]),
			Message:  C.GoString(&event.error_message[0]),
			Value:    uint64(event.attempts),
		}
	default:
		return gpu.ErrorEvent{Type: gpu.EventError, Code: "unknown_cuda_event", Message: "unknown CUDA event"}
	}
}

func convertCDevices(event *C.provanity_cuda_event) []gpu.Device {
	count := int(event.device_count)
	if count < 0 {
		count = 0
	}
	if count > MaxDevices {
		count = MaxDevices
	}
	devices := make([]gpu.Device, 0, count)
	for i := 0; i < count; i++ {
		devices = append(devices, convertCDevice(event.devices[i]))
	}
	return devices
}

func convertCDevice(device C.provanity_cuda_device) gpu.Device {
	return gpu.Device{
		ID:              int(device.id),
		Name:            C.GoString(&device.name[0]),
		GlobalMem:       uint64(device.global_mem),
		Multiprocessors: int(device.multiprocessors),
		ComputeMajor:    int(device.compute_major),
		ComputeMinor:    int(device.compute_minor),
		Hashrate:        uint64(device.hashrate),
	}
}
