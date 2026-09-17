package cudagen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Options configures the external CUDA producer. Its stdout protocol is a
// sequence of raw 32-byte private-key candidates. Diagnostics belong on stderr.
type Options struct {
	Executable string
	Device     int
	BatchSize  int
}

// Stream starts the CUDA helper and streams candidates until ctx is cancelled.
// The helper is deliberately a separate process so the normal WalletTools
// binary remains portable and has no CUDA runtime dependency.
func Stream(ctx context.Context, opt Options) (<-chan [32]byte, <-chan error, error) {
	if opt.Executable == "" {
		return nil, nil, errors.New("cuda executable is empty")
	}
	if opt.Device < 0 {
		return nil, nil, errors.New("cuda device must be non-negative")
	}
	if opt.BatchSize <= 0 {
		return nil, nil, errors.New("cuda batch size must be positive")
	}

	executable, err := resolveExecutable(opt.Executable)
	if err != nil {
		return nil, nil, err
	}

	cmd := exec.CommandContext(ctx, executable,
		"--device", strconv.Itoa(opt.Device),
		"--batch", strconv.Itoa(opt.BatchSize),
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("cuda stdout: %w", err)
	}
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start CUDA helper %q: %w (build it with scripts/build-cuda.ps1)", executable, err)
	}

	out := make(chan [32]byte, 1024)
	done := make(chan error, 1)
	go func() {
		defer close(out)
		var readErr error
		for {
			var key [32]byte
			if _, err := io.ReadFull(stdout, key[:]); err != nil {
				readErr = err
				break
			}
			select {
			case out <- key:
			case <-ctx.Done():
				readErr = ctx.Err()
				break
			}
			if readErr != nil {
				break
			}
		}

		waitErr := cmd.Wait()
		if ctx.Err() != nil {
			done <- ctx.Err()
		} else if waitErr != nil {
			done <- fmt.Errorf("CUDA helper stopped: %w: %s", waitErr, stderr.String())
		} else if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			done <- fmt.Errorf("read CUDA helper: %w", readErr)
		} else {
			done <- errors.New("CUDA helper stopped unexpectedly")
		}
		close(done)
	}()
	return out, done, nil
}

func resolveExecutable(executable string) (string, error) {
	if executable == "" {
		return "", errors.New("cuda executable is empty")
	}
	// os/exec intentionally refuses a bare executable name resolved through
	// the current directory (ErrDot). Portable installs keep both files together.
	if filepath.Base(executable) == executable {
		if absolute, err := filepath.Abs(executable); err == nil {
			if _, statErr := os.Stat(absolute); statErr == nil {
				return absolute, nil
			}
		}
		if strings.EqualFold(executable, "wallettools-cuda.exe") {
			return embeddedExecutable()
		}
	}
	return executable, nil
}

// limitedBuffer prevents a faulty helper from consuming unbounded memory.
type limitedBuffer struct{ b []byte }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const limit = 16 << 10
	if len(b.b) < limit {
		n := limit - len(b.b)
		if n > len(p) {
			n = len(p)
		}
		b.b = append(b.b, p[:n]...)
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return string(b.b) }
