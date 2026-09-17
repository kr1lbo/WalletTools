#pragma once

#include "point.cuh"
#include "keccak.cuh"
#include "sha256.cuh"
#include "secp256k1_tables.hpp"

#define CUDA_KERNEL_BOUNDS __launch_bounds__(256, 2)

/* Per-lane state retained across rounds is held as three parallel arrays
 * (structure-of-arrays): state_x caches the current public key's x coordinate,
 * state_lambda the slope used to reach it (so the next round's slope follows
 * from a constant-step recurrence instead of a fresh division), and state_inv
 * a scratch slot that the inverse stage fills with the folded slope numerator
 * for the upcoming step. SoA is what keeps the per-field reads coalesced: a
 * warp touching state_x[id..id+31] hits 32 adjacent 32-byte values, whereas an
 * interleaved {x,lambda,inv} struct would stride those reads 96 bytes apart and
 * waste ~2/3 of every cache line. */

/* The point added to every lane on each round of pv_iterate_step. It encodes
 * `size * G` (size being the total lane count of the current run) so that
 * lane j round r corresponds to the private-key offset j + r * size. The
 * value is filled by pv_compute_step before the iteration loop starts. */
__device__ Point g_iterate_step;

/* Twice the step point's y coordinate, mod p. Adding a fixed point S=(sx,sy)
 * repeatedly admits a slope recurrence lambda_{n+1} = 2*sy/(sx - x_{n+1}) -
 * lambda_n, so the inverse stage multiplies each lane's reciprocal by this
 * constant once and the step stage then needs only a subtraction (not a full
 * multiply) to obtain the next slope. Filled by pv_compute_step. */
__device__ Felt256 g_two_step_y;

/* Lanes handled per thread by the batched (Montgomery) inversion stage. This
 * is decoupled from PV_BATCH_LANES: a smaller group launches more threads, so
 * the inverse stage stops starving the GPU of blocks (the old value of 255
 * launched only ~64 blocks for a multi-hundred-SM card and left the kernel
 * latency-bound). With the interleaved (coalesced) lane layout below, 102 is
 * the measured throughput optimum on sm_120. 102 = 2*3*17 shares the factor
 * 3*17=51 with 255, so 255*batch_multiple is divisible by 102 whenever
 * batch_multiple is even — which holds for every batch_multiple the tuner and
 * memory-budget logic emit. Overridable at compile time for tuning sweeps.
 *
 * Defined here at the top of the file so both the init-pipeline batched
 * inverse (pv_batched_invert) and the steady-state inverse (pv_iterate_inverse)
 * can refer to it. */
#ifndef PV_INVERT_GROUP_SIZE
#define PV_INVERT_GROUP_SIZE 102
#endif

__device__ __forceinline__ void pv_keccak_address(const Felt256 *const px, const Felt256 *const py, Felt256 *const out)
{
	EthHash h;
#pragma unroll
	for (int i = 0; i < 50; ++i)
	{
		h.d[i] = 0;
	}
#pragma unroll
	for (int word = 0; word < FELT_U32_WORDS; ++word)
	{
		h.d[word] = bswap32(px->d[FELT_U32_WORDS - 1 - word]);
		h.d[word + FELT_U32_WORDS] = bswap32(py->d[FELT_U32_WORDS - 1 - word]);
	}
	h.d[16] ^= 0x01;
	sha3_keccakf(&h);
#pragma unroll
	for (int word = 0; word < 5; ++word)
	{
		out->d[word] = h.d[word + 3];
	}
}

__global__ void CUDA_KERNEL_BOUNDS pv_clear_results(Result *__restrict__ const result)
{
	const int id = blockIdx.x * blockDim.x + threadIdx.x;
	if (id <= PROVANITY_CUDA_MAX_SCORE)
	{
		result[id].found = 0;
	}
}

/* Single-thread helper that turns the host-provided lane count into the
 * shared step point `size * G`, then publishes it to g_iterate_step so every
 * lane in pv_iterate_step can read it from constant-cached memory. The cost
 * is one scalar multiplication amortised over the entire run. */
