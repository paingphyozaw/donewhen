import { test } from 'node:test';
import assert from 'node:assert/strict';
import { countLines, countChars, missingSections, saveBlocker, sectionProblem, daysSince, outline } from './handoff.js';

const sections = ['## Now', '## In flight', '## Next steps', '## Waiting on owner', '## Rules and gotchas', '## Where things are'];
const fmt = { sections, maxLines: 150, maxChars: 12000 };
const tmpl = sections.join('\n\n') + '\n';

test('counts match the server: trailing newlines do not count', () => {
	assert.equal(countLines('a\nb\n\n'), 2);
	assert.equal(countLines('a'), 1);
	assert.equal(countChars('añ😀'), 3);
});

test('missing sections ignore case and spacing', () => {
	assert.deepEqual(missingSections(tmpl, sections), []);
	assert.deepEqual(missingSections(tmpl.replace('## Now', '## now  '), sections), []);
	assert.deepEqual(missingSections(tmpl.replace('## Waiting on owner\n\n', ''), sections), ['## Waiting on owner']);
	assert.equal(missingSections('# Old handoff\ntext', sections).length, 6);
});

test('saveBlocker names the first problem', () => {
	assert.equal(saveBlocker(tmpl, fmt), '');
	assert.match(saveBlocker('free text', fmt), /^Missing: ## Now/);
	assert.match(saveBlocker(tmpl + 'x\n'.repeat(180), fmt), /Too long: 191 lines/);
	assert.match(saveBlocker(tmpl + 'x'.repeat(12001), fmt), /characters/);
	assert.equal(saveBlocker('anything', null), '');
});

test('daysSince floors whole days', () => {
	const now = Date.parse('2026-11-09T12:00:00Z');
	assert.equal(daysSince('2026-11-05T11:00:00Z', now), 4);
	assert.equal(daysSince('2026-11-09T01:00:00Z', now), 0);
	assert.equal(daysSince('nope', now), 0);
});

test('outline gives each section with its offset', () => {
	const o = outline('## Now\nx\n## In flight\n');
	assert.deepEqual(o, [
		{ title: 'Now', line: 0, offset: 0 },
		{ title: 'In flight', line: 2, offset: 9 }
	]);
});

test('headings inside code fences do not count', () => {
	const fenced = tmpl.replace('## Now\n', '```\n## Now\n```\n').replace('## Now\n\n', '');
	assert.deepEqual(missingSections('```\n## Now\n```\n', sections).includes('## Now'), true);
	assert.equal(outline('~~~\n## Fake\n~~~\n## Real\n').length, 1);
	assert.equal(outline('~~~\n## Fake\n~~~\n## Real\n')[0].title, 'Real');
	assert.ok(fenced);
});

test('repeated and out-of-order headings are reported', () => {
	assert.equal(sectionProblem(tmpl, sections), '');
	assert.match(sectionProblem(tmpl + '\n## Now\n', sections), /^Repeated: ## Now/);
	const swapped = ['## In flight', '## Now', ...sections.slice(2)].join('\n\n');
	assert.match(sectionProblem(swapped, sections), /^Out of order/);
	assert.match(saveBlocker(swapped, fmt), /^Out of order/);
});
