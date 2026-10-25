/**
 * Timing-safe comparison of the provided API key against the stored secret.
 */
export async function verifyApiKey(request: Request, secret: string): Promise<boolean> {
	const provided = request.headers.get('X-API-Key');
	if (!provided) return false;

	const a = new TextEncoder().encode(provided);
	const b = new TextEncoder().encode(secret);
	if (a.byteLength !== b.byteLength) return false;
	return await crypto.subtle.timingSafeEqual(a, b);
}
