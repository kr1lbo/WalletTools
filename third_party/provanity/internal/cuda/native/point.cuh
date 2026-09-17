#pragma once

#include "field.cuh"

/* Jacobian projective coordinates: an affine (x, y) is represented as
 * (X, Y, Z) with x = X/Z^2, y = Y/Z^3. Point addition in this form needs only
 * multiplications and squarings (no modular inversion), which lets the init
 * pipeline accumulate ~32 byte-decomposed precomp adds per lane without ever
 * inverting. One batched cross-lane inversion at the end converts the whole
 * batch back to affine. */
struct JacobianPoint
{
	Felt256 X;
	Felt256 Y;
	Felt256 Z;
};

__device__ __forceinline__ bool point_is_infinity(const Point *const p)
{
	return felt_is_zero(&p->x) && felt_is_zero(&p->y);
}

/* Lift an affine point to Jacobian by setting Z=1. */
__device__ __forceinline__ void jac_from_affine(JacobianPoint *const r, const Point *const a)
{
	r->X = a->x;
	r->Y = a->y;
	r->Z.v[0] = 1;
	r->Z.v[1] = 0;
	r->Z.v[2] = 0;
	r->Z.v[3] = 0;
}

/* Mixed Jacobian + Affine point addition (secp256k1, a=0). Caller is
 * responsible for guaranteeing the inputs are not infinity and do not collide
 * (H != 0). Both assumptions hold throughout pv_scalar_times_g_jac: the
 * accumulator is non-infinity from its first byte onward and the precomp table
 * never collides with the running sum at the precision used here. Cost:
 * 8 muls + 3 squarings (vs ~512 muls for one full inversion in the affine path). */
__device__ __forceinline__ void jac_add_affine(JacobianPoint *const r, const JacobianPoint *const j, const Point *const a)
{
	Felt256 Z1Z1;
	felt_mod_sqr(&Z1Z1, &j->Z);

	Felt256 U2;
	felt_mod_mul(&U2, &a->x, &Z1Z1);

	Felt256 t;
	felt_mod_mul(&t, &a->y, &j->Z);
	Felt256 S2;
	felt_mod_mul(&S2, &t, &Z1Z1);

	Felt256 H;
	felt_mod_sub(&H, &U2, &j->X);

	Felt256 HH;
	felt_mod_sqr(&HH, &H);

	Felt256 HHH;
	felt_mod_mul(&HHH, &H, &HH);

	Felt256 r_num;
	felt_mod_sub(&r_num, &S2, &j->Y);

	Felt256 V;
	felt_mod_mul(&V, &j->X, &HH);

	Felt256 X3;
	felt_mod_sqr(&X3, &r_num);
	felt_mod_sub(&X3, &X3, &HHH);
	felt_mod_sub(&X3, &X3, &V);
	felt_mod_sub(&X3, &X3, &V);

	felt_mod_sub(&t, &V, &X3);
	Felt256 Y3;
	felt_mod_mul(&Y3, &r_num, &t);
	felt_mod_mul(&t, &j->Y, &HHH);
	felt_mod_sub(&Y3, &Y3, &t);

	Felt256 Z3;
	felt_mod_mul(&Z3, &j->Z, &H);

	r->X = X3;
	r->Y = Y3;
	r->Z = Z3;
}

__device__ __forceinline__ void point_double_affine(Point *const r, const Point *const p)
{
	Felt256 xx;
	Felt256 numerator;
	Felt256 denominator;
	Felt256 slope;
	Felt256 x3;
	Felt256 y3;

	felt_mod_sqr(&xx, &p->x);
	felt_mod_add(&numerator, &xx, &xx);
	felt_mod_add(&numerator, &numerator, &xx);
	felt_mod_add(&denominator, &p->y, &p->y);
	felt_mod_inv(&denominator);
	felt_mod_mul(&slope, &numerator, &denominator);
	felt_mod_sqr(&x3, &slope);
	felt_mod_sub(&x3, &x3, &p->x);
	felt_mod_sub(&x3, &x3, &p->x);
	felt_mod_sub(&y3, &p->x, &x3);
	felt_mod_mul(&y3, &y3, &slope);
	felt_mod_sub(&y3, &y3, &p->y);
	r->x = x3;
	r->y = y3;
}

__device__ __forceinline__ void point_add(Point *const r, const Point *const a, const Point *const b)
{
	if (point_is_infinity(a))
	{
		*r = *b;
		return;
	}
	if (point_is_infinity(b))
	{
		*r = *a;
		return;
	}
	if (a->x.v[0] == b->x.v[0] && a->x.v[1] == b->x.v[1] && a->x.v[2] == b->x.v[2] && a->x.v[3] == b->x.v[3])
	{
		point_double_affine(r, a);
		return;
	}

	Felt256 rise;
	Felt256 run;
	Felt256 slope;
	Felt256 x3;
	Felt256 y3;

	felt_mod_sub(&rise, &b->y, &a->y);
	felt_mod_sub(&run, &b->x, &a->x);
	felt_mod_inv(&run);
	felt_mod_mul(&slope, &rise, &run);
	felt_mod_sqr(&x3, &slope);
	felt_mod_sub(&x3, &x3, &a->x);
	felt_mod_sub(&x3, &x3, &b->x);
	felt_mod_sub(&y3, &a->x, &x3);
	felt_mod_mul(&y3, &y3, &slope);
	felt_mod_sub(&y3, &y3, &a->y);
	r->x = x3;
	r->y = y3;
}
