<script lang="ts">
	import { ScrollText } from 'lucide-svelte';
</script>

<div class="h-full overflow-y-auto p-6">
	<div class="max-w-5xl mx-auto">
		<div class="flex items-center gap-3 mb-6">
			<ScrollText class="h-6 w-6 text-primary" />
			<h1 class="text-2xl font-bold">Audit Log</h1>
		</div>

		<div class="flex flex-col items-center justify-center py-20 text-center">
			<div class="p-4 rounded-full bg-muted mb-4">
				<ScrollText class="h-8 w-8 text-muted-foreground" />
			</div>
			<h3 class="font-semibold mb-1">Coming Soon</h3>
			<p class="text-sm text-muted-foreground">Agent actions will appear here once workflow execution is enabled</p>
		</div>
	</div>
</div>
