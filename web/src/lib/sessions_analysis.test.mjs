// BIZ-282: the analysis helpers of the Sessions views. Run with: npm test
import test from 'node:test';
import assert from 'node:assert/strict';
import {
	KINDS,
	overviewTotals,
	sortAgentTypes,
	insights,
	ticketRows,
	oneLineKeys,
	filtersFromSearch,
	filtersSearch,
	fillDays,
	kindOf,
	COORDINATOR
} from './sessions.js';

const kinds = (input, output, cacheRead, cacheWrite) => [
	{ key: 'input', tokens: 10, cacheReadTokens: 0, messages: 0, sessions: 1, costUsd: input },
	{ key: 'output', tokens: 10, cacheReadTokens: 0, messages: 0, sessions: 1, costUsd: output },
	{ key: 'cache_read', tokens: 10, cacheReadTokens: 0, messages: 0, sessions: 1, costUsd: cacheRead },
	{ key: 'cache_write', tokens: 10, cacheReadTokens: 0, messages: 0, sessions: 1, costUsd: cacheWrite }
];

test('KINDS has a fixed order and a colour token for each kind', () => {
	assert.deepEqual(
		KINDS.map((k) => k.key),
		['cache_read', 'cache_write', 'output', 'input']
	);
	for (const k of KINDS) assert.match(k.color, /^var\(--viz-\d\)$/);
	assert.equal(kindOf('output').label, 'Output');
});

test('overviewTotals: six tile values from the groups', () => {
	const days = [
		{ key: '2026-11-01', sessions: 2, messages: 10, tokens: 1000, cacheReadTokens: 800, costUsd: 6 },
		{ key: '2026-11-02', sessions: 1, messages: 30, tokens: 3000, cacheReadTokens: 2400, costUsd: 4 }
	];
	const projects = [{ key: '/a', sessions: 3, messages: 40, tokens: 4000, costUsd: 10 }];
	const types = [
		{ key: 'coordinator', runs: 3, costUsd: 5 },
		{ key: 'implementer', runs: 4, costUsd: 3 },
		{ key: 'reviewer', runs: 2, costUsd: 2 }
	];
	const t = overviewTotals(days, projects, kinds(1, 1, 7, 1), types);
	assert.equal(t.costUsd, 10);
	assert.equal(t.tokens, 4000);
	assert.equal(t.sessions, 3);
	assert.equal(t.runs, 6); // subagent runs only: the coordinator's runs are its sessions
	assert.equal(Math.round(t.cacheShare), 70);
	assert.equal(t.avgContext, 80); // 3,200 cache-read tokens / 40 messages
	const empty = overviewTotals([], [], [], []);
	assert.equal(empty.cacheShare, 0);
	assert.equal(empty.avgContext, 0);
});

test('sortAgentTypes puts the coordinator first, then by cost', () => {
	const rows = sortAgentTypes([
		{ key: 'reviewer', costUsd: 2 },
		{ key: 'coordinator', costUsd: 1 },
		{ key: 'implementer', costUsd: 5 }
	]);
	assert.deepEqual(
		rows.map((r) => r.key),
		['coordinator', 'implementer', 'reviewer']
	);
	assert.equal(COORDINATOR, 'coordinator');
});

test('insights: each rule shows only above its threshold', () => {
	const types = [
		{ key: 'coordinator', runs: 3, costUsd: 58 },
		{ key: 'implementer', runs: 4, costUsd: 27 },
		{ key: 'reviewer', runs: 4, costUsd: 11 },
		{ key: 'Explore', runs: 1, costUsd: 4 }
	];
	const out = insights({
		totals: { costUsd: 100, avgContext: 412000 },
		kinds: kinds(4, 10, 81, 5),
		types,
		tickets: [{ key: 'BIZ-9', costUsd: 40, coordinator: false }]
	});
	assert.deepEqual(out, [
		'Cache reads are 81% of cost: long sessions re-read their whole context on every message.',
		'The coordinator spent 58%; builders 27%; reviewers 11%; others 4%.',
		'Average context is 412,000 tokens per message: start a new session sooner.',
		'BIZ-9 alone took 40% of the cost ($40.00).',
		'Reviews cost 41% as much as builds ($11.00 against $27.00).'
	]);

	// Below every threshold: nothing to say.
	const quiet = insights({
		totals: { costUsd: 100, avgContext: 20000 },
		kinds: kinds(40, 30, 20, 10),
		types: [{ key: 'coordinator', runs: 1, costUsd: 100 }],
		tickets: [
			{ key: 'BIZ-1', costUsd: 10 },
			{ key: 'BIZ-2', costUsd: 10 }
		]
	});
	assert.deepEqual(quiet, []);
	assert.deepEqual(insights({ totals: { costUsd: 0 }, kinds: [], types: [], tickets: [] }), []);
	// Coordinator overhead and "no ticket" rows are not "a ticket took".
	assert.deepEqual(
		insights({
			totals: { costUsd: 10, avgContext: 0 },
			kinds: [],
			types: [],
			tickets: [
				{ key: 'BIZ-102', costUsd: 9, coordinator: true },
				{ key: '', costUsd: 1 }
			]
		}),
		[]
	);
});

