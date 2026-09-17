package cuda

import "github.com/woomai/provanity/internal/gpu"

const MaxDevices = 16
const cudaBackendVersion = "provanity-cuda/4"

// PatternLen matches PROVANITY_CUDA_PATTERN_LEN in the CUDA backend ABI.
const PatternLen = 40

// PatternWildcard is the sentinel byte used inside Config.Pattern to mark
// positions that the kernel must ignore. Mirrors
// PROVANITY_CUDA_PATTERN_WILDCARD in provanity_cuda.h.
const PatternWildcard byte = 0xff
const PatternGroupX byte = 0xfe
const PatternGroupY byte = 0xfd

// Tron ABI sizes, mirroring provanity_cuda.h. The prefix ladder holds up to
// TronMaxPrefixLevels nested [lo||hi] address intervals, each two 20-byte
// big-endian bounds.
const (
	TronAddressBytes    = 20
	TronMaxPrefixLevels = 16
	TronMaxSuffixLen    = 8
	TronPrefixLadderLen = TronMaxPrefixLevels * 2 * TronAddressBytes
)

type Mode int32

const (
	ModeLeading    Mode = 0
	ModePattern    Mode = 1
	ModeTronPrefix Mode = 2
	ModeTronSuffix Mode = 3
	ModeMask       Mode = 4
)

type Config struct {
	PublicKeyHex string
	Mode         Mode
	Contract     bool
	// Pattern encodes the target for the EVM modes:
	//   ModeLeading: Pattern[0] holds the target nibble (0..15).
	//   ModePattern: Pattern[i] holds the target nibble or PatternWildcard for
	//                unconstrained positions.
	// The Tron modes ignore Pattern and use the dedicated fields below.
	Pattern            [PatternLen]byte
	PatternMasks       [PatternLen]uint16
	DeviceIDs          []int
	BatchMultiple      uint32
	ProgressIntervalMS uint32
	WorkSize           uint32
	StopScore          byte

	// ModeTronPrefix: TronPrefixLevels nested intervals packed big-endian as
	// lo(20)||hi(20) per level; the kernel scores the deepest interval that
	// contains the candidate address (= matched characters after 'T').
	TronPrefixLadder [TronPrefixLadderLen]byte
	TronPrefixLevels byte
	// ModeTronSuffix: the target's trailing base58 digit values (index 0 = last
	// character) and the modulus 58^TronSuffixLen used to extract the address
	// tail from value mod 58^N.
	TronSuffixLen    byte
	TronSuffixDigits [TronMaxSuffixLen]byte
	TronSuffixMod    uint64
}

type EmitFunc func(gpu.Event) bool