__global__ void pv_compute_step(const Point *__restrict__ const precomp, const SeedWords step_scalar)
{
	if (threadIdx.x != 0 || blockIdx.x != 0)
	{
		return;
	}
	Point s;
	pv_scalar_times_g(&s, precomp, &step_scalar);
	g_iterate_step = s;
	felt_mod_add(&g_two_step_y, &s.y, &s.y);
}

/* Stage 1a: same arithmetic as pv_iterate_init but kept entirely in Jacobian
 * coordinates so the per-lane scalar mul + base add introduces *zero* modular
 * inversions. The Jacobian (X, Y, Z) for each lane is stashed in the existing
 * scratch slots (state_x, state_lambda, state_inv); the follow-up
 * pv_batched_invert + pv_iterate_init_finalize pair turns them into the affine
 * (x, prev_lambda, delta_x) layout the steady-state loop expects.
 *
 * Math:
 *   lane_pub_jac = (seed + id) * G   computed via byte-decomposed mixed adds
 *   p_jac        = lane_pub_jac + base  (another mixed add — base is affine)
 *
 * Why this matters: the affine path inside pv_scalar_times_g pays one full
 * felt_mod_inv per non-zero scalar byte. For random 256-bit scalars that is
 * ~32 inversions per lane plus one more for the base add — roughly ~17000
 * field multiplications. The Jacobian version replaces each add with 8M + 3S,
 * total ~350 multiplications, with a single batched cross-lane Z^-1 below to
 * close out. */
__global__ void CUDA_KERNEL_BOUNDS pv_iterate_init_jac(
	const Point *__restrict__ const precomp,
	Felt256 *__restrict__ const state_x,
	Felt256 *__restrict__ const state_lambda,
	Felt256 *__restrict__ const state_inv,
	const Point base,
	const SeedWords seed)
{
	const size_t id = blockIdx.x * blockDim.x + threadIdx.x;

	SeedWords scalar = seed;
	const uint64_t low = scalar.x + static_cast<uint64_t>(id);
	uint64_t carry = (low < scalar.x) ? 1ULL : 0ULL;
	scalar.x = low;
	scalar.y += carry;
	carry = (carry != 0ULL && scalar.y == 0ULL) ? 1ULL : 0ULL;
	scalar.z += carry;
	carry = (carry != 0ULL && scalar.z == 0ULL) ? 1ULL : 0ULL;
	scalar.w += carry;

	JacobianPoint lane_jac;
	pv_scalar_times_g_jac(&lane_jac, precomp, &scalar);

	JacobianPoint p_jac;
	jac_add_affine(&p_jac, &lane_jac, &base);

	state_x[id] = p_jac.X;
	state_lambda[id] = p_jac.Y;
	state_inv[id] = p_jac.Z;
}

/* Stage 1b: batched cross-lane inversion of the Jacobian Z values. Mirrors
 * pv_iterate_inverse but does NOT fold g_two_step_y into the result — the
 * caller wants 1/Z, not 2*sy/Z. Layout, ownership, and the prefix-product
 * algorithm are otherwise identical so the same coalescing properties apply. */
__global__ void CUDA_KERNEL_BOUNDS pv_batched_invert(Felt256 *__restrict__ const state, const uint32_t stride)
{
	const size_t tid = blockIdx.x * blockDim.x + threadIdx.x;
	if (tid >= stride)
	{
		return;
	}
	const size_t s = stride;

	Felt256 pfx[PV_INVERT_GROUP_SIZE];
	Felt256 prefix = state[tid];
	pfx[0] = prefix;
#pragma unroll 1
	for (int k = 1; k < PV_INVERT_GROUP_SIZE; ++k)
	{
		Felt256 v = state[tid + static_cast<size_t>(k) * s];
		felt_mod_mul(&prefix, &prefix, &v);
		pfx[k] = prefix;
	}

	Felt256 running;
	felt_mod_inv(running, prefix);

#pragma unroll 1
	for (int k = PV_INVERT_GROUP_SIZE - 1; k > 0; --k)
	{
		const size_t slot = tid + static_cast<size_t>(k) * s;
		Felt256 v = state[slot];
		Felt256 lane_inv;
		felt_mod_mul(&lane_inv, &running, &pfx[k - 1]);
		state[slot] = lane_inv;
		felt_mod_mul(&running, &running, &v);
	}
	state[tid] = running;
}

