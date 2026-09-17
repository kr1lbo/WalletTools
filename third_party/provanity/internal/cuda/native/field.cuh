#pragma once

#include "constants.cuh"

#define FIELD_DEVICE __device__ __forceinline__

FIELD_DEVICE uint64_t felt_add_carry(const uint64_t a, const uint64_t b, const uint64_t carry, uint64_t &out)
{
	const uint64_t s = a + b;
	const uint64_t c0 = s < a;
	out = s + carry;
	return c0 | (out < s);
}

FIELD_DEVICE uint64_t felt_sub_borrow(const uint64_t a, const uint64_t b, const uint64_t borrow, uint64_t &out)
{
	const uint64_t d = a - b;
	const uint64_t b0 = a < b;
	out = d - borrow;
	return b0 | (d < borrow);
}

FIELD_DEVICE uint64_t felt_add_raw(Felt256 &r, const Felt256 &a, const Felt256 &b)
{
	uint64_t carry = 0;
	carry = felt_add_carry(a.v[0], b.v[0], carry, r.v[0]);
	carry = felt_add_carry(a.v[1], b.v[1], carry, r.v[1]);
	carry = felt_add_carry(a.v[2], b.v[2], carry, r.v[2]);
	carry = felt_add_carry(a.v[3], b.v[3], carry, r.v[3]);
	return carry;
}

FIELD_DEVICE void felt_add_raw(uint64_t *const r, const uint64_t *const a, const uint64_t *const b, uint64_t *const carry_out)
{
	uint64_t carry = 0;
	carry = felt_add_carry(a[0], b[0], carry, r[0]);
	carry = felt_add_carry(a[1], b[1], carry, r[1]);
	carry = felt_add_carry(a[2], b[2], carry, r[2]);
	carry = felt_add_carry(a[3], b[3], carry, r[3]);
	*carry_out = carry;
}

FIELD_DEVICE uint64_t felt_sub_raw(Felt256 &r, const Felt256 &a, const Felt256 &b)
{
	uint64_t borrow = 0;
	borrow = felt_sub_borrow(a.v[0], b.v[0], borrow, r.v[0]);
	borrow = felt_sub_borrow(a.v[1], b.v[1], borrow, r.v[1]);
	borrow = felt_sub_borrow(a.v[2], b.v[2], borrow, r.v[2]);
	borrow = felt_sub_borrow(a.v[3], b.v[3], borrow, r.v[3]);
	return borrow;
}

FIELD_DEVICE void felt_sub_raw(uint64_t *const r, const uint64_t *const a, const uint64_t *const b, uint64_t *const borrow_out)
{
	uint64_t borrow = 0;
	borrow = felt_sub_borrow(a[0], b[0], borrow, r[0]);
	borrow = felt_sub_borrow(a[1], b[1], borrow, r[1]);
	borrow = felt_sub_borrow(a[2], b[2], borrow, r[2]);
	borrow = felt_sub_borrow(a[3], b[3], borrow, r[3]);
	*borrow_out = borrow;
}

FIELD_DEVICE bool felt_is_zero(const Felt256 &a)
{
	return (a.v[0] | a.v[1] | a.v[2] | a.v[3]) == 0;
}

FIELD_DEVICE bool felt_is_zero(const Felt256 *const a)
{
	return felt_is_zero(*a);
}

FIELD_DEVICE bool felt_gte(const Felt256 &a, const Felt256 &b)
{
	if (a.v[3] != b.v[3])
		return a.v[3] > b.v[3];
	if (a.v[2] != b.v[2])
		return a.v[2] > b.v[2];
	if (a.v[1] != b.v[1])
		return a.v[1] > b.v[1];
	return a.v[0] >= b.v[0];
}

FIELD_DEVICE bool felt_gte(const Felt256 *const a, const Felt256 *const b)
{
	return felt_gte(*a, *b);
}

FIELD_DEVICE void felt_fold_overflow(Felt256 &r)
{
	Felt256 add = {{977, 1, 0, 0, 0, 0, 0, 0}};
	uint64_t carry = felt_add_raw(r, r, add);
	if (carry)
	{
		felt_fold_overflow(r);
	}
}

FIELD_DEVICE void felt_reduce_once(Felt256 &r)
{
	Felt256 t;
	if (felt_sub_raw(t, r, g_mod) == 0)
	{
		r = t;
	}
}

FIELD_DEVICE void felt_normalize(Felt256 &r)
{
	for (int i = 0; i < 8; ++i)
	{
		felt_reduce_once(r);
	}
}

FIELD_DEVICE void felt_mod_add(Felt256 &r, const Felt256 &a, const Felt256 &b)
{
	uint64_t carry = felt_add_raw(r, a, b);
	if (carry)
	{
		felt_fold_overflow(r);
	}
	felt_normalize(r);
}

