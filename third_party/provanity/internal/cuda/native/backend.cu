#include "provanity_cuda.h"

#include <cuda_runtime.h>

#include <algorithm>
#include <chrono>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <cerrno>
#include <sstream>
#include <string>
#include <vector>

#ifdef _WIN32
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#include <bcrypt.h>
#pragma comment(lib, "bcrypt.lib")
#elif defined(__linux__)
#include <sys/random.h>
#include <unistd.h>
#endif

#include "types.cuh"
#include "secp256k1_tables.hpp"
#include "secp256k1_tables.cpp"
#include "kernels.cuh"

static_assert(sizeof(Felt256) == 32, "Felt256 layout mismatch");
static_assert(sizeof(Point) == 64, "Point layout mismatch");
static_assert(sizeof(Result) == 28, "Result layout mismatch");

namespace
{

	constexpr int kMaxScore = PROVANITY_CUDA_MAX_SCORE;

	struct DeviceMemory
	{
		Point *precomp = nullptr;
		Felt256 *hashes = nullptr;
		Result *results = nullptr;
		Felt256 *state_x = nullptr;
		Felt256 *state_lambda = nullptr;
		Felt256 *state_inv = nullptr;
	};

	struct Seed
	{
		uint64_t s[4];
	};

	void copy_error(char *dst, uint32_t len, const std::string &message)
	{
		if (dst == nullptr || len == 0)
		{
			return;
		}
		const auto count = std::min<size_t>(len - 1, message.size());
		std::memcpy(dst, message.data(), count);
		dst[count] = '\0';
	}

	std::string cuda_error(const char *action, cudaError_t err)
	{
		std::ostringstream out;
		out << action << ": " << cudaGetErrorString(err) << " (" << static_cast<int>(err) << ")";
		return out.str();
	}

	bool check_cuda(cudaError_t err, const char *action, std::string &error)
	{
		if (err == cudaSuccess)
		{
			return true;
		}
		error = cuda_error(action, err);
		return false;
	}

	void fill_event_error(provanity_cuda_event &event, const char *code, const std::string &message)
	{
		std::memset(&event, 0, sizeof(event));
		event.type = PROVANITY_CUDA_EVENT_ERROR;
		std::snprintf(event.error_code, sizeof(event.error_code), "%s", code);
		std::snprintf(event.error_message, sizeof(event.error_message), "%s", message.c_str());
	}

	void emit_error(provanity_cuda_callback callback, void *user_data, const char *code, const std::string &message)
	{
		if (callback == nullptr)
		{
			return;
		}
		provanity_cuda_event event;
		fill_event_error(event, code, message);
		callback(&event, user_data);
	}

	/* Emit a lifecycle event before each long-running setup step so the host
	 * UI can keep its status line moving while a single synchronous kernel
	 * (notably the pv_iterate_init_jac pipeline) runs. Returns true if the
	 * host asked us to
	 * cancel via the callback's non-zero return. Reuses error_code /
	 * error_message / devices[0].id / attempts to stay binary-compatible
	 * with the existing provanity_cuda_event layout — see
	 * PROVANITY_CUDA_EVENT_PHASE in the header for the field contract. */
	bool emit_phase(provanity_cuda_callback callback, void *user_data, int32_t device_id, const char *phase, const std::string &message, uint64_t value)
	{
		if (callback == nullptr)
		{
			return false;
		}
		provanity_cuda_event event;
		std::memset(&event, 0, sizeof(event));
		event.type = PROVANITY_CUDA_EVENT_PHASE;
		event.attempts = value;
		event.device_count = 1;
		event.devices[0].id = device_id;
		std::snprintf(event.error_code, sizeof(event.error_code), "%s", phase);
		std::snprintf(event.error_message, sizeof(event.error_message), "%s", message.c_str());
		return callback(&event, user_data) != 0;
	}

	std::string format_bytes(uint64_t bytes)
	{
		char buf[64];
		if (bytes >= (1ULL << 30))
		{
			std::snprintf(buf, sizeof(buf), "%.2f GiB", static_cast<double>(bytes) / static_cast<double>(1ULL << 30));
		}
		else if (bytes >= (1ULL << 20))
		{
			std::snprintf(buf, sizeof(buf), "%.0f MiB", static_cast<double>(bytes) / static_cast<double>(1ULL << 20));
		}
		else if (bytes >= (1ULL << 10))
		{
			std::snprintf(buf, sizeof(buf), "%.1f KiB", static_cast<double>(bytes) / static_cast<double>(1ULL << 10));
		}
		else
		{
			std::snprintf(buf, sizeof(buf), "%llu B", static_cast<unsigned long long>(bytes));
		}
		return std::string(buf);
	}

