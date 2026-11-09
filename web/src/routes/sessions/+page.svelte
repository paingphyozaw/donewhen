<script>
	// Sessions › Overview (BIZ-282): where the money goes and why. Six tiles,
	// plain-language insights, cost per day stacked by token kind, two bar
	// lists (by kind, by agent type) and the five most expensive tickets.
	import { getContext } from 'svelte';
	import { api } from '$lib/api.js';
	import {
		fmtInt,
		fmtCost,
		fillDays,
		query,
		overviewTotals,
		sortAgentTypes,
		insights,
		ticketRows,
		KINDS,
		COORDINATOR
	} from '$lib/sessions.js';
	import { issueInfo } from '$lib/sessions_issues.js';
	import KindCostChart from '$components/KindCostChart.svelte';
	import BarList from '$components/BarList.svelte';
	import SessionsEmpty from '$components/SessionsEmpty.svelte';

	const sx = getContext('sessions');

	let days = $state([]);
	let projects = $state([]);
	let kinds = $state([]);
	let types = $state([]);
	let tickets = $state([]);
	let titles = $state(new Map());
	let loading = $state(true);
	let error = $state('');

	let seq = 0;
	async function load(base) {
		const mine = ++seq;
		loading = true;
		error = '';
		try {
			const get = (group) => api.sessionSummary(query({ ...base, group }));
			const [d, p, k, t, tk] = await Promise.all([get('day'), get('project'), get('kind'), get('agent_type'), get('ticket')]);
			if (mine !== seq) return;
			days = d || [];
			projects = p || [];
			kinds = k || [];
			types = t || [];
			tickets = tk || [];
			const top = ticketRows(tickets).filter((r) => r.key && !r.coordinator).slice(0, 5);
			const info = await issueInfo(top.map((r) => r.key));
			if (mine === seq) titles = info;
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

	const totals = $derived(overviewTotals(days, projects, kinds, types));
	const chartDays = $derived(fillDays(days, sx.bounds.from, sx.bounds.to));
	const kindRows = $derived(
		KINDS.map((k) => ({ key: k.key, label: k.label, color: k.color, costUsd: kinds.find((r) => r.key === k.key)?.costUsd || 0 }))
	);
	const typeRows = $derived(
		sortAgentTypes(types).map((t) => ({
			key: t.key,
			label: t.key === COORDINATOR ? 'Coordinator' : t.key,
			sub: t.key === COORDINATOR ? `${fmtInt(t.runs)} sessions` : `${fmtInt(t.runs)} ${t.runs === 1 ? 'run' : 'runs'}`,
			costUsd: t.costUsd
		}))
	);
	const top5 = $derived(ticketRows(tickets, titles).filter((r) => r.key && !r.coordinator).slice(0, 5));
	const lines = $derived(insights({ totals, kinds, types, tickets }));
	const empty = $derived(!loading && !error && days.length === 0);
</script>

{#if error}
	<div class="err">
		{error}
		<button class="btn ghost" onclick={() => load(sx.base)}>Retry</button>
	</div>
{:else if empty}
	<SessionsEmpty from={sx.bounds.from} />
{:else}
	<div class="tiles" class:dim={loading}>
		<div class="tile"><span class="rh">Cost (US$)</span><span class="tv">{fmtCost(totals.costUsd)}</span></div>
		<div class="tile"><span class="rh">Tokens</span><span class="tv">{fmtInt(totals.tokens)}</span></div>
		<div class="tile"><span class="rh">Sessions</span><span class="tv">{fmtInt(totals.sessions)}</span></div>
		<div class="tile"><span class="rh">Agent runs</span><span class="tv">{fmtInt(totals.runs)}</span></div>
		<div class="tile"><span class="rh">Cache-read share of cost</span><span class="tv">{totals.cacheShare.toFixed(1)}%</span></div>
		<div class="tile">
			<span class="rh">Avg context per message</span><span class="tv">{fmtInt(totals.avgContext)}</span>
			<span class="tsub">cache-read tokens</span>
		</div>
	</div>

	{#if lines.length}
		<section class="block" class:dim={loading} aria-label="Insights">
			<h2 class="bh">Insights</h2>
			<ul class="ins">
				{#each lines as l (l)}<li>{l}</li>{/each}
			</ul>
		</section>
	{/if}

	<section class="block" class:dim={loading}>
		<h2 class="bh">Cost per day</h2>
		<KindCostChart days={chartDays} />
	</section>

	<section class="block" class:dim={loading}>
		<h2 class="bh">Where the money goes</h2>
		<div class="two">
			<div class="col">
				<span class="rh">By token kind</span>
				<BarList rows={kindRows} label="Cost by token kind" />
			</div>
			<div class="col">
				<span class="rh">By agent type</span>
				<BarList rows={typeRows} label="Cost by agent type" />
			</div>
		</div>
	</section>

	<section class="block" class:dim={loading}>
		<div class="bhrow">
			<h2 class="bh">Top 5 tickets</h2>
			<a class="more" href={sx.href('/sessions/tickets')}>All tickets</a>
		</div>
		<div class="grid" role="table" aria-label="Top 5 tickets">
			{#each top5 as r (r.id)}
				<a class="gr top" role="row" href={sx.href('/sessions/tickets')}>
					<span role="cell" class="key">{r.key}</span>
					<span role="cell" class="cut ttl" title={r.title}>{r.title || '—'}</span>
					<span role="cell" class="num">{fmtCost(r.costUsd)}</span>
					<span role="cell" class="num faint">{r.share.toFixed(1)}%</span>
				</a>
			{:else}
				<div class="gr" role="row"><span role="cell" class="faint">No ticket in this range.</span></div>
			{/each}
		</div>
	</section>
{/if}

<style>
	.tiles {
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
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
	.tsub {
		margin-top: -4px;
		font-size: var(--t-xs);
		color: var(--ink-3);
	}
	.ins {
		margin: 0;
		padding: 10px 14px 10px 30px;
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--r);
		display: flex;
		flex-direction: column;
		gap: 6px;
		font-size: var(--t-sm);
		color: var(--ink);
		line-height: 1.5;
	}
	.two {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 16px;
	}
	.col {
		display: flex;
		flex-direction: column;
		gap: 6px;
		min-width: 0;
	}
	.more {
		font-size: var(--t-sm);
		color: var(--accent);
		text-decoration: none;
	}
	.more:hover {
		text-decoration: underline;
	}
	.top {
		grid-template-columns: 80px minmax(0, 1fr) 100px 60px;
		text-decoration: none;
	}
	.ttl {
		color: var(--ink);
	}
	@media (max-width: 720px) {
		.tiles {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
		.two {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
