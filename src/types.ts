export interface Env {
	AI: any;
	API_KEY?: string;
	SANCTIONED_DOMAINS?: string;
}

export interface OCRRequest {
	image: string; // Base64 encoded image
}

export interface OCRWord {
	text: string;
	confidence: number;
	boundingBox?: {
		x: number;
		y: number;
		width: number;
		height: number;
	};
}

export interface OCRResponse {
	success: boolean;
	text?: string;
	words?: OCRWord[];
	error?: string;
}
