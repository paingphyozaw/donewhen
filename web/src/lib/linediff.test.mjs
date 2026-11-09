import { test } from 'node:test';
import assert from 'node:assert/strict';
import { lineDiff, diffStats } from './linediff.js';

const show = (rows) => rows.map((r) => ({ same: ' ', add: '+', del: '-' })[r.type] + r.text);

test('identical text has only same rows', () => {
	assert.deepEqual(show(lineDiff('a\nb', 'a\nb')), [' a', ' b']);
});

test('a changed line is one deletion and one addition', () => {
	assert.deepEqual(show(lineDiff('# H\nold\nend', '# H\nnew\nend')), [' # H', '-old', '+new', ' end']);
});

test('inserted and removed lines in the middle keep the rest aligned', () => {
	const rows = lineDiff('a\nb\nc\nd\ne', 'a\nc\nx\nd\ne');
	assert.deepEqual(show(rows), [' a', '-b', ' c', '+x', ' d', ' e']);
	assert.deepEqual(diffStats(rows), { add: 1, del: 1 });
});

test('empty sides', () => {
	assert.deepEqual(show(lineDiff('', 'a\nb')), ['+a', '+b']);
	assert.deepEqual(show(lineDiff('a\nb', '')), ['-a', '-b']);
	assert.deepEqual(lineDiff('', ''), []);
});

test('a long note with one edit stays a one-line change', () => {
	const base = Array.from({ length: 400 }, (_, i) => 'line ' + i);
	const next = base.slice();
	next[200] = 'changed';
	assert.deepEqual(diffStats(lineDiff(base.join('\n'), next.join('\n'))), { add: 1, del: 1 });
});
