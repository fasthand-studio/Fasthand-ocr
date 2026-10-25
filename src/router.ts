import { verifyApiKey } from './auth';
import { MAX_BODY_BYTES } from './config';
import { getDocsHTML } from './docs';
import { handleOcr } from './handlers/ocr';
import { securityHeaders } from './http/headers';
import { jsonResponse } from './http/json';
import type { Env } from './types';

export async function route(request: Request, env: Env): Promise<Response> {
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
		return await handleOcr(request, env, baseHeaders);
	} catch (error) {
		console.error('OCR Error:', error);
		return jsonResponse({
			success: false,
			error: error instanceof Error ? error.message : 'Unknown error occurred',
		}, 500, baseHeaders);
	}
}
