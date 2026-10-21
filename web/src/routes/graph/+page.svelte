<script>
	import NetworkGraph from '$components/NetworkGraph.svelte';
	import PageHeader from '$components/PageHeader.svelte';
</script>

<div class="page">
	<PageHeader crumbs={[{ label: 'Graph' }]} />
	<div class="fill"><NetworkGraph /></div>
</div>

<style>
	.page {
		height: 100%;
		display: flex;
		flex-direction: column;
	}
	.fill {
		flex: 1;
		min-height: 0;
	}
</style>
