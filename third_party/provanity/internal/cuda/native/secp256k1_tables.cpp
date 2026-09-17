#include "secp256k1_tables.hpp"

__device__ const Point kGenerator = {{
	{0x16f81798, 0x59f2815b, 0x2dce28d9, 0x029bfcdb, 0xce870b07, 0x55a06295, 0xf9dcbbac, 0x79be667e}},
	{{0xfb10d4b8, 0x9c47d08f, 0xa6855419, 0xfd17b448, 0x0e1108a8, 0x5da4fbfc, 0x26a3c465, 0x483ada77}}};

__global__ void pv_build_precomp(Point *const out)
{
	const int byte_index = blockIdx.x * blockDim.x + threadIdx.x;
	if (byte_index >= PV_PRECOMP_WORDS * PV_PRECOMP_BYTES)
	{
		return;
	}

	Point step = kGenerator;
	for (int bit = 0; bit < byte_index * 8; ++bit)
	{
		Point doubled;
		point_double_affine(&doubled, &step);
		step = doubled;
	}

	Point multiple = {{0}, {0}};
	const int offset = byte_index * PV_PRECOMP_VALUES;
	for (int value = 0; value < PV_PRECOMP_VALUES; ++value)
	{
		Point next;
		point_add(&next, &multiple, &step);
		multiple = next;
		out[offset + value] = multiple;
	}
}