/* Stage 1c: consume (X, Y) from (state_x, state_lambda) plus the inverted Z
 * from state_inv, produce the affine layout the steady-state loop wants —
 * state_x[id] = x_aff, state_lambda[id] = prev_lambda, state_inv[id] = delta_x,
 * and hashes[id] = keccak(x_aff || y_aff). The seed slope still pays one full
 * felt_mod_inv per lane (to fold (sy + y) onto delta_x^-1), but that is now
 * the *only* inversion per lane in the entire init pipeline. */
__global__ void CUDA_KERNEL_BOUNDS pv_iterate_init_finalize(
	Felt256 *__restrict__ const state_x,
	Felt256 *__restrict__ const state_lambda,
	Felt256 *__restrict__ const state_inv,
	Felt256 *__restrict__ const hashes)
{
	const size_t id = blockIdx.x * blockDim.x + threadIdx.x;

	const Felt256 X = state_x[id];
	const Felt256 Y = state_lambda[id];
	const Felt256 z_inv = state_inv[id];

	Felt256 z_inv2;
	felt_mod_sqr(&z_inv2, &z_inv);
	Felt256 z_inv3;
	felt_mod_mul(&z_inv3, &z_inv2, &z_inv);

	Felt256 x_aff;
	felt_mod_mul(&x_aff, &X, &z_inv2);
	Felt256 y_aff;
	felt_mod_mul(&y_aff, &Y, &z_inv3);

	pv_keccak_address(&x_aff, &y_aff, &hashes[id]);

	const Felt256 sx = g_iterate_step.x;
	const Felt256 sy = g_iterate_step.y;

	Felt256 delta_x;
	felt_mod_sub(&delta_x, &sx, &x_aff);

	Felt256 inv_dx = delta_x;
	felt_mod_inv(&inv_dx);

	Felt256 sy_plus_y;
	felt_mod_add(&sy_plus_y, &sy, &y_aff);

	Felt256 prev_lambda;
	felt_mod_mul(&prev_lambda, &sy_plus_y, &inv_dx);

	state_x[id] = x_aff;
	state_lambda[id] = prev_lambda;
	state_inv[id] = delta_x;
}

/* Stage 2: replace state_inv[*] (currently delta_x) with 2*sy times its
 * modular inverse, using Montgomery's batched inversion. Each thread owns
 * PV_INVERT_GROUP_SIZE consecutive lanes; the heavy 256-bit felt_mod_inv only
 * runs once per thread and the per-lane cost collapses to ~3 felt_mod_mul plus
 * a local-memory shuffle, which is what makes the differential iteration
 * scheme worthwhile.
 *
 * We keep a thread-local copy of the original delta_x values so we can reuse
 * state_inv[*] as scratch storage for the prefix-product chain, avoiding the
 * need for a separate prefix buffer in global memory. */
