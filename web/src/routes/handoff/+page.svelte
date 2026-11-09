<script>
	import PageHeader from '$components/PageHeader.svelte';
	import Markdown from '$components/Markdown.svelte';
	import { api, getWorkspace } from '$lib/api.js';
	import { initiatives, activeInitiative, aiName } from '$lib/store.js';
	import { showToast } from '$lib/ui.js';
	import { rel } from '$lib/format.js';
	import { lineDiff, diffStats } from '$lib/linediff.js';
	import { countLines, countChars, missingSections, saveBlocker, daysSince, outline } from '$lib/handoff.js';

	let iniId = $state('');
	let latest = $state(null); // newest version, with body; null = no handoff yet
	let versions = $state([]); // newest first, no bodies
	let shown = $state(null); // the version being read (the latest, or an older one with its body)
	let compare = $state(false);
	let loading = $state(false);
	let error = $state(null);

	let editing = $state(false);
	let draft = $state('');
	let summary = $state('');
	let saving = $state(false);
	let saveError = $state(null);

	// The format (sections, limits, rules) comes from the server, never a copy here.
	let fmt = $state(null);
	let rulesOpen = $state(true);
	let edEl = $state(null);
	let proseEl = $state(null);
	api.handoffFormat().then((f) => (fmt = f)).catch(() => {});

	const dLines = $derived(countLines(draft));
	const dChars = $derived(countChars(draft));
	const dMissing = $derived(fmt ? missingSections(draft, fmt.sections) : []);
	const blocker = $derived(!draft.trim() ? 'The handoff cannot be empty.' : saveBlocker(draft, fmt));
	const stale = $derived(latest && fmt ? daysSince(latest.createdAt) : 0);
	const isStale = $derived(!!fmt && stale > fmt.staleDays);
	const doneTickets = $derived(latest?.done_tickets ?? []);
	const toc = $derived(editing ? outline(draft) : shown ? outline(shown.bodyMd) : []);

	const who = (a) => (a === 'ai' ? $aiName : 'you');
	const abs = (iso) => (iso ? new Date(iso).toLocaleString() : '');
	const isOld = $derived(!!shown && !!latest && shown.version !== latest.version);
	const diff = $derived(isOld && compare ? lineDiff(shown.bodyMd, latest.bodyMd) : []);
	const stats = $derived(diffStats(diff));

	// The last project picked is remembered per browser and workspace.
	const pickKey = () => 'donewhen_handoff_project:' + (getWorkspace() || 'default');
	function remembered() {
		try {
			return localStorage.getItem(pickKey()) || '';
		} catch {
			return '';
		}
	}
	function pick(id) {
		iniId = id;
		try {
			localStorage.setItem(pickKey(), id);
		} catch {
			/* private mode: the choice just is not remembered */
		}
	}
	$effect(() => {
		const list = $initiatives;
		if (!list.length || list.some((i) => i.id === iniId)) return;
		const first = [remembered(), $activeInitiative, list[0].id].find((id) => id && list.some((i) => i.id === id));
		iniId = first || '';
	});

	let loadSeq = 0;
	async function load(id) {
		editing = false;
		compare = false;
		if (!id) return;
		const seq = ++loadSeq;
		loading = true;
		error = null;
		try {
			const [vs, head] = await Promise.all([
				api.handoffVersions(id),
				api.handoff(id).catch((e) => {
					if (e.status === 404) return null; // no handoff yet
					throw e;
				})
			]);
			if (seq !== loadSeq) return; // another project was picked meanwhile
			versions = vs || [];
			latest = head;
			shown = head;
		} catch (e) {
			if (seq !== loadSeq) return;
			error = e?.message || 'Failed to load the handoff.';
			latest = shown = null;
			versions = [];
		} finally {
			if (seq === loadSeq) loading = false;
		}
	}
	$effect(() => {
		load(iniId);
	});

	async function open(v) {
		compare = false;
		if (latest && v.version === latest.version) {
			shown = latest;
			return;
		}
		try {
			shown = await api.handoff(iniId, v.version);
		} catch (e) {
			showToast(e?.message || 'Could not load that version.');
		}
	}

	function startEdit(fromTemplate = false) {
		draft = fromTemplate ? (fmt?.template ?? '') : (latest?.bodyMd ?? '');
		summary = '';
		saveError = null;
		editing = true;
	}
	async function save() {
		if (blocker) return (saveError = blocker);
		if (!summary.trim()) return (saveError = 'Add a one-line summary of what changed.');
		saving = true;
		saveError = null;
		try {
			await api.saveHandoff(iniId, draft, summary.trim());
			const id = iniId;
			editing = false;
			await load(id);
			showToast('Handoff saved');
		} catch (e) {
			saveError = e?.message || 'Could not save.';
		} finally {
			saving = false;
		}
	}
	// Jump to a section: in the editor, select its heading; when reading, scroll to it.
	function jump(t) {
		if (editing && edEl) {
			edEl.focus();
			edEl.setSelectionRange(t.offset, t.offset + t.title.length + 3);
			const lh = parseFloat(getComputedStyle(edEl).lineHeight) || 18;
			edEl.scrollTop = Math.max(0, t.line * lh - 40);
		} else if (proseEl) {
			const h = [...proseEl.querySelectorAll('h1,h2,h3')].find((e) => e.textContent.trim() === t.title);
			h?.scrollIntoView({ block: 'start' });
		}
	}
	function editKey(e) {
		if (e.key === 'Escape') editing = false;
		else if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) save();
	}
