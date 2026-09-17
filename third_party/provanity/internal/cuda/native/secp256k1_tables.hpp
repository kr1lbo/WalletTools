#pragma once

#include "point.cuh"

#define PV_PRECOMP_WORDS 4
#define PV_PRECOMP_BYTES 8
#define PV_PRECOMP_VALUES 255
#define PV_PRECOMP_POINTS (PV_PRECOMP_WORDS * PV_PRECOMP_BYTES * PV_PRECOMP_VALUES)

__global__ void pv_build_precomp(Point *const out);

__device__ __forceinline__ size_t pv_precomp_index(const int word, const int byte, const int value)
{
	return static_cast<size_t>((word * PV_PRECOMP_BYTES + byte) * PV_PRECOMP_VALUES + value - 1);
}

/* Jacobian variant of pv_scalar_times_g. Same byte-decomposition logic, but
 * the accumulator is kept in projective (X, Y, Z) form so the per-step adds
 * (jac_add_affine) cost only 8M + 3S — no modular inversion. The output is
 * still Jacobian; the caller is expected to batch-invert Z across the lane
 * grid and convert back to affine in a follow-up kernel. This is what brings
 * pv_iterate_init from ~33 inversions per lane down to ~1 (the slope seed). */
__device__ __forceinline__ void pv_scalar_times_g_jac(JacobianPoint *const out, const Point *__restrict__ const precomp, const SeedWords *const scalar)
{
	const uint64_t limbs[PV_PRECOMP_WORDS] = {scalar->x, scalar->y, scalar->z, scalar->w};
	bool initialised = false;
	JacobianPoint sum;
	for (int word = 0; word < PV_PRECOMP_WORDS; ++word)
	{
		uint64_t limb = limbs[word];
		for (int byte = 0; byte < PV_PRECOMP_BYTES; ++byte)
		{
			const int v = static_cast<int>(limb & 0xffULL);
			limb >>= 8;
			if (v == 0)
			{
				continue;
			}
			Point term;
#pragma unroll
			for (int lane = 0; lane < FELT_LIMBS; ++lane)
			{
				const size_t idx = pv_precomp_index(word, byte, v);
				term.x.v[lane] = __ldg(&precomp[idx].x.v[lane]);
				term.y.v[lane] = __ldg(&precomp[idx].y.v[lane]);
			}
			if (!initialised)
			{
				jac_from_affine(&sum, &term);
				initialised = true;
			}
			else
			{
				JacobianPoint next;
				jac_add_affine(&next, &sum, &term);
				sum = next;
			}
		}
	}
	if (!initialised)
	{
		sum.X = {{0, 0, 0, 0}};
		sum.Y = {{0, 0, 0, 0}};
		sum.Z = {{0, 0, 0, 0}};
	}
	*out = sum;
}

/* Scalar*G via the byte-decomposed precomp table. Each non-zero byte of the
 * scalar selects one precomp entry; results are summed with affine point_add.
 * The first non-zero byte initialises the accumulator to avoid the cost of an
 * identity-element addition. */
__device__ __forceinline__ void pv_scalar_times_g(Point *const out, const Point *__restrict__ const precomp, const SeedWords *const scalar)
{
	const uint64_t limbs[PV_PRECOMP_WORDS] = {scalar->x, scalar->y, scalar->z, scalar->w};
	bool initialised = false;
	Point sum;
	for (int word = 0; word < PV_PRECOMP_WORDS; ++word)
	{
		uint64_t limb = limbs[word];
		for (int byte = 0; byte < PV_PRECOMP_BYTES; ++byte)
		{
			const int v = static_cast<int>(limb & 0xffULL);
			limb >>= 8;
			if (v == 0)
			{
				continue;
			}
			Point term;
#pragma unroll
			for (int lane = 0; lane < FELT_LIMBS; ++lane)
			{
				const size_t idx = pv_precomp_index(word, byte, v);
				term.x.v[lane] = __ldg(&precomp[idx].x.v[lane]);
				term.y.v[lane] = __ldg(&precomp[idx].y.v[lane]);
			}
			if (!initialised)
			{
				sum = term;
				initialised = true;
			}
			else
			{
				Point next;
				point_add(&next, &sum, &term);
				sum = next;
			}
		}
	}
	if (!initialised)
	{
		sum.x = {{0, 0, 0, 0}};
		sum.y = {{0, 0, 0, 0}};
	}
	*out = sum;
}
