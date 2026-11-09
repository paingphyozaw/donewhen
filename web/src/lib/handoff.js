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

// The "## " headings in the body, normalised.
export function headingsIn(body) {
	return body
		.split('\n')
		.filter((l) => l.startsWith('## '))
		.map(norm);
}

// Required headings that are not in the body, as written in the template.
export function missingSections(body, sections) {
	const have = new Set(headingsIn(body));
	return sections.filter((h) => !have.has(norm(h)));
}

// Why a body cannot be saved, or '' when it can.
export function saveBlocker(body, fmt) {
	if (!fmt) return '';
	const missing = missingSections(body, fmt.sections);
	if (missing.length) return 'Missing: ' + missing.join(', ');
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

// Outline entries for a body: the required sections that exist, with the
// character offset of the heading line (for jumping inside the editor) and the
// line index (for jumping in the rendered text).
export function outline(body) {
	const out = [];
	let off = 0;
	body.split('\n').forEach((line, i) => {
		if (line.startsWith('## ')) out.push({ title: line.slice(3).trim(), line: i, offset: off });
		off += line.length + 1;
	});
	return out;
}
