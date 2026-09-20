/**
 * Cloudflare Worker for Real-time OCR using Workers AI
 * This worker receives camera frames and returns extracted text using OCR
 */

import docsHTML from './docs.html';

export interface Env {
	AI: any;
	API_KEY?: string;
	SANCTIONED_DOMAINS?: string;
}

interface OCRRequest {
	image: string; // Base64 encoded image
}

interface OCRResponse {
	success: boolean;
	text?: string;
	words?: Array<{
		text: string;
		confidence: number;
		boundingBox?: {
			x: number;
			y: number;
			width: number;
			height: number;
		};
	}>;
	error?: string;
}

const MAX_BODY_BYTES = 8 * 1024 * 1024; // 8 MB upload cap (protects against oversized payload DoS)
const MAX_IMAGE_BYTES = 6 * 1024 * 1024; // ~6 MB decoded image cap

export default {
	async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
		// Base headers applied to every response (CORS + security headers)
		const baseHeaders: Record<string, string> = securityHeaders();

		// Handle CORS preflight requests
		if (request.method === 'OPTIONS') {
			return new Response(null, { status: 204, headers: baseHeaders });
		}

		const url = new URL(request.url);

		// Serve API docs on GET / (public, informational only)
		if (request.method === 'GET' && url.pathname === '/') {
			return new Response(getDocsHTML(), {
				status: 200,
				headers: {
					'Content-Type': 'text/html;charset=UTF-8',
					...baseHeaders,
					'Content-Security-Policy': "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; frame-ancestors 'none'",
				},
			});
		}

		// Only allow POST requests for API routes
		if (request.method !== 'POST') {
			return new Response('Method not allowed', { status: 405, headers: baseHeaders });
		}

		// ---- Authentication: every API call must carry a valid API key ----
		if (!env.API_KEY) {
			return jsonResponse({
				success: false,
				error: 'Server misconfigured: API_KEY secret is not set',
			}, 503, baseHeaders);
		}
		if (!(await verifyApiKey(request, env.API_KEY))) {
			return jsonResponse({
				success: false,
				error: 'Unauthorized: missing or invalid X-API-Key header',
			}, 401, baseHeaders);
		}

		// ---- Request size limits ----
		const contentLength = Number(request.headers.get('Content-Length') || 0);
		if (contentLength > MAX_BODY_BYTES) {
			return jsonResponse({
				success: false,
				error: 'Request body too large (max 8 MB)',
			}, 413, baseHeaders);
		}

		try {
			// Parse the incoming request
			const body = await request.json() as OCRRequest;

			if (!body.image || typeof body.image !== 'string') {
				return jsonResponse({
					success: false,
					error: 'No image provided',
				}, 400, baseHeaders);
			}

			// Remove the data URL prefix if present (e.g., "data:image/jpeg;base64,")
			const base64Image = body.image.replace(/^data:image\/\w+;base64,/, '');

			// Validate base64 before decoding (rejects garbage / attacks early)
			if (!isValidBase64(base64Image)) {
				return jsonResponse({
					success: false,
					error: 'Invalid base64 image data',
				}, 400, baseHeaders);
			}

			// Convert base64 to array buffer (array of numbers 0-255)
			const imageBuffer = Uint8Array.from(atob(base64Image), c => c.charCodeAt(0));

			// Enforce a decoded size cap so a single request can't exhaust memory/CPU
			if (imageBuffer.byteLength > MAX_IMAGE_BYTES) {
				return jsonResponse({
					success: false,
					error: 'Image too large (max 6 MB decoded)',
				}, 413, baseHeaders);
			}

			// Run OCR using Cloudflare Workers AI (LLaMA 3.2 Vision reads dense text far better than LLaVA)
			const visionResponse = await env.AI.run('@cf/meta/llama-3.2-11b-vision-instruct', {
				image: Array.from(imageBuffer),
				prompt: 'TRANSCRIBE ALL TEXT VISIBLE IN THIS IMAGE, CHARACTER FOR CHARACTER, IN ITS ORIGINAL LAYOUT AND ORDER. Include every label, heading, name, number, line, and field value exactly as written, preserving case and special characters. Do NOT describe the image, do NOT summarize, do NOT add commentary or bullets. Output only the raw text. If no text is legible, output exactly: NO TEXT',
				max_tokens: 4096,
			});

			// Parse the response to extract text
			let extractedText = '';
			
			if (visionResponse && typeof visionResponse === 'object') {
				if ('description' in visionResponse && typeof visionResponse.description === 'string') {
					extractedText = visionResponse.description;
				} else if ('response' in visionResponse && typeof visionResponse.response === 'string') {
					extractedText = visionResponse.response;
				} else {
					extractedText = JSON.stringify(visionResponse);
				}
			} else if (typeof visionResponse === 'string') {
				extractedText = visionResponse;
			}

			// Clean up the extracted text
			extractedText = cleanOCRText(extractedText);

			return jsonResponse({
				success: true,
				text: extractedText,
				words: parseWordsFromText(extractedText),
			}, 200, baseHeaders);

		} catch (error) {
			console.error('OCR Error:', error);
			return jsonResponse({
				success: false,
				error: error instanceof Error ? error.message : 'Unknown error occurred',
			}, 500, baseHeaders);
		}
	},
};

