#pragma once

#include <cstddef>
#include <cstdint>
#include <cuda_runtime.h>

#define PV_BATCH_LANES 255
#define FELT_LIMBS 4
#define FELT_U32_WORDS 8

struct __align__(16) Felt256
{
	union
	{
		uint32_t d[FELT_U32_WORDS];
		uint64_t v[FELT_LIMBS];
	};
};

struct __align__(16) Point
{
	Felt256 x;
	Felt256 y;
};

struct Result
{
	uint32_t found;
	uint32_t found_id;
	uint8_t found_hash[20];
};

struct SeedWords
{
	uint64_t x;
	uint64_t y;
	uint64_t z;
	uint64_t w;
};

union EthHash
{
	uint8_t b[200];
	uint64_t q[25];
	uint32_t d[50];
};

__device__ __forceinline__ uint64_t cuda_rotl64(const uint64_t value, const uint32_t shift)
{
	return (value << shift) | (value >> (64U - shift));
}

__device__ __forceinline__ uint32_t bswap32(const uint32_t n)
{
	return __byte_perm(n, 0, 0x0123);
}

__host__ __device__ __forceinline__ uint32_t felt_get_u32(const Felt256 &a, const uint32_t i)
{
	return a.d[i];
}

__host__ __device__ __forceinline__ void felt_set_u32(Felt256 &a, const uint32_t i, const uint32_t value)
{
	a.d[i] = value;
}
