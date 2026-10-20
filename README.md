# Fasthand OCR

Real-time OCR (optical character recognition) API. Send a base64-encoded image and get back the text printed on it — labels, ID cards, documents, packaging, signs.

Built by **Fasthand Studio**.

[![Deploy to Cloudflare Workers](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/fasthand-studio/Fasthand-ocr.git)

> The button above deploys the **legacy Cloudflare Worker** (`src/`), which is still what production runs today. The Go service in `cmd/` and `internal/` is the implementation being migrated to — see [Architecture](#architecture) for why the two coexist for now.

## What it does

- Accepts a base64 JPEG, PNG or WebP image and returns the transcribed text plus a per-word breakdown.
- Uses the **Llama 3.2 11B Vision** model, which reads small and mixed-script print far better than older vision models.
- Requires an API key on every OCR call.
- Serves interactive API docs at `GET /`.

## Features

- **Accurate extraction** on dense text via Llama 3.2 11B Vision
- **API-key protected** — every request must carry a valid `X-API-Key` header
- **Single static binary** — Go standard library only, zero third-party modules
- **Hardened** — constant-time key comparison, request and image size caps enforced on the actual bytes, strict base64, image format sniffing, bounded in-flight inference, security headers on every response
- **Live docs page** — browse `/` in your browser
- **CORS enabled** for all origins

## Architecture

**Cloudflare Workers cannot run Go.** The Workers runtime supports JavaScript, TypeScript, Python and Rust (Go only through WebAssembly, which is not a supported or maintainable path for an HTTP service). Because the product must keep using Workers AI, the service is split so that Go owns the application logic and talks to the model over its documented REST interface:

```
client ──HTTP──▶ Go service ──▶ Cloudflare Workers AI REST API
                   │              POST /client/v4/accounts/{id}/ai/run/{model}
                   └── serves / (docs) and POST / (OCR)
```

The model, prompt, response contract and pricing are unchanged. `internal/ocr.WorkerAI` is the only component that knows about Cloudflare; it satisfies the `ocr.Provider` interface, so tests and any future provider swap replace that one type.

Two hosting paths are supported by this code today:

| Path | What it means |
|---|---|
| Any container or VM host | Build `cmd/fasthand-ocr` (or the included `Dockerfile`) and run it. `CF_ACCOUNT_ID` + `CF_API_TOKEN` give it access to Workers AI. |
| Cloudflare Containers, with a thin Worker front door | Keeps the current `*.workers.dev` URL. Requires the **Workers Paid** plan and Docker to build the image. |

The existing Worker stays in `src/` and keeps production online until a host is chosen. Deleting it in the same change would take the service down, so the removal is deliberately deferred.

## Repo structure

```
├── cmd/fasthand-ocr/        # main: config, provider wiring, graceful shutdown
├── internal/
│   ├── config/              # environment parsing and validation
│   ├── ocr/                 # provider interface, Workers AI REST client, text cleanup
│   │   └── testdata/        # golden fixtures generated from the legacy JavaScript
│   └── server/              # HTTP routing, auth, limits, headers, docs
├── web/docs.html            # API documentation page (shared by both implementations)
├── tools/                   # golden fixture generator for the parity test
├── src/                     # legacy Cloudflare Worker, still what production runs
├── wrangler.toml            # legacy Worker config
├── Dockerfile               # container build for the Go service
└── .github/workflows/ci.yml # Go checks + legacy Worker bundle check
```

Never commit `.dev.vars`, `.env`, `.wrangler/`, `node_modules/`, or any real API key — all are in `.gitignore`.

## Prerequisites

- **Go 1.26 or newer** (`go.mod` declares `go 1.26`; CI uses the version from that file)
- A Cloudflare account with **Workers AI** enabled, for the API token
- Node.js 18+ and Wrangler only if you deploy or change the legacy Worker
- Docker only if you build the container image

## Configuration

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `API_KEY` | yes | — | Secret callers must send in `X-API-Key` |
| `CF_ACCOUNT_ID` | yes | — | Cloudflare account that owns the Workers AI subscription |
| `CF_API_TOKEN` | yes | — | API token allowed to run Workers AI models |
| `AI_MODEL` | no | `@cf/meta/llama-3.2-11b-vision-instruct` | Vision model slug |
| `ADDR` | no | `:8787` | Listen address |
| `UPSTREAM_TIMEOUT` | no | `60s` | Deadline for the model call |
| `REQUEST_TIMEOUT` | no | `90s` | Deadline for the model call plus cleanup, counted after the body has arrived |
| `MAX_CONCURRENT_REQUESTS` | no | `64` | OCR requests read into memory at once; over it returns `429` |
| `MAX_CONCURRENT_OCR` | no | `32` | Of those, how many may be in front of the model at once |

Timeouts are Go durations (`30s`, `2m`). `UPSTREAM_TIMEOUT` must not exceed `REQUEST_TIMEOUT`, and `MAX_CONCURRENT_OCR` must not exceed `MAX_CONCURRENT_REQUESTS` — an inference limit above the buffering limit could never be reached, so the combination is rejected at startup instead of being quietly ignored.

The two limits bound different things. A request body is buffered whole in memory before the model is called, so `MAX_CONCURRENT_REQUESTS` is the memory guard: worst case is `MAX_CONCURRENT_REQUESTS × 8 MB` of body bytes, which is why the default is modest. `MAX_CONCURRENT_OCR` is the quota guard, keeping a burst from spending your free-tier neurons in one second. The documentation page buffers nothing and is not counted against either.

**Startup behaviour.** A missing `CF_ACCOUNT_ID` or `CF_API_TOKEN` stops the process immediately and names the variable, because no request could ever succeed. A missing `API_KEY` only warns at startup and answers `503` on requests, matching what deployed clients already see.

**Token permissions — least privilege.** The Cloudflare API token needs only `Workers AI → Read & Edit` on the target account. It does not need Workers, DNS, or account-wide permissions. The token is sent as a bearer secret to `api.cloudflare.com` and is never returned to a caller or written to a log line.

## Run locally

```bash
# 1. Set the three required values (any strong random string for API_KEY)
export API_KEY="$(openssl rand -hex 32)"
export CF_ACCOUNT_ID="your-account-id"
export CF_API_TOKEN="your-token"

# 2. Start the service
go run ./cmd/fasthand-ocr

# 3. Docs page and a real request
open http://localhost:8787/
curl -X POST http://localhost:8787/ \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d "{\"image\": \"$(base64 < label.png | tr -d '\n')\"}"
```

Stop with Ctrl-C; in-flight requests get 15 seconds to finish.

The test suite needs **no credentials and no network** — the model is always mocked:

```bash
go test ./...              # all tests
go test -race ./...        # race detector (Linux/macOS or CI)
go vet ./...               # static analysis
gofmt -l .                 # formatting, must print nothing
go build ./cmd/fasthand-ocr
```

## API

### `POST /` — OCR text extraction

| Header | Required | Value |
|---|---|---|
| `X-API-Key` | yes | your secret key |
| `Content-Type` | recommended | `application/json` |

**Request body:**

```json
{ "image": "iVBORw0KGgoAAAANSUhEUg..." }
```

- `image` — base64-encoded JPEG, PNG, or WebP. A `data:image/...;base64,` prefix is stripped automatically.
- Max request body: **8 MB**; max decoded image: **6 MB**.

**Success response — `200 OK`:**

```json
{
  "success": true,
  "text": "Government of the People's Republic of Bangladesh\nNational ID Card",
  "words": [
    { "text": "Government", "confidence": 0.85 },
    { "text": "National", "confidence": 0.85 }
  ]
}
```

**Error responses:**

| Status | Cause | Body |
|---|---|---|
| `401` | Missing or invalid `X-API-Key` | `{"success":false,"error":"Unauthorized: missing or invalid X-API-Key header"}` |
| `400` | Missing/non-string `image`, or JSON that is not an object | `{"success":false,"error":"No image provided"}` |
| `400` | Malformed base64 | `{"success":false,"error":"Invalid base64 image data"}` |
| `400` | Bytes are not JPEG/PNG/WebP | `{"success":false,"error":"Unsupported image format (JPEG, PNG or WebP required)"}` |
| `400` | Body does not parse as JSON at all | `{"success":false,"error":"Invalid JSON body"}` |
| `413` | Body > 8 MB or decoded image > 6 MB | `{"success":false,"error":"Request body too large (max 8 MB)"}` |
| `404` | Any other path | `{"success":false,"error":"Not found"}` |
| `405` | Non-POST on `/` | `Method not allowed` (plain text) |
| `429` | `MAX_CONCURRENT_REQUESTS` or `MAX_CONCURRENT_OCR` reached | `{"success":false,"error":"Server busy, please retry"}` + `Retry-After` |
| `500` | Model or upstream failure | `{"success":false,"error":"Internal server error"}` |
| `503` | `API_KEY` not configured | `{"success":false,"error":"Server misconfigured: API_KEY secret is not set"}` |

### `GET /` — documentation page

Serves the API docs in the browser. Public, no key needed.

## Performance

Measured locally, not end-to-end: model inference dominates real latency and was not run against a live account for this change.

**Upstream request construction.** The legacy Worker converted the image to a JavaScript array of every byte value before sending it. That is the single biggest cost in the old path:

| Image size | Legacy payload | Legacy time | Base64 payload | Base64 time |
|---|---|---|---|---|
| 256 KB | 0.89 MB | 28.4 ms | 0.33 MB | 0.4 ms |
| 1 MB | 3.57 MB | 110.1 ms | 1.33 MB | 1.4 ms |
| 4 MB | 14.28 MB | 456.3 ms | 5.33 MB | 4.4 ms |

2.7× fewer bytes on the wire and 74–104× less time, measured in Node over the same algorithm the Worker used. It also removes the risk of hitting a message-size ceiling on the way to the model.

**Go service, local request pipeline** (`go test -bench . -benchmem`, 256 KB JPEG):

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| `BenchmarkImageFromBase64/raw` | 694,209 | 1,345,502 | 28 |
| `BenchmarkImageFromBase64/data_url` | 689,075 | 1,345,491 | 29 |
| `BenchmarkRequestBodyPipeline` | 1,094,679 | 1,698,922 | 31 |
| `BenchmarkClean` (dense ID-card text) | 287,794 | 67,167 | 38 |
| `BenchmarkBuildResult` | 337,175 | 103,594 | 37 |
| `BenchmarkRecognizePayload` (1 MB image) | 1,220,331 | 1,420,587 | 3 |

Base64 is decoded **once**, into a reader capped at 6 MB, instead of the legacy regex scan plus two separate decodes.

**Artifact size.** Worker bundle: **18.84 KiB** (5.70 KiB gzipped) — mostly the HTML docs page, which is inlined. Go static `linux/amd64` binary (`CGO_ENABLED=0`, `-trimpath -ldflags="-s -w"`): **7.6 MB**. The Worker is smaller because Cloudflare already ships the V8 runtime; the Go number is a self-contained runtime with no dependencies to install on a host.

## Accuracy

- Llama 3.2 11B Vision transcribes printed text **character-for-character**, including small fonts and dense layouts (ID cards, receipts, labels, forms).
- Verified against a real **Bangladesh National ID card** (mixed Bengali + English): the government header, holder name, father/mother names, date of birth, and NID number were all extracted correctly.
- Accuracy depends on input quality — crisp, well-lit, upright images transcribe almost perfectly; blur, glare, or heavily stylized text degrades results.

## About `confidence`

Workers AI does not return per-word confidence for its vision models. Every word therefore carries the same **0.85 estimate**, which is a placeholder and **not** a model measurement. It exists only because the published response shape has always included the field; `words[].confidence` must not be used to judge transcription quality. Treat `text` as the output.

## Language support

The model is multilingual and handles **all major world scripts**, including:

| Script / language | Example |
|---|---|
| English & other Latin scripts | English, French, Spanish, German, Portuguese |
| Bengali | বাংলাদেশ |
| Arabic | العربية |
| Hindi & Devanagari | हिन्दी |
| Chinese | 中文 |
| Japanese | 日本語 |
| Korean | 한국어 |
| Cyrillic | Русский |

Best results are on **printed** text (labels, documents, cards). Handwriting and very stylized fonts are less reliable.

Word splitting is Unicode-aware, so Bengali, Arabic and CJK text splits on real whitespace.

## Security

- **API key** compared with `crypto/subtle.ConstantTimeCompare` over SHA-256 digests, so neither the secret's bytes nor its **length** leak through timing.
- **Secrets come from the environment.** Nothing is hardcoded and there is no fallback key.
- **Limits enforced on bytes actually read** (`http.MaxBytesReader`), not on the `Content-Length` header, which can be absent or wrong.
- **Input validated before paid work**: base64 strictness, decoded-size cap, and JPEG/PNG/WebP magic-byte checks all run before the model is called.
- **Two resource bounds**: `MAX_CONCURRENT_REQUESTS` caps how many bodies are buffered in memory at once, and `MAX_CONCURRENT_OCR` caps how many calls reach the model. Both answer `429` with `Retry-After` instead of queueing, so one caller cannot exhaust the heap or the free-tier quota on behalf of everyone else.
- **Request deadlines and cancellation**: a client that hangs up cancels the upstream call instead of leaving it billed, and the write deadline is sized to cover the upload as well as the inference.
- **No internal detail leaks**: upstream errors are logged server-side, and clients receive a fixed message. An oversized or truncated upstream reply is refused rather than partially believed.
- **Logs stay clean**: method, path, status, byte count, duration. No keys, images, or transcribed text.
- **Headers on every response**: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Permissions-Policy`, `Cache-Control: no-store`, and a CSP on the docs page.
- **Connection reuse**: one `http.Client` with a shared transport for all upstream calls.
- Suggested extra: enable **Rate Limiting** for your route in the Cloudflare dashboard to cap requests per IP.

## Pricing

Model: `@cf/meta/llama-3.2-11b-vision-instruct`

| Line item | Rate |
|---|---|
| AI input tokens | **$0.049 / 1M tokens** |
| AI output tokens | **$0.68 / 1M tokens** |
| Typical cost per OCR scan | **≈ $0.00013** (≈ $0.13 per 1,000 scans) |

Workers AI gives you **10,000 free neurons every day**, which is ≈ $5 of model usage — roughly **30,000–40,000 free OCR scans per day** at the rate above. The exact number depends on image size and how much text comes back. A typical scan uses ~400–600 input tokens and ~100–200 output tokens. These figures describe the model itself and apply whether it is reached through a Worker binding or the REST API.

## Deployment

Nothing is deployed by this change; production keeps running the Worker in `src/`.

**Go service:**

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/fasthand-ocr ./cmd/fasthand-ocr
# or
docker build -t fasthand-ocr .
```

Run it behind whatever terminates TLS for you. It has no database, no cache and no state beyond configuration — the same image works for local, container and VM hosting. `GET /` returns `200` without a key, which doubles as a container health check.

Required secrets on the target platform: `API_KEY`, `CF_ACCOUNT_ID`, `CF_API_TOKEN`.

**Legacy Worker (current production):**

```bash
npx wrangler login
echo "your-key" | npx wrangler secret put API_KEY
npm run deploy
```

### Migration checklist

1. Choose a host and point a route or custom domain at the Go service.
2. Set `API_KEY` (same value as the Worker's secret if callers should not change), `CF_ACCOUNT_ID`, `CF_API_TOKEN`.
3. Verify with a live OCR request against the pinned model, then check `words`, `text` and error codes match what clients expect.
4. Cut traffic over, and only then delete `src/`, `wrangler.toml`, `package.json`, `package-lock.json`, `tsconfig.json` and the `worker` CI job.

## Known limitations and compatibility

- **The docs page at `GET /` is one file shared by both implementations** (`web/docs.html`), so it still describes only the responses the Worker can produce. The Go service can also return `429` (rate-limited) and `400` for non-image bytes; the authoritative tables are here in the README until the Worker is retired, at which point the page should gain those rows.
- **No deployment or live-inference verification.** All tests mock the provider; the suite passes without Cloudflare credentials. Checked without spending inference: the pinned model slug is present in the account's Workers AI catalog (`wrangler ai models`) and is not on the published deprecation list. The REST request shape and reply envelope follow Cloudflare's documented API and are asserted against a fake upstream; they have not been confirmed against a live call.
- **The container is not built locally.** No Docker on the development machine; the `container` CI job builds the image and smoke-tests startup, the docs page, `401` and `400` paths, so that job is the first real verification of the `Dockerfile`.
- **`go test -race` did not run locally.** The race detector needs cgo and there is no C toolchain on Windows here; the `go` CI job runs it on Linux.
- `POST /` still accepts any `Content-Type`, matching the previous build rather than the docs page that recommends `application/json`. Enforcing it would break existing clients.
- **Unreachable in practice:** the 6 MB decoded-image check. The 8 MB body cap already limits a decode to exactly 6 MB, so `413` for image size only triggers if the body cap is ever raised. It is kept because it is part of the published contract, and is unit-tested directly.

**Intentional behaviour changes** (each has a regression test):

| Change | Before | After |
|---|---|---|
| Unknown path with `POST` | Ran a paid OCR request on any path | `404` |
| Unknown path with `GET` | `405 Method not allowed` | `404` |
| `HEAD /` | `405 Method not allowed` | `200`, headers only |
| Malformed JSON body | `500` with the parser's message | `400 {"error":"Invalid JSON body"}` |
| Valid JSON that is not an object | `400 No image provided` | unchanged, and now distinguished from a parse failure |
| Non-image bytes | Sent to the model | `400` unsupported format |
| `500` response body | `error.message` from inside the runtime | fixed `Internal server error` |
| Oversized body without `Content-Length` | Parsed in full | `413` |
| `"NO TEXT"` answer | Reported as words `NO`, `TEXT` | `words: []` |
| Concurrent bodies | Unbounded; memory grew with the number of open connections | `429` + `Retry-After` past `MAX_CONCURRENT_REQUESTS` |
| Concurrent inference | Unbounded | `429` + `Retry-After` past `MAX_CONCURRENT_OCR` |
| Slow upload on a poor connection | — | Write deadline now covers the body, not just inference |
| Upstream reply shape | Whole object stringified into `text` | `500`, logged |
| `SANCTIONED_DOMAINS` | Declared, never read | removed |
| Docs page tag | `TypeScript` | `Go` |

Text cleanup rules and word splitting are covered by a golden fixture generated by the legacy JavaScript itself (`tools/gen-legacy-golden.mjs` → `internal/ocr/testdata/js_ocr_golden.jsonl`); 60 inputs are asserted identical, apart from the `"NO TEXT"` fix above.

## Stack

| Component | Detail |
|---|---|
| Language | Go 1.26+ (standard library only) |
| AI model | `@cf/meta/llama-3.2-11b-vision-instruct` |
| AI access | Workers AI REST API |
| Legacy runtime | Cloudflare Workers (TypeScript) + wrangler |

## License

MIT