FIELD_DEVICE void felt_mod_add(Felt256 *const r, const Felt256 *const a, const Felt256 *const b)
{
	felt_mod_add(*r, *a, *b);
}

FIELD_DEVICE void felt_mod_sub(Felt256 &r, const Felt256 &a, const Felt256 &b)
{
	if (felt_sub_raw(r, a, b))
	{
		uint64_t carry;
		felt_add_raw(r.v, r.v, g_mod.v, &carry);
	}
}

FIELD_DEVICE void felt_mod_sub(Felt256 *const r, const Felt256 *const a, const Felt256 *const b)
{
	felt_mod_sub(*r, *a, *b);
}

FIELD_DEVICE void felt_mod_sub_const(Felt256 &r, const Felt256 &a, const Felt256 &b)
{
	felt_mod_sub(r, a, b);
}

FIELD_DEVICE void felt_mod_sub_const(Felt256 *const r, const Felt256 *const a, const Felt256 *const b)
{
	felt_mod_sub(*r, *a, *b);
}

FIELD_DEVICE void felt_mod_neg(Felt256 &r, const Felt256 &a)
{
	if (felt_is_zero(a))
	{
		r = a;
		return;
	}
	felt_sub_raw(r, g_mod, a);
}

FIELD_DEVICE void felt_mod_neg(Felt256 *const r, const Felt256 *const a)
{
	felt_mod_neg(*r, *a);
}

/* 64-bit wide multiply expressed in PTX so the compiler picks the native
 * single-cycle mul.lo.u64 / mul.hi.u64 instructions instead of synthesising
 * the product from 32-bit halves. The host fallback below exists only so
 * nvcc's host compilation pass can parse this translation unit; it is never
 * executed at runtime. */
FIELD_DEVICE void felt_mul_wide_u64(const uint64_t a, const uint64_t b, uint64_t &lo, uint64_t &hi)
{
#if defined(__CUDA_ARCH__)
	asm("mul.lo.u64 %0, %2, %3;\n\t"
		"mul.hi.u64 %1, %2, %3;"
		: "=l"(lo), "=l"(hi)
		: "l"(a), "l"(b));
#else
	/* Host-pass shim — schoolbook 32x32 = 64-bit. Never invoked at runtime. */
	const uint64_t aLo = a & 0xffffffffULL;
	const uint64_t aHi = a >> 32;
	const uint64_t bLo = b & 0xffffffffULL;
	const uint64_t bHi = b >> 32;
	const uint64_t ll = aLo * bLo;
	const uint64_t lh = aLo * bHi;
	const uint64_t hl = aHi * bLo;
	const uint64_t hh = aHi * bHi;
	const uint64_t mid = (ll >> 32) + (lh & 0xffffffffULL) + (hl & 0xffffffffULL);
	lo = (ll & 0xffffffffULL) | (mid << 32);
	hi = hh + (lh >> 32) + (hl >> 32) + (mid >> 32);
#endif
}

/* Add a 128-bit value (lo, hi) into limbs starting at index `i` of an 8-limb
 * 64-bit accumulator, propagating carry as far as needed. The carry-out from
 * limb 7 is returned so callers can shovel it into the high half. */
FIELD_DEVICE uint64_t felt_acc_add_u128(uint64_t *const acc, const int i, const uint64_t lo, const uint64_t hi)
{
	uint64_t carry = 0;
#if defined(__CUDA_ARCH__)
	asm("add.cc.u64 %0, %0, %3;\n\t"
		"addc.cc.u64 %1, %1, %4;\n\t"
		"addc.u64 %2, 0, 0;"
		: "+l"(acc[i]), "+l"(acc[i + 1]), "=l"(carry)
		: "l"(lo), "l"(hi));
#else
	const uint64_t old0 = acc[i];
	acc[i] = old0 + lo;
	uint64_t c0 = acc[i] < old0 ? 1ULL : 0ULL;
	const uint64_t mid = acc[i + 1] + hi;
	uint64_t c1 = mid < acc[i + 1] ? 1ULL : 0ULL;
	acc[i + 1] = mid + c0;
	if (acc[i + 1] < mid)
	{
		c1 += 1;
	}
	carry = c1;
#endif
	for (int k = i + 2; carry != 0 && k < 8; ++k)
	{
		const uint64_t prev = acc[k];
		acc[k] = prev + carry;
		carry = acc[k] < prev ? 1ULL : 0ULL;
	}
	return carry;
}

/* Add a 128-bit product (lo, hi) into a 3-limb column accumulator (s0,s1,s2)
 * with full carry propagation. Branchless: the carry never escapes s2 because
 * each comba column folds at most four 128-bit products plus a <2^128 carry-in,
 * whose sum stays below 2^192. */
