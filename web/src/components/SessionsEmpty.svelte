<script>
	// The empty state of every Sessions view: nothing in this range.
	let { from = '' } = $props();
</script>

<div class="empty">
	<p class="eline">No sessions in this range.</p>
	<p class="hint">Send your Claude Code sessions with:</p>
	<code class="cmd">donewhen sessions push --dir ~/.claude/projects --since {from || '2026-01-01'}</code>
</div>

<style>
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
</style>
