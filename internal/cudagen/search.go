package cudagen

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
)

type Candidate struct {
	Address    string
	PrivateKey string
	Attempts   uint64
	Hashrate   uint64
}

type workerEvent struct {
	Event      string `json:"event"`
	Message    string `json:"message"`
	Address    string `json:"address"`
	PrivateKey string `json:"private_key"`
	Attempts   uint64 `json:"attempts"`
	Hashrate   uint64 `json:"hashrate"`
	Stats      struct {
		Attempts uint64 `json:"attempts"`
		Hashrate uint64 `json:"hashrate"`
	} `json:"stats"`
}

// Search runs a full CUDA vanity search. Unlike Stream, candidates are not
// transferred to Go: secp256k1, Keccak and matching all happen on the GPU.
func Search(ctx context.Context, opt Options, pattern string, progress func(attempts, hashrate uint64)) (Candidate, error) {
	executable, err := resolveExecutable(opt.Executable)
	if err != nil {
		return Candidate{}, err
	}
	args := []string{
		"--mode", "evm",
		"--pattern", pattern,
		"--devices", strconv.Itoa(opt.Device),
		"--progress-interval", "1000",
		"--work-size", "128",
	}
	if opt.BatchSize > 0 {
		args = append(args, "--batch-multiple", strconv.Itoa(opt.BatchSize))
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Candidate{}, fmt.Errorf("CUDA worker stdout: %w", err)
	}
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Candidate{}, fmt.Errorf("start CUDA worker %q: %w", executable, err)
	}

	var result Candidate
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var event workerEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		switch event.Event {
		case "progress":
			if progress != nil {
				progress(event.Attempts, event.Hashrate)
			}
		case "error":
			_ = cmd.Wait()
			return Candidate{}, fmt.Errorf("CUDA worker: %s", event.Message)
		case "result":
			result = Candidate{Address: event.Address, PrivateKey: event.PrivateKey, Attempts: event.Stats.Attempts, Hashrate: event.Stats.Hashrate}
		}
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return Candidate{}, ctx.Err()
	}
	if err := scanner.Err(); err != nil {
		return Candidate{}, fmt.Errorf("read CUDA worker: %w", err)
	}
	if waitErr != nil {
		return Candidate{}, fmt.Errorf("CUDA worker stopped: %w: %s", waitErr, stderr.String())
	}
	if result.PrivateKey == "" || result.Address == "" {
		return Candidate{}, errors.New("CUDA worker exited without a result")
	}
	return result, nil
}
