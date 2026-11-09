<script>
	// Sessions area (BIZ-282): four views as tabs under one filter header.
	// The range, project and ticket filters live in the URL and are shared by
	// every view; a view's own filters (the Agents view's type) ride along.
	// The views read the filters from the "sessions" context.
	import { onMount, setContext } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api.js';
	import {
		RANGES,
		VIEWS,
		rangeBounds,
		shortProject,
		query,
		filtersFromSearch,
		filtersSearch
	} from '$lib/sessions.js';
	import PageHeader from '$components/PageHeader.svelte';
	import { X } from '@lucide/svelte';

	let { children } = $props();

	const FILTER_KEYS = ['range', 'from', 'to', 'project', 'issue'];

	let f = $state(filtersFromSearch($page.url.search));
	let lastSearch = $page.url.search;
	let projects = $state([]); // every project path seen: [{ key, label }]
	let ready = $state(false);

	// extras: the URL keys that are a view's own, kept when a shared filter changes.
	function extras(search) {
		const out = {};
		for (const [k, v] of new URLSearchParams(search)) if (!FILTER_KEYS.includes(k)) out[k] = v;
		return out;
	}
	function setFilters(patch) {
		f = { ...f, ...patch };
		const s = filtersSearch(f, extras($page.url.search));
		lastSearch = s;
		goto($page.url.pathname + s, { replaceState: true, keepFocus: true, noScroll: true });
	}
	// A link inside the area (a tab, a ticket row, the sidebar) sets the filters.
	$effect(() => {
		const s = $page.url.search;
		if (s !== lastSearch) {
			lastSearch = s;
			f = filtersFromSearch(s);
		}
	});

	onMount(async () => {
		try {
			const rows = (await api.sessionSummary(query({ group: 'project' }))) || [];
			projects = rows
				.map((r) => ({ key: r.key, label: shortProject(r.key) }))
				.sort((a, b) => a.label.localeCompare(b.label));
		} catch {
			/* no list of projects: the filter then only shows "All projects" */
		}
		ready = true;
	});

	// The URL may name a project by its short name (8com-suite); the API wants the path.
	const projectPath = $derived(projects.find((p) => p.key === f.project || p.label === f.project)?.key ?? f.project);
	const bounds = $derived(rangeBounds(f.range, new Date(), f.from, f.to));

	setContext('sessions', {
		get f() {
			return f;
		},
		get ready() {
			return ready;
		},
		get projectPath() {
			return projectPath;
		},
		get bounds() {
			return bounds;
		},
		// base: the API filters every view sends.
		get base() {
			return { from: bounds.from, to: bounds.to, project: projectPath, issue: f.issue };
		},
		setFilters,
		// href: a view's path with the shared filters (plus a patch and extras).
		href(path, patch = {}, extra = {}) {
			return path + filtersSearch({ ...f, ...patch }, extra);
		}
	});

	const active = $derived(
		VIEWS.find((v) => v.path !== '/sessions' && $page.url.pathname.startsWith(v.path))?.key || 'overview'
	);
</script>

