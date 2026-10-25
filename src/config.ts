export const MAX_BODY_BYTES = 8 * 1024 * 1024; // 8 MB upload cap (protects against oversized payload DoS)
export const MAX_IMAGE_BYTES = 6 * 1024 * 1024; // ~6 MB decoded image cap

// LLaMA 3.2 Vision reads dense text far better than LLaVA
export const MODEL = '@cf/meta/llama-3.2-11b-vision-instruct';

export const OCR_PROMPT = 'TRANSCRIBE ALL TEXT VISIBLE IN THIS IMAGE, CHARACTER FOR CHARACTER, IN ITS ORIGINAL LAYOUT AND ORDER. Include every label, heading, name, number, line, and field value exactly as written, preserving case and special characters. Do NOT describe the image, do NOT summarize, do NOT add commentary or bullets. Output only the raw text. If no text is legible, output exactly: NO TEXT';
