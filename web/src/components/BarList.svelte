<script>
	// A compact bar list (BIZ-282): label · bar · cost · share. One row per
	// item; the bar is the item's share of the list's total. `color` is an
	// optional swatch (a token) for an item that has a chart colour.
	import { fmtCost } from '$lib/sessions.js';

	let { rows = [], label = '' } = $props();
	const total = $derived(rows.reduce((s, r) => s + (r.costUsd || 0), 0));
	const pct = (v) => (total > 0 ? (v / total) * 100 : 0);
</script>

<div class="bl" role="table" aria-label={label}>
	{#each rows as r (r.key)}
		<div class="br" role="row">
			<span class="bn" role="cell" title={r.hint || r.label}>
				{#if r.color}<span class="sw" style:background={r.color}></span>{/if}{r.label}
				{#if r.sub}<span class="sub">{r.sub}</span>{/if}
			</span>
			<span class="track" role="cell" aria-hidden="true"><span class="fill" style:width="{pct(r.costUsd)}%" style:background={r.color || 'var(--accent)'}></span></span>
			<span class="num" role="cell">{fmtCost(r.costUsd)}</span>
			<span class="num share" role="cell">{pct(r.costUsd).toFixed(1)}%</span>
		</div>
	{/each}
</div>

<style>
	.bl {
		border: 1px solid var(--line);
		border-radius: var(--r);
		background: var(--surface);
	}
	.br {
		display: grid;
		grid-template-columns: minmax(110px, 1.1fr) minmax(60px, 1fr) 90px 52px;
		align-items: center;
		gap: 12px;
		padding: 7px 12px;
		min-height: 34px;
		border-top: 1px solid var(--line);
		font-size: var(--t-sm);
		color: var(--ink-2);
	}
	.br:first-child {
		border-top: none;
	}
	.br > * {
		min-width: 0;
	}
	.bn {
		color: var(--ink);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.sub {
		margin-left: 6px;
		color: var(--ink-3);
		font-size: var(--t-xs);
	}
	.sw {
		display: inline-block;
		width: 8px;
		height: 8px;
		margin-right: 8px;
		border-radius: 2px; /* ds-ok: legend swatch */
	}
	.track {
		height: 6px;
		border-radius: 999px;
		background: var(--sunken);
		overflow: hidden;
	}
	.fill {
		display: block;
		height: 100%;
		border-radius: 999px;
		opacity: 0.85;
	}
	.num {
		text-align: right;
		font-variant-numeric: tabular-nums;
	}
	.share {
		color: var(--ink-3);
	}
</style>
