//go:build windows && !cudaembed

package cuda

import "fmt"

func Extract(cacheRoot, name string) (string, error) {
	return "", fmt.Errorf("embedded CUDA DLL %s is not included in this build", name)
}