__global__ void CUDA_KERNEL_BOUNDS pv_iterate_inverse(Felt256 *__restrict__ const state_inv, const uint32_t stride)
{
	const size_t tid = blockIdx.x * blockDim.x + threadIdx.x;
	if (tid >= stride)
	{
		return;
	}

	/* Interleaved (strided) lane assignment: this thread owns the lanes
	 * { tid, tid+stride, tid+2*stride, ... }, where `stride` is the total
	 * thread count. With a contiguous [tid*G, tid*G+G) assignment the 32 lanes
	 * of a warp touched state_inv addresses G*32 bytes apart on every element
	 * step (fully uncoalesced; the kernel ran at ~30% of memory bandwidth).
	 * Striding by the thread count instead makes the warp hit 32 consecutive
	 * Felt256 per step, so every global load/store coalesces. Each physical
	 * lane index is still covered exactly once and its inverse is written back
	 * to its own slot, so the result is identical to the contiguous layout. */
	const size_t s = stride;

	/* Keep the per-lane delta_x values in global state_inv and re-read them in
	 * the backward pass (an L2 hit), so the only thread-local array is the
	 * prefix-product chain. The earlier scheme stored delta_x in a local dx[]
	 * array AND streamed the prefix chain through global state_inv; that made
	 * the kernel local-memory bound on the dx[] spill. This layout reduces both
	 * the local traffic (one array instead of one array plus a preload round
	 * trip) and the global traffic (no separate prefix-product write/read pass),
	 * cutting the inverse stage ~28% at large batch. The strided lane ownership
	 * (and hence coalescing) is unchanged.
	 *
	 * Forward pass: pfx[k] = delta_x[0] * delta_x[1] * ... * delta_x[k]. */
	Felt256 pfx[PV_INVERT_GROUP_SIZE];
	Felt256 prefix = state_inv[tid];
	pfx[0] = prefix;
#pragma unroll 1
	for (int k = 1; k < PV_INVERT_GROUP_SIZE; ++k)
	{
		Felt256 v = state_inv[tid + static_cast<size_t>(k) * s];
		felt_mod_mul(&prefix, &prefix, &v);
		pfx[k] = prefix;
	}

	/* One full inversion gives the inverse of the total product. Folding the
	 * 2*sy constant in here means every per-lane reciprocal emerges already
	 * scaled to the slope numerator, so the step stage skips a multiply. */
	Felt256 running;
	felt_mod_inv(running, prefix);
	felt_mod_mul(&running, &running, &g_two_step_y);

	/* Backward pass: re-read delta_x[k] from global, write the lane inverse to
	 * the same slot. For k from N-1 down to 1:
	 *   inv(delta_x[k])  = running * pfx[k-1]
	 *   running_{k-1}    = running * delta_x[k]
	 * and the lane at tid receives whatever `running` accumulates to at the end. */
#pragma unroll 1
	for (int k = PV_INVERT_GROUP_SIZE - 1; k > 0; --k)
	{
		const size_t slot = tid + static_cast<size_t>(k) * s;
		Felt256 v = state_inv[slot];
		Felt256 lane_inv;
		felt_mod_mul(&lane_inv, &running, &pfx[k - 1]);
		state_inv[slot] = lane_inv;
		felt_mod_mul(&running, &running, &v);
	}
	state_inv[tid] = running;
}

/* Stage 3: advance every lane by the constant addend S = (sx, sy).
 *
 * Because S is fixed, the slope to the next point follows a recurrence rather
 * than a fresh division (see g_two_step_y). With `factor` = 2*sy/(sx - x) (the
 * folded reciprocal from the inverse stage) and the previous slope `prev`:
 *   lambda  = factor - prev                 (the new slope, one subtraction)
 *   x_new   = lambda^2 - sx - x
 *   y_new   = lambda * (sx - x_new) - sy
 *
 * The kernel writes the next-round hash into `hashes[id]`, carries the slope
 * forward in state_lambda[id], and stages the next reciprocal input
 * (sx - x_new) in state_inv[id] so the inverse->step cycle repeats without
 * touching the precomp table. */
__global__ void CUDA_KERNEL_BOUNDS pv_iterate_step(
	Felt256 *__restrict__ const state_x,
	Felt256 *__restrict__ const state_lambda,
	Felt256 *__restrict__ const state_inv,
	Felt256 *__restrict__ const hashes)
{
	const size_t id = blockIdx.x * blockDim.x + threadIdx.x;

	const Felt256 step_x = g_iterate_step.x;
	const Felt256 step_y = g_iterate_step.y;

	Felt256 x = state_x[id];
	Felt256 prev_lambda = state_lambda[id];
	Felt256 factor = state_inv[id]; /* == 2*step_y / (step_x - x) */

	/* Slope recurrence: lambda_n = 2*sy/(sx - x_n) - lambda_{n-1}. The first
	 * term is the folded reciprocal already sitting in `factor`, so the new
	 * slope costs a single subtraction rather than a multiply. */
	Felt256 lambda;
	felt_mod_sub(&lambda, &factor, &prev_lambda);

	Felt256 lambda_sq;
	felt_mod_sqr(&lambda_sq, &lambda);

	Felt256 x_new;
	felt_mod_sub(&x_new, &lambda_sq, &step_x);
	felt_mod_sub(&x_new, &x_new, &x);

	/* delta_next = step_x - x_new is both the next round's reciprocal input
	 * and the multiplier that recovers y_new = lambda*(sx - x_new) - sy. */
	Felt256 delta_next;
	felt_mod_sub(&delta_next, &step_x, &x_new);

	Felt256 y_new;
	felt_mod_mul(&y_new, &lambda, &delta_next);
	felt_mod_sub(&y_new, &y_new, &step_y);

	pv_keccak_address(&x_new, &y_new, &hashes[id]);

	state_x[id] = x_new;
	state_lambda[id] = lambda;
	state_inv[id] = delta_next;
}

