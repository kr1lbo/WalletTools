# ProVanity

GPU-accelerated vanity wallet generation for Ethereum-compatible and Tron addresses on NVIDIA GPUs.

This project is inspired by [profanity](https://github.com/johguse/profanity) and [1inch/profanity2](https://github.com/1inch/profanity2), rewritten in CUDA and Go — **up to +76% faster than profanity2** on modern NVIDIA GPUs (see [benchmarks](#performance)).

## Features

- GPU-accelerated vanity address search on NVIDIA GPUs with CUDA.
- EVM and Tron vanity wallet generation.
- Purely local by default. Wallet generation does not access the network.
- Guided wizard and live terminal dashboard for direct use.
- Standalone headless worker binary for automation; use its `--help` output for
  the worker interface.

## Requirements

- Windows x64 or Linux x64.
- NVIDIA GPU: Turing (`sm_75`) or newer. e.g. GTX 16-series, any RTX, T4, A100, H100, etc.
- NVIDIA Driver: >= 580.65 on Linux, >= 580.88 on Windows (CUDA 13.x baseline, Aug 2025+). Latest drivers are always recommended for best performance.

> Pascal and older (GTX 9xx, GTX 10xx, Titan X(p), Tesla M40/K80, etc.) are not supported — use [1inch/profanity2](https://github.com/1inch/profanity2) on those GPUs.

## Quickstart

Run the latest CLI through npm without a separate install:

```bash
npx provanity
```

If you prefer not to manage with npm, here is binary links for the latest release:

| Platform | Latest binary |
|---|---|
| Windows x64 | [provanity-windows-amd64.exe](https://github.com/WooMai/ProVanity/releases/latest/download/provanity-windows-amd64.exe) |
| Linux x64 | [provanity-linux-amd64](https://github.com/WooMai/ProVanity/releases/latest/download/provanity-linux-amd64) |

If you want to build from source on your own, see [Build From Source](./docs/build.md).

## Usage

Start the guided wizard:

```bash
provanity
```

Generate an EVM vanity wallet:

```bash
# Match 8 digits zero prefix
provanity generate --pattern leading:0:8

# Match an address starting with "0xdead" prefix and "beef" suffix
provanity generate --pattern pattern:deadXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXbeef
```

Generate a Tron vanity wallet (prefix or suffix; the leading "T" is implicit):

```bash
provanity generate-tron --pattern prefix:ABC  # address starts with TABC…
provanity generate-tron --pattern suffix:xyz  # address ends with …xyz
```

Run benchmark:

```bash
provanity bench
```

Use `provanity --help` or `provanity <command> --help` for the full command
reference.

## Disclaimer

ProVanity generates crypto wallet private keys. Before using it with any real funds, it is your responsibility to read and audit the source code yourself and make sure you understand how it works.

Always verify that the generated private key derives to the expected address before moving or receiving any funds.

You are fully responsible for the security of your machine, runtime environment, generated keys, and any funds controlled by those keys. Use this tool only if you accept those risks.

## Performance

> 3-minute sustained `--benchmark` runs against an unreachable target (EVM), all on headless Linux (no display compositor sharing the GPU). profanity2 measured from [`1inch/profanity2@13d16e8`](https://github.com/1inch/profanity2/commit/13d16e83).

| GPU | Architecture | ProVanity | profanity2 | Speedup |
|:---|:---|---:|---:|---:|
| RTX PRO 6000 Blackwell Server Edition | Blackwell `sm_120` | 3,437 MH/s | 1,950 MH/s | **+76%** |
| RTX 5090                              | Blackwell `sm_120` | 3,268 MH/s | 2,089 MH/s | **+56%** |
| RTX 4090                              | Ada `sm_89`        | 2,224 MH/s | 1,393 MH/s | **+60%** |
| H100 SXM                              | Hopper `sm_90`     | 2,113 MH/s | 1,393 MH/s | **+52%** |
| RTX 4070 Ti SUPER                     | Ada `sm_89`        | 1,299 MH/s |   836 MH/s | **+55%** |
| RTX 3090                              | Ampere `sm_86`     | 1,073 MH/s |   696 MH/s | **+54%** |
| RTX 3080                              | Ampere `sm_86`     | 1,026 MH/s |   696 MH/s | **+47%** |
| RTX 3060 Ti                           | Ampere `sm_86`     |   531 MH/s |   348 MH/s | **+53%** |
| RTX 3060                              | Ampere `sm_86`     |   405 MH/s |   264 MH/s | **+53%** |
| RTX 2080 Ti                           | Turing `sm_75`     |   760 MH/s |   464 MH/s | **+64%** |
| GTX 1660 Super                        | Turing `sm_75`     |   267 MH/s |   199 MH/s | **+34%** |

## License

ProVanity's original code is distributed under the [MIT License](./LICENSE).

## References

- [original profanity](https://github.com/johguse/profanity) - original vanity address search concept.
- [1inch/profanity2](https://github.com/1inch/profanity2) - design inspiration and security model reference.
- [1inch profanity vulnerability disclosure blog](https://1inch.com/blog/post/a-vulnerability-disclosed-in-profanity-an-ethereum-vanity-address-tool)
