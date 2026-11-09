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
		out.push(byDay.get(key) || { key, sessions: 0, tokens: 0, costUsd: 0, kinds: [] });
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

// totalsOf sums tokens and cost over the day rows. A session spans many days, so its count
// comes from the project rows instead: a session has exactly one project, so summing
// `sessions` over them counts each session once.
export function totalsOf(dayRows, projectRows = dayRows) {
	const t = dayRows.reduce(
		(s, r) => ({ tokens: s.tokens + r.tokens, costUsd: s.costUsd + r.costUsd }),
		{ tokens: 0, costUsd: 0 }
	);
	const sessions = projectRows.reduce((n, r) => n + r.sessions, 0);
	return { sessions, ...t, avgCostUsd: sessions ? t.costUsd / sessions : 0 };
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

// ---- BIZ-282: analysis views ----

// Token kinds in their fixed order (bottom of a stack first). Each has its own
// chart hue; the order never changes, so a kind keeps its colour everywhere.
export const KINDS = [
	{ key: 'cache_read', label: 'Cache read', color: 'var(--viz-1)' },
	{ key: 'cache_write', label: 'Cache write', color: 'var(--viz-2)' },
	{ key: 'output', label: 'Output', color: 'var(--viz-3)' },
	{ key: 'input', label: 'Input', color: 'var(--viz-4)' }
];
export const kindOf = (key) => KINDS.find((k) => k.key === key) || { key, label: key, color: 'var(--ink-3)' };

export const COORDINATOR = 'coordinator';

// The four views of the Sessions area: tabs under one filter header.
export const VIEWS = [
	{ key: 'overview', label: 'Overview', path: '/sessions' },
	{ key: 'tickets', label: 'Tickets', path: '/sessions/tickets' },
	{ key: 'agents', label: 'Agents', path: '/sessions/agents' },
	{ key: 'list', label: 'Sessions', path: '/sessions/list' }
];

const sum = (rows, f) => rows.reduce((s, r) => s + (Number(f(r)) || 0), 0);
const pct = (part, whole) => (whole > 0 ? (part / whole) * 100 : 0);

// overviewTotals: the six tiles. Sessions come from the project rows (a session
// has one project); runs are subagent runs (the coordinator's "runs" are its
// sessions); avgContext is cache-read tokens per message: how much context
// each message re-reads.
export function overviewTotals(days, projects, kinds, types) {
	const costUsd = sum(days, (r) => r.costUsd);
	const messages = sum(days, (r) => r.messages);
	const cacheRead = kinds.find((k) => k.key === 'cache_read');
	return {
		costUsd,
		tokens: sum(days, (r) => r.tokens),
		sessions: sum(projects, (r) => r.sessions),
		runs: sum(types.filter((t) => t.key !== COORDINATOR), (r) => r.runs),
		cacheShare: pct(cacheRead?.costUsd || 0, sum(kinds, (k) => k.costUsd)),
		avgContext: messages > 0 ? sum(days, (r) => r.cacheReadTokens) / messages : 0
	};
}

// sortAgentTypes: the coordinator first, then by cost.
export function sortAgentTypes(rows) {
	return [...rows].sort(
		(a, b) => (b.key === COORDINATOR) - (a.key === COORDINATOR) || b.costUsd - a.costUsd || a.key.localeCompare(b.key)
	);
}

// Insight thresholds: a rule says nothing below its threshold.
export const INSIGHT = {
	cacheShare: 50, // % of cost spent on cache reads
	avgContext: 100000, // cache-read tokens per message
	ticketShare: 25, // % of cost one ticket took
	reviewOfBuild: 30 // review cost as % of build cost
};

const typeWord = { coordinator: 'the coordinator', implementer: 'builders', reviewer: 'reviewers', tester: 'testers' };

// insights: up to five plain sentences about where the money went.
export function insights({ totals, kinds = [], types = [], tickets = [] }) {
	const out = [];
	const total = totals?.costUsd || 0;
	if (!(total > 0)) return out;

	const kindTotal = sum(kinds, (k) => k.costUsd);
	const cache = pct(kinds.find((k) => k.key === 'cache_read')?.costUsd || 0, kindTotal);
	if (cache >= INSIGHT.cacheShare) {
		out.push(`Cache reads are ${Math.round(cache)}% of cost: long sessions re-read their whole context on every message.`);
	}

	const typeTotal = sum(types, (t) => t.costUsd);
	if (types.length > 1 && types.some((t) => t.key === COORDINATOR) && typeTotal > 0) {
		const named = ['coordinator', 'implementer', 'reviewer', 'tester'];
		const parts = [];
		for (const k of named) {
			const row = types.find((t) => t.key === k);
			if (row && Math.round(pct(row.costUsd, typeTotal)) >= 1) parts.push([typeWord[k], pct(row.costUsd, typeTotal)]);
		}
		const others = pct(sum(types.filter((t) => !named.includes(t.key)), (t) => t.costUsd), typeTotal);
		if (Math.round(others) >= 1) parts.push(['others', others]);
		if (parts.length > 1) {
			const [first, ...rest] = parts;
			const head = first[0] === typeWord.coordinator ? `The coordinator spent ${Math.round(first[1])}%` : `${cap(first[0])} spent ${Math.round(first[1])}%`;
			out.push([head, ...rest.map(([w, p]) => `${w} ${Math.round(p)}%`)].join('; ') + '.');
		}
	}

	if ((totals.avgContext || 0) >= INSIGHT.avgContext) {
		out.push(`Average context is ${fmtInt(totals.avgContext)} tokens per message: start a new session sooner.`);
	}

	const top = [...tickets].filter((t) => t.key && !t.coordinator).sort((a, b) => b.costUsd - a.costUsd)[0];
	if (top && pct(top.costUsd, total) >= INSIGHT.ticketShare) {
		out.push(`${top.key} alone took ${Math.round(pct(top.costUsd, total))}% of the cost (${fmtCost(top.costUsd)}).`);
	}

	const build = types.find((t) => t.key === 'implementer')?.costUsd || 0;
	const review = types.find((t) => t.key === 'reviewer')?.costUsd || 0;
	if (build > 0 && pct(review, build) >= INSIGHT.reviewOfBuild) {
		out.push(`Reviews cost ${Math.round(pct(review, build))}% as much as builds (${fmtCost(review)} against ${fmtCost(build)}).`);
	}
	return out.slice(0, 5);
}
const cap = (s) => s.charAt(0).toUpperCase() + s.slice(1);

// Agent types per Tickets column: builds, reviews, the rest.
const BUILD = 'implementer';
const REVIEW = 'reviewer';
const TEST = 'tester';

// ticketRows shapes the ticket group for the Tickets table: cost by column,
// runs, the issue's title and state, and each row's share of the cost.
// Coordinator rows are the hub coordinator's overhead on the branch it sat on.
export function ticketRows(rows, issues = new Map()) {
	const total = sum(rows, (r) => r.costUsd);
	return [...rows]
		.sort((a, b) => b.costUsd - a.costUsd || a.key.localeCompare(b.key))
		.map((r) => {
			const by = r.costByType || {};
			const runs = r.runsByType || {};
			const build = by[BUILD] || 0;
			const review = by[REVIEW] || 0;
			const issue = r.coordinator || !r.key ? null : issues.get(r.key) || null;
			return {
				...r,
				id: (r.coordinator ? 'coord:' : 'key:') + r.key,
				label: r.coordinator ? `Coordinator (branch ${r.key || 'without a ticket'})` : r.key || ISSUE_NONE,
				title: issue?.title || '',
				state: issue?.state || null,
				build,
				review,
				other: Math.max(0, r.costUsd - build - review),
				runs: { build: runs[BUILD] || 0, review: runs[REVIEW] || 0, test: runs[TEST] || 0 },
				share: pct(r.costUsd, total)
			};
		});
}

// oneLineKeys: a session's tickets on one line: the first key and "+N".
export function oneLineKeys(keys = []) {
	return { first: keys[0] || '', more: Math.max(0, keys.length - 1), title: keys.join(', ') };
}

// The shared filters of the four views, as the URL spells them. The default
// range is 7 days, or all time when a ticket is chosen.
export function filtersFromSearch(search) {
	const q = new URLSearchParams(search);
	const issue = (q.get('issue') || '').trim().toUpperCase();
	return {
		range: parseRange(q.get('range')) || (issue ? 'all' : '7d'),
		from: q.get('from') || '',
		to: q.get('to') || '',
		project: q.get('project') || '',
		issue
	};
}

// filtersSearch writes the filters (and a view's own extra keys) back.
export function filtersSearch(f, extra = {}) {
	const withDefaultRange = f.range !== (f.issue ? 'all' : '7d');
	return query({
		range: withDefaultRange ? f.range : '',
		from: f.range === 'custom' ? f.from : '',
		to: f.range === 'custom' ? f.to : '',
		project: f.project,
		issue: f.issue,
		...extra
	});
}
