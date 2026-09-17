package vanity

import (
	"fmt"
	"strconv"
	"strings"
)

type PatternKind string

const (
	PatternPattern    PatternKind = "pattern"
	PatternMask       PatternKind = "mask"
	PatternLeading    PatternKind = "leading"
	PatternTronPrefix PatternKind = "tron-prefix"
	PatternTronSuffix PatternKind = "tron-suffix"
)

// MaxTronConcretePos is the largest number of characters a Tron prefix may pin
// after the leading 'T' (which is implicit). The Tron address is 34 base58
// characters and the 4-byte base58check checksum (value < 2^32 < 58^6) only
// perturbs the last ~6 characters (indices 27..33). Capping the prefix well
// inside that boundary lets the CUDA scorer match the address prefix with a
// 160-bit address-range compare and no checksum (no SHA-256); see
// crypto.TronPrefixLadder and PROVANITY_CUDA_MODE_TRON_PREFIX.
const MaxTronConcretePos = 16

// MaxTronSuffixLen caps a Tron suffix so the device can compute value mod 58^N
// in a single 64-bit Horner reduction (58^9 still fits a uint64). Covers every
// realistic vanity suffix; see PROVANITY_CUDA_MODE_TRON_SUFFIX.
const MaxTronSuffixLen = 8

// tronAddrMin and tronAddrMax are the smallest and largest possible Tron
// addresses: base58check(0x41 || 0x00*20) and base58check(0x41 || 0xff*20).
// Every 20-byte EVM address is reachable, so all valid Tron addresses sort, as
// equal-length base58 strings, within [tronAddrMin, tronAddrMax]. The base58
// alphabet is ASCII-ascending, so byte-wise string comparison matches numeric
// order. TestTronAddressRangeConstants guards these against
// crypto.TronAddressFromEVMAddress.
const (
	tronAddrMin = "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb"
	tronAddrMax = "TZJozAg1ruapycCicgz31GxvYJ1FraLjZa"
	tronAddrLen = 34
)

type Pattern struct {
	Raw         string
	Kind        PatternKind
	Value       string
	Count       int
	Description string
	Masks       [40]uint16
}

func ParsePattern(raw string) (Pattern, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Pattern{}, fmt.Errorf("pattern is empty")
	}

	name, value, hasValue := strings.Cut(raw, ":")
	if !hasValue {
		return Pattern{}, fmt.Errorf("unsupported pattern %q", raw)
	}

	switch strings.ToLower(strings.TrimSpace(name)) {
	case "pattern":
		return parsePattern(raw, value)
	case "mask":
		return parseMask(raw, value)
	case "leading":
		return parseLeading(raw, value)
	default:
		return Pattern{}, fmt.Errorf("unsupported pattern kind %q", name)
	}
}

func ParseTronPattern(raw string) (Pattern, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Pattern{}, fmt.Errorf("pattern is empty")
	}

	name, value, hasValue := strings.Cut(raw, ":")
	if !hasValue {
		return Pattern{}, fmt.Errorf("unsupported Tron pattern %q", raw)
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "prefix":
		return parseTronPrefix(raw, value)
	case "suffix":
		return parseTronSuffix(raw, value)
	default:
		return Pattern{}, fmt.Errorf("Tron supports prefix:VALUE or suffix:VALUE")
	}
}

func (p Pattern) MatchesAddressHex(address string) bool {
	address = normalizeAddressHex(address)
	switch p.Kind {
	case PatternPattern:
		if len(address) < len(p.Value) {
			return false
		}
		var gx, gy byte
		var haveX, haveY bool
		for i := 0; i < len(p.Value); i++ {
			if !isHexByte(address[i]) {
				return false
			}
			if p.Value[i] == 'X' {
				continue
			}
			if p.Value[i] == 'Y' {
				if !haveX {
					gx, haveX = address[i], true
				} else if address[i] != gx {
					return false
				}
				continue
			}
			if p.Value[i] == 'Z' {
				if !haveY {
					gy, haveY = address[i], true
				} else if address[i] != gy {
					return false
				}
				continue
			}
			if address[i] != p.Value[i] {
				return false
			}
		}
		return true
	case PatternMask:
		if len(address) != 40 {
			return false
		}
		for i := 0; i < 40; i++ {
			n := hexNibble(address[i])
			if n < 0 || p.Masks[i]&(uint16(1)<<n) == 0 {
				return false
			}
		}
		return true
	case PatternLeading:
		if p.Value == "" {
			return false
		}
		return countLeadingByte(address, p.Value[0]) >= p.Count
	default:
		return false
	}
}

func (p Pattern) MatchesAddress(address string) bool {
	switch p.Kind {
	case PatternTronPrefix:
		return strings.HasPrefix(strings.TrimSpace(address), p.Value)
	case PatternTronSuffix:
		return strings.HasSuffix(strings.TrimSpace(address), p.Value)
	default:
		return p.MatchesAddressHex(address)
	}
}

