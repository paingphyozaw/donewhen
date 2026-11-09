<script>
	// Sessions › Tickets (BIZ-282): one row per ticket, split by who spent it.
	// A "Coordinator (branch …)" row is the hub coordinator's own overhead on the
	// branch it sat on, kept apart so it does not inflate that ticket.
	import { getContext } from 'svelte';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api.js';
	import { fmtInt, fmtCost, query, ticketRows } from '$lib/sessions.js';
	import { issueInfo } from '$lib/sessions_issues.js';
	import StateIcon from '$components/StateIcon.svelte';
	import SessionsEmpty from '$components/SessionsEmpty.svelte';

	const sx = getContext('sessions');

	let raw = $state([]);
	let titles = $state(new Map());
	let loading = $state(true);
	let error = $state('');

	let seq = 0;
	async function load(base) {
		const mine = ++seq;
		loading = true;
		error = '';
		try {
			const rows = (await api.sessionSummary(query({ ...base, group: 'ticket' }))) || [];
			if (mine !== seq) return;
			raw = rows;
			const keys = [...new Set(rows.filter((r) => r.key && !r.coordinator).map((r) => r.key))];
			const info = await issueInfo(keys);
			if (mine === seq) titles = info;
		} catch (e) {
			if (mine !== seq) return;
			error = e?.message || 'Could not load tickets.';
		} finally {
			if (mine === seq) loading = false;
		}
	}
	$effect(() => {
		if (!sx.ready) return;
		load(sx.base);
	});

	const rows = $derived(ticketRows(raw, titles));
	const maxShare = $derived(Math.max(0, ...rows.map((r) => r.share)));
	const empty = $derived(!loading && !error && rows.length === 0);
	const hasCoordinator = $derived(rows.some((r) => r.coordinator));

	function open(r) {
		if (!r.key) return;
		goto(sx.href('/sessions/agents', { issue: r.key }));
	}
	const day = (iso) =>
		iso ? new Date(iso).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' }) : '';
	const runsText = (r) => `${r.runs.build} / ${r.runs.review} / ${r.runs.test}`;
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
		<div class="grid" role="table" aria-label="Cost per ticket">
			<div class="gr gh tk" role="row">
				<span role="columnheader">Ticket</span>
				<span role="columnheader" class="num" title="Runs: builder / reviewer / tester">Runs</span>
				<span role="columnheader" class="num">Build</span>
				<span role="columnheader" class="num">Review</span>
				<span role="columnheader" class="num">Other</span>
				<span role="columnheader" class="num">Total</span>
				<span role="columnheader" class="num">Tokens</span>
				<span role="columnheader" class="num">Last</span>
			</div>
			{#each rows as r (r.id)}
				<div
					class="gr tk"
					class:click={!!r.key}
					role="row"
					tabindex={r.key ? 0 : undefined}
					onclick={() => open(r)}
					onkeydown={(e) => e.key === 'Enter' && open(r)}
				>
					<span role="cell" class="tcell">
						{#if r.coordinator}
							<span class="tchip coord">{r.label}</span>
						{:else if r.key}
							<a class="key" href="/issue/{r.key}" onclick={(e) => e.stopPropagation()}>{r.key}</a>
							{#if r.state}
								<span class="st" title={r.state.name}><StateIcon category={r.state.category} color={r.state.color} name={r.state.name} size={12} /></span>
							{/if}
							<span class="cut ttl" title={r.title}>{r.title}</span>
						{:else}
							<span class="faint">{r.label}</span>
						{/if}
					</span>
					<span role="cell" class="num faint">{runsText(r)}</span>
					<span role="cell" class="num">{fmtCost(r.build)}</span>
					<span role="cell" class="num">{fmtCost(r.review)}</span>
					<span role="cell" class="num">{fmtCost(r.other)}</span>
					<span role="cell" class="num tot">
						<span class="bar" aria-hidden="true"><span style:width="{maxShare > 0 ? (r.share / maxShare) * 100 : 0}%"></span></span>
						<span class="tv">{fmtCost(r.costUsd)}</span>
					</span>
					<span role="cell" class="num">{fmtInt(r.tokens)}</span>
					<span role="cell" class="num faint">{day(r.lastAt)}</span>
				</div>
			{/each}
		</div>
		<p class="note">
			Build is implementer runs, Review is reviewer runs, Other is the rest (coordinator, tester, Explore…). Click a row for its agent runs.
			{#if hasCoordinator}A Coordinator row is a hub session's own work on the branch it sat on: it is not that ticket's work.{/if}
		</p>
	</section>
{/if}

<style>
	.tk {
		grid-template-columns: minmax(300px, 1fr) 64px 72px 72px 72px 124px 112px 84px;
		min-width: 940px;
	}
	.click {
		cursor: pointer;
	}
	.click:focus-visible {
		outline: none;
		box-shadow: inset 0 0 0 2px var(--focus);
	}
	.tcell {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
		white-space: nowrap;
	}
	.st {
		display: inline-flex;
		flex: none;
	}
	.ttl {
		color: var(--ink);
		min-width: 0;
	}
	.tot {
		display: flex;
		align-items: center;
		justify-content: flex-end;
		gap: 8px;
	}
	.bar {
		flex: 1;
		max-width: 50px;
		height: 6px;
		border-radius: 999px;
		background: var(--sunken);
		overflow: hidden;
		display: flex;
		justify-content: flex-start;
	}
	.bar span {
		display: block;
		height: 100%;
		border-radius: 999px;
		background: var(--accent);
		opacity: 0.8;
	}
	.tv {
		color: var(--ink);
	}
</style>