	std::string format_count(uint64_t value)
	{
		char buf[32];
		std::snprintf(buf, sizeof(buf), "%llu", static_cast<unsigned long long>(value));
		std::string s(buf);
		if (s.size() <= 3)
		{
			return s;
		}
		for (int insert_at = static_cast<int>(s.size()) - 3; insert_at > 0; insert_at -= 3)
		{
			s.insert(static_cast<size_t>(insert_at), ",");
		}
		return s;
	}

	uint8_t hex_value(char c)
	{
		if (c >= '0' && c <= '9')
		{
			return static_cast<uint8_t>(c - '0');
		}
		if (c >= 'a' && c <= 'f')
		{
			return static_cast<uint8_t>(10 + c - 'a');
		}
		if (c >= 'A' && c <= 'F')
		{
			return static_cast<uint8_t>(10 + c - 'A');
		}
		return 0xff;
	}

	bool decode_hex(const char *hex, uint8_t *out, size_t out_len)
	{
		if (hex == nullptr)
		{
			return false;
		}
		const size_t hex_len = std::strlen(hex);
		if (hex_len != out_len * 2)
		{
			return false;
		}
		for (size_t i = 0; i < out_len; ++i)
		{
			const auto hi = hex_value(hex[i * 2]);
			const auto lo = hex_value(hex[i * 2 + 1]);
			if (hi == 0xff || lo == 0xff)
			{
				return false;
			}
			out[i] = static_cast<uint8_t>((hi << 4) | lo);
		}
		return true;
	}

	uint64_t load_be64(const uint8_t *p)
	{
		uint64_t v = 0;
		for (int i = 0; i < 8; ++i)
		{
			v = (v << 8) | p[i];
		}
		return v;
	}

	SeedWords public_key_part(const uint8_t *data)
	{
		SeedWords value;
		value.x = load_be64(data + 24);
		value.y = load_be64(data + 16);
		value.z = load_be64(data + 8);
		value.w = load_be64(data);
		return value;
	}

	bool fill_os_random(void *data, size_t len, std::string &error)
	{
#ifdef _WIN32
		const NTSTATUS status = BCryptGenRandom(nullptr, static_cast<PUCHAR>(data), static_cast<ULONG>(len), BCRYPT_USE_SYSTEM_PREFERRED_RNG);
		if (status == 0)
		{
			return true;
		}
		std::ostringstream out;
		out << "BCryptGenRandom failed: 0x" << std::hex << static_cast<unsigned long>(status);
		error = out.str();
		return false;
#elif defined(__linux__)
		auto *p = static_cast<uint8_t *>(data);
		size_t done = 0;
		while (done < len)
		{
			const ssize_t n = getrandom(p + done, len - done, 0);
			if (n > 0)
			{
				done += static_cast<size_t>(n);
				continue;
			}
			if (n < 0 && errno == EINTR)
			{
				continue;
			}
			error = "getrandom failed";
			return false;
		}
		return true;
#else
		error = "OS CSPRNG is not implemented for this platform";
		return false;
#endif
	}

	bool random_seed(Seed &seed, std::string &error)
	{
		if (!fill_os_random(seed.s, sizeof(seed.s), error))
		{
			return false;
		}
		return true;
	}

	SeedWords to_seed_words(const Seed &seed)
	{
		return SeedWords{seed.s[0], seed.s[1], seed.s[2], seed.s[3]};
	}

	void hex_bytes(const uint8_t *data, size_t len, char *out)
	{
		static const char *digits = "0123456789abcdef";
		for (size_t i = 0; i < len; ++i)
		{
			out[i * 2] = digits[data[i] >> 4];
			out[i * 2 + 1] = digits[data[i] & 0xf];
		}
		out[len * 2] = '\0';
	}

	void format_offset(const Seed &seed, uint64_t attempt, char *out)
	{
		uint64_t s0 = seed.s[0] + attempt;
		uint64_t carry = s0 < attempt;
		uint64_t s1 = seed.s[1] + carry;
		carry = carry && s1 == 0;
		uint64_t s2 = seed.s[2] + carry;
		carry = carry && s2 == 0;
		uint64_t s3 = seed.s[3] + carry;
		std::snprintf(out, 65, "%016llx%016llx%016llx%016llx",
					  static_cast<unsigned long long>(s3),
					  static_cast<unsigned long long>(s2),
					  static_cast<unsigned long long>(s1),
					  static_cast<unsigned long long>(s0));
	}

