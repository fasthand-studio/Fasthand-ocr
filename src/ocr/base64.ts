/**
 * Validate a string as strict base64 (no whitespace, correct padding).
 */
export function isValidBase64(value: string): boolean {
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
