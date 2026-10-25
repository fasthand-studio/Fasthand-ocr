import type { OCRResponse } from '../types';

/**
 * Helper function to create JSON responses with security headers
 */
export function jsonResponse(data: OCRResponse, status: number, headers: Record<string, string>): Response {
	return new Response(JSON.stringify(data), {
		status,
		headers: {
			'Content-Type': 'application/json',
			...headers,
		},
	});
}
