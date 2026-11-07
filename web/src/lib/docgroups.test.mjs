import { test } from 'node:test';
import assert from 'node:assert/strict';
import { moduleIdOf, areaOf, groupDocs, matchesDoc, isOpen, groupKeys, readPrefs, writePrefs } from './docgroups.js';

const initiatives = [
	{ id: 'ini-pos', name: 'POS' },
	{ id: 'ini-fin', name: 'Finance' }
];
const projects = [
	{ id: 'ep-backend', name: 'POS backend', initiativeId: 'ini-pos' },
	{ id: 'ep-core', name: 'Core accounting', initiativeId: 'ini-fin' }
];
const issues = [{ id: 'is-245', key: 'BIZ-245', projectId: 'ep-backend' }];
const DOMAIN = 'g-domain';
const lb = (name, groupId = DOMAIN) => ({ id: 'l-' + name, groupId, name, color: '#000' });
const ctx = { initiatives, projects, issues, domainGroupId: DOMAIN };

const doc = (id, extra = {}) => ({
	id,
	title: id,
	type: 'change',
	author: 'ai',
	initiativeId: null,
	projectId: null,
	issueId: null,
	labels: [],
	updatedAt: '2026-11-01T00:00:00Z',
	...extra
});

test('module comes from the document, else its epic, else its issue epic', () => {
	assert.equal(moduleIdOf(doc('a', { initiativeId: 'ini-fin' }), ctx), 'ini-fin');
	assert.equal(moduleIdOf(doc('b', { projectId: 'ep-core' }), ctx), 'ini-fin');
	assert.equal(moduleIdOf(doc('c', { issueId: 'is-245' }), ctx), 'ini-pos');
	assert.equal(moduleIdOf(doc('d', { issueId: 'unknown' }), ctx), '');
	assert.equal(moduleIdOf(doc('e'), ctx), '');
});

test('area is the label in the domain group only', () => {
	assert.equal(areaOf(doc('a', { labels: [lb('docs', 'g-type'), lb('Accounting')] }), DOMAIN).name, 'Accounting');
	assert.equal(areaOf(doc('b', { labels: [lb('docs', 'g-type')] }), DOMAIN), null);
	assert.equal(areaOf(doc('c', { labels: [lb('Accounting')] }), ''), null);
});

test('issue → epic → initiative lands under POS › Purchase & stock', () => {
	const g = groupDocs([doc('x', { issueId: 'is-245', labels: [lb('Purchase & stock')] })], ctx);
	assert.equal(g.length, 1);
	assert.equal(g[0].name, 'POS');
	assert.equal(g[0].count, 1);
	assert.equal(g[0].areas[0].name, 'Purchase & stock');
	assert.equal(g[0].areas[0].count, 1);
	assert.deepEqual(g[0].areas[0].docs.map((d) => d.id), ['x']);
});

test('no domain label → No area, placed last; areas A–Z', () => {
	const g = groupDocs(
		[
			doc('n', { initiativeId: 'ini-pos' }),
			doc('p', { initiativeId: 'ini-pos', labels: [lb('Purchase & stock')] }),
			doc('a', { initiativeId: 'ini-pos', labels: [lb('Accounting')] })
		],
		ctx
	);
	assert.deepEqual(g[0].areas.map((a) => a.name), ['Accounting', 'Purchase & stock', 'No area']);
	assert.deepEqual(g[0].areas[2].docs.map((d) => d.id), ['n']);
});

test('modules follow initiative order, No module last', () => {
	const g = groupDocs(
		[doc('none'), doc('fin', { initiativeId: 'ini-fin' }), doc('pos', { initiativeId: 'ini-pos' })],
		ctx
	);
	assert.deepEqual(g.map((m) => m.name), ['POS', 'Finance', 'No module']);
	assert.equal(g[2].id, '');
});

test('overview first in its area, then newest first', () => {
	const area = [lb('Accounting')];
	const g = groupDocs(
		[
			doc('old', { initiativeId: 'ini-fin', labels: area, updatedAt: '2026-10-01T00:00:00Z' }),
			doc('new', { initiativeId: 'ini-fin', labels: area, updatedAt: '2026-11-05T00:00:00Z' }),
			doc('ov', { initiativeId: 'ini-fin', labels: area, type: 'overview', updatedAt: '2026-09-01T00:00:00Z' })
		],
		ctx
	);
	assert.deepEqual(g[0].areas[0].docs.map((d) => d.id), ['ov', 'new', 'old']);
	assert.deepEqual(g[0].overviews, []);
});

