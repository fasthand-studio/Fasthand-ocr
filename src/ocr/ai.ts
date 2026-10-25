import { MODEL, OCR_PROMPT } from '../config';
import type { Env } from '../types';

/**
 * Runs the vision model and returns the raw text it produced, before cleanup.
 */
export async function runOCR(env: Env, imageBuffer: Uint8Array): Promise<string> {
	const visionResponse = await env.AI.run(MODEL, {
		image: Array.from(imageBuffer),
		prompt: OCR_PROMPT,
		max_tokens: 4096,
	});

	if (visionResponse && typeof visionResponse === 'object') {
		if ('description' in visionResponse && typeof visionResponse.description === 'string') {
			return visionResponse.description;
		}
		if ('response' in visionResponse && typeof visionResponse.response === 'string') {
			return visionResponse.response;
		}
		return JSON.stringify(visionResponse);
	}
	if (typeof visionResponse === 'string') {
		return visionResponse;
	}
	return '';
}
