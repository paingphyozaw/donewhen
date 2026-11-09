// Ticket titles and states for the Sessions views (BIZ-282). The workspace's
// issues are already in the store; a key it does not hold (an archived or
// older ticket) is fetched once from the issues API. A key that is not found
// (another workspace's ticket) simply has no title. The issues API cannot
// filter by several keys (only state, epic, label, parent and a text query),
// so a missing key is fetched by itself, at most MAX_FETCH per call.
import { get } from 'svelte/store';
import { allIssues, states } from '$lib/store.js';
import { api } from '$lib/api.js';

const fetched = new Map(); // key → issue or null, for keys not in the store
const MAX_FETCH = 40; // per call; the rest show their key only

// issueInfo returns Map<key, { title, state: { name, category, color } | null }>.
export async function issueInfo(keys) {
	const byKey = new Map(get(allIssues).map((i) => [i.key, i]));
	const missing = keys.filter((k) => k && !byKey.has(k) && !fetched.has(k)).slice(0, MAX_FETCH);
	await Promise.all(
		missing.map(async (k) => {
			try {
				fetched.set(k, await api.issue(k));
			} catch {
				fetched.set(k, null);
			}
		})
	);
	const sts = new Map(get(states).map((s) => [s.id, s]));
	const out = new Map();
	for (const k of keys) {
		const i = byKey.get(k) || fetched.get(k);
		if (!i) continue;
		const s = sts.get(i.stateId);
		out.set(k, { title: i.title, state: s ? { name: s.name, category: s.category, color: s.color } : null });
	}
	return out;
}
