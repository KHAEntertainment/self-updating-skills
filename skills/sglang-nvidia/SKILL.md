---
name: sglang-nvidia
description: Install, launch and tune SGLang on NVIDIA GPUs (L4 and other data-center cards, plus Jetson Orin) against the live docs.sglang.io install and examples pages instead of stale training data. Covers the Docker image, pip/uv installs, the CUDA 13 requirement, sglang serve, FlashInfer and its fallbacks, CUDA_HOME errors, the OpenAI-compatible API and Jetson containers. Use when installing or upgrading SGLang on an NVIDIA machine, writing a launch command or Dockerfile for it, choosing model launch flags, or debugging a CUDA, driver, FlashInfer or wheel problem with SGLang.
license: MIT
allowed-tools: Bash(${CLAUDE_SKILL_DIR}/scripts/upkeep.sh *)
metadata:
  author: KHAEntertainment
  version: "0.1.0"
  upkeep-pins: "sglang=github:sgl-project/sglang@0.5.23"
  upkeep-threshold-days: "30"
  upkeep-source-base: "https://docs.sglang.io"
  upkeep-source-ext: ".md"
  upkeep-source-cache: "sglang-docs"
  upkeep-source-index: "https://docs.sglang.io/llms.txt"
  upkeep-source-pages: "docs/get-started/install, docs/get-started/examples, docs/hardware-platforms/nvidia-gpus, docs/hardware-platforms/nvidia_jetson, docs/get-started/quickstart, docs/advanced_features/server_arguments, docs/basic_usage/openai_api_completions"
  upkeep-frozen-scope: "sha256:89ce7319fbbc7103"
  upkeep-frozen-update-policy: "sha256:158012c28a938201"
---

# SGLang on NVIDIA GPUs

<!-- frozen:scope -->
This skill covers SGLang's general NVIDIA instructions and best practices. The NVIDIA
GPUs page in the docs only points to the main install guide, so the sources are the
install and examples pages. CUDA is built into SGLang, not a plugin, so CUDA setup
belongs here; plugin hooks and HiCache storage belong to the `sglang-plugins` skill.
The target hardware is NVIDIA L4 and Jetson.

docs.sglang.io is the canonical copy of the docs. The lmsysorg.mintlify.app
address mirrors it; do not track the mirror.
<!-- /frozen:scope -->

