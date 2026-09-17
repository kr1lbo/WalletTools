#pragma once

#include <stdint.h>

/* Minimal SHA-256 for single-block messages (length <= 55 bytes). Used only by
 * the Tron suffix scorer to derive the 4-byte base58check checksum: both inputs
 * (the 21-byte 0x41||address payload and the 32-byte first digest) fit in one
 * 512-bit block after padding, so there is no multi-block loop. */

__constant__ const uint32_t g_sha256_k[64] = {
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
	0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
	0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
	0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
	0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
	0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2};

__device__ __forceinline__ uint32_t pv_sha256_rotr(const uint32_t x, const uint32_t n)
{
	return (x >> n) | (x << (32 - n));
}

/* Hash a single-block message (len <= 55) into out[32], big-endian digest. */
__device__ __forceinline__ void pv_sha256(const uint8_t *const msg, const int len, uint8_t *const out)
{
	uint8_t block[64];
#pragma unroll
	for (int i = 0; i < 64; ++i)
	{
		block[i] = 0;
	}
	for (int i = 0; i < len; ++i)
	{
		block[i] = msg[i];
	}
	block[len] = 0x80;
	const uint64_t bits = static_cast<uint64_t>(len) * 8ULL;
#pragma unroll
	for (int i = 0; i < 8; ++i)
	{
		block[63 - i] = static_cast<uint8_t>(bits >> (8 * i));
	}

	uint32_t w[64];
#pragma unroll
	for (int i = 0; i < 16; ++i)
	{
		w[i] = (static_cast<uint32_t>(block[i * 4]) << 24) | (static_cast<uint32_t>(block[i * 4 + 1]) << 16) |
			   (static_cast<uint32_t>(block[i * 4 + 2]) << 8) | static_cast<uint32_t>(block[i * 4 + 3]);
	}
#pragma unroll
	for (int i = 16; i < 64; ++i)
	{
		const uint32_t s0 = pv_sha256_rotr(w[i - 15], 7) ^ pv_sha256_rotr(w[i - 15], 18) ^ (w[i - 15] >> 3);
		const uint32_t s1 = pv_sha256_rotr(w[i - 2], 17) ^ pv_sha256_rotr(w[i - 2], 19) ^ (w[i - 2] >> 10);
		w[i] = w[i - 16] + s0 + w[i - 7] + s1;
	}

	uint32_t a = 0x6a09e667, b = 0xbb67ae85, c = 0x3c6ef372, d = 0xa54ff53a;
	uint32_t e = 0x510e527f, f = 0x9b05688c, g = 0x1f83d9ab, h = 0x5be0cd19;
#pragma unroll
	for (int i = 0; i < 64; ++i)
	{
		const uint32_t S1 = pv_sha256_rotr(e, 6) ^ pv_sha256_rotr(e, 11) ^ pv_sha256_rotr(e, 25);
		const uint32_t ch = (e & f) ^ ((~e) & g);
		const uint32_t t1 = h + S1 + ch + g_sha256_k[i] + w[i];
		const uint32_t S0 = pv_sha256_rotr(a, 2) ^ pv_sha256_rotr(a, 13) ^ pv_sha256_rotr(a, 22);
		const uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
		const uint32_t t2 = S0 + maj;
		h = g;
		g = f;
		f = e;
		e = d + t1;
		d = c;
		c = b;
		b = a;
		a = t1 + t2;
	}

	a += 0x6a09e667;
	b += 0xbb67ae85;
	c += 0x3c6ef372;
	d += 0xa54ff53a;
	e += 0x510e527f;
	f += 0x9b05688c;
	g += 0x1f83d9ab;
	h += 0x5be0cd19;

	const uint32_t hh[8] = {a, b, c, d, e, f, g, h};
#pragma unroll
	for (int i = 0; i < 8; ++i)
	{
		out[i * 4] = static_cast<uint8_t>(hh[i] >> 24);
		out[i * 4 + 1] = static_cast<uint8_t>(hh[i] >> 16);
		out[i * 4 + 2] = static_cast<uint8_t>(hh[i] >> 8);
		out[i * 4 + 3] = static_cast<uint8_t>(hh[i]);
	}
}
