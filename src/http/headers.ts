/**
 * Security + CORS headers for every response.
 */
export function securityHeaders(): Record<string, string> {
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