</script>

{#snippet side(showHistory)}
	<aside class="hist" aria-label="Handoff guide">
		{#if fmt}
			<section class="rules">
				<button class="rh rtoggle" aria-expanded={rulesOpen} onclick={() => (rulesOpen = !rulesOpen)}>
					Rules <span aria-hidden="true">{rulesOpen ? '−' : '+'}</span>
				</button>
				{#if rulesOpen}
					<ul class="rl">
						{#each fmt.rules.split('\n') as line (line)}
							<li>{line}</li>
						{/each}
					</ul>
				{/if}
			</section>
		{/if}
		{#if toc.length}
			<h2 class="rh">Outline</h2>
			<ul class="toc">
				{#each toc as t (t.offset)}
					<li><button class="ver" onclick={() => jump(t)}>{t.title}</button></li>
				{/each}
			</ul>
		{/if}
		{#if showHistory}
			<h2 class="rh">History</h2>
			<ul>
				{#each versions as v (v.version)}
					<li>
						<button class="ver" class:on={shown.version === v.version} onclick={() => open(v)}>
							<span class="vn">v{v.version}{v.version === latest.version ? ' · latest' : ''}</span>
							<span class="vs">{v.summary}</span>
							<span class="vm">{who(v.actor)} · {rel(v.createdAt)}</span>
						</button>
					</li>
				{/each}
			</ul>
		{/if}
	</aside>
{/snippet}

<div class="pg">
	<PageHeader crumbs={[{ label: 'Handoff' }]} />
	<div class="pg-body">
		<div class="ho">
			<div class="ho-head">
				<h1 class="page-title">Handoff</h1>
				<select class="select" aria-label="Project" value={iniId} onchange={(e) => pick(e.currentTarget.value)}>
					{#each $initiatives as i (i.id)}
						<option value={i.id}>{i.name}</option>
					{/each}
				</select>
				{#if latest && !editing}
					<button class="btn" onclick={() => startEdit()}>Edit</button>
				{/if}
			</div>

			{#if error}
				<div class="note err">
					{error}
					<button class="btn ghost" onclick={() => load(iniId)}>Retry</button>
				</div>
			{:else if !$initiatives.length}
				<p class="note">No projects yet.</p>
			{:else if loading && !shown}
				<p class="note">Loading…</p>
			{:else if editing}
				<div class="ho-cols">
				<div class="ho-main">
					<!-- svelte-ignore a11y_autofocus -->
					<textarea class="textarea ed" bind:value={draft} bind:this={edEl} onkeydown={editKey} aria-label="Handoff Markdown" autofocus></textarea>
					{#if fmt}
						<p class="counts">
							<span class:over={dLines > fmt.maxLines}>{dLines} / {fmt.maxLines} lines</span>
							<span class:over={dChars > fmt.maxChars}>{dChars.toLocaleString()} / {fmt.maxChars.toLocaleString()} characters</span>
						</p>
						{#if dMissing.length}
							<p class="missing">Missing: {#each dMissing as m, n (m)}<code>{m}</code>{n < dMissing.length - 1 ? ' ' : ''}{/each}</p>
						{/if}
					{/if}
					<div class="savebar">
						<input
							class="input sum"
							bind:value={summary}
							maxlength="200"
							placeholder="One line: what changed in this version"
							aria-label="Summary"
							onkeydown={editKey}
						/>
						<button class="btn primary" disabled={saving || !!blocker} title={blocker} onclick={save}>{saving ? 'Saving…' : 'Save'}</button>
						<button class="btn ghost" disabled={saving} onclick={() => (editing = false)}>Cancel</button>
					</div>
					{#if blocker}<p class="saveerr">{blocker}</p>{:else if saveError}<p class="saveerr">{saveError}</p>{/if}
				</div>
				{@render side(false)}
				</div>
			{:else if !shown}
				<div class="note">
					<p>No handoff yet for this project.</p>
					<button class="btn primary" onclick={() => startEdit(true)} disabled={!fmt}>Start from template</button>
					<button class="btn ghost" onclick={() => startEdit()}>Start empty</button>
				</div>
			{:else}
				<div class="ho-cols">
					<div class="ho-main">
						<p class="meta">
							{#if isOld}Version {shown.version}, {/if}Updated <span title={abs(shown.createdAt)}>{rel(shown.createdAt)}</span> by {who(shown.actor)} — {shown.summary}
							{#if !isOld && isStale}<span class="pill stale" title={abs(latest.createdAt)}>Stale — updated {stale} days ago</span>{/if}
						</p>
						{#if !isOld && doneTickets.length}
							<div class="warnbox" role="status">
								<b>Done or canceled, still In flight:</b> {doneTickets.join(', ')}. Remove them from the next save.
							</div>
						{/if}
						{#if isOld}
							<div class="oldbar">
								<span>You are reading version {shown.version}. The latest is version {latest.version}.</span>
								<button class="btn" onclick={() => (compare = !compare)}>{compare ? 'Hide comparison' : 'Compare with latest'}</button>
								<button class="btn ghost" onclick={() => open(versions[0])}>Show latest</button>
							</div>
						{/if}
						{#if isOld && compare}
							<p class="meta">Version {shown.version} → latest: <span class="plus">+{stats.add}</span> <span class="minus">−{stats.del}</span> lines</p>
							<div class="diff" role="region" aria-label="Changes since version {shown.version}">
								{#each diff as row, n (n)}
									<div class="dl {row.type}">
										<span class="sign">{row.type === 'add' ? '+' : row.type === 'del' ? '−' : ''}</span>
										<span class="txt">{row.text}</span>
									</div>
								{/each}
							</div>
						{:else}
							<div class="prose" bind:this={proseEl}><Markdown source={shown.bodyMd} /></div>
						{/if}
					</div>
					{@render side(true)}
				</div>
			{/if}
		</div>
	</div>
</div>

<style>
	.ho {
		padding: 14px clamp(16px, 4vw, 40px) 40px;
		max-width: 1180px;
	}
	.ho-head {
		display: flex;
		align-items: center;
		gap: 12px;
		margin-bottom: 14px;
	}
	.ho-head .select {
		width: auto;
		min-width: 160px;
	}
	.ho-cols {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 260px;
		gap: 28px;
		align-items: start;
	}
	@media (max-width: 800px) {
		.ho-cols {
			grid-template-columns: minmax(0, 1fr);
		}
	}
	.ho-main {
		min-width: 0;
	}
	.meta {
		font-size: var(--t-sm);
		color: var(--ink-3);
		margin: 0 0 12px;
	}
	.plus {
		color: var(--st-done);
	}
	.minus {
		color: var(--danger);
	}
	.oldbar {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 8px;
		padding: 8px 12px;
		margin-bottom: 14px;
		border: 1px solid var(--line);
		border-radius: var(--r);
		background: var(--sunken);
		font-size: var(--t-sm);
		color: var(--ink-2);
	}
	.oldbar span {
		flex: 1;
		min-width: 200px;
	}
	.note {
		color: var(--ink-3);
		font-size: var(--t-base);
	}
	.note.err {
		color: var(--danger);
	}
	.ed {
		width: 100%;
		min-height: 60vh;
		font-family: var(--mono);
		font-size: var(--t-sm);
		line-height: 1.5;
	}
	.savebar {
		display: flex;
		gap: 8px;
		margin-top: 10px;
		flex-wrap: wrap;
	}
	.sum {
		flex: 1;
		min-width: 220px;
	}
	.saveerr {
		color: var(--danger);
		font-size: var(--t-sm);
		margin: 8px 0 0;
	}
	.diff {
		border: 1px solid var(--line);
		border-radius: var(--r);
		overflow: auto;
		font-family: var(--mono);
		font-size: var(--t-sm);
		line-height: 1.5;
	}
	.dl {
		display: flex;
		gap: 8px;
		padding: 0 10px;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.dl.add {
		background: var(--ok-soft);
	}
	.dl.del {
		background: var(--danger-soft);
	}
	.sign {
		flex: none;
		width: 1ch;
		color: var(--ink-3);
	}
	.txt {
		min-width: 0;
	}
	.hist {
		position: sticky;
		top: 0;
	}
	.hist ul {
		list-style: none;
		margin: 6px 0 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.rtoggle {
		display: flex;
		justify-content: space-between;
		width: 100%;
		background: none;
		border: 0;
		padding: 0;
		cursor: pointer;
	}
	.rules {
		border: 1px solid var(--line);
		border-radius: var(--r);
		padding: 8px 10px;
		margin-bottom: 14px;
	}
	.rl {
		margin-top: 8px !important;
		font-size: var(--t-xs);
		color: var(--ink-2);
		line-height: 1.45;
		gap: 4px !important;
	}
	.toc {
		margin-bottom: 14px !important;
	}
	.toc .ver {
		padding: 4px 10px;
		font-size: var(--t-sm);
	}
	.hist h2 + ul {
		margin-bottom: 14px;
	}
	.counts {
		display: flex;
		gap: 14px;
		margin: 6px 0 0;
		font-family: var(--mono);
		font-size: var(--t-xs);
		color: var(--ink-3);
	}
	.counts .over {
		color: var(--danger);
	}
	.missing {
		margin: 6px 0 0;
		font-size: var(--t-sm);
		color: var(--danger);
	}
	.missing code {
		margin-right: 6px;
	}
	.pill.stale {
		margin-left: 6px;
		color: var(--st-progress);
		border-color: var(--st-progress);
	}
	.warnbox {
		margin: 0 0 12px;
		padding: 8px 12px;
		border: 1px solid var(--st-progress);
		border-radius: var(--r);
		font-size: var(--t-sm);
		color: var(--ink-2);
	}
	.rh {
		font-family: var(--mono);
		font-size: var(--t-xs);
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--ink-3);
		font-weight: 500;
		margin: 0;
	}
	.ver {
		display: flex;
		flex-direction: column;
		gap: 2px;
		width: 100%;
		text-align: left;
		padding: 7px 10px;
		border-radius: var(--r-sm);
		border: 1px solid transparent;
		background: none;
		color: var(--ink);
	}
	.ver:hover {
		background: var(--hover);
	}
	.ver.on {
		background: var(--accent-soft);
		border-color: var(--line);
	}
	.vn {
		font-size: var(--t-sm);
		font-weight: 500;
	}
	.vs {
		font-size: var(--t-sm);
		color: var(--ink-2);
		overflow-wrap: anywhere;
	}
	.vm {
		font-size: var(--t-xs);
		color: var(--ink-3);
	}
</style>
