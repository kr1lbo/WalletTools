#pragma once

#include "keccak_f1600.cuh"

__device__ __forceinline__ void sha3_keccakf(EthHash *const h)
{
	uint64_t *const st = h->q;
	st[16] ^= 0x8000000000000000ULL;
	keccak_f1600(st);
}
