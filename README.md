# radsort

A Go implementation of **Radsort** — a stable LSD radix sort with **O(√n) space
overhead**, from *"Parallel O(√n) Overhead LSD Radix Sort"* by Robert Clausecker
and Florian Schintke ([arXiv:2607.05302v1](https://arxiv.org/abs/2607.05302),
2026). Ported from the authors' reference C implementation
([clausecker/radsort](https://github.com/clausecker/radsort),
`radixsort_permuted.c`).

## The idea

A conventional out-of-place LSD radix sort needs a second array of `n` elements:
each round scatters the input into an output buffer, then swaps buffers. That
`O(n)` overhead hurts, and scattering to 256 cold output buckets triggers
*read-for-ownership* cache traffic.

Radsort treats the input as a sequence of fixed-size **blocks** and *reuses each
input block for output once it has been consumed*. A permutation array `π`
tracks where each logical block physically lives, so blocks never need to be
copied until the very end. Only a constant number of scratch blocks plus
`O(n/b)` bookkeeping is required — `O(√n)` when `b ∈ Θ(√n)`. Because a
just-consumed input block is still hot in cache when it is reused as an output
block, the read-for-ownership penalty largely disappears.

The sort is **stable**. Each round has a *sort phase* (distribute elements into
per-bucket output blocks, drawing fresh blocks from already-consumed input) and
a *fixup phase* (deinterleave the buckets by rewriting `π`, moving no data). A
final *compact* step shuffles the blocks back into the caller's slice.

## Usage

[![Go Reference](https://pkg.go.dev/badge/klauspost/radsort.svg)](https://pkg.go.dev/github.com/klauspost/radsort)

```go
import "github.com/klauspost/radsort"

radsort.Uint32s(keys)   // []uint32, 4 rounds
radsort.Uint64s(keys)   // []uint64, 8 rounds
radsort.Int32s(keys)    // []int32
radsort.Int64s(keys)    // []int64
radsort.Float32s(keys)  // []float32  (NaNs sort last)
radsort.Float64s(keys)  // []float64

// Any element type, sorted stably by an unsigned key. rounds is the number of
// key bytes to consider (least-significant first).
type kv struct{ Key uint32; Val int }
radsort.SortKey(pairs, 4, func(p kv) uint64 { return uint64(p.Key) })
```

`Uint32s`/`Uint64s` use a monomorphised inner loop; `SortKey` is generic and
pays one (non-inlined) key call per element — roughly half the throughput (see
below), which is the price of working with an arbitrary element type.

### Reusing buffers (zero allocation)

The functions above allocate ~1–2 MiB of working buffers per call. To sort
repeatedly without allocating, keep a `Sorter[E]` and call its `SortKey` method;
after the first call has sized the buffers, further calls of equal-or-smaller
length allocate nothing:

```go
var s radsort.Sorter[uint32]
for _, batch := range batches {
    s.SortKey(batch, 4, func(v uint32) uint64 { return uint64(v) }) // 0 allocs after warm-up
}
```

The zero value is ready to use; a `Sorter` is bound to one element type and is
not safe for concurrent use.

### Iterating a map in key order

`Map` returns an `iter.Seq2[K, V]` over a map's entries in ascending key order,
for any key of a supported type (`uint32/uint64/int32/int64/float32/float64`).
It sorts the keys with the monomorphised path and looks up values during
iteration:

```go
for k, v := range radsort.Map(m) {
    ...
}
```

Do not mutate the map while iterating. (Float NaN keys are pathological — they
sort last and can't be looked up.)

### Iterating values in order

`Uint32Seq` returns an `iter.Seq[uint32]` over a slice's values in ascending
order, yielding straight from radsort's internal block layout and skipping the
final compaction pass (the paper calls this *avoiding finalisation*, §4.1):

```go
for v := range radsort.Uint32Seq(keys) {
    ...
}
```

It sorts using `keys` as scratch and never writes the result back, so `keys` is
left in an unspecified order once iteration starts — use `Uint32s` if you need
the slice itself sorted. `Uint64Seq`, `Int32Seq`, `Int64Seq`, `Float32Seq`, and
`Float64Seq` cover the other element types, and `SortKeySeq` sorts any element
type by a key function (as `SortKey` is to `Uint32s`). (`Map` above uses the same
mechanism internally.)

### Build tags

The `uint32`/`uint64` sorts use a little `unsafe` — a pointer-cursor inner loop
that drops a bounds check — for inputs above ~256 K elements, where it runs a few
percent faster; smaller inputs use the safe path regardless. Build with
`-tags nounsafe` (or `appengine`) to compile only the safe path; results are
identical.

### Concurrent sorting

`ParallelUint32s` / `ParallelUint64s` sort large slices using multiple
goroutines. They split on the most significant byte into independent buckets and
sort those concurrently, so they need an O(n) buffer (trading Radsort's O(√n)
space for parallelism). Below ~1M elements, or on a single CPU, they fall back
to the serial sort automatically.

```go
radsort.ParallelUint32s(x) // allocates working buffers per call

// reuse buffers across calls (avoids re-allocating the O(n) split buffer):
var ps radsort.ParallelSorter[uint32]
for _, batch := range batches {
    ps.Sort(batch)                              // uint32/uint64, fast
}
ps2 := &radsort.ParallelSorter[myType]{}
ps2.SortKey(data, rounds, keyOf)                // any type, via a key function
```

On a 16-core Zen 5 this gives ~2.4–3.2× over the serial sort for arrays of
16–64M elements, plateauing around 8 workers — radix sorting is memory-bandwidth
bound, so more threads mostly contend for memory channels. A `ParallelSorter`
runs its own goroutines; don't share one across concurrent callers.

The split is a single most-significant-byte partition, so it only balances work
when the top byte is reasonably spread. Inputs whose keys share a top byte (e.g.
small-range or pre-sorted data) collapse into one bucket and effectively run
serially — no speedup, but no slowdown either (it degrades to a serial sort plus
a cheap split pass). If your keys are known to be skewed in the top byte,
parallelism will not help.

## Correctness

- Structured tests over sizes that hit every block/scratch boundary (empty,
  sub-block, exact block multiples, spanning many blocks).
- An exhaustive sweep of **every** `n` from 0 to `4·b + σ`.
- Stability and multiset-preservation checks.
- Native fuzzing: `FuzzUints` (one byte corpus read as uint64s and truncated to
  uint32s, driving both integer paths) and `FuzzStable` (generic path +
  stability), each cross-checked against `slices.Sort`.

```
go test ./...
go test -run '^$' -fuzz FuzzUints
```

## Benchmarks

Compared against the standard library sorter (`slices.Sort` for scalars,
`slices.SortFunc` for pairs — Go's pattern-defeating quicksort). Throughput is
input MB/s, higher is better. Machine: AMD Ryzen 9 9950X (Zen 5, 16 C, 64 MB L3,
DDR5), Go 1.26, `windows/amd64`. Reproduce with `go test -bench . -benchmem`.

### Scaling with size

`[]uint32`, uniform random:

|          n | radsort | stdlib | speedup |
|-----------:|--------:|-------:|--------:|
|      1 000 |      48 |    758 |   0.06× |
|     10 000 |     269 |    116 |    2.3× |
|    100 000 |     969 |     89 |     11× |
|  1 000 000 |    1255 |     74 |     17× |
| 10 000 000 |     994 |     65 |     15× |
| 30 000 000 |     875 |     61 | **14×** |

`[]uint64` (8 rounds): radsort 720–1186 MB/s, stdlib 121–1537; **6.0×** at 30M.
Key/value pairs `struct{key,val uint32}` (radsort's design target, via `SortKey`):
790–940 MB/s vs stdlib's 78–545; **10×** at 30M.

Below ~10 000 elements (well inside cache) the fixed ~1–2 MiB scratch allocation
dominates and the comparison sort wins — exactly the crossover the paper
describes. Above it, radsort's flat throughput beats the comparison sort's
`O(n log n)`, cache-bound decline.

### Sensitivity to input distribution

`[]uint32`, n = 10 000 000. This is the most important comparison:

| distribution        | radsort |    stdlib | note                      |
|---------------------|--------:|----------:|---------------------------|
| uniform random      |     988 |        64 | radsort 15×               |
| **already sorted**  |     296 | **14193** | stdlib 48× (detects runs) |
| **reverse sorted**  |     290 |  **8969** | stdlib 31×                |
| few unique (16)     |    1078 |       397 | radsort 2.7×              |
| small range (0–999) |    1010 |       149 | radsort 6.8×              |
| nearly sorted       |     915 |       466 | radsort 2×                |

A radix sort does the same fixed number of passes regardless of the data, so
radsort's throughput barely moves (~290–1080 MB/s, the low end being the
cache-unfriendly strictly-sequential bucket pattern of pre-sorted input).
pdqsort's throughput swings **220×** because it detects sorted/reverse runs. If
your data is often already ordered, the comparison sort is unbeatable; for
genuinely unordered data at scale, radsort wins decisively and *predictably*.

### vs. a conventional LSD radix sort

The paper's actual claim is that Radsort beats a *plain out-of-place LSD radix
sort* above ~2 MiB. On this machine that does **not** reproduce — a tuned plain
LSD sort is faster at every size (~1.1–1.35 GB/s):

|          n | radsort MB/s | plain LSD MB/s | radsort mem | plain LSD mem |
|-----------:|-------------:|---------------:|------------:|--------------:|
|    100 000 |          969 |           1324 |     1.06 MB |        0.4 MB |
|  1 000 000 |         1255 |           1336 |     1.07 MB |          4 MB |
| 10 000 000 |          994 |           1105 |     1.24 MB |         40 MB |
| 30 000 000 |          875 |           1078 | **1.59 MB** |    **120 MB** |

The paper's target machines (POWER9, Icelake/Grace servers) are memory-bandwidth
starved, so plain LSD's read-for-ownership traffic dominates and Radsort's
block-reuse wins. A Zen 5 desktop has enormous cache (64 MB L3) and DDR5
bandwidth that hide that penalty, so plain LSD's simpler, lower-overhead inner
loop wins on wall-clock — while Radsort still delivers its headline benefit:
**~75× less memory** (`O(√n)`-with-fixed-`b` vs `O(n)`). The advantage is
hardware-dependent; expect Radsort to look better on bandwidth-bound servers.

### Concurrent sorting

`[]uint32`, uniform random, up to 8 worker goroutines (`-benchmem`):

|                     |       10M |       30M |
|---------------------|----------:|----------:|
| serial              |  943 MB/s |  832 MB/s |
| parallel (fresh)    | 2457 MB/s | 2682 MB/s |
| parallel (recycled) | 2637 MB/s | 2890 MB/s |
| speedup (recycled)  |      2.8× |      3.5× |

`recycled` reuses a `ParallelSorter` and so avoids re-allocating the O(n) split
buffer (a fresh 30M sort otherwise allocates ~120 MiB per call); it is both
faster and allocation-free after warm-up. Speedup is capped by memory bandwidth:
running independent sorts on more goroutines, aggregate throughput saturates
around 8 threads (~4.2 GB/s here), so a single parallel sort tops out around 3×.

## Memory / allocations

`radsort.Uint32s` reports `5 allocs/op`, all from one-time setup per call:

- the scratch **T** array — `2σ·b` elements, a *fixed* 1 MiB (uint32) / 2 MiB
  (uint64), independent of `n`;
- `perm`, `perm2` (`[]uint32`) and `assignments` (`[]uint8`), each `O(n/b)`;
- the `sorter` struct.

So `B/op` is ~1.06 MB even for tiny inputs and grows only slowly (1.59 MB at
30M uint32). This is the algorithm's working memory, matching the reference C.

Reusing a `Sorter` (above) drops this to **0 allocs/op** after warm-up. That is
pure win for small, frequently-sorted arrays where the fixed allocation
otherwise dominates, and irrelevant for large ones where it is already
amortized:

|         n | fresh (alloc/call) |      recycled | speedup from recycling |
|----------:|-------------------:|--------------:|-----------------------:|
|     1 000 |   46 MB/s, 1.05 MB | 268 MB/s, 0 B |               **5.8×** |
|    10 000 |           287 MB/s |      639 MB/s |                   2.2× |
|   100 000 |          1005 MB/s |     1157 MB/s |                  ~1.2× |
| 1 000 000 |          1262 MB/s |     1281 MB/s |                    ~1× |

(`[]uint32`, uniform, monomorphised path.)

## Not implemented

The core follows the paper's single-threaded `permuted` variant. Of the paper's
optional optimizations, two are implemented and two are not.

Implemented: **avoiding finalisation** (§4.1) — see `Uint32Seq`/`SortKeySeq`,
which `Map` also uses; and the portable half of **§4.2's "bitmanip"** end-of-block check — each
bucket is a `{cur, end}` pointer pair instead of a slice + index, dropping a
bounds check and shrinking the bucket table (~6–10 % faster on large
`uint32`/`uint64` sorts here, gated behind the build tags above).

Not implemented:

- **The full §4.2 form** — a *single* cursor tested for block-alignment by
  bitmask, which needs block-aligned storage (over-aligned scratch and handling
  the input's unaligned head). It would trim the bucket table further for a few
  percent more; the alignment machinery did not look worth it on this hardware.
- **The §4.3 block-chunk parallel scheme.** The concurrent sorts instead use the
  most-significant-byte split suggested at the end of §4.3, which needs an O(n)
  buffer and so does not preserve the O(√n) space bound that §4.3's scheme keeps.

Software write-combining is *not* a missing Radsort feature: the paper's `swc` is
a separate baseline (a conventional out-of-place radix sort with streaming
stores), and Radsort's block reuse keeps output blocks hot in cache, which the
paper notes makes write-combining "not needed" (§6).

## License

BSD-2-Clause, the same as the reference implementation. See [LICENSE](LICENSE);
the original copyright of Robert Clausecker is retained.
