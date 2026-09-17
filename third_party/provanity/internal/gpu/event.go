package gpu

type EventType string

const (
	EventReady    EventType = "ready"
	EventProgress EventType = "progress"
	EventFound    EventType = "found"
	EventError    EventType = "error"
	EventPhase    EventType = "phase"
)

type Event interface {
	EventType() EventType
}

type Device struct {
	ID              int    `json:"id"`
	Name            string `json:"name,omitempty"`
	GlobalMem       uint64 `json:"global_mem,omitempty"`
	Multiprocessors int    `json:"multiprocessors,omitempty"`
	ComputeMajor    int    `json:"compute_major,omitempty"`
	ComputeMinor    int    `json:"compute_minor,omitempty"`
	Hashrate        uint64 `json:"hashrate,omitempty"`
}

type ReadyEvent struct {
	Type    EventType `json:"type"`
	Devices []Device  `json:"devices"`
}

func (e ReadyEvent) EventType() EventType { return e.Type }

type ProgressEvent struct {
	Type              EventType `json:"type"`
	ElapsedSec        uint64    `json:"elapsed_sec"`
	ElapsedMS         uint64    `json:"elapsed_ms,omitempty"`
	Attempts          uint64    `json:"attempts"`
	Hashrate          uint64    `json:"hashrate"`
	HashrateUncertain bool      `json:"hashrate_uncertain,omitempty"`
	Devices           []Device  `json:"devices,omitempty"`
}

func (e ProgressEvent) EventType() EventType { return e.Type }

type FoundEvent struct {
	Type       EventType `json:"type"`
	Offset     string    `json:"offset"`
	Address    string    `json:"address"`
	ElapsedSec uint64    `json:"elapsed_sec"`
	ElapsedMS  uint64    `json:"elapsed_ms,omitempty"`
	Attempts   uint64    `json:"attempts"`
}

func (e FoundEvent) EventType() EventType { return e.Type }

type ErrorEvent struct {
	Type    EventType `json:"type"`
	Code    string    `json:"code,omitempty"`
	Message string    `json:"message"`
}

func (e ErrorEvent) EventType() EventType { return e.Type }

// PhaseEvent reports a long-running setup step that the CUDA backend is about
// to start (memory allocation, building precomp tables, initializing per-lane
// state). Emitted before the work begins so the host UI can keep its status
// line moving while a single synchronous kernel runs.
type PhaseEvent struct {
	Type     EventType `json:"type"`
	DeviceID int       `json:"device_id"`
	Phase    string    `json:"phase"`
	Message  string    `json:"message"`
	Value    uint64    `json:"value,omitempty"`
}

func (e PhaseEvent) EventType() EventType { return e.Type }
