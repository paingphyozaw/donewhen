// Grouping for the Artifacts page: module (initiative) → area (the document's
// label in the exclusive `domain` group) → documents, overviews first.
// Pure functions, so the page stays thin and this file is unit-tested.

const RESEARCH_AREA = 'research & decisions';
const NO_MODULE = 'No module';
const NO_AREA = 'No area';

// domainGroupIdOf picks the area group: named `domain` AND exclusive, so a
// document can never hold two areas. '' when the workspace has none.
export function domainGroupIdOf(labelGroups = []) {
	return labelGroups.find((g) => g.exclusive === true && (g.name || '').trim().toLowerCase() === 'domain')?.id || '';
}

// indexFor builds the id lookups once, so grouping stays linear in documents.
export function indexFor({ projects = [], issues = [], initiatives = [] }) {
	return {
		epicById: new Map(projects.map((p) => [p.id, p])),
		issueById: new Map(issues.map((i) => [i.id, i])),
		iniById: new Map(initiatives.map((ini) => [ini.id, ini])),
		iniRank: new Map(initiatives.map((ini, i) => [ini.id, i]))
	};
}

// moduleIdOf returns the document's initiative id: its own, else its epic's,
// else its issue's epic's. '' = no module. Pass an index from indexFor, or the
// raw { projects, issues } lists.
export function moduleIdOf(doc, ctx) {
	if (doc.initiativeId) return doc.initiativeId;
	const { epicById, issueById } = ctx.epicById ? ctx : indexFor(ctx);
	const epicIni = (epicId) => epicById.get(epicId)?.initiativeId || '';
	if (doc.projectId) return epicIni(doc.projectId);
	if (doc.issueId) {
		const is = issueById.get(doc.issueId);
		return is?.projectId ? epicIni(is.projectId) : '';
	}
	return '';
}

// areaOf returns the document's label in the domain group, or null.
export function areaOf(doc, domainGroupId) {
	if (!domainGroupId) return null;
	return (doc.labels || []).find((l) => l.groupId === domainGroupId) || null;
}

// Overview first, then newest first.
function byOverviewThenNewest(a, b) {
	const ov = (d) => (d.type === 'overview' ? 0 : 1);
	return ov(a) - ov(b) || String(b.updatedAt || '').localeCompare(String(a.updatedAt || ''));
}

// groupDocs builds [{ id, key, name, count, overviews, areas: [{ id, key, name, count, docs }] }].
// Modules follow the initiative order (the sidebar order), unknown ones after,
// "No module" last. Areas are A–Z, "No area" last. A module-level overview
// (overview type with no area, or in "Research & decisions") is shown once, at
// the top of the module, and not again inside its area.
export function groupDocs(docs, { initiatives = [], projects = [], issues = [], domainGroupId = '' }) {
	const idx = indexFor({ projects, issues, initiatives });
	const mods = new Map();
	for (const d of docs) {
		const mid = moduleIdOf(d, idx);
		if (!mods.has(mid)) mods.set(mid, { overviews: [], areas: new Map(), count: 0 });
		const m = mods.get(mid);
		m.count++;
		const area = areaOf(d, domainGroupId);
		if (d.type === 'overview' && (!area || area.name.trim().toLowerCase() === RESEARCH_AREA)) {
			m.overviews.push(d);
			continue;
		}
		const aid = area?.id || '';
		if (!m.areas.has(aid)) m.areas.set(aid, { name: area?.name || NO_AREA, docs: [] });
		m.areas.get(aid).docs.push(d);
	}

	const rank = (id) => (id === '' ? Infinity : idx.iniRank.has(id) ? idx.iniRank.get(id) : initiatives.length);
	const nameOf = (id) => (id === '' ? NO_MODULE : idx.iniById.get(id)?.name || 'Project');

	return [...mods.entries()]
		.map(([id, m]) => ({
			id,
			key: `m:${id}`,
			name: nameOf(id),
			count: m.count,
			overviews: [...m.overviews].sort(byOverviewThenNewest),
			areas: [...m.areas.entries()]
				.map(([aid, a]) => ({ id: aid, key: `a:${id}:${aid}`, name: a.name, count: a.docs.length, docs: [...a.docs].sort(byOverviewThenNewest) }))
				.sort((a, b) => (a.id === '') - (b.id === '') || a.name.localeCompare(b.name))
		}))
		.sort((a, b) => rank(a.id) - rank(b.id) || a.name.localeCompare(b.name));
}

// matchesDoc applies the page's search box, type filter and "AI only" switch.
export function matchesDoc(doc, { query = '', type = '', aiOnly = false }) {
	const q = query.trim().toLowerCase();
	if (q && !(doc.title || '').toLowerCase().includes(q)) return false;
	if (type && doc.type !== type) return false;
	if (aiOnly && doc.author !== 'ai') return false;
	return true;
}

// isOpen: a group is open unless collapsed; a typed search opens every group.
export function isOpen(key, collapsed, query) {
	if ((query || '').trim()) return true;
	return !collapsed.has(key);
}

// groupKeys lists every module and area key of a grouping (for "Collapse all").
export function groupKeys(groups) {
	return groups.flatMap((m) => [m.key, ...m.areas.map((a) => a.key)]);
}

// ---- remembered per device and per workspace ----
const prefsKey = (wsId) => `donewhen.artifacts.prefs.${wsId}`;

export function readPrefs(storage, wsId) {
	const fallback = { mode: 'module', collapsed: new Set() };
	try {
		const v = JSON.parse(storage.getItem(prefsKey(wsId)));
		if (!v || typeof v !== 'object') return fallback;
		return {
			mode: v.mode === 'none' ? 'none' : 'module',
			collapsed: new Set(Array.isArray(v.collapsed) ? v.collapsed.filter((k) => typeof k === 'string') : [])
		};
	} catch {
		return fallback;
	}
}

export function writePrefs(storage, wsId, { mode, collapsed }) {
	try {
		storage.setItem(prefsKey(wsId), JSON.stringify({ mode, collapsed: [...collapsed] }));
	} catch {
		/* storage blocked: the choice holds for this page only */
	}
}
