package generator

import (
	"strings"
	"testing"

	"WalletTools/pkg/config"
)

func TestGPUPatternCompilesSpecificPrefixAndSuffix(t *testing.T) {
	cfg := &config.PatternsConfig{Specific: []config.SpecificPattern{{Prefix: "Beef", Suffix: "0000", Final: true}}}
	got, err := gpuPatterns(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := "pattern:beef" + strings.Repeat("X", 32) + "0000"
	if len(got) != 1 || got[0].Pattern != want || !got[0].Final || got[0].Kind != "specific" {
		t.Fatalf("got %+v, want %q final=true", got, want)
	}
}

func TestGPURegexpCompilesVariablePrefixAndSuffix(t *testing.T) {
	cfg := &config.PatternsConfig{Regexp: []config.RegexpPattern{{
		Pattern: "^0x[0-9]{4,6}.*[a-f]{4,6}$", Final: true,
	}}}
	got, err := gpuPatterns(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "regexp" || !got[0].Approx || !got[0].Final {
		t.Fatalf("unexpected target: %+v", got)
	}
	parts := strings.Split(strings.TrimPrefix(got[0].Pattern, "mask:"), ",")
	if len(parts) != 40 || parts[0] != "03ff" || parts[3] != "03ff" || parts[36] != "fc00" || parts[39] != "fc00" {
		t.Fatalf("unexpected mask: %v", parts)
	}
}

func TestGPURegexpRejectsBackreference(t *testing.T) {
	cfg := &config.PatternsConfig{Regexp: []config.RegexpPattern{{Pattern: `^0x([a-f])\1`}}}
	if _, err := gpuPatterns(cfg); err == nil {
		t.Fatal("GPU accepted a regexp syntax unsupported by Go")
	}
}

func TestGPUPatternRejectsCPUOnlyMatchers(t *testing.T) {
	cfg := &config.PatternsConfig{
		Specific: []config.SpecificPattern{{Prefix: "beef"}},
		Edges:    config.EdgeConfig{MinCount: 4},
	}
	if _, err := gpuPatterns(cfg); err == nil {
		t.Fatal("GPU accepted a matcher it cannot execute")
	}
}

func TestGPUPatternCompilesLinkedSymmetricGroups(t *testing.T) {
	cfg := &config.PatternsConfig{Symmetric: []config.SymmetricPattern{{Prefix: "XXXX", Suffix: "YYYY", Final: true}}}
	got, err := gpuPatterns(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := "pattern:" + strings.Repeat("Y", 4) + strings.Repeat("X", 32) + strings.Repeat("Z", 4)
	if len(got) != 1 || got[0].Pattern != want || got[0].Kind != "symmetric" || !got[0].Final {
		t.Fatalf("got %+v want pattern %q", got, want)
	}
}
