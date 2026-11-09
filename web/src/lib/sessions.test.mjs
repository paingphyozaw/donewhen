// Run with: npm test
import test from 'node:test';
import assert from 'node:assert/strict';
import {
	fmtInt,
	fmtCost,
	rangeBounds,
	fillDays,
	withShare,
	shortProject,
	totalsOf,
	niceScale,
	barPath,
	sessionsLine,
	modelLabel,
	parseRange,
	ISSUE_NONE
} from './sessions.js';

test('fmtInt writes the full number with thousands separators', () => {
	assert.equal(fmtInt(0), '0');
	assert.equal(fmtInt(950), '950');
	assert.equal(fmtInt(1234567), '1,234,567');
	assert.equal(fmtInt(1000), '1,000');
	assert.equal(fmtInt(12345678901), '12,345,678,901');
	assert.equal(fmtInt(undefined), '0');
	assert.equal(fmtInt(1234.6), '1,235');
});

test('fmtCost has two decimals and separators', () => {
	assert.equal(fmtCost(12.4), '$12.40');
	assert.equal(fmtCost(0), '$0.00');
	assert.equal(fmtCost(1234.5), '$1,234.50');
	assert.equal(fmtCost(0.004), '$0.00');
	assert.equal(fmtCost(null), '$0.00');
	assert.equal(fmtCost(999.999), '$1,000.00');
});

test('rangeBounds uses UTC days, inclusive of today', () => {
	const now = new Date('2026-11-09T23:30:00Z');
	assert.deepEqual(rangeBounds('today', now), { from: '2026-11-09', to: '2026-11-09' });
	assert.deepEqual(rangeBounds('7d', now), { from: '2026-11-03', to: '2026-11-09' });
	assert.deepEqual(rangeBounds('30d', now), { from: '2026-10-11', to: '2026-11-09' });
	assert.deepEqual(rangeBounds('month', now), { from: '2026-11-01', to: '2026-11-09' });
	assert.deepEqual(rangeBounds('all', now), { from: '', to: '' });
	assert.deepEqual(rangeBounds('custom', now, '2026-10-01', '2026-10-05'), { from: '2026-10-01', to: '2026-10-05' });
});

test('rangeBounds crosses a year', () => {
	assert.deepEqual(rangeBounds('7d', new Date('2027-01-02T00:00:00Z')), { from: '2026-12-27', to: '2027-01-02' });
});

test('parseRange accepts known names only', () => {
	assert.equal(parseRange('30d'), '30d');
	assert.equal(parseRange('nope'), '');
	assert.equal(parseRange(null), '');
});

test('fillDays adds zero rows for days without sessions', () => {
	const rows = [
		{ key: '2026-11-03', sessions: 2, tokens: 10, costUsd: 1 },
		{ key: '2026-11-05', sessions: 1, tokens: 5, costUsd: 0.5 }
	];
	const out = fillDays(rows, '2026-11-03', '2026-11-06');
	assert.deepEqual(out.map((r) => r.key), ['2026-11-03', '2026-11-04', '2026-11-05', '2026-11-06']);
	assert.deepEqual(out[1], { key: '2026-11-04', sessions: 0, tokens: 0, costUsd: 0 });
	assert.equal(out[2].costUsd, 0.5);
});

test('fillDays with no bounds spans the first to the last row; empty stays empty', () => {
	const rows = [
		{ key: '2026-11-03', sessions: 1, tokens: 1, costUsd: 1 },
		{ key: '2026-11-05', sessions: 1, tokens: 1, costUsd: 1 }
	];
	assert.equal(fillDays(rows, '', '').length, 3);
	assert.deepEqual(fillDays([], '', ''), []);
	assert.equal(fillDays([], '2026-11-03', '2026-11-04').length, 2);
});

test('withShare sorts by cost and gives each row its share of the total cost', () => {
	const out = withShare([
		{ key: 'a', sessions: 1, tokens: 1, costUsd: 1 },
		{ key: 'b', sessions: 1, tokens: 1, costUsd: 3 }
	]);
	assert.deepEqual(out.map((r) => [r.key, r.share]), [['b', 75], ['a', 25]]);
	assert.equal(withShare([{ key: 'a', sessions: 1, tokens: 0, costUsd: 0 }])[0].share, 0);
});

test('shortProject keeps the last folder name', () => {
	assert.equal(shortProject('/Users/me/Documents/Projects/8com-suite'), '8com-suite');
	assert.equal(shortProject('/a/b/'), 'b');
	assert.equal(shortProject('C:\\work\\app'), 'app');
	assert.equal(shortProject(''), 'No project');
});

test('totalsOf counts distinct sessions from the project rows, not the day rows', () => {
	// one session that spans three days shows as 1 in each day row
	const days = [
		{ key: 'x', sessions: 1, tokens: 100, costUsd: 3 },
		{ key: 'y', sessions: 1, tokens: 50, costUsd: 1 },
		{ key: 'z', sessions: 1, tokens: 50, costUsd: 2 }
	];
	const projects = [{ key: '/p', sessions: 1, tokens: 200, costUsd: 6 }];
	assert.deepEqual(totalsOf(days, projects), { sessions: 1, tokens: 200, costUsd: 6, avgCostUsd: 6 });
	assert.deepEqual(totalsOf([], []), { sessions: 0, tokens: 0, costUsd: 0, avgCostUsd: 0 });
});

test('niceScale picks round axis steps that cover the max', () => {
	assert.deepEqual(niceScale(0), { max: 1, step: 0.25, ticks: [0, 0.25, 0.5, 0.75, 1] });
	const s = niceScale(12.4);
	assert.ok(s.max >= 12.4);
	assert.equal(s.ticks[0], 0);
	assert.equal(s.ticks.at(-1), s.max);
	assert.deepEqual(niceScale(100).ticks, [0, 25, 50, 75, 100]);
});

test('barPath rounds the top corners by up to 4px', () => {
	const p = barPath(10, 20, 8, 30);
	assert.match(p, /^M10,50 V24 A4,4 0 0 1 14,20 H14 A4,4 0 0 1 18,24 V50 Z$/);
	// a very short bar shrinks the radius instead of overshooting
	assert.match(barPath(0, 98, 8, 2), /A2,2 /);
});

test('sessionsLine is empty without sessions, else count, tokens, cost', () => {
	assert.equal(sessionsLine(null), '');
	assert.equal(sessionsLine({ sessions: 0, tokens: 0, costUsd: 0 }), '');
	assert.equal(
		sessionsLine({ sessions: 4, tokens: 1234567, costUsd: 18.2 }),
		'Sessions: 4 · 1,234,567 tokens · $18.20'
	);
	assert.equal(sessionsLine({ sessions: 1, tokens: 1, costUsd: 0 }), 'Sessions: 1 · 1 tokens · $0.00');
});

test('modelLabel drops the claude- prefix', () => {
	assert.equal(modelLabel('claude-sonnet-4-5'), 'sonnet-4-5');
	assert.equal(modelLabel(''), 'unknown');
});

test('the empty issue key is shown as No ticket', () => {
	assert.equal(ISSUE_NONE, 'No ticket');
});
