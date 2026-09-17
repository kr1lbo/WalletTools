//go:build !windows && !(linux && cgo)

package cuda

import (
	"context"
	"fmt"

	"github.com/woomai/provanity/internal/gpu"
)

func ListDevices() ([]gpu.Device, error) {
	return nil, fmt.Errorf("CUDA backend is not available in this build")
}

func Run(ctx context.Context, cfg Config, emit EmitFunc) error {
	return fmt.Errorf("CUDA backend is not available in this build")
}
