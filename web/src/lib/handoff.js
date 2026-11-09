// Handoff structure helpers (BIZ-283). The sections, limits and rules come from
// the server (GET /api/handoff/format); nothing here hard-codes them. The line
// and character counting mirrors the server's, so the page and the save agree.

const norm = (s) => s.trim().toLowerCase();

export function countLines(body) {
	return body.replace(/\n+$/, '').split('\n').length;
}
export function countChars(body) {
	return Array.from(body).length;
}

// Lines outside code fences, with their index and character offset.
function* plainLines(body) {
	let fence = '';
	let off = 0;
	const lines = body.split('\n');
	for (let i = 0; i < lines.length; i++) {
		const line = lines[i];
		const t = line.replace(/^ +/, '');
		const m = t.startsWith('```') ? '`' : t.startsWith('~~~') ? '~' : '';
		if (m && (!fence || fence === m)) fence = fence ? '' : m;
		else if (!fence) yield { line, i, off };
		off += line.length + 1;
	}
}

// The "## " headings in the body, normalised, outside code fences.
export function headingsIn(body) {
	const out = [];
	for (const { line } of plainLines(body)) if (line.startsWith('## ')) out.push(norm(line));
	return out;
}

// Required headings that are not in the body, as written in the template.
export function missingSections(body, sections) {
	const have = new Set(headingsIn(body));
	return sections.filter((h) => !have.has(norm(h)));
}

// Why the six headings are wrong (repeated or out of order), or ''.
export function sectionProblem(body, sections) {
	const heads = headingsIn(body);
	const req = sections.map(norm);
	const dup = sections.filter((h) => heads.filter((x) => x === norm(h)).length > 1);
	if (dup.length) return 'Repeated: ' + dup.join(', ');
	const seen = heads.filter((h) => req.includes(h));
	if (seen.join('|') !== req.join('|')) return 'Out of order. Use: ' + sections.join(', ');
	return '';
}

// Why a body cannot be saved, or '' when it can.
export function saveBlocker(body, fmt) {
	if (!fmt) return '';
	const missing = missingSections(body, fmt.sections);
	if (missing.length) return 'Missing: ' + missing.join(', ');
	const bad = sectionProblem(body, fmt.sections);
	if (bad) return bad;
	const lines = countLines(body);
	if (lines > fmt.maxLines) return `Too long: ${lines} lines, the limit is ${fmt.maxLines}`;
	const chars = countChars(body);
	if (chars > fmt.maxChars) return `Too long: ${chars} characters, the limit is ${fmt.maxChars}`;
	return '';
}

// Whole days since an ISO time (floored); 0 for a bad value.
export function daysSince(iso, now = Date.now()) {
	const t = new Date(iso).getTime();
	if (!Number.isFinite(t)) return 0;
	return Math.max(0, Math.floor((now - t) / 86400000));
}

// Outline entries for a body: each "## " heading outside code fences, with
// its line index and the character offset of the heading line.
export function outline(body) {
	const out = [];
	for (const { line, i, off } of plainLines(body)) {
		if (line.startsWith('## ')) out.push({ title: line.slice(3).trim(), line: i, offset: off });
	}
	return out;
}
