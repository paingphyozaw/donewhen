<script>
	// Sessions › Sessions (BIZ-279, reworked in BIZ-282): the breakdown by
	// project and model, then the list. Every row is one line: the tickets cell
	// shows the first key and "+N" (all keys in the tooltip), the branch is cut
	// with a tooltip. An open row shows the usage per model, the cost per token
	// kind and the session's agent runs.
	import { getContext } from 'svelte';
	import { api } from '$lib/api.js';
	import { duration } from '$lib/format.js';
	import { fmtInt, fmtCost, withShare, shortProject, modelLabel, query, oneLineKeys, KINDS } from '$lib/sessions.js';
	import SessionsEmpty from '$components/SessionsEmpty.svelte';
	import { ChevronRight } from '@lucide/svelte';

	const sx = getContext('sessions');
	const TABS = [
		{ key: 'project', label: 'By project' },
		{ key: 'model', label: 'By model' }
	];
	const LIST_LIMIT = 100;

	let tab = $state('project');
	let groups = $state({ project: [], model: [] });
	let list = $state([]);
	let loading = $state(true);
	let error = $state('');
	let open = $state(new Set());

	let seq = 0;
	async function load(base) {
		const mine = ++seq;
		loading = true;
		error = '';
		try {
			const [gp, gm, l] = await Promise.all([
				api.sessionSummary(query({ ...base, group: 'project' })),
				api.sessionSummary(query({ ...base, group: 'model' })),
				api.sessions(query({ ...base, limit: LIST_LIMIT }))
			]);
			if (mine !== seq) return;
			groups = { project: gp || [], model: gm || [] };
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
		if (!sx.ready) return;
		load(sx.base);
	});

	const sessions = $derived(groups.project.reduce((n, r) => n + r.sessions, 0));
	const rows = $derived(withShare(groups[tab]));
	const empty = $derived(!loading && !error && sessions === 0);

	function toggle(id) {
		const next = new Set(open);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		open = next;
	}
	const when = (iso) => {
		const d = new Date(iso);
		return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' }) + ' ' + d.toISOString().slice(11, 16);
	};
	const models = (s) => [...new Set(s.usage.map((u) => modelLabel(u.model)))].join(', ');
	const kindsOf = (s) => KINDS.map((k) => ({ ...k, costUsd: (s.kinds || []).find((x) => x.kind === k.key)?.costUsd || 0 }));
</script>

