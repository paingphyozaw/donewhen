<script>
	// Sessions › Agents (BIZ-282): one row per subagent run. Filter by type and
	// ticket (the ticket is the shared filter); sort by cost or by start.
	import { getContext } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api.js';
	import { duration } from '$lib/format.js';
	import { fmtInt, fmtCost, query, modelLabel } from '$lib/sessions.js';
	import SessionsEmpty from '$components/SessionsEmpty.svelte';

	const sx = getContext('sessions');
	const PAGE = 200;

	let rows = $state([]);
	let loading = $state(true);
	let error = $state('');
	let sort = $state('cost'); // cost | started
	let shown = $state(PAGE);

	const type = $derived($page.url.searchParams.get('type') || '');

	let seq = 0;
	async function load(base) {
		const mine = ++seq;
		loading = true;
		error = '';
		try {
			const r = (await api.sessionSummary(query({ ...base, group: 'agent' }))) || [];
			if (mine !== seq) return;
			rows = r;
			shown = PAGE;
		} catch (e) {
			if (mine !== seq) return;
			error = e?.message || 'Could not load agent runs.';
		} finally {
			if (mine === seq) loading = false;
		}
	}
	$effect(() => {
		if (!sx.ready) return;
		load(sx.base);
	});

	const types = $derived([...new Set(rows.map((r) => r.type))].sort());
	const tickets = $derived([...new Set(rows.map((r) => r.issue).filter(Boolean))].sort());
	const visible = $derived(
		rows
			.filter((r) => !type || r.type === type)
			.sort((a, b) =>
				sort === 'cost' ? b.costUsd - a.costUsd : String(b.startedAt).localeCompare(String(a.startedAt))
			)
	);
	const total = $derived(visible.reduce((s, r) => s + r.costUsd, 0));
	const empty = $derived(!loading && !error && rows.length === 0);

	function setType(t) {
		const q = new URLSearchParams($page.url.search);
		if (t) q.set('type', t);
		else q.delete('type');
		const s = q.toString();
		goto('/sessions/agents' + (s ? '?' + s : ''), { replaceState: true, keepFocus: true, noScroll: true });
	}
	const when = (iso) => {
		const d = new Date(iso);
		return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' }) + ' ' + d.toISOString().slice(11, 16);
	};
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
			<div class="vf">
				<label class="fl">
					<span class="rh">Type</span>
					<select class="select" value={type} onchange={(e) => setType(e.currentTarget.value)}>
						<option value="">All types</option>
						{#each types as t (t)}<option value={t}>{t}</option>{/each}
					</select>
				</label>
				<label class="fl">
					<span class="rh">Ticket</span>
					<select class="select" value={sx.f.issue} onchange={(e) => sx.setFilters({ issue: e.currentTarget.value })}>
						<option value="">All tickets</option>
						{#each tickets as k (k)}<option value={k}>{k}</option>{/each}
						{#if sx.f.issue && !tickets.includes(sx.f.issue)}<option value={sx.f.issue}>{sx.f.issue}</option>{/if}
					</select>
				</label>
			</div>
			<span class="note">{fmtInt(visible.length)} runs · {fmtCost(total)}</span>
		</div>
		<div class="grid" role="table" aria-label="Agent runs">
			<div class="gr gh ag" role="row">
				<span role="columnheader">
					<button class="sortb" class:on={sort === 'started'} onclick={() => (sort = 'started')}>Started</button>
				</span>
				<span role="columnheader">Type</span>
				<span role="columnheader">Description</span>
				<span role="columnheader">Ticket</span>
				<span role="columnheader">Model</span>
				<span role="columnheader" class="num">Duration</span>
				<span role="columnheader" class="num">Msgs</span>
				<span role="columnheader" class="num">Tokens</span>
				<span role="columnheader" class="num">
					<button class="sortb" class:on={sort === 'cost'} onclick={() => (sort = 'cost')}>Cost</button>
				</span>
			</div>
			{#each visible.slice(0, shown) as r (r.key)}
				<div class="gr ag" role="row">
					<span role="cell" class="faint nowrap">{when(r.startedAt)}</span>
					<span role="cell"><span class="tchip">{r.type}</span></span>
					<span role="cell" class="cut desc" title={r.description}>{r.description || '—'}</span>
					<span role="cell">{#if r.issue}<a class="key" href="/issue/{r.issue}">{r.issue}</a>{/if}</span>
					<span role="cell" class="cut mono">{modelLabel(r.model)}</span>
					<span role="cell" class="num">{duration(r.startedAt, r.endedAt)}</span>
					<span role="cell" class="num">{fmtInt(r.messages)}</span>
					<span role="cell" class="num">{fmtInt(r.tokens)}</span>
					<span role="cell" class="num cost">{fmtCost(r.costUsd)}</span>
				</div>
			{:else}
				<div class="gr" role="row"><span role="cell" class="faint">No agent runs match.</span></div>
			{/each}
		</div>
		{#if visible.length > shown}
			<button class="btn ghost more" onclick={() => (shown = visible.length)}>Show all {fmtInt(visible.length)} runs</button>
		{/if}
	</section>
{/if}

<style>
	.vf {
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
	.fl .select {
		width: auto;
		min-width: 140px;
	}
	.ag {
		grid-template-columns: 96px 108px minmax(200px, 1fr) 72px 110px 64px 52px 110px 76px;
		min-width: 940px;
	}
	.nowrap {
		white-space: nowrap;
	}
	.desc,
	.cost {
		color: var(--ink);
	}
	.sortb {
		background: none;
		border: none;
		padding: 0;
		font: inherit;
		letter-spacing: inherit;
		text-transform: inherit;
		color: inherit;
		cursor: pointer;
	}
	.sortb.on {
		color: var(--ink);
		text-decoration: underline;
		text-underline-offset: 3px;
	}
	.more {
		align-self: center;
	}
</style>
