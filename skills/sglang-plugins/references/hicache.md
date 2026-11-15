# HiCache Storage: Orientation

Verified 2026-11-14 against https://docs.sglang.io/docs/advanced_features/hicache_design.md,
https://docs.sglang.io/docs/advanced_features/hicache_best_practices.md and
https://docs.sglang.io/docs/advanced_features/hicache_storage_runtime_attach_detach.md.
The live pages win on any conflict. Exact flags, extra-config keys and metric names
live there: fetch the page and grep it rather than copying them from here.

## Tiers

| Tier | Medium | Shared across instances? |
|---|---|---|
| L1 | GPU memory | No |
| L2 | Host memory | No, not even two instances on one node |
| L3 | Storage backend | Only when every instance points at the same backend namespace |

HiCache extends RadixAttention prefix caching from the GPU to host memory and then
to a storage backend. A larger host pool only grows one instance's private L2.
Cross-instance reuse needs an L3 backend (`--hicache-storage-backend`).

An L3 hit is staged in the host pool before it reaches the GPU. If the host pool is
not larger than the GPU pool in tokens, L3 hits are dropped and the prompt is
recomputed (`hicache_best_practices`, "Host Pool Sizing").

## Hybrid Mamba models

Hybrid Mamba, GDN and KDA models keep a second host pool for recurrent states,
sized from the device Mamba pool. It can fill before host KV does; when it is full
its LRU drops the oldest states, and a prefix whose KV is still on host cannot be
matched until a request recomputes it. The best-practices page names the metrics to
watch (`grep -n 'mamba' "$f"`).

## Backends

| Backend | Use |
|---|---|
| `file` | Minimal reference backend; writes to `/tmp/hicache` by default, node-local |
| `fast_file` | Local NVMe or filesystem tier: same page format as `file`, parallel reads, atomic writes, LRU eviction |
| `mooncake`, `hf3fs`, `nixl`, `aibrix` | Distributed backends that can reach cluster scope |
| `eic` | Named by the attach and detach page; no setup section on the pages above |
| `dynamic` | Loads your own backend class from a module path, without changing SGLang |

LMCache is offered as an alternative to HiCache rather than as an HiCache backend.

## Writing a custom backend

- Backends implement `HiCacheStorage` (an abstract base class). The core operations
  are get, exists and set by key; batch v1 methods are optional and switched on
  through the extra config. Read the live page for the exact method set.
- Load it with `--hicache-storage-backend dynamic` and a JSON extra config naming
  the backend, the module path and the class name (`hicache_best_practices`,
  "Dynamic Backend Loading"). The other route is registering it in SGLang's
  `BackendFactory`, which edits SGLang's source.
- On attach, HiCache hands the backend its host memory pools in a fixed order: the
  anchor pool through `register_mem_pool_host`, then every other pool (Mamba state,
  indexer, SWA and others) through `register_mem_host_pool_v2`, then
  `on_host_pools_registered` once. A backend whose external service needs the full
  layout up front should finish setup in `on_host_pools_registered`; raising there
  fails the attach (`hicache_design`, "Unified Interfaces").
- The host memory layout must suit the backend. Some backends accept only certain
  layouts (Mooncake needs a page-first or page-head layout); a mismatch fails the
  attach.

## Runtime attach and detach

- `GET`, `PUT` and `DELETE` on `/hicache/storage-backend` query, attach and detach
  the L3 backend without a restart.
- The server must be idle: no running and no queued requests, or the call fails
  with HTTP 400 and changes nothing. Drain traffic first.
- The calls need the admin key as a bearer token. The server must be started with
  one; without it they return 403, and `--api-key` is not accepted.
- With data parallelism, success means every DP rank succeeded, and there is no
  automatic rollback. If an attach fails, detach, fix the config, then attach again.
- Detach stops SGLang using the backend; it does not delete stored data.
