// Per-device theme choice: 'system' (follow the OS), 'light' or 'dark'.
// app.html applies the stored choice before first paint; this module changes
// it at runtime. Stored in localStorage so each device keeps its own.
import { readable, writable } from 'svelte/store';

const KEY = 'donewhen.theme';
export const THEMES = ['system', 'light', 'dark'];

export function getTheme() {
	try {
		const t = localStorage.getItem(KEY);
		return THEMES.includes(t) ? t : 'system';
	} catch {
		return 'system';
	}
}

export function applyTheme(t) {
	const root = document.documentElement;
	if (t === 'light' || t === 'dark') root.dataset.theme = t;
	else delete root.dataset.theme;
}

export const theme = writable(typeof localStorage === 'undefined' ? 'system' : getTheme());

const osDark = () =>
	typeof matchMedia !== 'undefined' && matchMedia('(prefers-color-scheme: dark)').matches;

// What is on screen: 'light' or 'dark', with 'system' resolved against the OS.
export const resolve = (t, dark = osDark()) => (t === 'light' || t === 'dark' ? t : dark ? 'dark' : 'light');

// The theme on screen, kept current when the OS preference flips under 'system'.
export const resolvedTheme = readable('light', (set) => {
	if (typeof matchMedia === 'undefined') return;
	const mq = matchMedia('(prefers-color-scheme: dark)');
	let chosen = getTheme();
	const update = () => set(resolve(chosen, mq.matches));
	const stop = theme.subscribe((t) => ((chosen = t), update()));
	mq.addEventListener('change', update);
	return () => (stop(), mq.removeEventListener('change', update));
});

// The browser's <meta name="theme-color"> tags follow the OS only; point them
// at the paper colour of an explicit choice so the address bar matches.
const PAPER = { light: '#fafafa', dark: '#161616' };
function syncThemeColor(t) {
	for (const m of document.querySelectorAll('meta[name="theme-color"]')) {
		const os = /dark/.test(m.media) ? 'dark' : 'light';
		m.content = PAPER[t === 'light' || t === 'dark' ? t : os];
	}
}

export function setTheme(t) {
	if (!THEMES.includes(t)) t = 'system';
	try {
		if (t === 'system') localStorage.removeItem(KEY);
		else localStorage.setItem(KEY, t);
	} catch {
		/* private mode: still applies for this page */
	}
	const swap = () => {
		applyTheme(t);
		syncThemeColor(t);
		theme.set(t);
	};
	// Cross-fade the whole page where the browser can; otherwise switch at once.
	const reduced = typeof matchMedia !== 'undefined' && matchMedia('(prefers-reduced-motion: reduce)').matches;
	if (document.startViewTransition && !reduced) document.startViewTransition(swap);
	else swap();
}

// The quick toggle: flip whatever is on screen, and remember it as an explicit choice.
export function toggleTheme() {
	setTheme(resolve(getTheme()) === 'dark' ? 'light' : 'dark');
}
