# Fasthand OCR Worker

Real-time OCR (optical character recognition) API built on Cloudflare Workers AI. Send a base64-encoded image and get back the text printed on it — labels, ID cards, documents, packaging, signs.

Built by **Fasthand Studio**.

## Features

- **Accurate extraction** on dense text via the **Llama 3.2 11B Vision** model (reads small/mixed-script print far better than older vision models)
- **API-key protected** — every request must carry a valid `X-API-Key` header
- **Edge runtime** — runs on Cloudflare's global network (low latency)
- **Hardened** — timing-safe key comparison, request/image size caps, strict base64 validation, security headers on every response
- **Live docs page** — browse `/` in a browser for interactive-style API docs
- **CORS enabled** for all origins

## Accuracy

- The underlying **Llama 3.2 11B Vision** model is optimized for visual recognition and transcribes printed text **character-for-character**, including small fonts and dense layouts (ID cards, receipts, labels, forms).
- Verified against a real **Bangladesh National ID card** (mixed Bengali + English): the government header, holder name, father/mother names, date of birth, and NID number were all extracted correctly.
- Per-word **confidence** scores are returned in the response. Workers AI does not expose per-word scores, so each defaults to **0.85** as a conservative estimate.
- Accuracy depends on input quality — crisp, well-lit, upright images transcribe almost perfectly; blur, glare, or heavily stylized text degrades results.

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

## Repo structure

```
├── .gitignore               # keeps secrets/caches out of the repo
├── package.json             # npm scripts (dev, deploy)
├── package-lock.json
├── tsconfig.json
├── wrangler.toml            # worker config + AI binding
├── README.md                # this file
└── src/
    ├── index.ts             # worker logic (auth, validation, OCR)
    ├── docs.html            # API documentation page served at /
    └── html.d.ts            # type shim for the .html import
```

> **Never commit** `.dev.vars`, `.wrangler/`, `node_modules/`, or any actual API keys/secrets — all are already in `.gitignore`.

## Prerequisites

- Node.js 18+
- A Cloudflare account with **Workers** and **Workers AI** enabled
- Wrangler CLI (`npm install -g wrangler` or use `npx wrangler`)

## Setup

```bash
# 1. Install dependencies
npm install

# 2. Login to Cloudflare
npx wrangler login

# 3. Set your secret API key (protected, never stored in code)
echo "put-a-strong-random-key-here" | npx wrangler secret put API_KEY

# 4. Deploy
npm run deploy
```

You'll get a URL like `https://fasthand-ocr-worker.<your-subdomain>.workers.dev`.

> **Audience note:** anyone calling this worker needs the same `API_KEY` value. Rotate it anytime with `echo "newkey" | npx wrangler secret put API_KEY` — no code change needed.

## API Usage

### `POST /` — OCR text extraction

**Authentication (required).** Add your API key as a request header:

| Header | Required | Value |
|---|---|---|
| `X-API-Key` | yes | your secret key |
| `Content-Type` | yes | `application/json` |

**Request body:**

```json
{
  "image": "iVBORw0KGgoAAAANSUhEUg..."
}
```

- `image` — base64-encoded JPEG, PNG, or WebP. A `data:image/...;base64,` prefix is stripped automatically.
- Max request body: **8 MB**; max decoded image: **6 MB**.

**Success response — `200 OK`:**

```json
{
  "success": true,
  "text": "Government of the People's Republic of Bangladesh\nNational ID Card\n...",
  "words": [
    { "text": "Government", "confidence": 0.85 },
    { "text": "National", "confidence": 0.85 }
  ]
}
```

**Error responses:**

| Status | Cause |
|---|---|
| `401` | Missing or invalid `X-API-Key` |
| `400` | Missing/invalid `image` field or bad base64 |
| `413` | Body > 8 MB or decoded image > 6 MB |
| `405` | Non-POST request |
| `500` | Model or processing failure |

**cURL example:**

```bash
curl -X POST https://fasthand-ocr-worker.fasthand.workers.dev/ \
  -H "Content-Type: application/json" \
  -H "X-API-Key: YOUR_API_KEY" \
  -d '{"image": "iVBOR..."}'
```

### `GET /` — documentation page

Serves this project's API docs in the browser. Public (no key needed).

## Security

- **API-key auth** validated with `crypto.subtle.timingSafeEqual` (timing-attack resistant)
- **Secrets stored as Cloudflare secrets**, never in the repo
- **Payload limits** (8 MB request / 6 MB image) prevent DoS via oversized uploads
- **Strict base64 validation** rejects malformed input before decoding
- **Security headers** on every response: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Permissions-Policy`, `Cache-Control: no-store`, CSP on the docs page
- **HTTPS only** (default on workers.dev)
- Suggested extras: enable **Rate Limiting** for your worker in the Cloudflare dashboard to cap requests per IP.

## Pricing

Model: `@cf/meta/llama-3.2-11b-vision-instruct`

| Line item | Rate |
|---|---|
| AI input tokens | **$0.049 / 1M tokens** |
| AI output tokens | **$0.68 / 1M tokens** |
| Typical cost per OCR scan | **≈ $0.00013** (≈ $0.13 per 1,000 scans) |

### How many free scans per day?

Workers AI gives you **10,000 free neurons every day**, which is **≈ $5 of model usage per day**.

At a typical cost of **≈ $0.00013 per scan**, the free daily allowance covers roughly:

> **≈ 30,000–40,000 free OCR scans per day** (before any pay-as-you-go charges).

The exact number depends on image size (bigger images = more input tokens) and how much text the model returns. A typical scan uses ~400–600 input tokens and ~100–200 output tokens.

Workers free tier summary:

| Resource | Free allowance |
|---|---|
| Workers AI | **10,000 neurons/day** (≈ $5 of model usage, ≈ 30k–40k scans) |
| Worker requests | **100,000 requests/day** |
| Requests beyond free tier | $0.30 / 1M requests |

If you ever exceed the daily allowance, extra usage bills at the token rates above — still only **tenths of a cent per image**.

## Development

```bash
npm run dev          # local dev server → http://localhost:8787
npm run deploy       # deploy to production
npm run tail         # stream live logs
```

## Stack

| Component | Detail |
|---|---|
| Runtime | Cloudflare Workers (TypeScript) |
| AI model | `@cf/meta/llama-3.2-11b-vision-instruct` |
| Platform | wrangler + Workers AI |

## License

MIT