	std::vector<int> selected_devices(const provanity_cuda_config *config)
	{
		int count = 0;
		if (cudaGetDeviceCount(&count) != cudaSuccess || count <= 0)
		{
			return {};
		}

		std::vector<int> devices;
		if (config->device_count <= 0)
		{
			for (int i = 0; i < count && static_cast<int>(devices.size()) < PROVANITY_CUDA_MAX_DEVICES; ++i)
			{
				devices.push_back(i);
			}
			return devices;
		}

		for (int i = 0; i < config->device_count && i < PROVANITY_CUDA_MAX_DEVICES; ++i)
		{
			const int id = config->device_ids[i];
			if (id >= 0 && id < count)
			{
				devices.push_back(id);
			}
		}
		return devices;
	}

	bool fill_device_info(int id, provanity_cuda_device &out)
	{
		cudaDeviceProp prop;
		if (cudaGetDeviceProperties(&prop, id) != cudaSuccess)
		{
			return false;
		}
		std::memset(&out, 0, sizeof(out));
		out.id = id;
		std::snprintf(out.name, sizeof(out.name), "%s", prop.name);
		out.global_mem = static_cast<uint64_t>(prop.totalGlobalMem);
		out.multiprocessors = prop.multiProcessorCount;
		out.compute_major = prop.major;
		out.compute_minor = prop.minor;
		return true;
	}

	void free_memory(DeviceMemory &mem)
	{
		cudaFree(mem.precomp);
		cudaFree(mem.hashes);
		cudaFree(mem.results);
		cudaFree(mem.state_x);
		cudaFree(mem.state_lambda);
		cudaFree(mem.state_inv);
		mem = DeviceMemory{};
	}

	void free_run_resources(DeviceMemory &mem, cudaStream_t stream, Result *host_results)
	{
		if (host_results != nullptr)
		{
			cudaFreeHost(host_results);
		}
		if (stream != nullptr)
		{
			cudaStreamDestroy(stream);
		}
		free_memory(mem);
	}

	bool alloc_memory(DeviceMemory &mem, size_t size, std::string &error)
	{
		if (!check_cuda(cudaMalloc(&mem.precomp, PV_PRECOMP_POINTS * sizeof(Point)), "cudaMalloc(precomp)", error))
			return false;
		if (!check_cuda(cudaMalloc(&mem.hashes, size * sizeof(Felt256)), "cudaMalloc(hashes)", error))
			return false;
		if (!check_cuda(cudaMalloc(&mem.results, (kMaxScore + 1) * sizeof(Result)), "cudaMalloc(results)", error))
			return false;
		if (!check_cuda(cudaMalloc(&mem.state_x, size * sizeof(Felt256)), "cudaMalloc(state_x)", error))
			return false;
		if (!check_cuda(cudaMalloc(&mem.state_lambda, size * sizeof(Felt256)), "cudaMalloc(state_lambda)", error))
			return false;
		if (!check_cuda(cudaMalloc(&mem.state_inv, size * sizeof(Felt256)), "cudaMalloc(state_inv)", error))
			return false;
		return true;
	}

