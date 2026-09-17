// Command provanity-worker is the standalone headless GPU worker binary.
// It runs vanity searches without any interactive UI and is intended for
// supervised, scripted, or remote orchestration deployments.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/woomai/provanity/internal/worker"
)

func main() {
	if err := worker.NewRootCommand().Execute(); err != nil {
		var suppressor interface{ SuppressMainPrint() bool }
		if !errors.As(err, &suppressor) || !suppressor.SuppressMainPrint() {
			fmt.Fprintln(os.Stderr, err)
		}
		var exitCoder interface{ ExitCode() int }
		if errors.As(err, &exitCoder) {
			os.Exit(exitCoder.ExitCode())
		}
		os.Exit(1)
	}
}
