package vanity

import (
	"bytes"
	"strings"
	"testing"

	"github.com/woomai/provanity/internal/crypto"
)

func TestParsePatternWithWildcards(t *testing.T) {
	pattern, err := ParsePattern("pattern:Deadx*?Xbeef")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}

	if pattern.String() != "pattern:deadXXXXbeef" {
		t.Fatalf("description = %q", pattern.String())
	}
	// "deed0000beef" mismatches pattern position 2 ('e' vs 'a') so 7 of 8
	// concrete positions match; partial scores let the TUI show progress.
	if got := pattern.ScoreAddressHex("deed0000beef"); got != 7 {
		t.Fatalf("pattern partial score = %d, want 7", got)
	}
	if got := pattern.ScoreAddressHex("dead1234beef"); got != 8 {
		t.Fatalf("pattern full score = %d, want 8", got)
	}
	if pattern.TargetScore() != 8 {
		t.Fatalf("target score = %d", pattern.TargetScore())
	}
	if !pattern.MatchesAddressHex("0xdead1234beef0000000000000000000000000000") {
		t.Fatal("pattern did not match wildcard address")
	}
	if pattern.MatchesAddressHex("0xdeed1234beef0000000000000000000000000000") {
		t.Fatal("pattern matched wrong concrete nibble")
	}
}

func TestPatternAcceptsAllWildcardSpellings(t *testing.T) {
	for _, raw := range []string{"pattern:aXc", "pattern:axc", "pattern:a*c", "pattern:a?c"} {
		pattern, err := ParsePattern(raw)
		if err != nil {
			t.Fatalf("ParsePattern(%q) error = %v", raw, err)
		}
		if pattern.Value != "aXc" {
			t.Fatalf("ParsePattern(%q) value = %q", raw, pattern.Value)
		}
		if !pattern.MatchesAddressHex("abc") {
			t.Fatalf("ParsePattern(%q) did not match wildcard address", raw)
		}
		if pattern.MatchesAddressHex("agc") {
			t.Fatalf("ParsePattern(%q) matched non-hex wildcard nibble", raw)
		}
	}
}

func TestParseLeadingPattern(t *testing.T) {
	pattern, err := ParsePattern("leading:F:4")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}

	if pattern.String() != "leading:f:4" {
		t.Fatalf("description = %q", pattern.String())
	}
	if !pattern.MatchesAddressHex("ffffabcd") {
		t.Fatal("leading pattern did not match expected address")
	}
	if pattern.MatchesAddressHex("fffabcde") {
		t.Fatal("leading pattern matched short run")
	}
	if got := pattern.ScoreAddressHex("fffffabcd"); got != 5 {
		t.Fatalf("leading score = %d", got)
	}
	if pattern.TargetScore() != 4 {
		t.Fatalf("target score = %d", pattern.TargetScore())
	}
}

func TestParseTronPrefix(t *testing.T) {
	pattern, err := ParseTronPattern("prefix:AB")
	if err != nil {
		t.Fatalf("ParseTronPattern() error = %v", err)
	}
	if pattern.Kind != PatternTronPrefix {
		t.Fatalf("kind = %q", pattern.Kind)
	}
	if pattern.Value != "TAB" {
		t.Fatalf("value = %q, want TAB", pattern.Value)
	}
	if pattern.String() != "prefix:AB" {
		t.Fatalf("description = %q", pattern.String())
	}
	if pattern.TargetScore() != 2 {
		t.Fatalf("target score = %d, want 2", pattern.TargetScore())
	}
	if !pattern.MatchesAddress("TAB12000000000000000000000000000000") {
		t.Fatal("Tron prefix did not match address")
	}
	if pattern.MatchesAddress("TAC12000000000000000000000000000000") {
		t.Fatal("Tron prefix matched wrong character")
	}
	// Longest leading run after T: "TAX..." matches only 'A'.
	if got := pattern.ScoreAddress("TAX12000000000000000000000000000000"); got != 1 {
		t.Fatalf("prefix score = %d, want 1", got)
	}
	if got := pattern.ScoreAddress("TAB12000000000000000000000000000000"); got != 2 {
		t.Fatalf("prefix score = %d, want 2", got)
	}
}

func TestParseTronPrefixStripsImplicitT(t *testing.T) {
	withT, err := ParseTronPattern("prefix:TAB")
	if err != nil {
		t.Fatalf("ParseTronPattern(prefix:TAB) error = %v", err)
	}
	if withT.Value != "TAB" || withT.Count != 2 {
		t.Fatalf("prefix:TAB -> value %q count %d, want TAB/2", withT.Value, withT.Count)
	}
}