/**
 * Timing-safe comparison of the provided API key against the stored secret.
 */
async function verifyApiKey(request: Request, secret: string): Promise<boolean> {
	const provided = request.headers.get('X-API-Key');
	if (!provided) return false;

	const a = new TextEncoder().encode(provided);
	const b = new TextEncoder().encode(secret);
	if (a.byteLength !== b.byteLength) return false;
	return await crypto.subtle.timingSafeEqual(a, b);
}

/**
 * Validate a string as strict base64 (no whitespace, correct padding).
 */
function isValidBase64(value: string): boolean {
	if (value.length === 0 || value.length % 4 !== 0) return false;
	if (!/^[A-Za-z0-9+/]*={0,2}$/.test(value)) return false;
	// Ensure padding is only at the end and length is sane
	const unpadded = value.replace(/=/g, '');
	if (unpadded.length % 4 === 1) return false;
	try {
		atob(value);
		return true;
	} catch {
		return false;
	}
}

/**
 * Security + CORS headers for every response.
 */
function securityHeaders(): Record<string, string> {
	return {
		'Access-Control-Allow-Origin': '*',
		'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
		'Access-Control-Allow-Headers': 'Content-Type, X-API-Key',
		'Access-Control-Max-Age': '86400',
		'X-Content-Type-Options': 'nosniff',
		'X-Frame-Options': 'DENY',
		'Referrer-Policy': 'no-referrer',
		'Permissions-Policy': 'camera=(), geolocation=(), microphone=()',
		'Cache-Control': 'no-store',
	};
}

/**
 * Helper function to create JSON responses with security headers
 */
function jsonResponse(data: OCRResponse, status: number, headers: Record<string, string>): Response {
	return new Response(JSON.stringify(data), {
		status,
		headers: {
			'Content-Type': 'application/json',
			...headers,
		},
	});
}

/**
 * Clean up OCR text by removing model artifacts
 */
function cleanOCRText(text: string): string {
	// Remove common LLM response patterns
	text = text.replace(/^(The text (?:visible )?in (?:the|this) image (?:is|says|reads):?\s*)/i, '');
	text = text.replace(/^(I (?:can )?see the following text:?\s*)/i, '');
	text = text.replace(/^((?:Here is |Here's )?the text (?:from|in) the image:?\s*)/i, '');
	
	// Remove quotes if the entire text is wrapped in them
	text = text.replace(/^["'](.+)["']$/s, '$1');
	
	// Remove markdown code fences if the model wrapped the text in them
	text = text.replace(/^\s*```[a-z]*\s*\n?/i, '');
	text = text.replace(/\s*```\s*$/, '');

	// Strip stray markdown formatting artifacts (bold, headings, list bullets)
	text = text.replace(/(\*\*|__|`)/g, '');
	text = text.replace(/^#{1,6}\s*/gm, '');
	text = text.replace(/^\s*[-*]\s+/gm, '');
	text = text.replace(/^\s{0,3}\d+\.\s+/gm, '');

	// Trim whitespace
	text = text.trim();
	
	return text;
}

/**
 * Parse individual words from extracted text
 */
function parseWordsFromText(text: string): Array<{ text: string; confidence: number }> {
	if (!text || text === 'NO TEXT FOUND') {
		return [];
	}

	// Split text into words and create word objects
	const words = text.split(/\s+/).filter(word => word.length > 0);
	
	return words.map(word => ({
		text: word,
		confidence: 0.85, // Estimated confidence (Workers AI doesn't provide per-word confidence)
	}));
}

function getDocsHTML(): string {
	return docsHTML;
}