{#if error}
	<div class="err">
		{error}
		<button class="btn ghost" onclick={() => load(sx.base)}>Retry</button>
	</div>
{:else if empty}
	<SessionsEmpty from={sx.bounds.from} />
{:else}
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
				<span role="columnheader">{tab === 'project' ? 'Project' : 'Model'}</span>
				<span role="columnheader" class="num">Sessions</span>
				<span role="columnheader" class="num">Tokens</span>
				<span role="columnheader" class="num">Cost</span>
				<span role="columnheader" class="num">Share</span>
			</div>
			{#each rows as r (r.key)}
				<div class="gr bk" role="row">
					<span role="cell" class="cut main" title={tab === 'project' ? r.key : undefined}>{tab === 'project' ? shortProject(r.key) : r.key}</span>
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
			{#if sessions > list.length}
				<span class="note">Newest {fmtInt(list.length)} of {fmtInt(sessions)}</span>
			{/if}
		</div>
		<div class="grid" role="table" aria-label="Sessions">
			<div class="gr gh sl" role="row">
				<span role="columnheader">Started</span>
				<span role="columnheader">Project</span>
				<span role="columnheader">Branch</span>
				<span role="columnheader">Tickets</span>
				<span role="columnheader">Models</span>
				<span role="columnheader" class="num">Runs</span>
				<span role="columnheader" class="num">Duration</span>
				<span role="columnheader" class="num">Tokens</span>
				<span role="columnheader" class="num">Cost</span>
			</div>
			{#each list as s (s.id)}
				{@const k = oneLineKeys(s.issueKeys || [])}
				<div class="gr sl" role="row">
					<span role="cell">
						<button class="rowbtn" aria-expanded={open.has(s.id)} onclick={() => toggle(s.id)}>
							<span class="chev" class:open={open.has(s.id)}><ChevronRight size={12} strokeWidth={2.4} /></span>{when(s.startedAt)}
						</button>
					</span>
					<span role="cell" class="cut" title={s.projectPath}>{shortProject(s.projectPath)}</span>
					<span role="cell" class="cut mono" title={s.gitBranch}>{s.gitBranch}</span>
					<span role="cell" class="keys" title={k.title}>
						{#if k.first}<a class="key" href="/issue/{k.first}">{k.first}</a>{/if}
						{#if k.more}<span class="tchip">+{k.more}</span>{/if}
					</span>
					<span role="cell" class="cut">{models(s)}</span>
					<span role="cell" class="num">{fmtInt((s.agents || []).length)}</span>
					<span role="cell" class="num">{duration(s.startedAt, s.endedAt)}</span>
					<span role="cell" class="num">{fmtInt(s.tokens)}</span>
					<span role="cell" class="num">{fmtCost(s.costUsd)}</span>
					{#if open.has(s.id)}
						<div class="detail" role="cell">
							<div class="dh">Usage per model</div>
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

							<div class="dh">Cost per token kind</div>
							<div class="kinds">
								{#each kindsOf(s) as kd (kd.key)}
									<span class="kd"><span class="sw" style:background={kd.color}></span>{kd.label}<b>{fmtCost(kd.costUsd)}</b></span>
								{/each}
							</div>

							<div class="dh">Agent runs</div>
							{#each s.agents || [] as a (a.id)}
								<div class="ar">
									<span><span class="tchip">{a.type || 'unknown'}</span></span>
									<span class="cut" title={a.description}>{a.description || '—'}</span>
									<span>{#if a.issue}<a class="key" href="/issue/{a.issue}">{a.issue}</a>{/if}</span>
									<span class="num">{fmtInt(a.tokens)}</span>
									<span class="num">{fmtCost(a.costUsd)}</span>
								</div>
							{:else}
								<p class="note">No agent runs: the coordinator did all the work.</p>
							{/each}
						</div>
					{/if}
				</div>
			{/each}
		</div>
	</section>
{/if}

<style>
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
	.bk {
		grid-template-columns: minmax(120px, 1fr) 90px 150px 100px 70px;
	}
	.main {
		color: var(--ink);
	}
	.sl {
		grid-template-columns: 112px 96px minmax(110px, 1.3fr) 112px minmax(80px, 0.8fr) 48px 72px 110px 76px;
		min-width: 900px;
	}
	.keys {
		display: flex;
		align-items: center;
		gap: 6px;
		white-space: nowrap;
		overflow: hidden;
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
	.detail {
		grid-column: 1 / -1;
		margin: 2px 0 4px 16px;
		padding: 6px 0 2px;
		border-top: 1px dashed var(--line);
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.dh {
		margin-top: 6px;
		font-family: var(--mono);
		font-size: var(--t-xs);
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--ink-3);
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
	.kinds {
		display: flex;
		flex-wrap: wrap;
		gap: 4px 18px;
		padding: 3px 0;
	}
	.kd {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		font-variant-numeric: tabular-nums;
	}
	.kd b {
		font-weight: 500;
		color: var(--ink);
	}
	.sw {
		display: inline-block;
		width: 8px;
		height: 8px;
		border-radius: 2px; /* ds-ok: legend swatch */
	}
	.ar {
		display: grid;
		grid-template-columns: 120px minmax(0, 1fr) 72px 110px 76px;
		gap: 12px;
		align-items: center;
		padding: 3px 0;
	}
	.ar > * {
		min-width: 0;
	}
	@media (max-width: 720px) {
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
