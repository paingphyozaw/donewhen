<script>
	// Cost per day: one series, thin bars with a rounded top, a quiet grid and a
	// hover tooltip. Plain SVG; the colours are the app's tokens, so it follows
	// light and dark. `days` is one row per UTC day: { key, sessions, tokens, costUsd }.
	import { fmtInt, fmtCost, niceScale, barPath } from '$lib/sessions.js';

	let { days = [] } = $props();

	const HEIGHT = 220;
	const M = { top: 12, right: 12, bottom: 26, left: 64 };

	let width = $state(0);
	let hover = $state(-1);

	const plotW = $derived(Math.max(0, width - M.left - M.right));
	const plotH = HEIGHT - M.top - M.bottom;
	const scale = $derived(niceScale(Math.max(0, ...days.map((d) => d.costUsd))));
	const band = $derived(days.length ? plotW / days.length : 0);
	const barW = $derived(Math.max(2, Math.min(18, band * 0.55)));
	const yOf = (v) => M.top + plotH - (v / scale.max) * plotH;
	const every = $derived(Math.max(1, Math.ceil(days.length / Math.max(1, Math.floor(plotW / 64)))));

	const dayShort = (key) =>
		new Date(key + 'T00:00:00Z').toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' });
	const dayLong = (key) =>
		new Date(key + 'T00:00:00Z').toLocaleDateString('en-US', {
			weekday: 'short',
			month: 'short',
			day: 'numeric',
			year: 'numeric',
			timeZone: 'UTC'
		});
	const tick = (v) => (scale.step < 1 ? fmtCost(v) : '$' + fmtInt(v));

	const tipLeft = $derived(
		hover < 0 ? 0 : Math.min(Math.max(M.left + band * (hover + 0.5), 90), Math.max(90, width - 90))
	);
</script>

<div class="chart" bind:clientWidth={width}>
	{#if width > 0 && days.length}
		<svg {width} height={HEIGHT} role="img" aria-label="Cost per day, {days.length} days" onmouseleave={() => (hover = -1)}>
			{#each scale.ticks as t (t)}
				<line class="grid" class:base={t === 0} x1={M.left} x2={width - M.right} y1={yOf(t)} y2={yOf(t)} />
				<text class="ylab" x={M.left - 8} y={yOf(t)} dy="0.32em" text-anchor="end">{tick(t)}</text>
			{/each}
			{#each days as d, i (d.key)}
				{@const h = (d.costUsd / scale.max) * plotH}
				{#if h > 0}
					<path
						class="bar"
						class:on={hover === i}
						d={barPath(M.left + band * i + (band - barW) / 2, yOf(d.costUsd), barW, Math.max(1, h))}
					/>
				{/if}
				{#if i % every === 0}
					<text class="xlab" x={M.left + band * (i + 0.5)} y={HEIGHT - 8} text-anchor="middle">{dayShort(d.key)}</text>
				{/if}
				<rect
					class="hit"
					x={M.left + band * i}
					y={M.top}
					width={band}
					height={plotH}
					role="presentation"
					onmouseenter={() => (hover = i)}
					onpointerdown={() => (hover = i)}
				/>
			{/each}
		</svg>
		{#if hover >= 0}
			{@const d = days[hover]}
			<div class="tip" style:left="{tipLeft}px" role="status">
				<div class="tday">{dayLong(d.key)}</div>
				<div class="trow"><span>Cost</span><b>{fmtCost(d.costUsd)}</b></div>
				<div class="trow"><span>Tokens</span><b>{fmtInt(d.tokens)}</b></div>
				<div class="trow"><span>Sessions</span><b>{fmtInt(d.sessions)}</b></div>
			</div>
		{/if}
	{/if}
</div>

<style>
	.chart {
		position: relative;
		width: 100%;
		height: 220px;
	}
	svg {
		display: block;
	}
	.grid {
		stroke: var(--line);
		stroke-width: 1;
	}
	.grid.base {
		stroke: var(--line-strong);
	}
	.ylab,
	.xlab {
		fill: var(--ink-3);
		font-family: var(--mono);
		font-size: var(--t-xs);
	}
	.bar {
		fill: var(--accent);
		opacity: 0.75;
	}
	.bar.on {
		opacity: 1;
	}
	.hit {
		fill: transparent;
	}
	.tip {
		position: absolute;
		top: 4px;
		transform: translateX(-50%);
		pointer-events: none;
		z-index: 2;
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--r-lg);
		box-shadow: var(--shadow-2);
		padding: 8px 10px;
		min-width: 150px;
		font-size: var(--t-sm);
		color: var(--ink-2);
	}
	.tday {
		color: var(--ink);
		font-weight: 500;
		margin-bottom: 4px;
	}
	.trow {
		display: flex;
		justify-content: space-between;
		gap: 16px;
		font-variant-numeric: tabular-nums;
	}
	.trow b {
		font-weight: 500;
		color: var(--ink);
	}
</style>
