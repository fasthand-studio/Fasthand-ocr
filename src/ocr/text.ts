/**
 * Clean up OCR text by removing model artifacts
 */
export function cleanOCRText(text: string): string {
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
export function parseWordsFromText(text: string): Array<{ text: string; confidence: number }> {
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