test('module-level overviews (no area, or Research & decisions) sit at the top of the module', () => {
	const g = groupDocs(
		[
			doc('plain', { initiativeId: 'ini-pos' }),
			doc('ov-none', { initiativeId: 'ini-pos', type: 'overview' }),
			doc('ov-rd', { initiativeId: 'ini-pos', type: 'overview', labels: [lb('Research & decisions')] }),
			doc('ov-acc', { initiativeId: 'ini-pos', type: 'overview', labels: [lb('Accounting')] })
		],
		ctx
	);
	const m = g[0];
	assert.deepEqual(m.overviews.map((d) => d.id).sort(), ['ov-none', 'ov-rd']);
	assert.equal(m.count, 4);
	// not listed twice: the area keeps only the rest
	assert.deepEqual(m.areas.map((a) => a.name), ['Accounting', 'No area']);
	assert.deepEqual(m.areas[0].docs.map((d) => d.id), ['ov-acc']);
	assert.deepEqual(m.areas[1].docs.map((d) => d.id), ['plain']);
});

test('search matches the title, case-insensitive; type and AI filters still apply', () => {
	const d = doc('t', { title: 'Commercial TAX setup', type: 'decision', author: 'human' });
	assert.equal(matchesDoc(d, { query: 'tax' }), true);
	assert.equal(matchesDoc(d, { query: '  Tax ' }), true);
	assert.equal(matchesDoc(d, { query: 'stock' }), false);
	assert.equal(matchesDoc(d, { type: 'change' }), false);
	assert.equal(matchesDoc(d, { aiOnly: true }), false);
	assert.equal(matchesDoc(d, {}), true);
});

test('a search shows only matching docs, groups without matches are hidden', () => {
	const list = [
		doc('tax', { title: 'Tax rules', initiativeId: 'ini-fin' }),
		doc('imei', { title: 'IMEI life', initiativeId: 'ini-pos' })
	].filter((d) => matchesDoc(d, { query: 'tax' }));
	const g = groupDocs(list, ctx);
	assert.deepEqual(g.map((m) => m.name), ['Finance']);
});

test('isOpen: collapsed keys close a group, a typed search opens everything', () => {
	const collapsed = new Set(['m:ini-pos', 'a:ini-pos:l-Accounting']);
	assert.equal(isOpen('m:ini-pos', collapsed, ''), false);
	assert.equal(isOpen('m:ini-fin', collapsed, ''), true);
	assert.equal(isOpen('a:ini-pos:l-Accounting', collapsed, '   '), false);
	assert.equal(isOpen('m:ini-pos', collapsed, 'tax'), true);
	assert.equal(isOpen('a:ini-pos:l-Accounting', collapsed, 'tax'), true);
});

test('groupKeys lists every module and area key', () => {
	const g = groupDocs([doc('a', { initiativeId: 'ini-pos', labels: [lb('Accounting')] }), doc('b')], ctx);
	assert.deepEqual(groupKeys(g).sort(), ['a::', 'a:ini-pos:l-Accounting', 'm:', 'm:ini-pos'].sort());
});

test('prefs round-trip per workspace and fall back when storage fails', () => {
	const mem = new Map();
	const store = { getItem: (k) => mem.get(k) ?? null, setItem: (k, v) => mem.set(k, String(v)) };
	writePrefs(store, 'ws1', { mode: 'none', collapsed: new Set(['m:ini-pos']) });
	const p = readPrefs(store, 'ws1');
	assert.equal(p.mode, 'none');
	assert.deepEqual([...p.collapsed], ['m:ini-pos']);
	// another workspace is untouched
	const q = readPrefs(store, 'ws2');
	assert.equal(q.mode, 'module');
	assert.equal(q.collapsed.size, 0);
	// broken storage → defaults, everything open; writing does not throw
	const bad = {
		getItem: () => {
			throw new Error('blocked');
		},
		setItem: () => {
			throw new Error('blocked');
		}
	};
	const r = readPrefs(bad, 'ws1');
	assert.equal(r.mode, 'module');
	assert.equal(r.collapsed.size, 0);
	assert.doesNotThrow(() => writePrefs(bad, 'ws1', { mode: 'none', collapsed: new Set() }));
	// junk JSON → defaults
	mem.set('donewhen.artifacts.prefs.ws3', '{not json');
	assert.equal(readPrefs(store, 'ws3').mode, 'module');
});