	bool copy_inputs(DeviceMemory &mem, const provanity_cuda_config *config, std::string &error)
	{
		const int threads = 32;
		const int blocks = (PV_PRECOMP_WORDS * PV_PRECOMP_BYTES + threads - 1) / threads;
		pv_build_precomp<<<blocks, threads>>>(mem.precomp);
		if (!check_cuda(cudaGetLastError(), "pv_build_precomp launch", error) || !check_cuda(cudaDeviceSynchronize(), "pv_build_precomp", error))
			return false;
		if (!check_cuda(cudaMemcpyToSymbol(g_pattern, config->pattern, PROVANITY_CUDA_PATTERN_LEN), "cudaMemcpyToSymbol(g_pattern)", error))
			return false;
		if (!check_cuda(cudaMemcpyToSymbol(g_pattern_masks, config->pattern_masks, sizeof(config->pattern_masks)), "cudaMemcpyToSymbol(g_pattern_masks)", error))
			return false;
		if (!check_cuda(cudaMemcpyToSymbol(g_tron_prefix_ladder, config->tron_prefix_ladder, PROVANITY_CUDA_TRON_PREFIX_LADDER_LEN), "cudaMemcpyToSymbol(g_tron_prefix_ladder)", error))
			return false;
		if (!check_cuda(cudaMemcpyToSymbol(g_tron_prefix_levels, &config->tron_prefix_levels, sizeof(config->tron_prefix_levels)), "cudaMemcpyToSymbol(g_tron_prefix_levels)", error))
			return false;
		if (!check_cuda(cudaMemcpyToSymbol(g_tron_suffix_digits, config->tron_suffix_digits, PROVANITY_CUDA_TRON_MAX_SUFFIX_LEN), "cudaMemcpyToSymbol(g_tron_suffix_digits)", error))
			return false;
		if (!check_cuda(cudaMemcpyToSymbol(g_tron_suffix_len, &config->tron_suffix_len, sizeof(config->tron_suffix_len)), "cudaMemcpyToSymbol(g_tron_suffix_len)", error))
			return false;
		if (!check_cuda(cudaMemcpyToSymbol(g_tron_suffix_mod, &config->tron_suffix_mod, sizeof(config->tron_suffix_mod)), "cudaMemcpyToSymbol(g_tron_suffix_mod)", error))
			return false;
		return true;
	}

	void launch_score(const provanity_cuda_config *config, DeviceMemory &mem, dim3 grid, dim3 block, cudaStream_t stream, uint8_t score_max)
	{
		switch (config->mode)
		{
		case PROVANITY_CUDA_MODE_LEADING:
			pv_score_leading<<<grid, block, 0, stream>>>(mem.hashes, mem.results, score_max);
			break;
		case PROVANITY_CUDA_MODE_PATTERN:
			pv_score_pattern<<<grid, block, 0, stream>>>(mem.hashes, mem.results, score_max);
			break;
		case PROVANITY_CUDA_MODE_MASK:
			pv_score_mask<<<grid, block, 0, stream>>>(mem.hashes, mem.results, score_max);
			break;
		case PROVANITY_CUDA_MODE_TRON_PREFIX:
			pv_score_tron_prefix<<<grid, block, 0, stream>>>(mem.hashes, mem.results, score_max);
			break;
		case PROVANITY_CUDA_MODE_TRON_SUFFIX:
			pv_score_tron_suffix<<<grid, block, 0, stream>>>(mem.hashes, mem.results, score_max);
			break;
		default:
			pv_score_leading<<<grid, block, 0, stream>>>(mem.hashes, mem.results, score_max);
			break;
		}
	}

	/* Fused step + inline score for non-contract modes. Compared with
	 * (pv_iterate_step + pv_score_*) this drops the 32B hashes[id] write/read
	 * round-trip per slot per iteration (~15% of total memory traffic). */
	void launch_step_scored(const provanity_cuda_config *config, DeviceMemory &mem, dim3 grid, dim3 block, cudaStream_t stream, uint8_t score_max)
	{
		switch (config->mode)
		{
		case PROVANITY_CUDA_MODE_LEADING:
			pv_iterate_step_scored<ScoreOpLeading><<<grid, block, 0, stream>>>(mem.state_x, mem.state_lambda, mem.state_inv, mem.results, score_max);
			break;
		case PROVANITY_CUDA_MODE_PATTERN:
			pv_iterate_step_scored<ScoreOpPattern><<<grid, block, 0, stream>>>(mem.state_x, mem.state_lambda, mem.state_inv, mem.results, score_max);
			break;
		case PROVANITY_CUDA_MODE_MASK:
			pv_iterate_step_scored<ScoreOpMask><<<grid, block, 0, stream>>>(mem.state_x, mem.state_lambda, mem.state_inv, mem.results, score_max);
			break;
		case PROVANITY_CUDA_MODE_TRON_PREFIX:
			pv_iterate_step_scored<ScoreOpTronPrefix><<<grid, block, 0, stream>>>(mem.state_x, mem.state_lambda, mem.state_inv, mem.results, score_max);
			break;
		case PROVANITY_CUDA_MODE_TRON_SUFFIX:
			pv_iterate_step_scored<ScoreOpTronSuffix><<<grid, block, 0, stream>>>(mem.state_x, mem.state_lambda, mem.state_inv, mem.results, score_max);
			break;
		default:
			pv_iterate_step_scored<ScoreOpLeading><<<grid, block, 0, stream>>>(mem.state_x, mem.state_lambda, mem.state_inv, mem.results, score_max);
			break;
		}
	}