struct Hash20Words
{
	uint32_t w[5];
};

__device__ __forceinline__ Hash20Words pv_load_hash20(const Felt256 *__restrict__ const hashes, const size_t id)
{
	const uint4 head = *reinterpret_cast<const uint4 *>(&hashes[id]);
	const Hash20Words hash = {{head.x, head.y, head.z, head.w, hashes[id].d[4]}};
	return hash;
}

__device__ __forceinline__ uint8_t pv_hash20_byte(const Hash20Words *const hash, const int index)
{
	const uint32_t word = hash->w[index >> 2];
	return static_cast<uint8_t>(word >> ((index & 3) * 8));
}

__device__ __forceinline__ void pv_store_hash20(uint8_t *const out, const Hash20Words *const hash)
{
#pragma unroll
	for (int i = 0; i < 20; ++i)
	{
		out[i] = pv_hash20_byte(hash, i);
	}
}

/* Unpack the 20-byte address into 40 hex nibbles, MSB nibble first within
 * each byte. All scoring kernels operate on this nibble stream so the
 * scoring code stays uniform and never mixes hi/lo byte masking with
 * comparison logic. */
__device__ __forceinline__ void pv_unpack_nibbles(const Hash20Words *const hash, uint8_t out[40])
{
	for (int i = 0; i < 20; ++i)
	{
		const uint8_t b = pv_hash20_byte(hash, i);
		out[i * 2] = static_cast<uint8_t>(b >> 4);
		out[i * 2 + 1] = static_cast<uint8_t>(b & 0x0fU);
	}
}

/* Publish a candidate at slot `score` if it strictly improves on the
 * current best. The first writer for each score wins the slot; later
 * equal-score winners only bump the counter so we never overwrite
 * already-published data. */
__device__ __forceinline__ void pv_publish_candidate(const size_t id, const Hash20Words *const hash, Result *const result, const int score, const uint8_t score_max)
{
	if (score <= 0 || score <= static_cast<int>(score_max))
	{
		return;
	}
	const unsigned int prev = atomicAdd(reinterpret_cast<unsigned int *>(&result[score].found), 1U);
	if (prev != 0U)
	{
		return;
	}
	result[score].found_id = static_cast<uint32_t>(id);
	if (hash != nullptr)
	{
		pv_store_hash20(result[score].found_hash, hash);
	}
}

/* Rehash an EOA address into the contract address derived from a nonce-0
 * deployment: keccak256(rlp([address, 0])). The RLP encoding of a 20-byte
 * address followed by zero nonce is the fixed 23-byte prefix
 *   0xd6 0x94 <20-byte address> 0x80
 * which we lay out manually so the keccak input layout never depends on
 * compiler aliasing of the EthHash union. */
__global__ void CUDA_KERNEL_BOUNDS pv_transform_contract(Felt256 *__restrict__ const hashes)
{
	const size_t id = blockIdx.x * blockDim.x + threadIdx.x;
	const Hash20Words hash = pv_load_hash20(hashes, id);

	EthHash h;
#pragma unroll
	for (int i = 0; i < 50; ++i)
	{
		h.d[i] = 0;
	}

	/* RLP-encoded list header + 20-byte address + zero-nonce terminator. */
	h.b[0] = 0xd6;
	h.b[1] = 0x94;
	pv_store_hash20(&h.b[2], &hash);
	h.b[22] = 0x80;
	/* Keccak-256 length-23 padding byte (0x01) then the high bit terminator
	 * applied inside sha3_keccakf. */
	h.b[23] = 0x01;
	sha3_keccakf(&h);

#pragma unroll
	for (int i = 0; i < 5; ++i)
	{
		hashes[id].d[i] = h.d[i + 3];
	}
}