func (p Pattern) ScoreAddressHex(address string) int {
	address = normalizeAddressHex(address)
	switch p.Kind {
	case PatternPattern:
		if len(address) < len(p.Value) {
			return 0
		}
		matched := 0
		var gx, gy byte
		var haveX, haveY bool
		for i := 0; i < len(p.Value); i++ {
			if !isHexByte(address[i]) {
				break
			}
			if p.Value[i] == 'X' {
				continue
			}
			if p.Value[i] == 'Y' {
				if !haveX {
					gx, haveX = address[i], true
				}
				if address[i] == gx {
					matched++
				}
				continue
			}
			if p.Value[i] == 'Z' {
				if !haveY {
					gy, haveY = address[i], true
				}
				if address[i] == gy {
					matched++
				}
				continue
			}
			if address[i] == p.Value[i] {
				matched++
			}
		}
		return matched
	case PatternMask:
		if len(address) != 40 {
			return 0
		}
		matched := 0
		for i := 0; i < 40; i++ {
			n := hexNibble(address[i])
			if n >= 0 && p.Masks[i]&(uint16(1)<<n) != 0 {
				matched++
			}
		}
		return matched
	case PatternLeading:
		if p.Value == "" {
			return 0
		}
		return countLeadingByte(address, p.Value[0])
	default:
		return 0
	}
}

func (p Pattern) ScoreAddress(address string) int {
	switch p.Kind {
	case PatternTronPrefix:
		return p.scoreTronPrefix(address)
	case PatternTronSuffix:
		return p.scoreTronSuffix(address)
	default:
		return p.ScoreAddressHex(address)
	}
}

func (p Pattern) TargetScore() int {
	switch p.Kind {
	case PatternPattern, PatternMask, PatternLeading, PatternTronPrefix, PatternTronSuffix:
		return p.Count
	default:
		return 0
	}
}

func parseMask(raw, value string) (Pattern, error) {
	parts := strings.Split(strings.TrimSpace(value), ",")
	if len(parts) != 40 {
		return Pattern{}, fmt.Errorf("mask pattern requires exactly 40 comma-separated masks")
	}
	p := Pattern{Raw: raw, Kind: PatternMask, Description: raw}
	for i, part := range parts {
		n, err := strconv.ParseUint(strings.TrimSpace(part), 16, 16)
		if err != nil || n == 0 {
			return Pattern{}, fmt.Errorf("invalid mask at position %d", i)
		}
		p.Masks[i] = uint16(n)
	}
	p.Count = 40
	return p, nil
}

func hexNibble(ch byte) int {
	switch {
	case ch >= '0' && ch <= '9':
		return int(ch - '0')
	case ch >= 'a' && ch <= 'f':
		return int(ch-'a') + 10
	case ch >= 'A' && ch <= 'F':
		return int(ch-'A') + 10
	default:
		return -1
	}
}

func (p Pattern) String() string {
	if p.Description != "" {
		return p.Description
	}
	return p.Raw
}

func parsePattern(raw, value string) (Pattern, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Pattern{}, fmt.Errorf("pattern value is empty")
	}
	if len(value) > 40 {
		return Pattern{}, fmt.Errorf("pattern value cannot exceed 40 nibbles")
	}

	var normalized strings.Builder
	concrete := 0
	for i := 0; i < len(value); i++ {
		ch := value[i]
		switch {
		case ch >= '0' && ch <= '9':
			normalized.WriteByte(ch)
			concrete++
		case ch >= 'a' && ch <= 'f':
			normalized.WriteByte(ch)
			concrete++
		case ch >= 'A' && ch <= 'F':
			normalized.WriteByte(ch + ('a' - 'A'))
			concrete++
		case ch == 'X' || ch == 'x' || ch == '*' || ch == '?':
			normalized.WriteByte('X')
		case ch == 'Y':
			normalized.WriteByte('Y')
			concrete++
		case ch == 'Z':
			normalized.WriteByte('Z')
			concrete++
		default:
			return Pattern{}, fmt.Errorf("pattern value must contain only hex nibbles or X/x/*/? wildcards")
		}
	}

	value = normalized.String()
	return Pattern{
		Raw:         raw,
		Kind:        PatternPattern,
		Value:       value,
		Count:       concrete,
		Description: "pattern:" + value,
	}, nil
}

// parseTronPrefix parses prefix:VALUE, where VALUE is the base58 characters that
// follow the implicit leading 'T'. A single leading 'T' in VALUE is tolerated
// and stripped so both prefix:ABC and prefix:TABC mean the address "TABC…".
func parseTronPrefix(raw, value string) (Pattern, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "T")
	if value == "" {
		return Pattern{}, fmt.Errorf("Tron prefix must include at least one character after the implicit leading T")
	}
	if len(value) > MaxTronConcretePos {
		return Pattern{}, fmt.Errorf("Tron prefix cannot exceed %d characters after T", MaxTronConcretePos)
	}
	for i := 0; i < len(value); i++ {
		if !isBase58Byte(value[i]) {
			return Pattern{}, fmt.Errorf("Tron prefix must contain only Base58 characters")
		}
	}
	full := "T" + value
	if !tronPrefixReachable(full) {
		return Pattern{}, fmt.Errorf("Tron prefix %q is not a reachable address prefix (valid addresses range from %s to %s)", full, tronAddrMin, tronAddrMax)
	}
	return Pattern{
		Raw:         raw,
		Kind:        PatternTronPrefix,
		Value:       full,
		Count:       len(value),
		Description: "prefix:" + value,
	}, nil
}

