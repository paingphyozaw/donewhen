<script>
	// Cost per day, stacked by token kind (BIZ-282). Same frame as CostChart: one
	// axis (US$), thin bars, a quiet grid, a hover tooltip. Kinds stack in their
	// fixed order (KINDS) with a 2px surface gap between segments; only the top
	// segment has the rounded end. `days`: one row per UTC day with `kinds`.
	import { fmtInt, fmtCost, niceScale, barPath, KINDS } from '$lib/sessions.js';

	let { days = [] } = $props();

	const HEIGHT = 220;
	const GAP = 2;
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

	const costOf = (d, key) => d.kinds?.find((k) => k.kind === key)?.costUsd || 0;

	// segments: the stacked pieces of one day, bottom first, in pixels.
	function segments(d) {
		const out = [];
		let base = 0;
		for (const k of KINDS) {
			const v = costOf(d, k.key);
			if (v <= 0) continue;
			const y0 = yOf(base);
			const y1 = yOf(base + v);
			out.push({ key: k.key, color: k.color, y: y1, h: y0 - y1 });
			base += v;
		}
		// The gap is cut from the bottom of every segment above the first.
		return out
			.map((s, i) => (i === 0 ? s : { ...s, h: s.h - GAP }))
			.map((s, i, all) => ({ ...s, top: i === all.length - 1 }))
			.filter((s) => s.h >= 0.5);
	}

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
		hover < 0 ? 0 : Math.min(Math.max(M.left + band * (hover + 0.5), 100), Math.max(100, width - 100))
	);
</script>

<div class="legend" aria-hidden="true">
	{#each KINDS as k (k.key)}
		<span class="lg"><span class="sw" style:background={k.color}></span>{k.label}</span>
	{/each}
</div>
<div class="chart" bind:clientWidth={width}>
	{#if width > 0 && days.length}
		<svg {width} height={HEIGHT} role="img" aria-label="Cost per day by token kind, {days.length} days" onmouseleave={() => (hover = -1)}>
			{#each scale.ticks as t (t)}
				<line class="grid" class:base={t === 0} x1={M.left} x2={width - M.right} y1={yOf(t)} y2={yOf(t)} />
				<text class="ylab" x={M.left - 8} y={yOf(t)} dy="0.32em" text-anchor="end">{tick(t)}</text>
			{/each}
			{#each days as d, i (d.key)}
				{@const x = M.left + band * i + (band - barW) / 2}
				<g class="bar" class:on={hover === i} class:off={hover >= 0 && hover !== i}>
					{#each segments(d) as s (s.key)}
						{#if s.top}
							<path d={barPath(x, s.y, barW, Math.max(1, s.h))} style:fill={s.color} />
						{:else}
							<rect {x} y={s.y} width={barW} height={Math.max(1, s.h)} style:fill={s.color} />
						{/if}
					{/each}
				</g>
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
				{#each [...KINDS].reverse() as k (k.key)}
					<div class="trow"><span><span class="sw" style:background={k.color}></span>{k.label}</span><b>{fmtCost(costOf(d, k.key))}</b></div>
				{/each}
				<div class="trow tot"><span>Total</span><b>{fmtCost(d.costUsd)}</b></div>
				<div class="trow"><span>Tokens</span><b>{fmtInt(d.tokens)}</b></div>
			</div>
		{/if}
	{/if}
</div>

<style>
	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: 4px 14px;
		font-size: var(--t-xs);
		color: var(--ink-2);
	}
	.lg {
		display: inline-flex;
		align-items: center;
		gap: 6px;
	}
	.sw {
		display: inline-block;
		width: 8px;
		height: 8px;
		border-radius: 2px; /* ds-ok: legend swatch */
		margin-right: 6px;
		vertical-align: 0;
	}
	.lg .sw {
		margin-right: 0;
	}
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
		transition: opacity var(--dur) var(--ease);
	}
	.bar.off {
		opacity: 0.45;
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
		min-width: 170px;
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
	.trow.tot {
		margin-top: 3px;
		padding-top: 3px;
		border-top: 1px solid var(--line);
	}
	.trow b {
		font-weight: 500;
		color: var(--ink);
	}
</style>