/* Leading nibble run: count the longest prefix of the 40-nibble address
 * representation whose entries all equal g_pattern[0]. */
__global__ void CUDA_KERNEL_BOUNDS pv_score_leading(Felt256 *__restrict__ const hashes, Result *__restrict__ const result, const uint8_t score_max)
{
	const size_t id = blockIdx.x * blockDim.x + threadIdx.x;
	const Hash20Words hash = pv_load_hash20(hashes, id);
	uint8_t nibbles[40];
	pv_unpack_nibbles(&hash, nibbles);

	const uint8_t target = g_pattern[0];
	int score = 0;
	while (score < 40 && nibbles[score] == target)
	{
		++score;
	}

	pv_publish_candidate(id, &hash, result, score, score_max);
}

__global__ void CUDA_KERNEL_BOUNDS pv_score_mask(Felt256 *__restrict__ const hashes, Result *__restrict__ const result, const uint8_t score_max)
{
	const size_t id = blockIdx.x * blockDim.x + threadIdx.x;
	const Hash20Words hash = pv_load_hash20(hashes, id);
	uint8_t nibbles[40];
	pv_unpack_nibbles(&hash, nibbles);
	int score = 0;
#pragma unroll
	for (int i = 0; i < 40; ++i)
		score += (g_pattern_masks[i] & (static_cast<uint16_t>(1U) << nibbles[i])) != 0 ? 1 : 0;
	pv_publish_candidate(id, &hash, result, score, score_max);
}

/* Positional pattern: each concrete g_pattern[i] in 0..15 contributes
 * one point when nibble i matches; PROVANITY_CUDA_PATTERN_WILDCARD slots
 * are ignored. */
__global__ void CUDA_KERNEL_BOUNDS pv_score_pattern(Felt256 *__restrict__ const hashes, Result *__restrict__ const result, const uint8_t score_max)
{
	const size_t id = blockIdx.x * blockDim.x + threadIdx.x;
	const Hash20Words hash = pv_load_hash20(hashes, id);
	uint8_t nibbles[40];
	pv_unpack_nibbles(&hash, nibbles);

	int score = 0;
	uint8_t gx = 0, gy = 0;
	bool have_x = false, have_y = false;
#pragma unroll
	for (int i = 0; i < 40; ++i)
	{
		const uint8_t want = g_pattern[i];
		if (want == PROVANITY_CUDA_PATTERN_GROUP_X) {
			if (!have_x) { gx = nibbles[i]; have_x = true; }
			score += nibbles[i] == gx ? 1 : 0;
		} else if (want == PROVANITY_CUDA_PATTERN_GROUP_Y) {
			if (!have_y) { gy = nibbles[i]; have_y = true; }
			score += nibbles[i] == gy ? 1 : 0;
		} else {
			const bool concrete = want != PROVANITY_CUDA_PATTERN_WILDCARD;
			score += (concrete && nibbles[i] == (want & 0x0fU)) ? 1 : 0;
		}
	}

	pv_publish_candidate(id, &hash, result, score, score_max);
}

/* True when the 20-byte address is within the inclusive big-endian bounds of
 * ladder level `level` (lo at g_tron_prefix_ladder[level*40 .. +19], hi at
 * +20 .. +39). Byte 0 is most significant; the running cmp_* hold the first
 * non-equal comparison's sign. */
__device__ __forceinline__ bool pv_addr_in_level(const Hash20Words *const hash, const int level)
{
	const uint8_t *const lo = &g_tron_prefix_ladder[level * 40];
	const uint8_t *const hi = &g_tron_prefix_ladder[level * 40 + 20];
	int cmp_lo = 0;
	int cmp_hi = 0;
#pragma unroll
	for (int i = 0; i < 20; ++i)
	{
		const uint8_t a = pv_hash20_byte(hash, i);
		if (cmp_lo == 0)
		{
			cmp_lo = (a < lo[i]) ? -1 : (a > lo[i]) ? 1 : 0;
		}
		if (cmp_hi == 0)
		{
			cmp_hi = (a < hi[i]) ? -1 : (a > hi[i]) ? 1 : 0;
		}
	}
	return cmp_lo >= 0 && cmp_hi <= 0;
}

