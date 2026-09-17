//go:build linux && !cudaembed

package cuda

import "fmt"

func Extract(cacheRoot, name string) (string, error) {
	return "", fmt.Errorf("embedded CUDA shared library %s is not included in this build", name)
}
