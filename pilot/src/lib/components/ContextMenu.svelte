<script lang="ts">
	import { onMount } from 'svelte';

	interface MenuItem {
		label: string;
		icon?: typeof import('lucide-svelte').Play;
		action: () => void;
		variant?: 'default' | 'destructive';
		separator?: boolean;
	}

	interface Props {
		x: number;
		y: number;
		items: MenuItem[];
		onClose: () => void;
	}

	let { x, y, items, onClose }: Props = $props();

	let menuEl: HTMLDivElement;

	onMount(() => {
		// Adjust position if menu would overflow viewport
		if (menuEl) {
			const rect = menuEl.getBoundingClientRect();
			if (rect.right > window.innerWidth) {
				menuEl.style.left = `${x - rect.width}px`;
			}
			if (rect.bottom > window.innerHeight) {
				menuEl.style.top = `${y - rect.height}px`;
			}
		}

		function handleClick(e: MouseEvent) {
			if (menuEl && !menuEl.contains(e.target as Node)) {
				onClose();
			}
		}
		function handleEscape(e: KeyboardEvent) {
			if (e.key === 'Escape') onClose();
		}

		document.addEventListener('click', handleClick);
		document.addEventListener('keydown', handleEscape);
		return () => {
			document.removeEventListener('click', handleClick);
			document.removeEventListener('keydown', handleEscape);
		};
	});
</script>

<div
	bind:this={menuEl}
	class="fixed z-[100] min-w-[160px] bg-card border border-border rounded-lg shadow-lg py-1 animate-in fade-in zoom-in-95 duration-100"
	style:left="{x}px"
	style:top="{y}px"
>
	{#each items as item}
		{#if item.separator}
			<div class="h-px bg-border my-1"></div>
		{/if}
		<button
			onclick={() => { item.action(); onClose(); }}
			class="w-full flex items-center gap-2 px-3 py-1.5 text-sm transition-colors {item.variant === 'destructive' ? 'text-destructive hover:bg-destructive/10' : 'text-foreground hover:bg-muted'}"
		>
			{#if item.icon}
				<item.icon class="h-3.5 w-3.5" />
			{/if}
			{item.label}
		</button>
	{/each}
</div>