	Point make_base_point(const SeedWords &seed_x, const SeedWords &seed_y)
	{
		Point base;
		base.x.v[0] = seed_x.x;
		base.x.v[1] = seed_x.y;
		base.x.v[2] = seed_x.z;
		base.x.v[3] = seed_x.w;
		base.y.v[0] = seed_y.x;
		base.y.v[1] = seed_y.y;
		base.y.v[2] = seed_y.z;
		base.y.v[3] = seed_y.w;
		return base;
	}

	SeedWords step_scalar_from_size(size_t size)
	{
		/* The shared step point is size * G, so the host-side scalar is just
		 * `size` packed into the low limb. PV_BATCH_LANES * batch_multiple is
		 * at most ~32 bits today, so the upper limbs are always zero. */
		SeedWords scalar = {0, 0, 0, 0};
		scalar.x = static_cast<uint64_t>(size);
		return scalar;
	}

	bool launch_init(DeviceMemory &mem, size_t size, const SeedWords &seed_device, const SeedWords &seed_x, const SeedWords &seed_y, dim3 grid, dim3 block, cudaStream_t stream, std::string &error)
	{
		pv_clear_results<<<1, 64, 0, stream>>>(mem.results);
		if (!check_cuda(cudaGetLastError(), "pv_clear_results launch", error))
			return false;

		const SeedWords step_scalar = step_scalar_from_size(size);
		pv_compute_step<<<1, 1, 0, stream>>>(mem.precomp, step_scalar);
		if (!check_cuda(cudaGetLastError(), "pv_compute_step launch", error))
			return false;

		const Point base = make_base_point(seed_x, seed_y);

		/* Three-kernel init pipeline (Jacobian → batched Z^-1 → affine finalize).
		 * Replaces a single pv_iterate_init that paid ~33 modular inversions per
		 * lane with a sequence that pays one batched inversion shared across the
		 * lane grid plus a single per-lane slope-seed inversion. On a 1M-lane
		 * batch this is the difference between ~26 s and ~1-2 s of wallclock. */
		pv_iterate_init_jac<<<grid, block, 0, stream>>>(mem.precomp, mem.state_x, mem.state_lambda, mem.state_inv, base, seed_device);
		if (!check_cuda(cudaGetLastError(), "pv_iterate_init_jac launch", error))
			return false;

		const uint32_t threads = 256;
		const uint32_t lanes_per_thread = PV_INVERT_GROUP_SIZE;
		const uint32_t inv_groups = static_cast<uint32_t>(size / lanes_per_thread);
		const uint32_t inv_blocks = (inv_groups + threads - 1) / threads;
		pv_batched_invert<<<inv_blocks, threads, 0, stream>>>(mem.state_inv, inv_groups);
		if (!check_cuda(cudaGetLastError(), "pv_batched_invert launch", error))
			return false;

		pv_iterate_init_finalize<<<grid, block, 0, stream>>>(mem.state_x, mem.state_lambda, mem.state_inv, mem.hashes);
		if (!check_cuda(cudaGetLastError(), "pv_iterate_init_finalize launch", error))
			return false;

		return check_cuda(cudaStreamSynchronize(stream), "iterate init", error);
	}

	bool launch_round(const provanity_cuda_config *config, DeviceMemory &mem, size_t size, dim3 grid, dim3 block, cudaStream_t stream, uint8_t score_max, uint64_t round_index, std::string &error)
	{
		/* The hashes buffer is populated either by pv_iterate_init_finalize
		 * (round 0) or by the previous pv_iterate_step (subsequent rounds in
		 * contract mode). Non-contract round>0 uses the fused step+score
		 * kernel which keeps the hash in registers instead. */
		if (round_index > 0)
		{
			const uint32_t threads = 256;
			const uint32_t lanes_per_thread = PV_INVERT_GROUP_SIZE;
			const uint32_t inv_groups = static_cast<uint32_t>(size / lanes_per_thread);
			const uint32_t inv_blocks = (inv_groups + threads - 1) / threads;
			/* `inv_groups` is also the stride between successive lanes owned by
			 * one thread: the inverse kernel assigns lanes interleaved by the
			 * thread count so its global accesses coalesce. */
			pv_iterate_inverse<<<inv_blocks, threads, 0, stream>>>(mem.state_inv, inv_groups);
			if (!config->contract)
			{
				/* Non-contract: fused step+score avoids the hashes[] round-trip. */
				launch_step_scored(config, mem, grid, block, stream, score_max);
				return check_cuda(cudaGetLastError(), "kernel launch", error);
			}
			/* Contract: keep the unfused path so pv_transform_contract has
			 * something to read out of hashes[]. */
			pv_iterate_step<<<grid, block, 0, stream>>>(mem.state_x, mem.state_lambda, mem.state_inv, mem.hashes);
		}
		if (config->contract)
		{
			pv_transform_contract<<<grid, block, 0, stream>>>(mem.hashes);
		}
		launch_score(config, mem, grid, block, stream, score_max);
		return check_cuda(cudaGetLastError(), "kernel launch", error);
	}

