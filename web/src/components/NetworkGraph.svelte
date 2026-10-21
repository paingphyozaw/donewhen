<script>
	// The workspace as a living network, drawn on a canvas. Every ticket is a
	// glowing dot in its state's colour, sized by how much work waits on it;
	// "blocked by" links are soft curves with small dots flowing from the
	// blocker to the ticket it holds up; decisions are small squares on their
	// ticket. A force layout keeps linked work together and lets the rest
	// breathe. Hover a ticket to light its chain, drag the map or a dot,
	// ⌘ + scroll to zoom, click to open. The rail on the right is "Next up"
	// (GET /api/next): the Ready tickets that hold up the most work.
	import { tick, untrack } from 'svelte';
	import { api } from '$lib/api.js';
	import { allIssues, states, blockLinks, projects, activeWorkspace } from '$lib/store.js';
	import { openIssue } from '$lib/ui.js';
	import StateIcon from './StateIcon.svelte';
	import { Plus, Minus, Maximize, Network, Sparkles, ChevronRight, ChevronLeft, Scale } from '@lucide/svelte';

	// ── data ────────────────────────────────────────────────────────────
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
	// Moves and new links change what is next.
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
		// Finished work is noise on the map: Done and Canceled start hidden.
		if (!statesInit && $states.length) {
			statesInit = true;
			hiddenStates = new Set($states.filter((s) => s.category === 'canceled' || s.category === 'completed').map((s) => s.id));
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

	const model = $derived.by(() => {
		const shown = $allIssues.filter((i) => !hiddenStates.has(i.stateId) && (!epic || i.projectId === epic));
		const ids = new Set(shown.map((i) => i.id));
		const blocks = $blockLinks.filter((l) => ids.has(l.issueId) && ids.has(l.blockerId));
		const unblocks = new Map();
		for (const l of blocks) unblocks.set(l.blockerId, (unblocks.get(l.blockerId) || 0) + 1);
		const dec = decisions.filter((d) => ids.has(d.issueId));
		const decCount = new Map();
		for (const d of dec) decCount.set(d.issueId, (decCount.get(d.issueId) || 0) + 1);
		const stateById = new Map($states.map((s) => [s.id, s]));
		const colour = new Map(shown.map((i) => [i.id, stateById.get(i.stateId)?.color || '#8a8a8a']));
		return { shown, blocks, dec, unblocks, decCount, stateById, colour };
	});

	// ── layout (plain objects, outside Svelte's reactivity) ─────────────
	// id → {x, y, vx, vy, fixed, born, glow, target}; a re-filter keeps the dots where they were.
	const pos = new Map();
	let alpha = 0;
	const radius = (id) => 5 + Math.min(9, (model.unblocks.get(id) || 0) * 1.6);

	// Tickets of one epic drift toward the same spot, without any drawn shape.
	let anchors = new Map();
	function placeAnchors(shown) {
		const groups = [...new Set(shown.map((i) => i.projectId || ''))];
		const ring = groups.length > 1 ? 90 + 18 * Math.sqrt(shown.length) : 0;
		anchors = new Map(
			groups.map((g, k) => {
				const a = (k / groups.length) * Math.PI * 2;
				return [g, { x: Math.cos(a) * ring * 1.3, y: Math.sin(a) * ring }];
			})
		);
	}
	function seed(id, near, group) {
		if (pos.has(id)) return;
		const p = (near && pos.get(near)) || anchors.get(group || '') || { x: 0, y: 0 };
		const a = Math.random() * Math.PI * 2, r = near ? 18 : 50 * Math.sqrt(Math.random());
		pos.set(id, { x: p.x + Math.cos(a) * r, y: p.y + Math.sin(a) * r, vx: 0, vy: 0, fixed: false, born: performance.now(), glow: 0, target: 0 });
	}

	function stepLayout() {
		const m = model;
		const items = [];
		for (const i of m.shown) items.push([pos.get(i.id), i.projectId || '']);
		for (const d of m.dec) items.push([pos.get('d' + d.id), null]);
		// repulsion: every pair pushes apart, stronger when close
		for (let i = 0; i < items.length; i++) {
			const a = items[i][0];
			if (!a) continue;
			for (let j = i + 1; j < items.length; j++) {
				const b = items[j][0];
				if (!b) continue;
				let dx = a.x - b.x, dy = a.y - b.y;
				let d2 = dx * dx + dy * dy;
				if (d2 > 62500) continue;
				if (d2 < 1) (dx = Math.random() - 0.5), (dy = Math.random() - 0.5), (d2 = 1);
				const f = (420 / d2) * alpha;
				a.vx += dx * f; a.vy += dy * f;
				b.vx -= dx * f; b.vy -= dy * f;
			}
		}
		const spring = (ia, ib, len, k) => {
			const a = pos.get(ia), b = pos.get(ib);
			if (!a || !b) return;
			const dx = b.x - a.x, dy = b.y - a.y, d = Math.hypot(dx, dy) || 1;
			const f = ((d - len) / d) * k * alpha;
			a.vx += dx * f; a.vy += dy * f;
			b.vx -= dx * f; b.vy -= dy * f;
		};
		for (const l of m.blocks) spring(l.blockerId, l.issueId, 55, 0.06);
		for (const d of m.dec) spring(d.issueId, 'd' + d.id, 22, 0.3);
		for (const [p, g] of items) {
			if (!p) continue;
			const an = g === null ? null : anchors.get(g);
			if (an) {
				p.vx -= (p.x - an.x) * 0.012 * alpha;
				p.vy -= (p.y - an.y) * 0.012 * alpha;
			}
			if (p.fixed) (p.vx = 0), (p.vy = 0);
			p.x += p.vx;
			p.y += p.vy;
			p.vx *= 0.58;
			p.vy *= 0.58;
		}
	}
	function heat(a, settle = 0) {
		alpha = Math.max(alpha, a);
		for (let i = 0; i < settle; i++) (stepLayout(), (alpha *= 0.985));
		wake();
	}

	let placedOnce = false;
	$effect(() => {
		const m = model;
		// The layout mutates plain objects; untracked, so it never re-runs this effect.
		untrack(() => {
			placeAnchors(m.shown);
			for (const l of m.blocks) {
				const is = m.shown.find((i) => i.id === l.blockerId);
				seed(l.blockerId, null, is?.projectId);
				seed(l.issueId, l.blockerId);
			}
			for (const i of m.shown) seed(i.id, null, i.projectId);
			for (const d of m.dec) seed('d' + d.id, d.issueId);
			if (!placedOnce && m.shown.length) {
				placedOnce = true;
				heat(1, 420);
				tick().then(() => fit(false));
			} else heat(0.35);
		});
	});

	// ── focus: hover, search, a Next-up row ─────────────────────────────
	let hover = $state('');
	let focusId = $state('');
	let litKeys = $state(null);
	const byKey = $derived(new Map($allIssues.map((i) => [i.key, i])));
	const nextIds = $derived(new Set(next.map((n) => n.id)));
	// The ticket in focus and its whole chain: what it waits on, what it unblocks.
	const chain = $derived.by(() => {
		if (litKeys) return new Set([...litKeys].map((k) => byKey.get(k)?.id).filter(Boolean));
		const f = hover || focusId;
		if (!f) return null;
		const up = new Map(), down = new Map();
		for (const l of model.blocks) {
			(up.get(l.issueId) || up.set(l.issueId, []).get(l.issueId)).push(l.blockerId);
			(down.get(l.blockerId) || down.set(l.blockerId, []).get(l.blockerId)).push(l.issueId);
		}
		const s = new Set([f]);
		const walk = (id, m) => {
			for (const n of m.get(id) || []) if (!s.has(n)) (s.add(n), walk(n, m));
		};
		walk(f, up);
		walk(f, down);
		return s;
	});

	let query = $state('');
	function find() {
		const q = query.trim().toLowerCase();
		if (!q) return (focusId = '');
		const hit =
			model.shown.find((i) => i.key.toLowerCase() === q) ||
			model.shown.find((i) => i.key.toLowerCase().includes(q) || i.title.toLowerCase().includes(q));
		if (!hit) return (focusId = '');
		focusId = hit.id;
		centreOn(hit.id, Math.max(zoom, 1.3));
	}
	function searchKey(e) {
		if (e.key === 'Enter') find();
		else if (e.key === 'Escape') (query = ''), (focusId = '');
	}

	// ── camera ──────────────────────────────────────────────────────────
	let view = $state(null);
	let canvas = $state(null);
	let tx = $state(0), ty = $state(0), zoom = $state(1);
	let userMoved = false;
	let cam = null; // {tx, ty, zoom} the camera glides to
	const clampZoom = (z) => Math.min(4, Math.max(0.15, z));
	const toWorld = (cx, cy) => ({ x: (cx - tx) / zoom, y: (cy - ty) / zoom });
	function centreOn(id, z) {
		const p = pos.get(id);
		if (!p || !view) return;
		userMoved = true;
		const r = view.getBoundingClientRect();
		cam = { zoom: z, tx: r.width / 2 - p.x * z, ty: r.height / 2 - p.y * z };
		wake();
	}
	function fit(glide = false) {
		if (!view) return;
		let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
		for (const i of model.shown) {
			const p = pos.get(i.id);
			if (!p) continue;
			x0 = Math.min(x0, p.x); y0 = Math.min(y0, p.y);
			x1 = Math.max(x1, p.x); y1 = Math.max(y1, p.y);
		}
		if (x0 === Infinity) return;
		const r = view.getBoundingClientRect();
		const top = 30, bottom = 70, side = 40;
		const z = clampZoom(Math.min(2, (r.width - 2 * side) / (x1 - x0 || 1), (r.height - top - bottom) / (y1 - y0 || 1)));
		const t = { zoom: z, tx: r.width / 2 - ((x0 + x1) / 2) * z, ty: top + (r.height - top - bottom) / 2 - ((y0 + y1) / 2) * z };
		if (glide) cam = t;
		else (zoom = t.zoom), (tx = t.tx), (ty = t.ty);
		wake();
	}
	function zoomAt(z, cx, cy) {
		const nz = clampZoom(z);
		tx = cx - ((cx - tx) * nz) / zoom;
		ty = cy - ((cy - ty) * nz) / zoom;
		zoom = nz;
		cam = null;
		wake();
	}
	function step(f) {
		userMoved = true;
		const r = view.getBoundingClientRect();
		zoomAt(zoom * f, r.width / 2, r.height / 2);
	}

	// ── pointer ─────────────────────────────────────────────────────────
	let drag = null; // {kind:'map'|'node', id, x, y, tx, ty, moved}
	let dragging = $state(false);
	let cardAt = $state({ x: 0, y: 0 });
	function hitTest(cx, cy) {
		const w = toWorld(cx, cy);
		let best = '', bd = Infinity;
		for (const i of model.shown) {
			const p = pos.get(i.id);
			if (!p) continue;
			const d = Math.hypot(p.x - w.x, p.y - w.y);
			if (d < radius(i.id) + 6 / zoom && d < bd) (bd = d), (best = i.id);
		}
		return best;
	}
	function local(e) {
		const r = view.getBoundingClientRect();
		return { x: e.clientX - r.left, y: e.clientY - r.top };
	}
	function onDown(e) {
		if (e.button !== 0) return;
		const l = local(e);
		const id = hitTest(l.x, l.y);
		drag = id ? { kind: 'node', id, x: e.clientX, y: e.clientY, moved: false } : { kind: 'map', x: e.clientX, y: e.clientY, tx, ty, moved: false };
	}
	function onMove(e) {
		if (!view) return;
		if (!drag) {
			const l = local(e);
			const inside = l.x >= 0 && l.y >= 0 && l.x <= view.clientWidth && l.y <= view.clientHeight;
			const id = inside ? hitTest(l.x, l.y) : '';
			if (id !== hover) (hover = id), wake();
			if (id) cardAt = { x: l.x + 16, y: l.y + 16 };
			return;
		}
		const dx = e.clientX - drag.x, dy = e.clientY - drag.y;
		if (!drag.moved && Math.hypot(dx, dy) < 4) return;
		drag.moved = true;
		dragging = true;
		userMoved = true;
		if (drag.kind === 'map') {
			cam = null;
			tx = drag.tx + dx;
			ty = drag.ty + dy;
			wake();
		} else {
			const l = local(e);
			const w = toWorld(l.x, l.y);
			const p = pos.get(drag.id);
			if (p) Object.assign(p, { x: w.x, y: w.y, vx: 0, vy: 0, fixed: true });
			heat(0.3);
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
	function onWheel(e) {
		e.preventDefault();
		userMoved = true;
		const l = local(e);
		if (e.ctrlKey || e.metaKey) zoomAt(zoom * Math.exp(-e.deltaY * 0.01), l.x, l.y);
		else (tx -= e.deltaX), (ty -= e.deltaY), (cam = null), wake();
	}
	$effect(() => {
		if (!view) return;
		view.addEventListener('wheel', onWheel, { passive: false });
		return () => view.removeEventListener('wheel', onWheel);
	});
	function onKey(e) {
		if (e.key === '+' || e.key === '=') step(1.2);
		else if (e.key === '-') step(1 / 1.2);
		else if (e.key === '0') fit(true);
		else if (e.key === 'Escape') (litKeys = null), (focusId = ''), (query = '');
	}

	// ── drawing ─────────────────────────────────────────────────────────
	const reduceMotion = typeof matchMedia !== 'undefined' && matchMedia('(prefers-reduced-motion: reduce)').matches;
	let theme = {};
	function readTheme() {
		const cs = getComputedStyle(document.documentElement);
		const v = (n) => cs.getPropertyValue(n).trim();
		const set = document.documentElement.dataset.theme;
		theme = {
			line: v('--line'), ink: v('--ink'), ink3: v('--ink-3'), accent: v('--accent'), decision: v('--st-review'),
			mono: v('--mono'), surface: v('--surface'),
			dark: set === 'dark' || (set !== 'light' && matchMedia('(prefers-color-scheme: dark)').matches)
		};
	}
	// "#rrggbb" + alpha → rgba(); anything else is returned as is.
	function rgba(hex, a) {
		const m = /^#?([0-9a-f]{6})$/i.exec(hex || '');
		if (!m) return hex;
		const n = parseInt(m[1], 16);
		return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${a})`;
	}
	function lighten(hex, f) {
		const m = /^#?([0-9a-f]{6})$/i.exec(hex || '');
		if (!m) return hex;
		const n = parseInt(m[1], 16);
		const c = (s) => Math.round(((n >> s) & 255) + (255 - ((n >> s) & 255)) * f);
		return `rgb(${c(16)}, ${c(8)}, ${c(0)})`;
	}
	// A gentle curve from a to b; the bend has a fixed side so a pair never overlaps.
	function curve(a, b) {
		const dx = b.x - a.x, dy = b.y - a.y, d = Math.hypot(dx, dy) || 1;
		const bend = Math.min(40, d * 0.16);
		return { cx: (a.x + b.x) / 2 - (dy / d) * bend, cy: (a.y + b.y) / 2 + (dx / d) * bend, d };
	}
	const qpt = (a, c, b, t) => ({
		x: (1 - t) * (1 - t) * a.x + 2 * (1 - t) * t * c.cx + t * t * b.x,
		y: (1 - t) * (1 - t) * a.y + 2 * (1 - t) * t * c.cy + t * t * b.y
	});
	const hash = (s) => {
		let h = 0;
		for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
		return (h >>> 0) / 4294967295;
	};

	let raf = 0;
	let lastDraw = 0;
	let busy = true; // the layout, camera or a glow moved in the last frame
	function wake() {
		busy = true;
		if (!raf && typeof requestAnimationFrame !== 'undefined') raf = requestAnimationFrame(frame);
	}
	function frame(now) {
		raf = 0;
		if (!canvas || !view) return;
		// Only the slow ambient motion left: 30 frames a second is plenty.
		if (!busy && alpha <= 0.005 && !cam && now - lastDraw < 33) {
			raf = requestAnimationFrame(frame);
			return;
		}
		lastDraw = now;
		if (alpha > 0.005) {
			stepLayout();
			alpha *= 0.965;
			if (alpha <= 0.005 && !userMoved) fit(true);
		}
		if (cam) {
			tx += (cam.tx - tx) * 0.18;
			ty += (cam.ty - ty) * 0.18;
			zoom += (cam.zoom - zoom) * 0.18;
			if (Math.abs(cam.tx - tx) < 0.5 && Math.abs(cam.ty - ty) < 0.5 && Math.abs(cam.zoom - zoom) < 0.002)
				(tx = cam.tx), (ty = cam.ty), (zoom = cam.zoom), (cam = null);
		}
		const moving = draw(now);
		busy = moving || alpha > 0.005 || !!cam;
		// Keep going while anything animates: the layout, the camera, a glow, the flowing dots and pulses.
		if (busy || !reduceMotion) raf = requestAnimationFrame(frame);
	}

	// Draws one frame; returns true while a glow is still easing.
	function draw(now) {
		const dpr = window.devicePixelRatio || 1;
		const w = view.clientWidth, h = view.clientHeight;
		if (canvas.width !== Math.round(w * dpr) || canvas.height !== Math.round(h * dpr)) {
			canvas.width = Math.round(w * dpr);
			canvas.height = Math.round(h * dpr);
		}
		const ctx = canvas.getContext('2d');
		ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
		ctx.clearRect(0, 0, w, h);

		// a quiet dot grid that moves with the map
		const gs = 28 * zoom;
		if (gs > 10) {
			ctx.fillStyle = theme.line;
			const ox = ((tx % gs) + gs) % gs, oy = ((ty % gs) + gs) % gs;
			for (let x = ox; x < w; x += gs) for (let y = oy; y < h; y += gs) ctx.fillRect(x - 0.6, y - 0.6, 1.2, 1.2);
		}

		ctx.setTransform(dpr * zoom, 0, 0, dpr * zoom, dpr * tx, dpr * ty);
		const m = model;
		const lit = chain;
		const on = (id) => !lit || lit.has(id);
		const t = now / 1000;
		const sz = 1 / Math.sqrt(zoom); // line widths shrink gently when zoomed in

		// links
		if (edgeMode !== 'decisions') {
			for (const l of m.blocks) {
				const a = pos.get(l.blockerId), b = pos.get(l.issueId);
				if (!a || !b) continue;
				const c = curve(a, b);
				const hot = !!lit && lit.has(l.blockerId) && lit.has(l.issueId);
				const faded = !!lit && !hot;
				const ca = m.colour.get(l.blockerId), cb = m.colour.get(l.issueId);
				const al = faded ? 0.06 : l.done ? 0.22 : hot ? 0.95 : 0.42;
				const g = ctx.createLinearGradient(a.x, a.y, b.x, b.y);
				g.addColorStop(0, rgba(ca, al * 0.45));
				g.addColorStop(1, rgba(cb, al));
				ctx.strokeStyle = hot ? theme.accent : g;
				ctx.lineWidth = (hot ? 2.2 : 1.3) * sz;
				ctx.setLineDash(l.done ? [4 * sz, 4 * sz] : []);
				ctx.beginPath();
				ctx.moveTo(a.x, a.y);
				ctx.quadraticCurveTo(c.cx, c.cy, b.x, b.y);
				ctx.stroke();
				ctx.setLineDash([]);
				// arrow head where the curve meets the ticket it holds up
				const tEnd = Math.max(0.5, 1 - (radius(l.issueId) + 3) / c.d);
				const p1 = qpt(a, c, b, tEnd), p0 = qpt(a, c, b, tEnd - 0.04);
				const ang = Math.atan2(p1.y - p0.y, p1.x - p0.x), s = 5.5 * sz;
				ctx.fillStyle = hot ? theme.accent : rgba(cb, faded ? 0.08 : 0.75);
				ctx.beginPath();
				ctx.moveTo(p1.x, p1.y);
				ctx.lineTo(p1.x - Math.cos(ang - 0.45) * s, p1.y - Math.sin(ang - 0.45) * s);
				ctx.lineTo(p1.x - Math.cos(ang + 0.45) * s, p1.y - Math.sin(ang + 0.45) * s);
				ctx.closePath();
				ctx.fill();
				// work flowing from the blocker to the ticket it holds up
				if (!reduceMotion && !l.done && !faded) {
					const n = hot ? 3 : 2;
					for (let k = 0; k < n; k++) {
						const ph = (t * (hot ? 0.55 : 0.28) + hash(l.blockerId + l.issueId) + k / n) % 1;
						const pt = qpt(a, c, b, 0.06 + ph * (tEnd - 0.1));
						ctx.globalAlpha = Math.sin(ph * Math.PI);
						ctx.fillStyle = hot ? theme.accent : rgba(cb, 0.95);
						ctx.beginPath();
						ctx.arc(pt.x, pt.y, (hot ? 2.5 : 1.9) * sz, 0, Math.PI * 2);
						ctx.fill();
						ctx.globalAlpha = 1;
					}
				}
			}
		}

		// decisions
		if (edgeMode !== 'blocks') {
			for (const d of m.dec) {
				const a = pos.get(d.issueId), b = pos.get('d' + d.id);
				if (!a || !b) continue;
				ctx.globalAlpha = on(d.issueId) ? 0.85 : 0.12;
				ctx.strokeStyle = theme.decision;
				ctx.lineWidth = 1 * sz;
				ctx.beginPath();
				ctx.moveTo(a.x, a.y);
				ctx.lineTo(b.x, b.y);
				ctx.stroke();
				ctx.fillStyle = theme.decision;
				ctx.beginPath();
				ctx.roundRect(b.x - 3.5, b.y - 3.5, 7, 7, 1.8);
				ctx.fill();
				ctx.globalAlpha = 1;
			}
		}

		// tickets
		let easing = false;
		for (const i of m.shown) {
			const p = pos.get(i.id);
			if (!p) continue;
			const st = m.stateById.get(i.stateId);
			const col = m.colour.get(i.id);
			const born = Math.min(1, (now - p.born) / 450);
			const r = radius(i.id) * (0.4 + 0.6 * (1 - Math.pow(1 - born, 3)));
			p.target = i.id === hover || i.id === focusId ? 1 : 0;
			p.glow += (p.target - p.glow) * 0.2;
			if (Math.abs(p.target - p.glow) > 0.01 || born < 1) easing = true;
			const faded = !on(i.id);
			ctx.globalAlpha = faded ? 0.13 : 1;
			// soft halo
			const hr = r * (2.6 + p.glow * 1.6);
			const halo = ctx.createRadialGradient(p.x, p.y, r * 0.5, p.x, p.y, hr);
			halo.addColorStop(0, rgba(col, theme.dark ? 0.34 : 0.24));
			halo.addColorStop(1, rgba(col, 0));
			ctx.fillStyle = halo;
			ctx.beginPath();
			ctx.arc(p.x, p.y, hr, 0, Math.PI * 2);
			ctx.fill();
			// work in progress breathes
			if (st?.category === 'started' && st?.name !== 'Blocked' && !reduceMotion && !faded) {
				const pulse = (t * 0.6 + hash(i.id)) % 1;
				ctx.strokeStyle = rgba(col, 0.55 * (1 - pulse));
				ctx.lineWidth = 1.5 * sz;
				ctx.beginPath();
				ctx.arc(p.x, p.y, r + 2 + pulse * 10, 0, Math.PI * 2);
				ctx.stroke();
			}
			// the dot itself, lighter towards its top-left
			const fill = ctx.createRadialGradient(p.x - r * 0.35, p.y - r * 0.35, r * 0.1, p.x, p.y, r);
			fill.addColorStop(0, lighten(col, 0.5));
			fill.addColorStop(1, col);
			ctx.fillStyle = fill;
			ctx.beginPath();
			ctx.arc(p.x, p.y, r + p.glow * 1.5, 0, Math.PI * 2);
			ctx.fill();
			ctx.strokeStyle = p.glow > 0.05 ? theme.ink : theme.surface;
			ctx.lineWidth = (1.2 + p.glow) * sz;
			ctx.stroke();
			// Next up: a slowly turning dashed ring
			if (nextIds.has(i.id)) {
				ctx.strokeStyle = theme.accent;
				ctx.lineWidth = 1.6 * sz;
				ctx.setLineDash([3 * sz, 2.5 * sz]);
				ctx.lineDashOffset = reduceMotion ? 0 : -t * 8 * sz;
				ctx.beginPath();
				ctx.arc(p.x, p.y, r + 4.5, 0, Math.PI * 2);
				ctx.stroke();
				ctx.setLineDash([]);
				ctx.lineDashOffset = 0;
			}
			if (i.id === focusId) {
				ctx.strokeStyle = theme.accent;
				ctx.lineWidth = 2.4 * sz;
				ctx.beginPath();
				ctx.arc(p.x, p.y, r + 8, 0, Math.PI * 2);
				ctx.stroke();
			}
			// key beside the dot, when there is room or it is in focus
			const named = i.id === hover || i.id === focusId;
			const hub = (m.unblocks.get(i.id) || 0) >= 3; // the busiest tickets keep their key even zoomed out
			if (named || (!faded && (zoom > 0.9 || (hub && zoom > 0.4) || (lit && zoom > 0.45)))) {
				ctx.font = `${(named ? 12 : 11) / zoom}px ${theme.mono}`;
				ctx.fillStyle = named ? theme.ink : theme.ink3;
				ctx.textBaseline = 'middle';
				ctx.fillText(i.key, p.x + r + 5 / zoom, p.y);
			}
			ctx.globalAlpha = 1;
		}
		return easing;
	}

	// Redraw when something drawn changes outside the animation loop.
	$effect(() => {
		model; chain; edgeMode; nextIds; focusId; hover; zoom; tx; ty;
		untrack(wake);
	});
	$effect(() => {
		if (!canvas || !view) return;
		readTheme();
		const mq = matchMedia('(prefers-color-scheme: dark)');
		const again = () => (readTheme(), wake());
		mq.addEventListener('change', again);
		const mo = new MutationObserver(again);
		mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
		const ro = new ResizeObserver(() => wake());
		ro.observe(view);
		wake();
		return () => {
			mq.removeEventListener('change', again);
			mo.disconnect();
			ro.disconnect();
			cancelAnimationFrame(raf);
			raf = 0;
		};
	});

	// ── what the overlays show ──────────────────────────────────────────
	const hoverIssue = $derived(hover ? $allIssues.find((i) => i.id === hover) : null);
	const waitsOn = (id) => model.blocks.filter((l) => l.issueId === id && !l.done).length;
	const shownStates = $derived($states.filter((s) => $allIssues.some((i) => i.stateId === s.id)));
	const countIn = (sid) => $allIssues.filter((i) => i.stateId === sid && (!epic || i.projectId === epic)).length;
	const epicName = (i) => $projects.find((p) => p.id === i.projectId)?.name;
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
				class:pointing={!!hover && !dragging}
				bind:this={view}
				onmousedown={onDown}
				onkeydown={onKey}
				tabindex="0"
				role="application"
				aria-label="Ticket network"
			>
				<canvas bind:this={canvas} class="map" aria-hidden="true"></canvas>

				{#if hoverIssue && !dragging}
					{@const st = stOf(hoverIssue)}
					<div class="card" style:left="{cardAt.x}px" style:top="{cardAt.y}px">
						<div class="ch"><StateIcon category={st?.category} color={st?.color} name={st?.name} /><span class="key">{hoverIssue.key}</span><span class="stn">{st?.name || ''}</span></div>
						<div class="ct">{hoverIssue.title}</div>
						{#if epicName(hoverIssue)}<div class="ce">{epicName(hoverIssue)}</div>{/if}
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
					<button onclick={() => fit(true)} aria-label="Fit to screen" title="Fit (0)"><Maximize size={13} /></button>
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
	.view.pointing {
		cursor: pointer;
	}
	.view.grabbing {
		cursor: grabbing;
	}
	.map {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		display: block;
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
		animation: pop var(--dur) var(--ease);
	}
	@keyframes pop {
		from {
			opacity: 0;
			transform: translateY(4px);
		}
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
	.ce {
		font-size: var(--t-xs);
		color: var(--ink-3);
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
		transition: background var(--dur) var(--ease);
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
	@media (prefers-reduced-motion: reduce) {
		.card {
			animation: none;
		}
	}
</style>