FIELD_DEVICE void felt_acc3(uint64_t &s0, uint64_t &s1, uint64_t &s2, const uint64_t lo, const uint64_t hi)
{
#if defined(__CUDA_ARCH__)
	asm("add.cc.u64 %0, %0, %3;\n\t"
		"addc.cc.u64 %1, %1, %4;\n\t"
		"addc.u64 %2, %2, 0;"
		: "+l"(s0), "+l"(s1), "+l"(s2)
		: "l"(lo), "l"(hi));
#else
	const uint64_t o0 = s0 + lo;
	const uint64_t c0 = o0 < s0 ? 1ULL : 0ULL;
	s0 = o0;
	const uint64_t o1 = s1 + hi;
	uint64_t c1 = o1 < s1 ? 1ULL : 0ULL;
	const uint64_t o1b = o1 + c0;
	c1 += (o1b < o1) ? 1ULL : 0ULL;
	s1 = o1b;
	s2 += c1;
#endif
}

/* Product-scanning (comba) 256x256 -> 512-bit product. Each output limb is the
 * sum of all a[i]*b[j] with i+j == column, folded through a 3-limb running
 * accumulator with no data-dependent carry-propagation loop. This keeps the
 * carry chains fully unrolled and branchless. */
FIELD_DEVICE void felt_mul_512_comba(const Felt256 &a, const Felt256 &b, uint64_t lo[4], uint64_t hi[4])
{
	uint64_t s0 = 0, s1 = 0, s2 = 0, pl, ph;
#define PV_COMBA_PROD(ii, jj)                            \
	felt_mul_wide_u64(a.v[ii], b.v[jj], pl, ph);         \
	felt_acc3(s0, s1, s2, pl, ph)
#define PV_COMBA_EMIT(dst)        \
	(dst) = s0;                   \
	s0 = s1; s1 = s2; s2 = 0

	PV_COMBA_PROD(0, 0);                                    PV_COMBA_EMIT(lo[0]);
	PV_COMBA_PROD(0, 1); PV_COMBA_PROD(1, 0);               PV_COMBA_EMIT(lo[1]);
	PV_COMBA_PROD(0, 2); PV_COMBA_PROD(1, 1); PV_COMBA_PROD(2, 0); PV_COMBA_EMIT(lo[2]);
	PV_COMBA_PROD(0, 3); PV_COMBA_PROD(1, 2); PV_COMBA_PROD(2, 1); PV_COMBA_PROD(3, 0); PV_COMBA_EMIT(lo[3]);
	PV_COMBA_PROD(1, 3); PV_COMBA_PROD(2, 2); PV_COMBA_PROD(3, 1); PV_COMBA_EMIT(hi[0]);
	PV_COMBA_PROD(2, 3); PV_COMBA_PROD(3, 2);               PV_COMBA_EMIT(hi[1]);
	PV_COMBA_PROD(3, 3);                                    PV_COMBA_EMIT(hi[2]);
	hi[3] = s0;

#undef PV_COMBA_PROD
#undef PV_COMBA_EMIT
}

/* secp256k1 fast reduction: p = 2^256 - 2^32 - 977, so 2^256 ≡ 2^32 + 977
 * mod p, i.e. the high 256 bits H can be folded back as `H * 0x1000003d1`.
 * We apply that fold twice (once to collapse the 512-bit product into 320
 * bits, once to fold the new high 64 bits back into the low 256), then run a
 * couple of conditional subtractions to bring the result fully into [0, p). */
FIELD_DEVICE void felt_reduce_512(Felt256 &r, const uint64_t lo[4], const uint64_t hi[4])
{
	constexpr uint64_t kC = 0x1000003d1ULL; /* 2^32 + 977 */

	uint64_t low[4] = {lo[0], lo[1], lo[2], lo[3]};
	uint64_t carry_out = 0;
#pragma unroll
	for (int i = 0; i < 4; ++i)
	{
		uint64_t mlo;
		uint64_t mhi;
		felt_mul_wide_u64(hi[i], kC, mlo, mhi);

		uint64_t acc[8] = {low[0], low[1], low[2], low[3], carry_out, 0, 0, 0};
		const uint64_t propagated = felt_acc_add_u128(acc, i, mlo, mhi);
		low[0] = acc[0];
		low[1] = acc[1];
		low[2] = acc[2];
		low[3] = acc[3];
		carry_out = acc[4] + propagated;
	}

	/* Second fold: bring `carry_out` (which is < 2^64) back below 2^256. */
	while (carry_out != 0)
	{
		uint64_t mlo;
		uint64_t mhi;
		felt_mul_wide_u64(carry_out, kC, mlo, mhi);
		carry_out = 0;
		uint64_t acc[8] = {low[0], low[1], low[2], low[3], 0, 0, 0, 0};
		carry_out += felt_acc_add_u128(acc, 0, mlo, mhi);
		low[0] = acc[0];
		low[1] = acc[1];
		low[2] = acc[2];
		low[3] = acc[3];
		carry_out += acc[4];
	}

	r.v[0] = low[0];
	r.v[1] = low[1];
	r.v[2] = low[2];
	r.v[3] = low[3];

	/* Final conditional subtractions: after the folds the value is at most a
	 * few multiples of p above 2^256, so two passes are enough in practice. */
	felt_reduce_once(r);
	felt_reduce_once(r);
}