/* Tron prefix score: the depth of the deepest nested interval that contains the
 * address (= number of matched characters after the implicit 'T'). The ladder
 * intervals are nested, so scanning from the deepest level down and returning
 * the first containing level yields the matched-character count. No base58, no
 * SHA-256 -- just up to g_tron_prefix_levels 160-bit compares. */
__device__ __forceinline__ int pv_tron_prefix_depth(const Hash20Words *const hash)
{
	const int levels = static_cast<int>(g_tron_prefix_levels);
	for (int j = levels; j >= 1; --j)
	{
		if (pv_addr_in_level(hash, j - 1))
		{
			return j;
		}
	}
	return 0;
}

__global__ void CUDA_KERNEL_BOUNDS pv_score_tron_prefix(Felt256 *__restrict__ const hashes, Result *__restrict__ const result, const uint8_t score_max)
{
	const size_t id = blockIdx.x * blockDim.x + threadIdx.x;
	const Hash20Words hash = pv_load_hash20(hashes, id);
	const int score = pv_tron_prefix_depth(&hash);
	pv_publish_candidate(id, &hash, result, score, score_max);
}

/* Tron suffix score: the length of the longest trailing run that matches the
 * target. The suffix is checksum-dependent, so we compute the real base58check
 * checksum (SHA-256 twice over 0x41||address), then reduce the 25-byte payload
 * modulo g_tron_suffix_mod (= 58^len) to recover the trailing base58 digits.
 * No big-number base58 division -- one 64-bit Horner reduction over 25 bytes. */
__device__ __forceinline__ int pv_tron_suffix_depth(const Hash20Words *const hash)
{
	uint8_t payload[25];
	payload[0] = 0x41;
	pv_store_hash20(&payload[1], hash);

	uint8_t digest[32];
	pv_sha256(payload, 21, digest);
	pv_sha256(digest, 32, digest);
	payload[21] = digest[0];
	payload[22] = digest[1];
	payload[23] = digest[2];
	payload[24] = digest[3];

	const uint64_t mod = g_tron_suffix_mod;
	uint64_t r = 0;
#pragma unroll
	for (int i = 0; i < 25; ++i)
	{
		r = (r * 256ULL + static_cast<uint64_t>(payload[i])) % mod;
	}

	const int n = static_cast<int>(g_tron_suffix_len);
	int score = 0;
	for (int i = 0; i < n; ++i)
	{
		const uint8_t digit = static_cast<uint8_t>(r % 58ULL);
		r /= 58ULL;
		if (digit != g_tron_suffix_digits[i])
		{
			break;
		}
		++score;
	}
	return score;
}

__global__ void CUDA_KERNEL_BOUNDS pv_score_tron_suffix(Felt256 *__restrict__ const hashes, Result *__restrict__ const result, const uint8_t score_max)
{
	const size_t id = blockIdx.x * blockDim.x + threadIdx.x;
	const Hash20Words hash = pv_load_hash20(hashes, id);
	const int score = pv_tron_suffix_depth(&hash);
	pv_publish_candidate(id, &hash, result, score, score_max);
}

/* Pack a keccak-output Felt256 (which has d[0..4] populated with the 20-byte
 * address) directly into a Hash20Words without going through global memory.
 * Used by the fused step+score kernel below so the hash never leaves registers
 * between the step kernel that produces it and the score logic that consumes
 * it. */
__device__ __forceinline__ Hash20Words pv_pack_hash20_from_felt(const Felt256 *const hash)
{
	Hash20Words out;
#pragma unroll
	for (int i = 0; i < 5; ++i)
	{
		out.w[i] = hash->d[i];
	}
	return out;
}

/* Score functors used by the fused step+score kernel template. Each computes a
 * candidate score from a register-resident Hash20Words. Mirrors the logic in
 * the standalone pv_score_* kernels but is reusable from within a template. */
struct ScoreOpLeading
{
	__device__ __forceinline__ static int compute(const Hash20Words *const hash)
	{
		uint8_t nibbles[40];
		pv_unpack_nibbles(hash, nibbles);
		const uint8_t target = g_pattern[0];
		int score = 0;
		while (score < 40 && nibbles[score] == target)
		{
			++score;
		}
		return score;
	}
};

