import { redirect } from '@sveltejs/kit';

// The Graph page is gone; Sessions took its place in the sidebar.
export function load() {
	redirect(307, '/sessions');
}
