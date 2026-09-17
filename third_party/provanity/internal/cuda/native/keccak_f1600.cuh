#pragma once

#include "types.cuh"

/* Keccak-f[1600] permutation (FIPS 202).
 *
 * The 25 lanes are kept in a single array that the compiler promotes to
 * registers once the round loop is unrolled. Theta uses five column-parity
 * words; rho+pi is applied in place by walking the 24-lane permutation cycle
 * with a single carried temporary (so no second 25-lane scratch array is ever
 * live); chi runs row by row with five locals. This keeps the working set at
 * the 25 lanes plus a handful of temporaries, which is what lets the hot
 * iterate kernel inline the hash without spilling. */
__device__ __forceinline__ void keccak_f1600(uint64_t *const state)
{
	static constexpr uint64_t round_constants[24] = {
		0x0000000000000001ULL, 0x0000000000008082ULL, 0x800000000000808aULL, 0x8000000080008000ULL,
		0x000000000000808bULL, 0x0000000080000001ULL, 0x8000000080008081ULL, 0x8000000000008009ULL,
		0x000000000000008aULL, 0x0000000000000088ULL, 0x0000000080008009ULL, 0x000000008000000aULL,
		0x000000008000808bULL, 0x800000000000008bULL, 0x8000000000008089ULL, 0x8000000000008003ULL,
		0x8000000000008002ULL, 0x8000000000000080ULL, 0x000000000000800aULL, 0x800000008000000aULL,
		0x8000000080008081ULL, 0x8000000000008080ULL, 0x0000000080000001ULL, 0x8000000080008008ULL};
	/* Destination lane index for each step of the rho+pi cycle starting from
	 * lane 1, paired with the matching rho rotation amount. */
	static constexpr int pi_lane[24] = {
		10, 7, 11, 17, 18, 3, 5, 16, 8, 21, 24, 4,
		15, 23, 19, 13, 12, 2, 20, 14, 22, 9, 6, 1};
	static constexpr int rho_rot[24] = {
		1, 3, 6, 10, 15, 21, 28, 36, 45, 55, 2, 14,
		27, 41, 56, 8, 25, 43, 62, 18, 39, 61, 20, 44};

	uint64_t s[25];
#pragma unroll
	for (int i = 0; i < 25; ++i)
	{
		s[i] = state[i];
	}

#pragma unroll
	for (int round = 0; round < 24; ++round)
	{
		/* Theta. */
		uint64_t c[5];
#pragma unroll
		for (int x = 0; x < 5; ++x)
		{
			c[x] = s[x] ^ s[x + 5] ^ s[x + 10] ^ s[x + 15] ^ s[x + 20];
		}
		uint64_t d[5];
#pragma unroll
		for (int x = 0; x < 5; ++x)
		{
			d[x] = c[(x + 4) % 5] ^ cuda_rotl64(c[(x + 1) % 5], 1U);
		}
#pragma unroll
		for (int i = 0; i < 25; ++i)
		{
			s[i] ^= d[i % 5];
		}

		/* Rho + Pi, walked in place from lane 1 around the 24-step cycle. */
		uint64_t carry = s[1];
#pragma unroll
		for (int i = 0; i < 24; ++i)
		{
			const int j = pi_lane[i];
			const uint64_t next = s[j];
			s[j] = cuda_rotl64(carry, static_cast<uint32_t>(rho_rot[i]));
			carry = next;
		}

		/* Chi, one row of five lanes at a time. */
#pragma unroll
		for (int row = 0; row < 25; row += 5)
		{
			const uint64_t r0 = s[row + 0];
			const uint64_t r1 = s[row + 1];
			const uint64_t r2 = s[row + 2];
			const uint64_t r3 = s[row + 3];
			const uint64_t r4 = s[row + 4];
			s[row + 0] = r0 ^ ((~r1) & r2);
			s[row + 1] = r1 ^ ((~r2) & r3);
			s[row + 2] = r2 ^ ((~r3) & r4);
			s[row + 3] = r3 ^ ((~r4) & r0);
			s[row + 4] = r4 ^ ((~r0) & r1);
		}

		/* Iota. */
		s[0] ^= round_constants[round];
	}

#pragma unroll
	for (int i = 0; i < 25; ++i)
	{
		state[i] = s[i];
	}
}
