<script>
	// The workspace as a network: every ticket a dot coloured by its state,
	// "blocked by" links as arrows, decisions as small squares on their
	// ticket. Tickets settle by a small force layout: linked work pulls
	// together, everything else pushes apart. The rail on the right is
	// "Next up" (GET /api/next): the Ready tickets that hold up the most work,
	// with their chain lit on the map. Drag the map or a dot, ⌘ + scroll to
	// zoom, hover for a card, click to open the ticket.
	import { tick, untrack } from 'svelte';
	import { api } from '$lib/api.js';
	import { allIssues, states, blockLinks, projects, activeWorkspace } from '$lib/store.js';
	import { openIssue } from '$lib/ui.js';
	import StateIcon from './StateIcon.svelte';
	import { Plus, Minus, Maximize, Network, Sparkles, ChevronRight, ChevronLeft, Scale } from '@lucide/svelte';

	let decisions = $state([]);
	let next = $state([]);
	async function loadExtras() {
		const [d, n] = await Promise.all([api.get('/decisions').catch(() => null), api.get('/next?limit=8').catch(() => null)]);
		decisions = d || [];
		next = n || [];
	}
	// Load once the workspace is known (the page can mount before it is).
	$effect(() => {
		if ($activeWorkspace?.id) loadExtras();
	});
	// Moves and new links change what is next: refresh when the links or tickets change.
	let lastSig = '';
	$effect(() => {
		const sig = $blockLinks.length + ':' + $allIssues.map((i) => i.stateId).join('');
		if (lastSig && sig !== lastSig) loadExtras();
		lastSig = sig;
	});

	// ── filters ─────────────────────────────────────────────────────────
	let hiddenStates = $state(new Set());
	let statesInit = false;
	$effect(() => {
		// Canceled work is noise on the map: hidden until asked for.
		if (!statesInit && $states.length) {
			statesInit = true;
			hiddenStates = new Set($states.filter((s) => s.category === 'canceled').map((s) => s.id));
		}
	});
	function toggleState(id) {
		const s = new Set(hiddenStates);
		s.has(id) ? s.delete(id) : s.add(id);
		hiddenStates = s;
	}
	let edgeMode = $state('all'); // all | blocks | decisions
	let epic = $state('');
	let railOpen = $state(true);

	const stOf = (n) => $states.find((s) => s.id === n.stateId);
	const isClosed = (st) => st?.category === 'completed' || st?.category === 'canceled';

	// ── the graph ───────────────────────────────────────────────────────
	const R = 7; // ticket dot radius before its weight
	const model = $derived.by(() => {
		const shown = $allIssues.filter((i) => !hiddenStates.has(i.stateId) && (!epic || i.projectId === epic));
		const ids = new Set(shown.map((i) => i.id));
		const blocks = $blockLinks.filter((l) => ids.has(l.issueId) && ids.has(l.blockerId));
		const unblocks = new Map(); // blocker → count
		for (const l of blocks) unblocks.set(l.blockerId, (unblocks.get(l.blockerId) || 0) + 1);
		const dec = decisions.filter((d) => ids.has(d.issueId));
		const decCount = new Map();
		for (const d of dec) decCount.set(d.issueId, (decCount.get(d.issueId) || 0) + 1);
		// Each epic is an island: its tickets gather around their own anchor.
		const groupOf = new Map(); // node id → epic id ('' = no epic)
		const groups = new Map(); // epic id → ticket ids
		for (const i of shown) {
			const g = i.projectId || '';
			groupOf.set(i.id, g);
			(groups.get(g) || groups.set(g, []).get(g)).push(i.id);
		}
		for (const d of dec) groupOf.set('d' + d.id, groupOf.get(d.issueId));
		return { shown, blocks, dec, unblocks, decCount, groupOf, groups };
	});

	// Positions live outside the reactive model so a re-filter keeps the dots
	// where they were and only new ones are placed.
	const pos = new Map(); // id → {x, y, vx, vy, fixed}
	let frame = $state(0); // bumped when positions change
	let alpha = 0;
	let raf = 0;

	// Epic anchors on a ring, the largest epic first; tickets with no epic in the middle.
	let anchors = new Map();
	function placeAnchors(groups) {
		const named = [...groups.entries()].filter(([g]) => g).sort((a, b) => b[1].length - a[1].length);
		const ring = named.length > 1 ? 120 + 45 * named.length : 0;
		anchors = new Map([['', { x: 0, y: 0 }]]);
		named.forEach(([g], k) => {
			const a = (k / named.length) * Math.PI * 2 - Math.PI / 2;
			anchors.set(g, { x: Math.cos(a) * ring * 1.3, y: Math.sin(a) * ring });
		});
	}
	function seed(id, near) {
		if (pos.has(id)) return;
		const p = (near && pos.get(near)) || anchors.get(model.groupOf.get(id) || '') || { x: 0, y: 0 };
		const a = Math.random() * Math.PI * 2, r = near ? 30 : 60 * Math.sqrt(Math.random());
		pos.set(id, { x: p.x + Math.cos(a) * r, y: p.y + Math.sin(a) * r, vx: 0, vy: 0, fixed: false });
	}

	function stepLayout() {
		const m = model;
		const nodes = [
			...m.shown.map((i) => ({ id: i.id, w: 1 })),
			...m.dec.map((d) => ({ id: 'd' + d.id, w: 0.35 }))
		];
		const P = nodes.map((n) => pos.get(n.id) || { x: 0, y: 0, vx: 0, vy: 0, fixed: true });
		// repulsion: every pair pushes apart, stronger when close
		for (let i = 0; i < P.length; i++) {
			const a = P[i];
			for (let j = i + 1; j < P.length; j++) {
				const b = P[j];
				let dx = a.x - b.x, dy = a.y - b.y;
				let d2 = dx * dx + dy * dy;
				if (d2 > 90000) continue;
				if (d2 < 1) (dx = Math.random() - 0.5, dy = Math.random() - 0.5, (d2 = 1));
				const f = (500 / d2) * alpha;
				a.vx += dx * f; a.vy += dy * f;
				b.vx -= dx * f; b.vy -= dy * f;
			}
		}
		// springs: a blocked-by link wants 70px, a decision sits 26px from its ticket
		const spring = (ia, ib, len, k) => {
			const a = pos.get(ia), b = pos.get(ib);
			if (!a || !b) return;
			const dx = b.x - a.x, dy = b.y - a.y, d = Math.hypot(dx, dy) || 1;
			const f = ((d - len) / d) * k * alpha;
			a.vx += dx * f; a.vy += dy * f;
			b.vx -= dx * f; b.vy -= dy * f;
		};
		for (const l of m.blocks) spring(l.blockerId, l.issueId, 70, 0.08);
		for (const d of m.dec) spring(d.issueId, 'd' + d.id, 26, 0.3);
		// each ticket is pulled to its epic's anchor, so epics form islands
		for (let i = 0; i < P.length; i++) {
			const p = P[i];
			const an = anchors.get(m.groupOf.get(nodes[i].id) || '') || { x: 0, y: 0 };
			p.vx -= (p.x - an.x) * 0.06 * alpha;
			p.vy -= (p.y - an.y) * 0.06 * alpha;
			if (p.fixed) (p.vx = 0), (p.vy = 0);
			p.x += p.vx;
			p.y += p.vy;
			p.vx *= 0.6;
			p.vy *= 0.6;
		}
	}

	function run(heat = 1, settle = 0) {
		alpha = Math.max(alpha, heat);
		for (let i = 0; i < settle; i++) (stepLayout(), (alpha *= 0.985));
		frame++;
		if (raf) return;
		const loop = () => {
			stepLayout();
			alpha *= 0.96;
			frame++;
			raf = alpha > 0.01 ? requestAnimationFrame(loop) : 0;
			// Until someone moves the map, keep the whole network in view.
			if (!raf && !userMoved) fit();
		};
		raf = requestAnimationFrame(loop);
	}

	let placedOnce = false;
	$effect(() => {
		const m = model;
		// The layout writes `frame`; untracked, so it never re-runs this effect.
		untrack(() => {
			placeAnchors(m.groups);
			for (const l of m.blocks) (seed(l.blockerId), seed(l.issueId, l.blockerId));
			for (const i of m.shown) seed(i.id);
			for (const d of m.dec) seed('d' + d.id, d.issueId);
			if (!placedOnce && m.shown.length) {
				placedOnce = true;
				run(1, 400); // settle before the first paint, then fit
				tick().then(fit);
			} else run(0.3);
		});
	});

	// ── chain highlight (hover a ticket, or a Next-up row) ──────────────
	let hover = $state('');
	let litKeys = $state(null); // Set of keys on a Next-up chain
	const byKey = $derived(new Map($allIssues.map((i) => [i.key, i])));
	const nextIds = $derived(new Set(next.map((n) => n.id)));
	let focusId = $state(''); // the ticket found by search, lit until cleared
	const neighbours = $derived.by(() => {
		const f = hover || focusId;
		if (!f) return null;
		const s = new Set([f]);
		for (const l of model.blocks) {
			if (l.issueId === f) s.add(l.blockerId);
			if (l.blockerId === f) s.add(l.issueId);
		}
		return s;
	});
	let query = $state('');
	function find() {
		const q = query.trim().toLowerCase();
		if (!q) return (focusId = '');
		const hit =
			model.shown.find((i) => i.key.toLowerCase() === q) ||
			model.shown.find((i) => i.key.toLowerCase().includes(q) || i.title.toLowerCase().includes(q));
		if (!hit || !view) return (focusId = '');
		focusId = hit.id;
		const p = pos.get(hit.id), r = view.getBoundingClientRect();
		userMoved = true;
		zoom = Math.max(zoom, 1.2);
		tx = r.width / 2 - p.x * zoom;
		ty = r.height / 2 - p.y * zoom;
	}
	function searchKey(e) {
		if (e.key === 'Enter') find();
		else if (e.key === 'Escape') ((query = ''), (focusId = ''));
	}
	const litIds = $derived(litKeys ? new Set([...litKeys].map((k) => byKey.get(k)?.id).filter(Boolean)) : null);
	const isDim = (id) => (neighbours && !neighbours.has(id)) || (litIds && !litIds.has(id));
	const edgeLit = (l) =>
		(neighbours && (l.issueId === (hover || focusId) || l.blockerId === (hover || focusId))) || (litIds && litIds.has(l.issueId) && litIds.has(l.blockerId));

	// ── pan, zoom, drag a dot ───────────────────────────────────────────
	let view = $state(null);
	let tx = $state(0), ty = $state(0), zoom = $state(1);
	let drag = null; // {kind:'map'|'node', id, x, y, tx, ty, moved}
	let userMoved = false; // the map was panned or zoomed by hand
	let dragging = $state(false);
	const clampZoom = (z) => Math.min(3, Math.max(0.2, z));
	const toWorld = (cx, cy) => ({ x: (cx - tx) / zoom, y: (cy - ty) / zoom });

	function downMap(e) {
		if (e.button !== 0) return;
		drag = { kind: 'map', x: e.clientX, y: e.clientY, tx, ty, moved: false };
	}
	function downNode(e, id) {
		if (e.button !== 0) return;
		e.stopPropagation();
		drag = { kind: 'node', id, x: e.clientX, y: e.clientY, moved: false };
	}
	function onMove(e) {
		if (!drag) return;
		const dx = e.clientX - drag.x, dy = e.clientY - drag.y;
		if (!drag.moved && Math.hypot(dx, dy) < 4) return;
		drag.moved = true;
		dragging = true;
		if (drag.kind === 'map') {
			userMoved = true;
			tx = drag.tx + dx;
			ty = drag.ty + dy;
		} else {
			const r = view.getBoundingClientRect();
			const w = toWorld(e.clientX - r.left, e.clientY - r.top);
			const p = pos.get(drag.id);
			if (p) Object.assign(p, { x: w.x, y: w.y, vx: 0, vy: 0, fixed: true });
			run(0.25);
		}
	}
	function onUp() {
		if (drag?.kind === 'node') {
			const p = pos.get(drag.id);
			if (p) p.fixed = false;
			if (!drag.moved) {
				const is = $allIssues.find((i) => i.id === drag.id);
				if (is) openIssue(is.key);
			}
		}
		setTimeout(() => (dragging = false), 0);
		drag = null;
	}
	function zoomAt(z, cx, cy) {
		const nz = clampZoom(z);
		tx = cx - ((cx - tx) * nz) / zoom;
		ty = cy - ((cy - ty) * nz) / zoom;
		zoom = nz;
	}
	function onWheel(e) {
		e.preventDefault();
		userMoved = true;
		const r = view.getBoundingClientRect();
		if (e.ctrlKey || e.metaKey) zoomAt(zoom * Math.exp(-e.deltaY * 0.01), e.clientX - r.left, e.clientY - r.top);
		else (tx -= e.deltaX), (ty -= e.deltaY);
	}
	$effect(() => {
		if (!view) return;
		view.addEventListener('wheel', onWheel, { passive: false });
		return () => view.removeEventListener('wheel', onWheel);
	});
	function step(f) {
		userMoved = true;
		const r = view.getBoundingClientRect();
		zoomAt(zoom * f, r.width / 2, r.height / 2);
	}
	function fit() {
		if (!view) return;
		const ids = [...model.shown.map((i) => i.id), ...model.dec.map((d) => 'd' + d.id)];
		if (!ids.length) return;
		let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
		for (const id of ids) {
			const p = pos.get(id);
			if (!p) continue;
			x0 = Math.min(x0, p.x); y0 = Math.min(y0, p.y);
			x1 = Math.max(x1, p.x); y1 = Math.max(y1, p.y);
		}
		// Leave room for the labels, and for the legend along the bottom.
		const r = view.getBoundingClientRect();
		const top = 44, bottom = 84, side = 48;
		const w = x1 - x0 || 1, h = y1 - y0 || 1;
		zoom = clampZoom(Math.min(1.6, (r.width - 2 * side) / w, (r.height - top - bottom) / h));
		tx = r.width / 2 - ((x0 + x1) / 2) * zoom;
		ty = top + (r.height - top - bottom) / 2 - ((y0 + y1) / 2) * zoom;
	}
	function onKey(e) {
		if (e.key === '+' || e.key === '=') step(1.2);
		else if (e.key === '-') step(1 / 1.2);
		else if (e.key === '0') fit();
		else if (e.key === 'Escape') ((litKeys = null), (focusId = ''), (query = ''));
	}

	// ── what a dot shows ────────────────────────────────────────────────
	const radius = (id) => R + Math.min(7, (model.unblocks.get(id) || 0) * 1.5);
	// A fresh object each frame: the layout moves the stored point in place,
	// so handing it out directly would look unchanged to the template.
	const P = (id) => {
		frame;
		const p = pos.get(id);
		return p ? { x: p.x, y: p.y } : { x: 0, y: 0 };
	};
	function arrow(l) {
		const a = P(l.blockerId), b = P(l.issueId);
		const dx = b.x - a.x, dy = b.y - a.y, d = Math.hypot(dx, dy) || 1;
		const ra = radius(l.blockerId) + 2, rb = radius(l.issueId) + 5;
		return { x1: a.x + (dx / d) * ra, y1: a.y + (dy / d) * ra, x2: b.x - (dx / d) * rb, y2: b.y - (dy / d) * rb };
	}
	// The box around each epic's island, with its name on top.
	const hulls = $derived.by(() => {
		frame;
		if (epic || model.groups.size < 2) return [];
		const out = [];
		for (const [g, ids] of model.groups) {
			if (!g) continue;
			let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
			for (const id of ids) {
				const p = pos.get(id);
				if (!p) continue;
				const r = radius(id);
				x0 = Math.min(x0, p.x - r); y0 = Math.min(y0, p.y - r);
				x1 = Math.max(x1, p.x + r + 44); y1 = Math.max(y1, p.y + r);
			}
			if (x0 === Infinity) continue;
			out.push({ id: g, name: $projects.find((p) => p.id === g)?.name || 'Epic', x: x0 - 16, y: y0 - 30, w: x1 - x0 + 32, h: y1 - y0 + 46 });
		}
		return out;
	});
	const hoverIssue = $derived(hover ? $allIssues.find((i) => i.id === hover) : null);
	const waitsOn = (id) => model.blocks.filter((l) => l.issueId === id && !l.done).length;
	const shownStates = $derived($states.filter((s) => $allIssues.some((i) => i.stateId === s.id)));
	const countIn = (sid) => $allIssues.filter((i) => i.stateId === sid && (!epic || i.projectId === epic)).length;
	let cardAt = $state({ x: 0, y: 0 });
	function enter(e, id) {
		hover = id;
		const r = view.getBoundingClientRect();
		cardAt = { x: e.clientX - r.left + 14, y: e.clientY - r.top + 14 };
	}
