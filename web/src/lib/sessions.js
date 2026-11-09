// Display and data-shaping helpers for the Sessions page. Everything here is
// pure: no DOM, no fetch. Dates are UTC, like the server's day grouping.

export const ISSUE_NONE = 'No ticket';

export const RANGES = [
	{ key: 'today', label: 'Today' },
	{ key: '7d', label: '7 days' },
	{ key: '30d', label: '30 days' },
	{ key: 'month', label: 'This month' },
	{ key: 'all', label: 'All time' },
	{ key: 'custom', label: 'Custom' }
];

// fmtInt writes a whole number in full with thousands separators: 1,234,567.
export function fmtInt(n) {
	const v = Math.round(Number(n) || 0);
	return String(v).replace(/\B(?=(\d{3})+(?!\d))/g, ',');
}

// fmtCost writes US dollars with two decimals and separators: $1,234.50.
export function fmtCost(n) {
	const cents = Math.round((Number(n) || 0) * 100);
	const whole = Math.floor(cents / 100);
	return '$' + fmtInt(whole) + '.' + String(cents % 100).padStart(2, '0');
}

const DAY = 86400000;
const iso = (ms) => new Date(ms).toISOString().slice(0, 10);
const startOfDay = (d) => Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate());

export function parseRange(v) {
	return RANGES.some((r) => r.key === v) ? v : '';
}

// rangeBounds turns a range name into inclusive UTC dates. 'all' has no bounds.
export function rangeBounds(range, now = new Date(), customFrom = '', customTo = '') {
	const today = startOfDay(now);
	switch (range) {
		case 'today':
			return { from: iso(today), to: iso(today) };
		case '7d':
			return { from: iso(today - 6 * DAY), to: iso(today) };
		case '30d':
			return { from: iso(today - 29 * DAY), to: iso(today) };
		case 'month':
			return { from: iso(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1)), to: iso(today) };
		case 'custom':
			return { from: customFrom || '', to: customTo || '' };
		default:
			return { from: '', to: '' };
	}
}

// fillDays makes the chart's series continuous: one row per UTC day from..to
// (or first..last row when a bound is missing), zeros where nothing happened.
export function fillDays(rows, from, to) {
	const byDay = new Map(rows.map((r) => [r.key, r]));
	const keys = rows.map((r) => r.key).sort();
	const start = from || keys[0];
	const end = to || keys.at(-1);
	if (!start || !end || start > end) return [];
	const out = [];
	for (let t = Date.parse(start); t <= Date.parse(end); t += DAY) {
		const key = iso(t);
		out.push(byDay.get(key) || { key, sessions: 0, tokens: 0, costUsd: 0 });
	}
	return out;
}

// withShare sorts breakdown rows by cost and adds each row's share (%) of the total cost.
export function withShare(rows) {
	const total = rows.reduce((s, r) => s + r.costUsd, 0);
	return [...rows]
		.sort((a, b) => b.costUsd - a.costUsd || b.tokens - a.tokens)
		.map((r) => ({ ...r, share: total > 0 ? (r.costUsd / total) * 100 : 0 }));
}

export function shortProject(path) {
	const parts = String(path || '').split(/[\\/]/).filter(Boolean);
	return parts.at(-1) || 'No project';
}

export function modelLabel(model) {
	return model ? model.replace(/^claude-/, '') : 'unknown';
}

// totalsOf sums day rows (a session belongs to exactly one day, so nothing is counted twice).
export function totalsOf(dayRows) {
	const t = dayRows.reduce(
		(s, r) => ({ sessions: s.sessions + r.sessions, tokens: s.tokens + r.tokens, costUsd: s.costUsd + r.costUsd }),
		{ sessions: 0, tokens: 0, costUsd: 0 }
	);
	return { ...t, avgCostUsd: t.sessions ? t.costUsd / t.sessions : 0 };
}

// niceScale picks a round axis for 0..max: four steps, 1/2/2.5/5 x 10^n.
export function niceScale(max) {
	if (!(max > 0)) return { max: 1, step: 0.25, ticks: [0, 0.25, 0.5, 0.75, 1] };
	const raw = max / 4;
	const mag = 10 ** Math.floor(Math.log10(raw));
	const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw - 1e-12);
	const top = Math.ceil(max / step - 1e-9) * step;
	const ticks = [];
	for (let v = 0; v <= top + step / 2; v += step) ticks.push(Math.round(v * 1e6) / 1e6);
	return { max: top, step, ticks };
}

// barPath draws a bar with a rounded top (radius up to 4px) and a square base.
export function barPath(x, y, w, h) {
	const r = Math.max(0, Math.min(4, w / 2, h));
	const x2 = x + w;
	return `M${x},${y + h} V${y + r} A${r},${r} 0 0 1 ${x + r},${y} H${x2 - r} A${r},${r} 0 0 1 ${x2},${y + r} V${y + h} Z`;
}

// sessionsLine is the one line on the issue page. Empty means: show nothing.
export function sessionsLine(row) {
	if (!row || !row.sessions) return '';
	return `Sessions: ${fmtInt(row.sessions)} · ${fmtInt(row.tokens)} tokens · ${fmtCost(row.costUsd)}`;
}

// query builds a query string from the set values only.
export function query(params) {
	const q = new URLSearchParams();
	for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== '') q.set(k, v);
	const s = q.toString();
	return s ? '?' + s : '';
}