struct ScoreOpPattern
{
	__device__ __forceinline__ static int compute(const Hash20Words *const hash)
	{
		uint8_t nibbles[40];
		pv_unpack_nibbles(hash, nibbles);
		int score = 0;
		uint8_t gx = 0, gy = 0;
		bool have_x = false, have_y = false;
#pragma unroll
		for (int i = 0; i < 40; ++i)
		{
			const uint8_t want = g_pattern[i];
			if (want == PROVANITY_CUDA_PATTERN_GROUP_X) {
				if (!have_x) { gx = nibbles[i]; have_x = true; }
				score += nibbles[i] == gx ? 1 : 0;
			} else if (want == PROVANITY_CUDA_PATTERN_GROUP_Y) {
				if (!have_y) { gy = nibbles[i]; have_y = true; }
				score += nibbles[i] == gy ? 1 : 0;
			} else {
				const bool concrete = want != PROVANITY_CUDA_PATTERN_WILDCARD;
				score += (concrete && nibbles[i] == (want & 0x0fU)) ? 1 : 0;
			}
		}
		return score;
	}
};

struct ScoreOpMask
{
	__device__ __forceinline__ static int compute(const Hash20Words *const hash)
	{
		uint8_t nibbles[40];
		pv_unpack_nibbles(hash, nibbles);
		int score = 0;
#pragma unroll
		for (int i = 0; i < 40; ++i)
			score += (g_pattern_masks[i] & (static_cast<uint16_t>(1U) << nibbles[i])) != 0 ? 1 : 0;
		return score;
	}
};

struct ScoreOpTronPrefix
{
	__device__ __forceinline__ static int compute(const Hash20Words *const hash)
	{
		return pv_tron_prefix_depth(hash);
	}
};

struct ScoreOpTronSuffix
{
	__device__ __forceinline__ static int compute(const Hash20Words *const hash)
	{
		return pv_tron_suffix_depth(hash);
	}
};

/* Fused step + score: same arithmetic as pv_iterate_step but with the hash
 * scored inline from registers and never written to global `hashes[id]`. Saves
 * the 32B write in step + 32B read in the standalone score kernel = ~15% of
 * total memory traffic per iteration. Caller invokes the right instantiation
 * (Leading / Pattern / Tron). Only used for non-contract address modes; the
 * contract path still goes through the unfused pipeline because it needs a
 * second keccak on the hashes[] buffer. */
template <typename ScoreOp>
__global__ void CUDA_KERNEL_BOUNDS pv_iterate_step_scored(
	Felt256 *__restrict__ const state_x,
	Felt256 *__restrict__ const state_lambda,
	Felt256 *__restrict__ const state_inv,
	Result *__restrict__ const result,
	const uint8_t score_max)
{
	const size_t id = blockIdx.x * blockDim.x + threadIdx.x;

	const Felt256 step_x = g_iterate_step.x;
	const Felt256 step_y = g_iterate_step.y;

	Felt256 x = state_x[id];
	Felt256 prev_lambda = state_lambda[id];
	Felt256 factor = state_inv[id];

	Felt256 lambda;
	felt_mod_sub(&lambda, &factor, &prev_lambda);

	Felt256 lambda_sq;
	felt_mod_sqr(&lambda_sq, &lambda);

	Felt256 x_new;
	felt_mod_sub(&x_new, &lambda_sq, &step_x);
	felt_mod_sub(&x_new, &x_new, &x);

	Felt256 delta_next;
	felt_mod_sub(&delta_next, &step_x, &x_new);

	Felt256 y_new;
	felt_mod_mul(&y_new, &lambda, &delta_next);
	felt_mod_sub(&y_new, &y_new, &step_y);

	Felt256 hash_buf;
	pv_keccak_address(&x_new, &y_new, &hash_buf);

	const Hash20Words hash = pv_pack_hash20_from_felt(&hash_buf);
	const int score = ScoreOp::compute(&hash);
	pv_publish_candidate(id, &hash, result, score, score_max);

	state_x[id] = x_new;
	state_lambda[id] = lambda;
	state_inv[id] = delta_next;
	/* No write to hashes[id] — the hash never leaves registers. */
}