</script>

<svelte:window onmouseup={onUp} onmousemove={onMove} />

<div class="ng">
	<div class="bar">
		<div class="seg" role="group" aria-label="Links shown">
			{#each [['all', 'All links'], ['blocks', 'Blocked by'], ['decisions', 'Decisions']] as [k, label] (k)}
				<button class:on={edgeMode === k} aria-pressed={edgeMode === k} onclick={() => (edgeMode = k)}>{label}</button>
			{/each}
		</div>
		<select class="select epic" bind:value={epic} aria-label="Epic">
			<option value="">All epics</option>
			{#each $projects as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
		</select>
		<input
			class="input find"
			type="search"
			placeholder="Find a ticket…"
			aria-label="Find a ticket"
			bind:value={query}
			oninput={find}
			onkeydown={searchKey}
		/>
		<span class="spacer"></span>
		<span class="count">{model.shown.length} tickets · {model.blocks.length} links · {model.dec.length} decisions</span>
	</div>

	<div class="body">
		{#if !model.shown.length}
			<div class="empty">
				<div class="ic"><Network size={16} strokeWidth={1.8} /></div>
				<p class="etitle">Nothing to show</p>
				<p class="esub">No tickets in the states and epic picked. Turn a state on in the legend.</p>
			</div>
		{:else}
			<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
			<div
				class="view"
				class:grabbing={dragging}
				bind:this={view}
				onmousedown={downMap}
				onkeydown={onKey}
				tabindex="0"
				role="application"
				aria-label="Ticket network"
			>
				<svg class="map" aria-hidden="true">
					<defs>
						<marker id="ng-a" viewBox="0 0 8 8" refX="6" refY="4" markerWidth="6" markerHeight="6" orient="auto"><path d="M0,0 L8,4 L0,8 z" class="ah" /></marker>
						<marker id="ng-lit" viewBox="0 0 8 8" refX="6" refY="4" markerWidth="6" markerHeight="6" orient="auto"><path d="M0,0 L8,4 L0,8 z" class="ah lit" /></marker>
					</defs>
					<g transform="translate({tx} {ty}) scale({zoom})">
						{#each hulls as h (h.id)}
							<rect x={h.x} y={h.y} width={h.w} height={h.h} rx="14" class="hull" />
							<text x={h.x + 12} y={h.y + 18} class="hl">{h.name}</text>
						{/each}
						{#if edgeMode !== 'blocks'}
							{#each model.dec as d (d.id)}
								{@const a = P(d.issueId)}
								{@const b = P('d' + d.id)}
								<line x1={a.x} y1={a.y} x2={b.x} y2={b.y} class="dl" class:dim={isDim(d.issueId)} />
							{/each}
						{/if}
						{#if edgeMode !== 'decisions'}
							{#each model.blocks as l (l.blockerId + l.issueId)}
								{@const g = arrow(l)}
								{@const lit = edgeLit(l)}
								<line
									x1={g.x1} y1={g.y1} x2={g.x2} y2={g.y2}
									class="bl" class:done={l.done} class:lit class:dim={!lit && (neighbours || litIds)}
									marker-end="url(#ng-{lit ? 'lit' : 'a'})"
								/>
							{/each}
						{/if}
						{#if edgeMode !== 'blocks'}
							{#each model.dec as d (d.id)}
								{@const b = P('d' + d.id)}
								<rect x={b.x - 4} y={b.y - 4} width="8" height="8" rx="2" class="dq" class:dim={isDim(d.issueId)}>
									<title>Decision: {d.bodyMd.slice(0, 160)}</title>
								</rect>
							{/each}
						{/if}
						{#each model.shown as i (i.id)}
							{@const p = P(i.id)}
							{@const st = stOf(i)}
							{@const r = radius(i.id)}
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<g
								class="nd"
								class:dim={isDim(i.id)}
								class:closed={isClosed(st)}
								onmousedown={(e) => downNode(e, i.id)}
								onmouseenter={(e) => enter(e, i.id)}
								onmousemove={(e) => enter(e, i.id)}
								onmouseleave={() => (hover = '')}
							>
								{#if nextIds.has(i.id)}<circle cx={p.x} cy={p.y} r={r + 5} class="ring" />{/if}
								{#if focusId === i.id}<circle cx={p.x} cy={p.y} r={r + 8} class="found" />{/if}
								<circle cx={p.x} cy={p.y} {r} style:fill={st?.color || 'var(--line-strong)'} class="dot" />
								{#if zoom > 0.6 || hover === i.id || focusId === i.id || nextIds.has(i.id)}
									<text x={p.x + r + 4} y={p.y + 3.5} class="lbl">{i.key}</text>
								{/if}
							</g>
						{/each}
					</g>
				</svg>

				{#if hoverIssue && !dragging}
					{@const st = stOf(hoverIssue)}
					<div class="card" style:left="{cardAt.x}px" style:top="{cardAt.y}px">
						<div class="ch"><StateIcon category={st?.category} color={st?.color} name={st?.name} /><span class="key">{hoverIssue.key}</span><span class="stn">{st?.name || ''}</span></div>
						<div class="ct">{hoverIssue.title}</div>
						<div class="cm">
							<span>Waits on <b>{waitsOn(hoverIssue.id)}</b></span>
							<span>Unblocks <b>{model.unblocks.get(hoverIssue.id) || 0}</b></span>
							<span>Decisions <b>{model.decCount.get(hoverIssue.id) || 0}</b></span>
						</div>
					</div>
				{/if}

				<div class="legend" role="group" aria-label="States shown">
					{#each shownStates as s (s.id)}
						<button class="chip" class:off={hiddenStates.has(s.id)} aria-pressed={!hiddenStates.has(s.id)} onclick={() => toggleState(s.id)}>
							<span class="sw" style:background={s.color}></span>{s.name}<span class="n">{countIn(s.id)}</span>
						</button>
					{/each}
					<span class="sep"></span>
					<span class="key-item"><span class="sq"></span>Decision</span>
					<span class="key-item"><span class="rg"></span>Next up</span>
				</div>

				<div class="zoom" role="group" aria-label="Zoom">
					<button onclick={() => step(1.2)} aria-label="Zoom in" title="Zoom in (+)"><Plus size={14} /></button>
					<span class="zv">{Math.round(zoom * 100)}%</span>
					<button onclick={() => step(1 / 1.2)} aria-label="Zoom out" title="Zoom out (−)"><Minus size={14} /></button>
					<button onclick={fit} aria-label="Fit to screen" title="Fit (0)"><Maximize size={13} /></button>
				</div>
			</div>
		{/if}

		<aside class="rail" class:closed={!railOpen} aria-label="Next up">
			<button class="rh" onclick={() => (railOpen = !railOpen)} aria-expanded={railOpen}>
				{#if railOpen}<Sparkles size={13} strokeWidth={2} /><span>Next up</span><span class="spacer"></span><ChevronRight size={14} />{:else}<ChevronLeft size={14} />{/if}
			</button>
			{#if railOpen}
				<p class="rs">Ready, nothing in the way. Longest chain of work first.</p>
				{#each next as n, idx (n.id)}
					<button
						class="nx"
						class:on={litKeys && litKeys.has(n.key)}
						onmouseenter={() => (litKeys = new Set(n.chainKeys))}
						onmouseleave={() => (litKeys = null)}
						onclick={() => openIssue(n.key)}
					>
						<span class="ni">{idx + 1}</span>
						<span class="nb">
							<span class="nk">{n.key}</span>
							<span class="nt">{n.title}</span>
							<span class="nm">
								{#if n.chain > 1}chain {n.chain} · unlocks {n.unlocks}{:else}nothing waits on it{/if}
							</span>
						</span>
					</button>
				{:else}
					<p class="none">No Ready ticket is free to start.</p>
				{/each}
				<div class="rf"><Scale size={12} strokeWidth={2} />Squares are decisions: record one with a comment of kind “decision”.</div>
			{/if}
		</aside>
	</div>
</div>

<style>
	.ng {
		height: 100%;
		display: flex;
		flex-direction: column;
		min-height: 0;
	}
	.bar {
		display: flex;
		align-items: center;
		gap: var(--s3);
		padding: var(--s2) var(--s4);
		border-bottom: 1px solid var(--line);
		flex-wrap: wrap;
	}
	.seg {
		display: inline-flex;
		border: 1px solid var(--line);
		border-radius: var(--r-sm);
		overflow: hidden;
	}
	.seg button {
		font: inherit;
		font-size: var(--t-sm);
		background: var(--surface);
		color: var(--ink-2);
		border: 0;
		border-left: 1px solid var(--line);
		padding: var(--s1) var(--s3);
		cursor: pointer;
	}
	.seg button:first-child {
		border-left: 0;
	}
	.seg button.on {
		background: var(--accent-soft);
		color: var(--ink);
		font-weight: 500;
	}
	.epic {
		width: auto;
		max-width: 220px;
	}
	.find {
		width: 200px;
	}
	.count {
		font-size: var(--t-xs);
		color: var(--ink-3);
	}
	.spacer {
		flex: 1;
	}
	.body {
		flex: 1;
		min-height: 0;
		display: flex;
	}
	.view {
		position: relative;
		flex: 1;
		min-width: 0;
		overflow: hidden;
		background: var(--paper);
		cursor: grab;
		outline: none;
	}
	.view.grabbing {
		cursor: grabbing;
	}
	.map {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
	}
	.bl {
		stroke: var(--ink-3);
		stroke-width: 1.3;
		transition: opacity var(--dur) var(--ease);
	}
	.bl.done {
		stroke: var(--line-strong);
		stroke-dasharray: 3 3;
	}
	.bl.lit {
		stroke: var(--accent);
		stroke-width: 2.2;
	}
	.ah {
		fill: var(--ink-3);
	}
	.ah.lit {
		fill: var(--accent);
	}
	.dl {
		stroke: var(--st-review);
		stroke-width: 1;
		opacity: 0.6;
	}
	.dq {
		fill: var(--st-review);
	}
	.nd {
		cursor: pointer;
		transition: opacity var(--dur) var(--ease);
	}
	.dot {
		stroke: var(--surface);
		stroke-width: 2;
	}
	.nd.closed .dot {
		opacity: 0.45;
	}
	.nd:hover .dot {
		stroke: var(--ink);
	}
	.found {
		fill: none;
		stroke: var(--accent);
		stroke-width: 2.5;
	}
	.ring {
		fill: none;
		stroke: var(--accent);
		stroke-width: 2;
		stroke-dasharray: 3 2;
	}
	.lbl {
		font-family: var(--mono);
		font-size: var(--t-xs);
		fill: var(--ink-3);
		pointer-events: none;
	}
	.hull {
		fill: var(--sunken);
		stroke: var(--line);
		stroke-width: 1;
	}
	.hl {
		font-size: var(--t-xs);
		font-weight: 500;
		fill: var(--ink-2);
		letter-spacing: 0.02em;
		pointer-events: none;
	}
	.dim {
		opacity: 0.15;
	}
	.card {
		position: absolute;
		z-index: 3;
		max-width: 300px;
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--r-lg);
		box-shadow: var(--shadow-2);
		padding: var(--s3);
		pointer-events: none;
		display: flex;
		flex-direction: column;
		gap: var(--s1);
	}
	.ch {
		display: flex;
		align-items: center;
		gap: var(--s2);
		font-size: var(--t-xs);
	}
	.key {
		font-family: var(--mono);
		color: var(--ink-2);
	}
	.stn {
		color: var(--ink-3);
	}
	.ct {
		font-family: var(--serif);
		font-size: var(--t-base);
		color: var(--ink);
	}
	.cm {
		display: flex;
		gap: var(--s3);
		font-size: var(--t-xs);
		color: var(--ink-3);
	}
	.cm b {
		color: var(--ink);
		font-weight: 500;
	}
	.legend {
		position: absolute;
		left: var(--s3);
		bottom: var(--s3);
		display: flex;
		flex-wrap: wrap;
		gap: var(--s1) var(--s2);
		align-items: center;
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--r);
		padding: var(--s2) var(--s3);
		width: max-content;
		max-width: calc(100% - 160px);
	}
	.chip {
		font: inherit;
		font-size: var(--t-xs);
		display: inline-flex;
		align-items: center;
		gap: var(--s1);
		background: none;
		border: 0;
		color: var(--ink-2);
		cursor: pointer;
		padding: 0;
	}
	.chip.off {
		opacity: 0.4;
		text-decoration: line-through;
	}
	.chip .n {
		color: var(--ink-3);
	}
	.sw {
		width: 9px;
		height: 9px;
		border-radius: 50%;
	}
	.sep {
		width: 1px;
		height: 12px;
		background: var(--line);
	}
	.key-item {
		font-size: var(--t-xs);
		color: var(--ink-3);
		display: inline-flex;
		align-items: center;
		gap: var(--s1);
	}
	.sq {
		width: 8px;
		height: 8px;
		border-radius: var(--r-sm);
		background: var(--st-review);
	}
	.rg {
		width: 10px;
		height: 10px;
		border-radius: 50%;
		border: 1.5px dashed var(--accent);
	}
	.zoom {
		position: absolute;
		right: var(--s3);
		bottom: var(--s3);
		display: flex;
		align-items: center;
		gap: 2px;
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--r);
		padding: 2px;
	}
	.zoom button {
		display: grid;
		place-items: center;
		width: 26px;
		height: 26px;
		border: 0;
		background: none;
		color: var(--ink-2);
		border-radius: var(--r-sm);
		cursor: pointer;
	}
	.zoom button:hover {
		background: var(--hover);
	}
	.zv {
		font-size: var(--t-xs);
		color: var(--ink-3);
		min-width: 36px;
		text-align: center;
	}
	.rail {
		width: 270px;
		flex: none;
		border-left: 1px solid var(--line);
		background: var(--surface);
		display: flex;
		flex-direction: column;
		overflow: auto;
	}
	.rail.closed {
		width: 36px;
	}
	.rh {
		font: inherit;
		font-size: var(--t-sm);
		font-weight: 500;
		display: flex;
		align-items: center;
		gap: var(--s2);
		background: none;
		border: 0;
		border-bottom: 1px solid var(--line);
		color: var(--ink);
		padding: var(--s3);
		cursor: pointer;
		text-align: left;
	}
	.rs {
		margin: 0;
		padding: var(--s2) var(--s3);
		font-size: var(--t-xs);
		color: var(--ink-3);
	}
	.nx {
		font: inherit;
		display: flex;
		gap: var(--s2);
		align-items: flex-start;
		text-align: left;
		background: none;
		border: 0;
		border-top: 1px solid var(--line);
		padding: var(--s2) var(--s3);
		cursor: pointer;
		color: var(--ink);
	}
	.nx:hover,
	.nx.on {
		background: var(--accent-soft);
	}
	.ni {
		font-family: var(--mono);
		font-size: var(--t-xs);
		color: var(--accent);
		padding-top: 2px;
		min-width: 14px;
	}
	.nb {
		display: flex;
		flex-direction: column;
		min-width: 0;
		gap: 1px;
	}
	.nk {
		font-family: var(--mono);
		font-size: var(--t-xs);
		color: var(--ink-2);
	}
	.nt {
		font-size: var(--t-sm);
		overflow: hidden;
		text-overflow: ellipsis;
		display: -webkit-box;
		-webkit-line-clamp: 2;
		line-clamp: 2;
		-webkit-box-orient: vertical;
	}
	.nm {
		font-size: var(--t-xs);
		color: var(--ink-3);
	}
	.none {
		margin: 0;
		padding: var(--s3);
		font-size: var(--t-sm);
		color: var(--ink-3);
	}
	.rf {
		margin-top: auto;
		display: flex;
		gap: var(--s2);
		align-items: flex-start;
		padding: var(--s3);
		border-top: 1px solid var(--line);
		font-size: var(--t-xs);
		color: var(--ink-3);
	}
	.empty {
		flex: 1;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: var(--s2);
		color: var(--ink-3);
		text-align: center;
		padding: var(--s8);
	}
	.empty .ic {
		width: 32px;
		height: 32px;
		display: grid;
		place-items: center;
		border: 1px solid var(--line);
		border-radius: var(--r);
		color: var(--ink-2);
	}
	.etitle {
		margin: 0;
		color: var(--ink);
		font-weight: 500;
	}
	.esub {
		margin: 0;
		font-size: var(--t-sm);
		max-width: 360px;
	}
	@media (max-width: 760px) {
		.rail {
			display: none;
		}
	}
</style>