func TestParseTronSuffix(t *testing.T) {
	pattern, err := ParseTronPattern("suffix:xyz")
	if err != nil {
		t.Fatalf("ParseTronPattern() error = %v", err)
	}
	if pattern.Kind != PatternTronSuffix {
		t.Fatalf("kind = %q", pattern.Kind)
	}
	if pattern.Value != "xyz" || pattern.Count != 3 {
		t.Fatalf("value/count = %q/%d, want xyz/3", pattern.Value, pattern.Count)
	}
	if pattern.String() != "suffix:xyz" {
		t.Fatalf("description = %q", pattern.String())
	}
	if !pattern.MatchesAddress("T00000000000000000000000000000xyz") {
		t.Fatal("Tron suffix did not match address")
	}
	if pattern.MatchesAddress("T00000000000000000000000000000xyw") {
		t.Fatal("Tron suffix matched wrong trailing character")
	}
	// Longest trailing run: "...ayz" matches "yz" but not the 'x'.
	if got := pattern.ScoreAddress("T00000000000000000000000000000ayz"); got != 2 {
		t.Fatalf("suffix score = %d, want 2", got)
	}
	if got := pattern.ScoreAddress("T00000000000000000000000000000xyz"); got != 3 {
		t.Fatalf("suffix score = %d, want 3", got)
	}
}

func TestParseTronPatternRejectsInvalidValues(t *testing.T) {
	for _, raw := range []string{
		"leading:T:2",
		"pattern:TAB",
		"prefix:",
		"prefix:T",
		"prefix:0",
		"prefix:O",
		"prefix:l",
		"suffix:",
		"suffix:0",
		"suffix:O",
		"prefix:" + strings.Repeat("a", MaxTronConcretePos+1),
		"suffix:" + strings.Repeat("a", MaxTronSuffixLen+1),
	} {
		if _, err := ParseTronPattern(raw); err == nil {
			t.Fatalf("ParseTronPattern(%q) succeeded, want error", raw)
		}
	}
}

func TestParseTronPrefixRejectsUnreachable(t *testing.T) {
	// "T1..." sorts below the smallest Tron address (2nd char floor is '9');
	// "Tz..." sorts above the largest (2nd char ceiling is 'Z').
	for _, raw := range []string{"prefix:1abc", "prefix:z", "prefix:2zz"} {
		if _, err := ParseTronPattern(raw); err == nil {
			t.Fatalf("ParseTronPattern(%q) succeeded, want unreachable-prefix error", raw)
		}
	}
}

func TestTronAddressRangeConstants(t *testing.T) {
	min, err := crypto.TronAddressFromEVMAddress(make([]byte, crypto.AddressSize))
	if err != nil {
		t.Fatalf("TronAddressFromEVMAddress(zero) error = %v", err)
	}
	max, err := crypto.TronAddressFromEVMAddress(bytes.Repeat([]byte{0xff}, crypto.AddressSize))
	if err != nil {
		t.Fatalf("TronAddressFromEVMAddress(0xff) error = %v", err)
	}
	if min != tronAddrMin {
		t.Fatalf("tronAddrMin = %q, want %q", tronAddrMin, min)
	}
	if max != tronAddrMax {
		t.Fatalf("tronAddrMax = %q, want %q", tronAddrMax, max)
	}
	if len(tronAddrMin) != tronAddrLen || len(tronAddrMax) != tronAddrLen {
		t.Fatalf("tron address range constants must be %d chars", tronAddrLen)
	}
}

func TestParsePatternRejectsOldGrammar(t *testing.T) {
	for _, raw := range []string{
		"leading-zero:4",
		"zero:4",
		"zeros:4",
		"prefix:dead",
		"matching:dead",
		"match:dead",
		"dead",
		"0xdead",
		"leading:f",
		"leading-same:6",
	} {
		if _, err := ParsePattern(raw); err == nil {
			t.Fatalf("ParsePattern(%q) succeeded, want error", raw)
		}
	}
}

func TestParsePatternRejectsInvalidNewGrammar(t *testing.T) {
	for _, raw := range []string{
		"pattern:",
		"pattern:dead_z",
		"pattern:" + strings.Repeat("a", 41),
		"leading:0:0",
		"leading:0:41",
		"leading:00:1",
		"leading:g:1",
		"leading:0",
	} {
		if _, err := ParsePattern(raw); err == nil {
			t.Fatalf("ParsePattern(%q) succeeded, want error", raw)
		}
	}
}
