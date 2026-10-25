/**
 * Cloudflare Worker for Real-time OCR using Workers AI
 * This worker receives camera frames and returns extracted text using OCR
 */

import { route } from './router';
import type { Env } from './types';

export default {
	async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
		return route(request, env);
	},
};
