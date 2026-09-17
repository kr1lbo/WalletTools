#ifndef PROVANITY_CUDA_BACKEND_H
#define PROVANITY_CUDA_BACKEND_H

#include <stdint.h>

#ifdef __cplusplus
#define PROVANITY_CUDA_EXTERN extern "C"
#else
#define PROVANITY_CUDA_EXTERN extern
#endif

#ifdef _WIN32
#define PROVANITY_CUDA_API PROVANITY_CUDA_EXTERN __declspec(dllexport)
#define PROVANITY_CUDA_CALL __stdcall
#else
#define PROVANITY_CUDA_API PROVANITY_CUDA_EXTERN __attribute__((visibility("default")))
#define PROVANITY_CUDA_CALL
#endif

#define PROVANITY_CUDA_MAX_DEVICES 16
#define PROVANITY_CUDA_MAX_SCORE 40
#define PROVANITY_CUDA_PATTERN_LEN 40
#define PROVANITY_CUDA_PATTERN_WILDCARD 0xff
#define PROVANITY_CUDA_PATTERN_GROUP_X 0xfe
#define PROVANITY_CUDA_PATTERN_GROUP_Y 0xfd

enum provanity_cuda_mode
{
	/* EVM leading-nibble run: pattern[0] is the target nibble (0..15).
	 * Score equals the length of the maximum prefix of nibbles that all
	 * equal pattern[0]. */
	PROVANITY_CUDA_MODE_LEADING = 0,
	/* EVM positional pattern: pattern[i] is the target nibble for position
	 * i (0..15), or PROVANITY_CUDA_PATTERN_WILDCARD for a wildcard. The
	 * score is the number of concrete positions that match. */
	PROVANITY_CUDA_MODE_PATTERN = 1,
	/* Tron prefix, graded. tron_prefix_ladder holds tron_prefix_levels nested
	 * [lo||hi] 20-byte address intervals (big-endian, most significant byte
	 * first), one per prefix length, precomputed on the host
	 * (crypto.TronPrefixLadder). Because base58check is monotonic in the
	 * address, level j+1 is contained in level j, so the kernel reports the
	 * depth of the deepest interval that contains the candidate -- i.e. the
	 * number of leading characters after 'T' that match. No base58, no SHA-256.
	 * The exact base58check address is reconstructed on the CPU for survivors. */
	PROVANITY_CUDA_MODE_TRON_PREFIX = 2,
	/* Tron suffix, graded. The suffix is checksum-dependent, so the kernel
	 * computes the real 4-byte base58check checksum (two single-block SHA-256
	 * rounds), reduces the 25-byte payload modulo tron_suffix_mod (= 58^N) to
	 * recover the trailing base58 digits, and counts the longest trailing run
	 * matching tron_suffix_digits (index 0 = last character). No big-number
	 * base58 division. */
	PROVANITY_CUDA_MODE_TRON_SUFFIX = 3,
	/* EVM positional character-class masks: bit N in pattern_masks[i]
	 * accepts hexadecimal nibble N at address position i. */
	PROVANITY_CUDA_MODE_MASK = 4
};

enum provanity_cuda_event_type
{
	PROVANITY_CUDA_EVENT_READY = 1,
	PROVANITY_CUDA_EVENT_PROGRESS = 2,
	PROVANITY_CUDA_EVENT_FOUND = 3,
	PROVANITY_CUDA_EVENT_ERROR = 4,
	/* Lifecycle event fired before each long-running setup step inside
	 * provanity_cuda_run. Reuses existing fields to keep the event struct
	 * binary-stable: error_code holds a short phase tag (alloc / precomp /
	 * init_lanes / search_start), error_message holds a human-readable
	 * description, devices[0].id holds the device id the phase belongs to,
	 * and attempts holds a phase-specific numeric value (lane count, byte
	 * count) for callers that want to format their own message. */
	PROVANITY_CUDA_EVENT_PHASE = 5
};

typedef struct provanity_cuda_device
{
	int32_t id;
	char name[128];
	uint64_t global_mem;
	int32_t multiprocessors;
	int32_t compute_major;
	int32_t compute_minor;
	uint64_t hashrate;
} provanity_cuda_device;

typedef struct provanity_cuda_event
{
	int32_t type;
	uint64_t elapsed_sec;
	uint64_t elapsed_ms;
	uint64_t attempts;
	uint64_t hashrate;
	int32_t score;
	int32_t device_count;
	provanity_cuda_device devices[PROVANITY_CUDA_MAX_DEVICES];
	char offset[65];
	char address[41];
	char error_code[64];
	char error_message[256];
} provanity_cuda_event;

typedef int32_t(PROVANITY_CUDA_CALL *provanity_cuda_callback)(const provanity_cuda_event *event, void *user_data);

#define PROVANITY_CUDA_TRON_MAX_PREFIX_LEVELS 16
#define PROVANITY_CUDA_TRON_MAX_SUFFIX_LEN 8
#define PROVANITY_CUDA_TRON_PREFIX_LADDER_LEN (PROVANITY_CUDA_TRON_MAX_PREFIX_LEVELS * 2 * 20)

typedef struct provanity_cuda_config
{
	const char *public_key_hex;
	int32_t mode;
	int32_t contract;
	uint8_t pattern[PROVANITY_CUDA_PATTERN_LEN];
	uint16_t pattern_masks[PROVANITY_CUDA_PATTERN_LEN];
	int32_t device_ids[PROVANITY_CUDA_MAX_DEVICES];
	int32_t device_count;
	uint32_t batch_multiple;
	uint32_t progress_interval_ms;
	uint32_t work_size;
	/* tron_suffix_mod (= 58^tron_suffix_len) sits here while the running offset
	 * is still 8-aligned so the struct needs no interior padding. The Go ABI
	 * mirrors in internal/cuda/loader_windows.go and loader_linux.go must keep
	 * this exact field order. */
	uint64_t tron_suffix_mod;
	uint8_t stop_score;
	uint8_t tron_prefix_levels;
	uint8_t tron_suffix_len;
	uint8_t tron_suffix_digits[PROVANITY_CUDA_TRON_MAX_SUFFIX_LEN];
	uint8_t tron_prefix_ladder[PROVANITY_CUDA_TRON_PREFIX_LADDER_LEN];
} provanity_cuda_config;

PROVANITY_CUDA_API int32_t PROVANITY_CUDA_CALL provanity_cuda_version(char *version, uint32_t version_len);
PROVANITY_CUDA_API int32_t PROVANITY_CUDA_CALL provanity_cuda_list_devices(provanity_cuda_device *devices, int32_t max_devices, char *error, uint32_t error_len);
PROVANITY_CUDA_API int32_t PROVANITY_CUDA_CALL provanity_cuda_run(const provanity_cuda_config *config, provanity_cuda_callback callback, void *user_data, char *error, uint32_t error_len);

#endif /* PROVANITY_CUDA_BACKEND_H */