<div class="pg">
	<PageHeader crumbs={[{ label: 'Sessions' }]} />
	<div class="pg-body">
		<div class="sx">
			<div class="filters">
				<h1 class="page-title">Sessions</h1>
				<div class="fctl">
					<label class="fl">
						<span class="rh">Range</span>
						<select class="select" value={f.range} onchange={(e) => setFilters({ range: e.currentTarget.value })}>
							{#each RANGES as r (r.key)}<option value={r.key}>{r.label}</option>{/each}
						</select>
					</label>
					{#if f.range === 'custom'}
						<label class="fl">
							<span class="rh">From</span>
							<input class="input" type="date" value={f.from} onchange={(e) => setFilters({ from: e.currentTarget.value })} />
						</label>
						<label class="fl">
							<span class="rh">To</span>
							<input class="input" type="date" value={f.to} onchange={(e) => setFilters({ to: e.currentTarget.value })} />
						</label>
					{/if}
					<label class="fl">
						<span class="rh">Project</span>
						<select class="select" value={projectPath} onchange={(e) => setFilters({ project: e.currentTarget.value })}>
							<option value="">All projects</option>
							{#each projects as p (p.key)}<option value={p.key}>{p.label}</option>{/each}
							{#if projectPath && !projects.some((p) => p.key === projectPath)}
								<option value={projectPath}>{shortProject(projectPath)}</option>
							{/if}
						</select>
					</label>
					{#if f.issue}
						<span class="chip">
							Ticket {f.issue}
							<button class="chipx" aria-label="Clear the ticket filter" onclick={() => setFilters({ issue: '', range: f.range })}>
								<X size={12} strokeWidth={2.4} />
							</button>
						</span>
					{/if}
				</div>
				<span class="utc">Days and times are UTC</span>
			</div>

			<nav class="tabs" aria-label="Sessions views">
				{#each VIEWS as v (v.key)}
					<a class="tab" class:on={active === v.key} aria-current={active === v.key ? 'page' : undefined} href={v.path + filtersSearch(f)}>{v.label}</a>
				{/each}
			</nav>

			{@render children()}
		</div>
	</div>
</div>

<style>
	.sx {
		max-width: 1200px;
		margin: 0 auto;
		padding: 16px clamp(16px, 4vw, 40px) 48px;
		display: flex;
		flex-direction: column;
		gap: 16px;
	}
	.filters {
		display: flex;
		align-items: flex-end;
		flex-wrap: wrap;
		gap: 12px 20px;
	}
	.fctl {
		display: flex;
		align-items: flex-end;
		flex-wrap: wrap;
		gap: 12px;
	}
	.fl {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.fl .select,
	.fl .input {
		width: auto;
		min-width: 140px;
	}
	.utc {
		margin-left: auto;
		font-size: var(--t-xs);
		color: var(--ink-3);
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		height: 27px;
		padding: 0 6px 0 10px;
		border-radius: 999px;
		border: 1px solid var(--accent);
		background: var(--accent-soft);
		font-size: var(--t-sm);
		color: var(--ink);
		box-sizing: border-box;
	}
	.chipx {
		display: inline-flex;
		background: none;
		border: none;
		color: var(--ink-2);
		padding: 2px;
		border-radius: 50%;
	}
	.chipx:hover {
		color: var(--ink);
	}
	.tabs {
		display: flex;
		gap: 20px;
		border-bottom: 1px solid var(--line);
		overflow-x: auto;
	}
	.tab {
		padding: 6px 0 8px;
		margin-bottom: -1px;
		border-bottom: 2px solid transparent;
		font-size: var(--t-sm);
		color: var(--ink-2);
		text-decoration: none;
		white-space: nowrap;
	}
	.tab:hover {
		color: var(--ink);
	}
	.tab.on {
		color: var(--ink);
		font-weight: 500;
		border-bottom-color: var(--accent);
	}

	/* Shared by the four views: the compact Breakdown table look. */
	.sx :global(.rh) {
		font-family: var(--mono);
		font-size: var(--t-xs);
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--ink-3);
	}
	.sx :global(.dim) {
		opacity: 0.55;
		transition: opacity var(--dur) var(--ease);
	}
	.sx :global(.block) {
		display: flex;
		flex-direction: column;
		gap: 10px;
		min-width: 0;
	}
	.sx :global(.bhrow) {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		flex-wrap: wrap;
	}
	.sx :global(.bh) {
		margin: 0;
		font-size: var(--t-base);
		font-weight: 600;
		color: var(--ink);
	}
	.sx :global(.grid) {
		border: 1px solid var(--line);
		border-radius: var(--r);
		background: var(--surface);
		overflow-x: auto;
	}
	.sx :global(.gr) {
		display: grid;
		align-items: center;
		gap: 12px;
		padding: 7px 12px;
		min-height: 36px;
		border-top: 1px solid var(--line);
		font-size: var(--t-sm);
		color: var(--ink-2);
		min-width: 0;
	}
	.sx :global(.gr:first-child) {
		border-top: none;
	}
	.sx :global(.gr.gh) {
		min-height: 30px;
		background: var(--sunken);
		font-family: var(--mono);
		font-size: var(--t-xs);
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--ink-3);
	}
	.sx :global(.gr:not(.gh):hover) {
		background: var(--hover);
	}
	.sx :global(.gr > *) {
		min-width: 0;
	}
	.sx :global(.num) {
		text-align: right;
		font-variant-numeric: tabular-nums;
	}
	.sx :global(.cut) {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.sx :global(.mono) {
		font-family: var(--mono);
	}
	.sx :global(.key) {
		font-family: var(--mono);
		color: var(--accent);
		text-decoration: none;
	}
	.sx :global(.key:hover) {
		text-decoration: underline;
	}
	.sx :global(.faint) {
		color: var(--ink-3);
	}
	.sx :global(.note) {
		margin: 0;
		font-size: var(--t-xs);
		color: var(--ink-3);
	}
	.sx :global(.tchip) {
		display: inline-flex;
		align-items: center;
		height: 20px;
		padding: 0 7px;
		border-radius: 999px;
		border: 1px solid var(--line);
		background: var(--sunken);
		font-size: var(--t-xs);
		color: var(--ink-2);
		white-space: nowrap;
	}
	.sx :global(.tchip.coord) {
		border-color: var(--accent);
		background: var(--accent-soft);
		color: var(--ink);
	}
	.sx :global(.err) {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 14px;
		background: var(--danger-soft);
		border: 1px solid var(--danger);
		border-radius: var(--r);
		color: var(--danger);
		font-size: var(--t-sm);
	}
	@media (max-width: 720px) {
		.utc {
			margin-left: 0;
		}
	}
</style>
