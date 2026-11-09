// A simple line diff (longest common subsequence) for the handoff page's
// "Compare with latest". Returns rows { type: 'same' | 'add' | 'del', text }
// in reading order. Common head and tail lines are trimmed first, so two
// versions of a long note that differ in a few places stay cheap.

export function lineDiff(oldText, newText) {
	const a = oldText === '' ? [] : oldText.split('\n');
	const b = newText === '' ? [] : newText.split('\n');
	let head = 0;
	while (head < a.length && head < b.length && a[head] === b[head]) head++;
	let tail = 0;
	while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++;
	const x = a.slice(head, a.length - tail);
	const y = b.slice(head, b.length - tail);

	const rows = a.slice(0, head).map((text) => ({ type: 'same', text }));
	rows.push(...middle(x, y));
	rows.push(...a.slice(a.length - tail).map((text) => ({ type: 'same', text })));
	return rows;
}

function middle(x, y) {
	if (!x.length) return y.map((text) => ({ type: 'add', text }));
	if (!y.length) return x.map((text) => ({ type: 'del', text }));
	// A pathological pair (both huge, nothing in common) is shown as one
	// removal and one addition instead of building a huge table.
	if (x.length * y.length > 4_000_000) {
		return [...x.map((text) => ({ type: 'del', text })), ...y.map((text) => ({ type: 'add', text }))];
	}
	const n = x.length;
	const m = y.length;
	const w = m + 1;
	const t = new Uint32Array((n + 1) * w); // t[i][j] = LCS length of x[i:] and y[j:]
	for (let i = n - 1; i >= 0; i--) {
		for (let j = m - 1; j >= 0; j--) {
			t[i * w + j] = x[i] === y[j] ? t[(i + 1) * w + j + 1] + 1 : Math.max(t[(i + 1) * w + j], t[i * w + j + 1]);
		}
	}
	const out = [];
	let i = 0;
	let j = 0;
	while (i < n && j < m) {
		if (x[i] === y[j]) {
			out.push({ type: 'same', text: x[i] });
			i++;
			j++;
		} else if (t[(i + 1) * w + j] >= t[i * w + j + 1]) {
			out.push({ type: 'del', text: x[i++] });
		} else {
			out.push({ type: 'add', text: y[j++] });
		}
	}
	while (i < n) out.push({ type: 'del', text: x[i++] });
	while (j < m) out.push({ type: 'add', text: y[j++] });
	return out;
}

// diffStats counts the added and removed lines of a diff.
export function diffStats(rows) {
	let add = 0;
	let del = 0;
	for (const r of rows) {
		if (r.type === 'add') add++;
		else if (r.type === 'del') del++;
	}
	return { add, del };
}
