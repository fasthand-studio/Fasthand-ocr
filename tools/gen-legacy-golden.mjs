// Generates the golden fixture from the LEGACY JavaScript implementation, copied
// verbatim from src/index.ts, so the Go port can be diffed against real output
// rather than against a hand-written expectation.
import { writeFileSync } from 'node:fs';

// ---- verbatim from src/index.ts ----
function cleanOCRText(text) {
	text = text.replace(/^(The text (?:visible )?in (?:the|this) image (?:is|says|reads):?\s*)/i, '');
	text = text.replace(/^(I (?:can )?see the following text:?\s*)/i, '');
	text = text.replace(/^((?:Here is |Here's )?the text (?:from|in) the image:?\s*)/i, '');
	text = text.replace(/^["'](.+)["']$/s, '$1');
	text = text.replace(/^\s*```[a-z]*\s*\n?/i, '');
	text = text.replace(/\s*```\s*$/, '');
	text = text.replace(/(\*\*|__|`)/g, '');
	text = text.replace(/^#{1,6}\s*/gm, '');
	text = text.replace(/^\s*[-*]\s+/gm, '');
	text = text.replace(/^\s{0,3}\d+\.\s+/gm, '');
	text = text.trim();
	return text;
}

function parseWordsFromText(text) {
	if (!text || text === 'NO TEXT FOUND') {
		return [];
	}
	const words = text.split(/\s+/).filter((word) => word.length > 0);
	return words.map((word) => ({ text: word, confidence: 0.85 }));
}
// ---- end verbatim ----

const inputs = [
	'',
	'   \n\t ',
	'The text in the image is: Hello World',
	'The text visible in the image is Milk',
	'The text in this image reads: Bread',
	'The text in the image says: Eggs\nButter',
	'I can see the following text: Rice',
	'I see the following text: Beans',
	'Here is the text from the image:\nSugar',
	"Here's the text in the image: Salt",
	'the text in the image is: lowercase preamble',
	'"Organic Milk"',
	"'Single quoted'",
	'"Multi\nline quoted"',
	'```\nOrganic Milk\n```',
	'```text\nOrganic Milk\n```',
	'Total **50** items',
	'file__name here',
	'price `1.20` today',
	'# National ID Card\nName: A',
	'### Heading three',
	'- Government\n- Bangladesh',
	'* one\n* two',
	'1. first\n2. second',
	'  3. indented number',
	'NO TEXT',
	'NO TEXT FOUND',
	'no text',
	'Government of Bangladesh',
	'Government of the People\'s Republic of Bangladesh\nNational ID Card\nName: Mohammed Rahman\nFather\'s Name: Abdul Karim\nDOB: 01-01-1990\nNID: 1990 1234 5678',
	'বাংলাদেশ সরকার\nজাতীয় পরিচয় পত্র',
	'العربية السعودية',
	'Total 100 * 200 = 20000',
	'**Bold** and __under__ and `code` mixed',
	'The only * bullet is mid line',
	'Line with trailing spaces   ',
	'Line\nwith\nnewlines',
	'"Text" in "quotes" inside',
	'```unclosed fence',
	'unclosed fence at start ```',
	'##',
	'- - -',
	'http://example.com/path?q=1',
	'Email: a@b.co.uk',
	'12% off **today**',
	'Version 1.2.3',
	'3.14 is a number',
	'Table | col | col',
	'Quote " inside \' single',
	'\u00a0non-breaking space\u00a0here',
	'\u200bzero width\u200bspace',
	'Full-width ｉｄｅｏｇｒａｐｈｉｃ ｔｅｘｔ',
	'   The text in the image is:  padded  ',
	'NOTEXT',
	'NO TEXT HERE',
	'The text is important',
	'_under_single_',
	'*** triple bold ***',
	'   \n```\n\nReal text\n\n```\n   ',
	'# Heading\n```\n**fenced bold**\n```',
];

const lines = inputs.map((input) => {
	const cleaned = cleanOCRText(input);
	return JSON.stringify({ input, cleaned, words: parseWordsFromText(cleaned) });
});

writeFileSync(process.argv[2], lines.join('\n') + '\n', 'utf8');
console.log(`wrote ${lines.length} golden cases to ${process.argv[2]}`);