<!-- frozen:update-policy -->
The tooling that keeps this skill current (self-updating-skills and upkeep.sh) is
an untested MVP. Never rewrite this skill's files on your own. When `upkeep.sh
check` reports STALE or CONTENT-LAG, or a live page disagrees with anything here,
write the change you propose as a unified diff, show it to the owner together with
the live page and section it comes from, and apply it only after the owner
approves that diff. This covers pins, Verified dates, references and the body of
this file alike.
<!-- /frozen:update-policy -->

## Freshness & Integrity Check

!`"${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" check "${CLAUDE_SKILL_DIR}"`

- If the line above was not expanded into a `VERDICT:` line (non-Claude harnesses), run `scripts/upkeep.sh check` from this skill's directory first.
- **`VERDICT: TAMPERED`** → Stop. A frozen owner block was changed without approval; tell the owner.
- **`VERDICT: STALE`** or **`CONTENT-LAG`** → SGLang released a new version or a Verified date is old. Read the live install page before giving install or launch steps, and propose a diff for this skill as the update policy says. Do not edit the skill.
- **`VERDICT: FRESH`** → Use the orientation below, and still read the live page for exact flags and image tags.

## Workflow

1. **Read the live page before giving a command.** Install steps, image tags and
   CUDA requirements change between releases:

   ```bash
   f=$("${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" fetch docs/get-started/install)
   grep -n -i 'cuda' "$f"
   ```

   A `⚠` line from `fetch` means the live page was unreachable and you got a cached
   copy or nothing. Say so, and never guess an unavailable page.
2. **Look up model-specific flags in the cookbook**, not from memory. Cookbook pages
   are listed by `fetch --index` under `cookbook/`; fetch one by that path.
3. **Look up any other flag in `server_arguments`.** It is about 160 KB: grep it, never
   read it whole or copy it into an answer.
4. **Verify the server** with the quickstart request before building on it.

| Task | Page (pass to `upkeep.sh fetch`) |
|---|---|
| Install (Docker, pip/uv, source), deployment, troubleshooting | `docs/get-started/install` |
| First server, a larger model, streaming, batches, a tool-using agent | `docs/get-started/examples` |
| Check a running server | `docs/get-started/quickstart` |
| Jetson Orin | `docs/hardware-platforms/nvidia_jetson` |
| Any server flag | `docs/advanced_features/server_arguments` (grep only) |
| OpenAI-compatible request options | `docs/basic_usage/openai_api_completions` |
| Launch flags for one model | `fetch --index`, then a `cookbook/...` page |

## Orientation

Verified 2026-11-14 against https://docs.sglang.io/docs/get-started/install.md and
https://docs.sglang.io/docs/get-started/examples.md.

- **CUDA 13 only.** Wheels and images ship CUDA 13 and need a CUDA 13-compatible
  driver. SGLang 0.5.19 was the last release with a CUDA 12 lane, and
  `lmsysorg/sglang:v0.5.19-cu129` is the last CUDA 12 image.
- **Docker** is the documented default: `lmsysorg/sglang:latest` (newest stable),
  run with `--gpus all`, `--ipc=host`, a large `--shm-size` and the Hugging Face
  cache mounted. The host needs the NVIDIA Container Toolkit. Pin a release or dated
  nightly tag for reproducible deployments.
- **pip/uv** needs Python 3.11 or newer and installs with
  `uv pip install --prerelease=allow sglang`.
- **Launch** with `sglang serve <model> --host ... --port ...` (port 30000 in every
  example). Some pages still show `python -m sglang.launch_server --model-path`.
- **FlashInfer** is the default attention backend and needs sm75 or newer (the docs
  list L4 among the supported cards). If FlashInfer fails, the documented fallback
  is `--attention-backend triton --sampling-backend pytorch`.
- **The API** is OpenAI-compatible at `http://<host>:30000/v1`; the examples use
  the `openai` Python client with `api_key="EMPTY"` on a server without auth.

## Old habit → current path

Verified 2026-11-14 against the pages in the last column.

| Old habit | Current path | Page |
|---|---|---|
| A `-cu129` image or CUDA 12 wheels | CUDA 13 images and wheels; CUDA 12 ended with 0.5.19 | `docs/get-started/install` |
| `uv pip install sglang` with no flag | Add `--prerelease=allow`: uv older than 0.12.0 otherwise installs SGLang 0.5.9 without warning | `docs/get-started/install` |
| `python -m sglang.launch_server --model-path M` | `sglang serve M`, the form the install and examples pages lead with | `docs/get-started/install` |
| Launch flags recalled from memory | The model's cookbook page for your GPU | `docs/get-started/install` ("Start a model server") |
| Rebuild everything after a FlashInfer error | Try `--attention-backend triton --sampling-backend pytorch`; reinstall FlashInfer and clear `~/.cache/flashinfer` if needed | `docs/get-started/install` ("FlashInfer") |
| `OSError: CUDA_HOME environment variable is not set` → reinstall CUDA | Export `CUDA_HOME` to the toolkit directory, or install FlashInfer first | `docs/get-started/install` ("CUDA_HOME") |

## Jetson Orin

Verified 2026-11-14 against https://docs.sglang.io/docs/hardware-platforms/nvidia_jetson.md.

- The page targets the Jetson AGX Orin devkit on JetPack 6.1 or later, in
  high-performance mode (`sudo nvpmodel -m 0`).
- It builds SGLang with `jetson-containers` (`jetson-containers build sglang`),
  not with the main Docker image or a pip wheel.
- Its launch example uses `--dtype half` and `--context-length 8192` because the
  board's resources are limited.
- **Open question:** the Jetson page does not say which SGLang release
  `jetson-containers` builds, or how it relates to the CUDA 13 requirement on the
  install page. Check the version the container reports before applying flags or
  steps from the main install guide, and tell the owner if they disagree.
