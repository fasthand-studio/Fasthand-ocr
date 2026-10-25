import { MAX_IMAGE_BYTES } from '../config';
import { jsonResponse } from '../http/json';
import { isValidBase64 } from '../ocr/base64';
import { runOCR } from '../ocr/ai';
import { cleanOCRText, parseWordsFromText } from '../ocr/text';
import type { Env, OCRRequest } from '../types';

/**
 * Parses the OCR request, enforces the image checks, runs the model and
 * returns the transcribed text. The caller has already authenticated and
 * bounded the request body.
 */
export async function handleOcr(
	request: Request,
	env: Env,
	baseHeaders: Record<string, string>,
): Promise<Response> {
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

	const extractedText = cleanOCRText(await runOCR(env, imageBuffer));

	return jsonResponse({
		success: true,
		text: extractedText,
		words: parseWordsFromText(extractedText),
	}, 200, baseHeaders);
}