FIELD_DEVICE void felt_mod_mul(Felt256 &r, const Felt256 &a, const Felt256 &b)
{
	uint64_t lo[4];
	uint64_t hi[4];
	felt_mul_512_comba(a, b, lo, hi);
	felt_reduce_512(r, lo, hi);
}

FIELD_DEVICE void felt_mod_mul(Felt256 *const r, const Felt256 *const a, const Felt256 *const b)
{
	felt_mod_mul(*r, *a, *b);
}

FIELD_DEVICE void felt_mod_sqr(Felt256 &r, const Felt256 &a)
{
	felt_mod_mul(r, a, a);
}

FIELD_DEVICE void felt_mod_sqr(Felt256 *const r, const Felt256 *const a)
{
	felt_mod_sqr(*r, *a);
}

/* Right-shift the 256-bit value by one bit, in place. */
FIELD_DEVICE void felt_shr1(Felt256 &r)
{
	const uint64_t b0 = r.v[1] << 63;
	const uint64_t b1 = r.v[2] << 63;
	const uint64_t b2 = r.v[3] << 63;
	r.v[0] = (r.v[0] >> 1) | b0;
	r.v[1] = (r.v[1] >> 1) | b1;
	r.v[2] = (r.v[2] >> 1) | b2;
	r.v[3] = (r.v[3] >> 1);
}

/* Halve a value modulo the secp256k1 prime. If the value is odd we first add
 * p (which is itself odd) to make it even, then shift right one bit. The
 * addition can overflow into a 257-bit intermediate, so we track the top bit
 * separately and fold it back in during the shift. */
FIELD_DEVICE void felt_mod_half(Felt256 &r)
{
	uint64_t top = 0;
	if (r.v[0] & 1ULL)
	{
		top = felt_add_raw(r, r, g_mod);
	}
	felt_shr1(r);
	if (top)
	{
		r.v[3] |= 1ULL << 63;
	}
}

/* Modular inversion via the binary extended Euclidean algorithm. Maintains
 * pairs (u, x) and (v, y) with the invariants
 *   u ≡ a * x  (mod p)
 *   v ≡ a * y  (mod p)
 * until one of u or v reaches 1, at which point the matching x or y is the
 * inverse. The inner loop shifts out trailing zero bits and halves the
 * associated coefficient using felt_mod_half; the outer step subtracts the
 * smaller pair from the larger. This is the same shape used by classical
 * Stein-style inverters; it is dramatically faster than the Fermat fallback
 * because the cost is dominated by 256-bit shifts and additions rather than
 * by ~255 field squarings. */
FIELD_DEVICE void felt_mod_inv(Felt256 &r, const Felt256 &a)
{
	if (felt_is_zero(a))
	{
		r = {{0, 0, 0, 0}};
		return;
	}

	Felt256 u = a;
	Felt256 v = g_mod;
	Felt256 x = {{1, 0, 0, 0}};
	Felt256 y = {{0, 0, 0, 0}};

	while (!felt_is_zero(u) && !felt_is_zero(v))
	{
		while ((u.v[0] & 1ULL) == 0)
		{
			felt_shr1(u);
			felt_mod_half(x);
		}
		while ((v.v[0] & 1ULL) == 0)
		{
			felt_shr1(v);
			felt_mod_half(y);
		}
		if (felt_gte(u, v))
		{
			Felt256 t;
			felt_sub_raw(t, u, v);
			u = t;
			felt_mod_sub(x, x, y);
		}
		else
		{
			Felt256 t;
			felt_sub_raw(t, v, u);
			v = t;
			felt_mod_sub(y, y, x);
		}
	}

	/* When u hits zero, v holds gcd(a, p) and y holds the inverse of a
	 * modulo p; the symmetric case picks x. For a in the multiplicative
	 * group of GF(p), gcd is 1 and exactly one of u/v hits zero first. */
	if (felt_is_zero(u))
	{
		felt_normalize(y);
		r = y;
	}
	else
	{
		felt_normalize(x);
		r = x;
	}
}

FIELD_DEVICE void felt_mod_inv(Felt256 *const r)
{
	Felt256 a = *r;
	felt_mod_inv(*r, a);
}

#undef FIELD_DEVICE
