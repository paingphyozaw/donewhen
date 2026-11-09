<script>
	// Sessions: where the tokens and the money go. Reads only the /api/sessions
	// routes (the CLI writes them). One filter row drives the tiles, the chart,
	// the breakdown tabs and the list, so their totals always agree.
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api.js';
	import { duration } from '$lib/format.js';
	import {
		RANGES,
		ISSUE_NONE,
		fmtInt,
		fmtCost,
		rangeBounds,
		parseRange,
		fillDays,
		withShare,
		totalsOf,
		shortProject,
		modelLabel,
		query
	} from '$lib/sessions.js';
	import PageHeader from '$components/PageHeader.svelte';
	import CostChart from '$components/CostChart.svelte';
	import { ChevronRight, X } from '@lucide/svelte';

	const TABS = [
		{ key: 'issue', label: 'By ticket' },
		{ key: 'project', label: 'By project' },
		{ key: 'model', label: 'By model' }
	];
	const LIST_LIMIT = 100;

	// Filters live in the URL, so a link (the issue page's) opens the page already filtered.
	function fromSearch(search) {
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
	let f = $state(fromSearch($page.url.search));
	let lastSearch = $page.url.search;
	let tab = $state('issue');
	let projects = $state([]); // every project path seen: [{ key, label }]
	let projectsReady = $state(false);

	let days = $state([]);
	let groups = $state({ issue: [], project: [], model: [] });
	let list = $state([]);
	let loading = $state(true);
	let error = $state('');
	let open = $state(new Set());

	// The filters as the URL spells them.
	function search(next) {
		const withDefaultRange = next.range !== (next.issue ? 'all' : '7d');
		return query({
			range: withDefaultRange ? next.range : '',
			from: next.range === 'custom' ? next.from : '',
			to: next.range === 'custom' ? next.to : '',
			project: next.project,
			issue: next.issue
		});
	}
	function setFilters(patch) {
		f = { ...f, ...patch };
		const s = search(f);
		lastSearch = s;
		goto('/sessions' + s, { replaceState: true, keepFocus: true, noScroll: true });
	}
	// A link to /sessions while the page is open (the sidebar, say) resets the filters.
	$effect(() => {
		const s = $page.url.search;
		if (s !== lastSearch) {
			lastSearch = s;
			f = fromSearch(s);
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
		projectsReady = true;
	});

	// The URL may name a project by its short name (8com-suite); the API wants the path.
	const projectPath = $derived(
		projects.find((p) => p.key === f.project || p.label === f.project)?.key ?? f.project
	);
	const bounds = $derived(rangeBounds(f.range, new Date(), f.from, f.to));

	let seq = 0;
	async function load(b, project, issue) {
		const mine = ++seq;
		loading = true;
		error = '';
		const base = { from: b.from, to: b.to, project, issue };
		try {
			const [d, gi, gp, gm, l] = await Promise.all([
				api.sessionSummary(query({ ...base, group: 'day' })),
				api.sessionSummary(query({ ...base, group: 'issue' })),
				api.sessionSummary(query({ ...base, group: 'project' })),
				api.sessionSummary(query({ ...base, group: 'model' })),
				api.sessions(query({ ...base, limit: LIST_LIMIT }))
			]);
			if (mine !== seq) return; // a newer filter already landed
			days = d || [];
			groups = { issue: gi || [], project: gp || [], model: gm || [] };
			list = l || [];
			open = new Set();
		} catch (e) {
			if (mine !== seq) return;
			error = e?.message || 'Could not load sessions.';
		} finally {
			if (mine === seq) loading = false;
		}
	}
	$effect(() => {
		if (!projectsReady) return;
		load(bounds, projectPath, f.issue);
	});

	const totals = $derived(totalsOf(days, groups.project));
	const chartDays = $derived(fillDays(days, bounds.from, bounds.to));
	const rows = $derived(withShare(groups[tab]));
	const empty = $derived(!loading && !error && totals.sessions === 0);

	function toggle(id) {
		const next = new Set(open);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		open = next;
	}
	const when = (iso) => {
		const d = new Date(iso);
		return (
			d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' }) +
			' ' +
			d.toISOString().slice(11, 16)
		);
	};
	const models = (s) => [...new Set(s.usage.map((u) => modelLabel(u.model)))].join(', ');
	const keyLabel = (k) => (tab === 'issue' && k === '' ? ISSUE_NONE : tab === 'project' ? shortProject(k) : k);
</script>

<div class="pg">
	<PageHeader crumbs={[{ label: 'Sessions' }]} />
	<div class="pg-body">
		<div class="wrap">
			<div class="filters">
				<h1 class="page-title">Sessions</h1>
				<div class="fctl">
					<label class="fl">
						<span class="rh">Range</span>
						<select
							class="select"
							value={f.range}
							onchange={(e) => setFilters({ range: e.currentTarget.value })}
						>
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
						<select
							class="select"
							value={projectPath}
							onchange={(e) => setFilters({ project: e.currentTarget.value })}
						>
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

			{#if error}
				<div class="err">
					{error}
					<button class="btn ghost" onclick={() => load(bounds, projectPath, f.issue)}>Retry</button>
				</div>
			{:else if empty}
				<div class="empty">
					<p class="eline">No sessions in this range.</p>
					<p class="hint">Send your Claude Code sessions with:</p>
					<code class="cmd">donewhen sessions push --dir ~/.claude/projects --since {bounds.from || '2026-01-01'}</code>
				</div>
			{:else}
				<div class="tiles" class:dim={loading}>
					<div class="tile"><span class="rh">Cost (US$)</span><span class="tv">{fmtCost(totals.costUsd)}</span></div>
					<div class="tile"><span class="rh">Tokens</span><span class="tv">{fmtInt(totals.tokens)}</span></div>
					<div class="tile"><span class="rh">Sessions</span><span class="tv">{fmtInt(totals.sessions)}</span></div>
					<div class="tile"><span class="rh">Avg cost per session</span><span class="tv">{fmtCost(totals.avgCostUsd)}</span></div>
				</div>

				<section class="block" class:dim={loading}>
					<h2 class="bh">Cost per day</h2>
					<CostChart days={chartDays} />
				</section>

				<section class="block" class:dim={loading}>
					<div class="bhrow">
						<h2 class="bh">Breakdown</h2>
						<div class="seg">
							{#each TABS as t (t.key)}
								<button class="sg" aria-pressed={tab === t.key} class:on={tab === t.key} onclick={() => (tab = t.key)}>{t.label}</button>
							{/each}
						</div>
					</div>
					<div class="grid" role="table" aria-label="Breakdown {TABS.find((t) => t.key === tab).label}">
						<div class="gr gh bk" role="row">
							<span role="columnheader">{tab === 'issue' ? 'Ticket' : tab === 'project' ? 'Project' : 'Model'}</span>
							<span role="columnheader" class="num">Sessions</span>
							<span role="columnheader" class="num">Tokens</span>
							<span role="columnheader" class="num">Cost</span>
							<span role="columnheader" class="num">Share</span>
						</div>
						{#each rows as r (r.key)}
							<div class="gr bk" role="row">
								<span role="cell" class="cell-main" title={tab === 'project' ? r.key : undefined}>
									{#if tab === 'issue' && r.key}
										<a class="key" href="/issue/{r.key}">{r.key}</a>
									{:else if tab === 'issue'}
										<span class="faint">{ISSUE_NONE}</span>
									{:else}
										{keyLabel(r.key)}
									{/if}
								</span>
								<span role="cell" class="num">{fmtInt(r.sessions)}</span>
								<span role="cell" class="num">{fmtInt(r.tokens)}</span>
								<span role="cell" class="num">{fmtCost(r.costUsd)}</span>
								<span role="cell" class="num">{r.share.toFixed(1)}%</span>
							</div>
						{/each}
					</div>
					{#if tab === 'model'}
						<p class="note">A session that used several models counts under each of them.</p>
					{/if}
				</section>

				<section class="block" class:dim={loading}>
					<div class="bhrow">
						<h2 class="bh">Sessions</h2>
						{#if totals.sessions > list.length}
							<span class="note">Newest {fmtInt(list.length)} of {fmtInt(totals.sessions)}</span>
						{/if}
					</div>
					<div class="grid" role="table" aria-label="Sessions">
						<div class="gr gh sl" role="row">
							<span role="columnheader">Started</span>
							<span role="columnheader">Project</span>
							<span role="columnheader" class="c-branch">Branch</span>
							<span role="columnheader">Tickets</span>
							<span role="columnheader" class="c-model">Models</span>
							<span role="columnheader" class="num c-dur">Duration</span>
							<span role="columnheader" class="num">Tokens</span>
							<span role="columnheader" class="num">Cost</span>
						</div>
						{#each list as s (s.id)}
							<div class="gr sl" class:isopen={open.has(s.id)} role="row">
								<span role="cell">
									<button class="rowbtn" aria-expanded={open.has(s.id)} onclick={() => toggle(s.id)}>
										<span class="chev" class:open={open.has(s.id)}><ChevronRight size={12} strokeWidth={2.4} /></span>{when(s.startedAt)}
									</button>
								</span>
								<span role="cell" class="cut" title={s.projectPath}>{shortProject(s.projectPath)}</span>
								<span role="cell" class="cut mono c-branch" title={s.gitBranch}>{s.gitBranch}</span>
								<span role="cell" class="keys">
									{#each s.issueKeys || [] as k (k)}<a class="key" href="/issue/{k}">{k}</a>{/each}
								</span>
								<span role="cell" class="cut c-model">{models(s)}</span>
								<span role="cell" class="num c-dur">{duration(s.startedAt, s.endedAt)}</span>
								<span role="cell" class="num">{fmtInt(s.tokens)}</span>
								<span role="cell" class="num">{fmtCost(s.costUsd)}</span>
								{#if open.has(s.id)}
									<div class="usage" role="cell">
										<div class="ur uh">
											<span>Model</span><span class="num">Input</span><span class="num">Output</span><span class="num">Cache read</span><span class="num">Cache write</span><span class="num">Cost</span>
										</div>
										{#each s.usage as u (u.model)}
											<div class="ur">
												<span class="mono">{modelLabel(u.model)}</span>
												<span class="num">{fmtInt(u.inputTokens)}</span>
												<span class="num">{fmtInt(u.outputTokens)}</span>
												<span class="num">{fmtInt(u.cacheReadTokens)}</span>
												<span class="num">{fmtInt(u.cacheWriteTokens)}</span>
												<span class="num">{fmtCost(u.costUsd)}</span>
											</div>
										{/each}
									</div>
								{/if}
							</div>
						{/each}
					</div>
				</section>
			{/if}
		</div>
	</div>
</div>

<style>
	.wrap {
		max-width: 1040px;
		margin: 0 auto;
		padding: 16px clamp(16px, 4vw, 40px) 48px;
		display: flex;
		flex-direction: column;
		gap: 20px;
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
	.rh {
		font-family: var(--mono);
		font-size: var(--t-xs);
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--ink-3);
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
	.tiles {
		display: grid;
		grid-template-columns: repeat(4, minmax(0, 1fr));
		gap: 12px;
	}
	.tile {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 12px 14px;
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--r);
		min-width: 0;
	}
	.tv {
		font-size: var(--t-xl);
		font-weight: 500;
		color: var(--ink);
		font-variant-numeric: tabular-nums;
		overflow-wrap: anywhere;
	}
	.dim {
		opacity: 0.55;
		transition: opacity var(--dur) var(--ease);
	}
	.block {
		display: flex;
		flex-direction: column;
		gap: 10px;
	}
	.bhrow {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		flex-wrap: wrap;
	}
	.bh {
		margin: 0;
		font-size: var(--t-base);
		font-weight: 600;
		color: var(--ink);
	}
	.seg {
		display: flex;
		gap: 2px;
		background: var(--sunken);
		border: 1px solid var(--line);
		border-radius: var(--r-sm);
		padding: 2px;
	}
	.sg {
		height: 26px;
		padding: 0 12px;
		border-radius: var(--r-sm);
		font-size: var(--t-sm);
		color: var(--ink-2);
		background: none;
		border: none;
	}
	.sg:hover {
		color: var(--ink);
	}
	.sg.on {
		background: var(--surface);
		color: var(--ink);
		font-weight: 500;
		box-shadow: var(--shadow-1);
	}
	.grid {
		border: 1px solid var(--line);
		border-radius: var(--r);
		background: var(--surface);
		overflow-x: auto;
	}
	.gr {
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
	.gr:first-child {
		border-top: none;
	}
	.gr.gh {
		min-height: 30px;
		background: var(--sunken);
		font-family: var(--mono);
		font-size: var(--t-xs);
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--ink-3);
	}
	.gr:not(.gh):hover {
		background: var(--hover);
	}
	.bk {
		grid-template-columns: minmax(120px, 1fr) 90px 150px 100px 70px;
	}
	.sl {
		grid-template-columns: 130px 110px minmax(100px, 1.2fr) minmax(90px, 1fr) minmax(80px, 0.8fr) 80px 120px 80px;
		min-width: 860px;
	}
	.gr > :global(*) {
		min-width: 0;
	}
	.num {
		text-align: right;
		font-variant-numeric: tabular-nums;
	}
	.cut {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.cell-main {
		color: var(--ink);
	}
	.mono {
		font-family: var(--mono);
	}
	.keys {
		display: flex;
		flex-wrap: wrap;
		gap: 2px 8px;
	}
	.key {
		font-family: var(--mono);
		color: var(--accent);
		text-decoration: none;
	}
	.key:hover {
		text-decoration: underline;
	}
	.rowbtn {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		background: none;
		border: none;
		padding: 0;
		color: var(--ink);
		font-size: var(--t-sm);
		font-family: inherit;
		white-space: nowrap;
	}
	.rowbtn:focus-visible {
		outline: none;
		box-shadow: 0 0 0 2px var(--focus);
		border-radius: var(--r-sm);
	}
	.chev {
		display: inline-flex;
		color: var(--ink-3);
		transition: transform var(--dur) var(--ease);
	}
	.chev.open {
		transform: rotate(90deg);
	}
	.usage {
		grid-column: 1 / -1;
		margin: 2px 0 4px 16px;
		padding: 6px 0 2px;
		border-top: 1px dashed var(--line);
	}
	.ur {
		display: grid;
		grid-template-columns: minmax(120px, 1.2fr) repeat(5, minmax(70px, 1fr));
		gap: 12px;
		padding: 3px 0;
		font-variant-numeric: tabular-nums;
	}
	.uh {
		color: var(--ink-3);
		font-size: var(--t-xs);
	}
	.note {
		margin: 0;
		font-size: var(--t-xs);
		color: var(--ink-3);
	}
	.empty {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 10px;
		padding: 60px 20px;
		text-align: center;
	}
	.eline {
		margin: 0;
		font-family: var(--serif);
		font-weight: 400;
		font-size: var(--t-xl);
		color: var(--ink);
	}
	.hint {
		margin: 0;
		font-size: var(--t-sm);
		color: var(--ink-3);
	}
	.cmd {
		max-width: 100%;
		overflow-x: auto;
		padding: 8px 12px;
		background: var(--sunken);
		border: 1px solid var(--line);
		border-radius: var(--r-sm);
		font-family: var(--mono);
		font-size: var(--t-sm);
		color: var(--ink);
		white-space: nowrap;
	}
	.err {
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
		.tiles {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
		.utc {
			margin-left: 0;
		}
		.bk {
			grid-template-columns: minmax(90px, 1fr) 64px 110px 80px 56px;
			gap: 8px;
		}
		.ur {
			grid-template-columns: minmax(90px, 1.2fr) repeat(5, minmax(60px, 1fr));
			min-width: 520px;
		}
	}
</style>