	uint64_t elapsed_seconds(const std::chrono::steady_clock::time_point &start)
	{
		return static_cast<uint64_t>(std::chrono::duration_cast<std::chrono::seconds>(std::chrono::steady_clock::now() - start).count());
	}

	uint64_t elapsed_milliseconds(const std::chrono::steady_clock::time_point &start)
	{
		return static_cast<uint64_t>(std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::steady_clock::now() - start).count());
	}

	bool callback_event(provanity_cuda_callback callback, void *user_data, provanity_cuda_event &event)
	{
		if (callback == nullptr)
		{
			return false;
		}
		return callback(&event, user_data) != 0;
	}

} // namespace

PROVANITY_CUDA_API int32_t PROVANITY_CUDA_CALL provanity_cuda_version(char *version, uint32_t version_len)
{
	if (version == nullptr || version_len == 0)
	{
		return -1;
	}
	copy_error(version, version_len, "provanity-cuda/4");
	return 0;
}

PROVANITY_CUDA_API int32_t PROVANITY_CUDA_CALL provanity_cuda_list_devices(provanity_cuda_device *devices, int32_t max_devices, char *error, uint32_t error_len)
{
	if (devices == nullptr || max_devices <= 0)
	{
		copy_error(error, error_len, "device output buffer is empty");
		return -1;
	}
	int count = 0;
	const auto err = cudaGetDeviceCount(&count);
	if (err != cudaSuccess)
	{
		copy_error(error, error_len, cuda_error("cudaGetDeviceCount", err));
		return -1;
	}
	const int n = std::min(count, max_devices);
	for (int i = 0; i < n; ++i)
	{
		if (!fill_device_info(i, devices[i]))
		{
			copy_error(error, error_len, "cudaGetDeviceProperties failed");
			return -1;
		}
	}
	return n;
}