// parseTronSuffix parses suffix:VALUE, where VALUE is the trailing base58
// characters of the address. Suffixes are checksum-dependent, so the device
// computes the real base58check tail per candidate; the length cap keeps that a
// single 64-bit modular reduction (see MaxTronSuffixLen).
func parseTronSuffix(raw, value string) (Pattern, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Pattern{}, fmt.Errorf("Tron suffix value is empty")
	}
	if len(value) > MaxTronSuffixLen {
		return Pattern{}, fmt.Errorf("Tron suffix cannot exceed %d characters", MaxTronSuffixLen)
	}
	for i := 0; i < len(value); i++ {
		if !isBase58Byte(value[i]) {
			return Pattern{}, fmt.Errorf("Tron suffix must contain only Base58 characters")
		}
	}
	return Pattern{
		Raw:         raw,
		Kind:        PatternTronSuffix,
		Value:       value,
		Count:       len(value),
		Description: "suffix:" + value,
	}, nil
}

func parseLeading(raw, value string) (Pattern, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return Pattern{}, fmt.Errorf("leading pattern requires leading:H:N")
	}
	hexChar := strings.ToLower(strings.TrimSpace(parts[0]))
	if len(hexChar) != 1 || !isHexByte(hexChar[0]) {
		return Pattern{}, fmt.Errorf("leading pattern requires one hex character")
	}
	count, err := parseCount(parts[1])
	if err != nil {
		return Pattern{}, fmt.Errorf("parse leading count: %w", err)
	}
	if count > 40 {
		return Pattern{}, fmt.Errorf("leading count cannot exceed 40")
	}

	return Pattern{
		Raw:         raw,
		Kind:        PatternLeading,
		Value:       hexChar,
		Count:       count,
		Description: fmt.Sprintf("leading:%s:%d", hexChar, count),
	}, nil
}

func parseCount(value string) (int, error) {
	count, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	if count <= 0 {
		return 0, fmt.Errorf("count must be positive")
	}
	return count, nil
}

func normalizeAddressHex(address string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(address)), "0x")
}

// scoreTronPrefix returns the number of leading characters after the implicit
// 'T' that match, stopping at the first mismatch (the longest matching prefix
// run). p.Value is "T"+concrete prefix, so scoring starts at index 1.
func (p Pattern) scoreTronPrefix(address string) int {
	address = strings.TrimSpace(address)
	matched := 0
	for i := 1; i < len(p.Value); i++ {
		if i >= len(address) || address[i] != p.Value[i] {
			break
		}
		matched++
	}
	return matched
}

// scoreTronSuffix returns the number of trailing characters that match, counting
// from the end and stopping at the first mismatch (the longest matching suffix
// run).
func (p Pattern) scoreTronSuffix(address string) int {
	address = strings.TrimSpace(address)
	matched := 0
	for i := 0; i < len(p.Value); i++ {
		ai := len(address) - 1 - i
		si := len(p.Value) - 1 - i
		if ai < 0 || address[ai] != p.Value[si] {
			break
		}
		matched++
	}
	return matched
}

func countLeadingByte(value string, want byte) int {
	count := 0
	for i := 0; i < len(value); i++ {
		if value[i] != want {
			break
		}
		count++
	}
	return count
}

func isHexByte(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

// tronPrefixReachable reports whether some valid Tron address matches the
// normalized pattern (concrete characters fixed, '?' wildcards free). It pads
// the pattern to a full 34-char address, fills wildcard/unset positions with
// the smallest ('1') and largest ('z') base58 characters to bracket the
// matching set, and tests that bracket against [tronAddrMin, tronAddrMax]. This
// is exact for prefix patterns (a contiguous range) and conservative for
// interior-wildcard patterns: it never rejects a reachable pattern.
func tronPrefixReachable(value string) bool {
	const minChar, maxChar = '1', 'z'
	var lo, hi [tronAddrLen]byte
	for i := 0; i < tronAddrLen; i++ {
		if i < len(value) && value[i] != '?' {
			lo[i] = value[i]
			hi[i] = value[i]
			continue
		}
		lo[i] = minChar
		hi[i] = maxChar
	}
	return string(lo[:]) <= tronAddrMax && string(hi[:]) >= tronAddrMin
}

func isBase58Byte(ch byte) bool {
	switch {
	case ch >= '1' && ch <= '9':
		return true
	case ch >= 'A' && ch <= 'Z':
		return ch != 'I' && ch != 'O'
	case ch >= 'a' && ch <= 'z':
		return ch != 'l'
	default:
		return false
	}
}