test('ticketRows: build, review and other cost, runs, title and share', () => {
	const rows = ticketRows(
		[
			{
				key: 'BIZ-278',
				costUsd: 10,
				tokens: 100,
				costByType: { implementer: 6, reviewer: 3, tester: 1 },
				runsByType: { implementer: 2, reviewer: 1, tester: 1 },
				lastAt: '2026-11-08T10:00:00Z'
			},
			{ key: 'BIZ-102', coordinator: true, costUsd: 30, tokens: 300, costByType: { coordinator: 30 } },
			{ key: '', costUsd: 0, tokens: 1, costByType: {} }
		],
		new Map([['BIZ-278', { title: 'Sessions backend', state: { name: 'In Review' } }]])
	);
	assert.equal(rows[0].key, 'BIZ-102');
	assert.equal(rows[0].label, 'Coordinator (branch BIZ-102)');
	assert.equal(rows[0].other, 30);
	const b = rows.find((r) => r.key === 'BIZ-278');
	assert.equal(b.build, 6);
	assert.equal(b.review, 3);
	assert.equal(b.other, 1);
	assert.deepEqual(b.runs, { build: 2, review: 1, test: 1 });
	assert.equal(b.title, 'Sessions backend');
	assert.equal(Math.round(b.share), 25);
	assert.equal(rows.find((r) => r.key === '').label, 'No ticket');
});

test('oneLineKeys: first key plus a +N chip, all keys in the tooltip', () => {
	const keys = ['BIZ-101', 'BIZ-102', 'BIZ-103', 'BIZ-104', 'BIZ-105', 'BIZ-106', 'BIZ-107'];
	assert.deepEqual(oneLineKeys(keys), { first: 'BIZ-101', more: 6, title: keys.join(', ') });
	assert.deepEqual(oneLineKeys(['BIZ-1']), { first: 'BIZ-1', more: 0, title: 'BIZ-1' });
	assert.deepEqual(oneLineKeys([]), { first: '', more: 0, title: '' });
});

test('filters live in the URL and survive a round trip', () => {
	const f = filtersFromSearch('?range=30d&project=8com-suite&issue=biz-9');
	assert.deepEqual(f, { range: '30d', from: '', to: '', project: '8com-suite', issue: 'BIZ-9' });
	assert.equal(filtersSearch(f), '?range=30d&project=8com-suite&issue=BIZ-9');
	// Defaults stay out of the URL: 7 days, or all time with a ticket.
	assert.equal(filtersSearch(filtersFromSearch('')), '');
	assert.equal(filtersFromSearch('?issue=BIZ-1').range, 'all');
	assert.equal(filtersSearch({ range: 'custom', from: '2026-11-01', to: '2026-11-02', project: '', issue: '' }),
		'?range=custom&from=2026-11-01&to=2026-11-02');
	// Extra keys (a view's own filters) pass through.
	assert.equal(filtersSearch(filtersFromSearch(''), { type: 'reviewer' }), '?type=reviewer');
});

test('fillDays keeps the kinds of a day and fills empty days with none', () => {
	const rows = [{ key: '2026-11-02', sessions: 1, tokens: 1, costUsd: 1, kinds: [{ kind: 'input', costUsd: 1 }] }];
	const out = fillDays(rows, '2026-11-01', '2026-11-02');
	assert.deepEqual(out[0].kinds, []);
	assert.equal(out[1].kinds.length, 1);
});