PROVANITY_CUDA_API int32_t PROVANITY_CUDA_CALL provanity_cuda_run(const provanity_cuda_config *config, provanity_cuda_callback callback, void *user_data, char *error, uint32_t error_len)
{
	if (config == nullptr)
	{
		copy_error(error, error_len, "config is null");
		return -1;
	}

	uint8_t public_key[64];
	if (!decode_hex(config->public_key_hex, public_key, sizeof(public_key)))
	{
		copy_error(error, error_len, "public key must be 128 hexadecimal characters");
		return -1;
	}

	const auto devices = selected_devices(config);
	if (devices.empty())
	{
		copy_error(error, error_len, "no CUDA devices selected");
		emit_error(callback, user_data, "no_devices", "no CUDA devices selected");
		return -1;
	}
	if (devices.size() > 1)
	{
		copy_error(error, error_len, "multi-GPU CUDA search is not implemented yet");
		emit_error(callback, user_data, "multi_gpu_not_implemented", "multi-GPU CUDA search is not implemented yet");
		return -1;
	}
	if (config->batch_multiple == 0 || config->work_size == 0)
	{
		const std::string message = "batch_multiple and work_size must be greater than zero";
		copy_error(error, error_len, message);
		emit_error(callback, user_data, "invalid_config", message);
		return -1;
	}
	/* pv_iterate_inverse processes PV_INVERT_GROUP_SIZE lanes per thread, so
	 * the total lane count (PV_BATCH_LANES * batch_multiple) must be a
	 * multiple of PV_INVERT_GROUP_SIZE. When the group size equals
	 * PV_BATCH_LANES this holds for any batch_multiple; otherwise the product
	 * itself must divide evenly. */
	if (((static_cast<uint64_t>(PV_BATCH_LANES) * config->batch_multiple) % PV_INVERT_GROUP_SIZE) != 0)
	{
		std::ostringstream out;
		out << "PV_BATCH_LANES * batch_multiple must be a multiple of " << PV_INVERT_GROUP_SIZE
			<< " (got " << (static_cast<uint64_t>(PV_BATCH_LANES) * config->batch_multiple) << ")";
		const std::string message = out.str();
		copy_error(error, error_len, message);
		emit_error(callback, user_data, "invalid_config", message);
		return -1;
	}

	provanity_cuda_event ready;
	std::memset(&ready, 0, sizeof(ready));
	ready.type = PROVANITY_CUDA_EVENT_READY;
	ready.device_count = static_cast<int32_t>(devices.size());
	for (size_t i = 0; i < devices.size(); ++i)
	{
		if (!fill_device_info(devices[i], ready.devices[i]))
		{
			copy_error(error, error_len, "cudaGetDeviceProperties failed");
			return -1;
		}
	}
	if (callback_event(callback, user_data, ready))
	{
		return 0;
	}

	std::string cuda_err;
	const int device_id = devices[0];

	/* The first CUDA API call on this process triggers context creation and,
	 * on a cold driver cache, PTX→SASS JIT for this device's compute
	 * capability — that can take several seconds. Tell the host first so the
	 * dashboard does not look frozen. */
	if (emit_phase(callback, user_data, device_id, "ctx", "preparing CUDA context", 0))
	{
		return 0;
	}
	if (!check_cuda(cudaSetDevice(device_id), "cudaSetDevice", cuda_err))
	{
		copy_error(error, error_len, cuda_err);
		emit_error(callback, user_data, "cuda_error", cuda_err);
		return -1;
	}

	const uint32_t progress_ms = config->progress_interval_ms;

	const SeedWords seed_x = public_key_part(public_key);
	const SeedWords seed_y = public_key_part(public_key + 32);
	const uint32_t batch_multiple = config->batch_multiple;
	const size_t size = static_cast<size_t>(PV_BATCH_LANES) * batch_multiple;
	const uint32_t work_size = config->work_size;

	/* Mirrors the cudaMalloc sizes in alloc_memory(): precomp + (state_x +
	 * state_lambda + state_inv + hashes) + results. The 4× size factor
	 * captures the four Felt256 buffers; results is constant-tiny but
	 * included for honesty. */
	const uint64_t total_alloc_bytes =
		static_cast<uint64_t>(PV_PRECOMP_POINTS) * sizeof(Point) +
		4ULL * static_cast<uint64_t>(size) * sizeof(Felt256) +
		static_cast<uint64_t>(kMaxScore + 1) * sizeof(Result);

	DeviceMemory mem;
	const std::string alloc_msg = "allocating " + format_bytes(total_alloc_bytes) + " on GPU";
	if (emit_phase(callback, user_data, device_id, "alloc", alloc_msg, total_alloc_bytes))
	{
		return 0;
	}
	if (!alloc_memory(mem, size, cuda_err))
	{
		free_memory(mem);
		copy_error(error, error_len, cuda_err);
		emit_error(callback, user_data, "cuda_error", cuda_err);
		return -1;
	}

	if (emit_phase(callback, user_data, device_id, "precomp", "building secp256k1 precomp tables", 0))
	{
		free_memory(mem);
		return 0;
	}
	if (!copy_inputs(mem, config, cuda_err))
	{
		free_memory(mem);
		copy_error(error, error_len, cuda_err);
		emit_error(callback, user_data, "cuda_error", cuda_err);
		return -1;
	}

	const dim3 block(work_size);
	const dim3 grid(static_cast<unsigned int>((size + work_size - 1) / work_size));

	Seed seed;
	if (!random_seed(seed, cuda_err))
	{
		free_memory(mem);
		copy_error(error, error_len, cuda_err);
		emit_error(callback, user_data, "cuda_error", cuda_err);
		return -1;
	}
	const SeedWords seed_device = to_seed_words(seed);

	cudaStream_t stream = nullptr;
	if (!check_cuda(cudaStreamCreateWithFlags(&stream, cudaStreamNonBlocking), "cudaStreamCreateWithFlags", cuda_err))
	{
		free_memory(mem);
		copy_error(error, error_len, cuda_err);
		emit_error(callback, user_data, "cuda_error", cuda_err);
		return -1;
	}

	Result *host_results = nullptr;
	if (!check_cuda(cudaMallocHost(&host_results, (kMaxScore + 1) * sizeof(Result)), "cudaMallocHost(results)", cuda_err))
	{
		free_run_resources(mem, stream, host_results);
		copy_error(error, error_len, cuda_err);
		emit_error(callback, user_data, "cuda_error", cuda_err);
		return -1;
	}

	/* The init pipeline derives the per-lane starting point via Jacobian
	 * scalar mul + batched cross-lane Z^-1, then a finalize pass producing
	 * the affine layout the steady-state loop expects. Surface the lane
	 * count so users on slow cards understand the wait. */
	const std::string init_msg = "initializing " + format_count(static_cast<uint64_t>(size)) + " lanes";
	if (emit_phase(callback, user_data, device_id, "init_lanes", init_msg, static_cast<uint64_t>(size)))
	{
		free_run_resources(mem, stream, host_results);
		return 0;
	}
	if (!launch_init(mem, size, seed_device, seed_x, seed_y, grid, block, stream, cuda_err))
	{
		free_run_resources(mem, stream, host_results);
		copy_error(error, error_len, cuda_err);
		emit_error(callback, user_data, "cuda_error", cuda_err);
		return -1;
	}

	std::memset(host_results, 0, (kMaxScore + 1) * sizeof(Result));
	uint8_t score_max = 0;
	uint64_t rounds = 0;
	uint64_t last_progress_attempts = 0;
	const auto start = std::chrono::steady_clock::now();
	auto last_progress = start;

	for (;;)
	{
		const uint64_t attempt_base = rounds * size;
		if (!launch_round(config, mem, size, grid, block, stream, score_max, rounds, cuda_err))
		{
			free_run_resources(mem, stream, host_results);
			copy_error(error, error_len, cuda_err);
			emit_error(callback, user_data, "cuda_error", cuda_err);
			return -1;
		}

		++rounds;
		if (!check_cuda(cudaMemcpyAsync(host_results, mem.results, (kMaxScore + 1) * sizeof(Result), cudaMemcpyDeviceToHost, stream), "cudaMemcpyAsync(results)", cuda_err) ||
			!check_cuda(cudaStreamSynchronize(stream), "kernel execution", cuda_err))
		{
			free_run_resources(mem, stream, host_results);
			copy_error(error, error_len, cuda_err);
			emit_error(callback, user_data, "cuda_error", cuda_err);
			return -1;
		}

		for (int score = kMaxScore; score > score_max; --score)
		{
			if (host_results[score].found > 0)
			{
				score_max = static_cast<uint8_t>(score);

				/* The kernel derived this candidate from
				 * `seed + attempt_base + found_id`, so the offset and
				 * total-attempts we report back to the host must use the
				 * `attempt_base` for the round that just produced it. Using
				 * `rounds * size` here would be one full batch too far
				 * because `rounds` has already been incremented above to
				 * count the round we just executed. */
				const uint64_t found_attempt = attempt_base + host_results[score].found_id;

				provanity_cuda_event found;
				std::memset(&found, 0, sizeof(found));
				found.type = PROVANITY_CUDA_EVENT_FOUND;
				found.elapsed_sec = elapsed_seconds(start);
				found.elapsed_ms = elapsed_milliseconds(start);
				found.attempts = found_attempt;
				found.score = score;
				format_offset(seed, found_attempt, found.offset);
				hex_bytes(host_results[score].found_hash, 20, found.address);
				if (callback_event(callback, user_data, found))
				{
					free_run_resources(mem, stream, host_results);
					return 0;
				}
				break;
			}
		}

		const auto now = std::chrono::steady_clock::now();
		const auto elapsed_ms = std::chrono::duration_cast<std::chrono::milliseconds>(now - last_progress).count();
		if (progress_ms > 0 && elapsed_ms >= progress_ms)
		{
			const uint64_t attempts = rounds * size;
			const auto interval_ms = std::max<int64_t>(1, elapsed_ms);
			const uint64_t hashrate = ((attempts - last_progress_attempts) * 1000ULL) / static_cast<uint64_t>(interval_ms);
			last_progress = now;
			last_progress_attempts = attempts;

			provanity_cuda_event progress;
			std::memset(&progress, 0, sizeof(progress));
			progress.type = PROVANITY_CUDA_EVENT_PROGRESS;
			progress.elapsed_sec = elapsed_seconds(start);
			progress.elapsed_ms = elapsed_milliseconds(start);
			progress.attempts = attempts;
			progress.hashrate = hashrate;
			progress.device_count = 1;
			progress.devices[0] = ready.devices[0];
			progress.devices[0].hashrate = hashrate;
			if (callback_event(callback, user_data, progress))
			{
				free_run_resources(mem, stream, host_results);
				return 0;
			}
		}
	}
}
