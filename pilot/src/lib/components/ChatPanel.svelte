<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { Send, Bot, User, Loader2, Sparkles } from 'lucide-svelte';
	import { chatState, sendMessage, createNewChat, fetchChats } from '$lib/stores/chat.svelte';
	import { models as modelsApi } from '$lib/api/client';
	import type { Model } from '$lib/types';
	import { Marked } from 'marked';

	const marked = new Marked({
		breaks: true,
		gfm: true,
	});

	function renderMarkdown(content: string): string {
		try {
			return marked.parse(content) as string;
		} catch {
			return content;
		}
	}

	let inputValue = $state('');
	let inputEl: HTMLTextAreaElement;
	let messagesEndEl: HTMLDivElement;
	let models = $state<Model[]>([]);

	onMount(async () => {
		await fetchChats();
		try {
			models = await modelsApi.list();
		} catch {
			// ignore
		}
	});

	$effect(() => {
		// Auto-scroll when messages change or streaming content updates
		if (chatState.messages.length || chatState.streamingContent) {
			tick().then(() => {
				messagesEndEl?.scrollIntoView({ behavior: 'smooth' });
			});
		}
	});

	async function handleSubmit(e?: SubmitEvent) {
		e?.preventDefault();
		const content = inputValue.trim();
		if (!content || chatState.streaming) return;

		// Create chat if none active
		if (!chatState.activeChat) {
			await createNewChat();
		}

		inputValue = '';
		await sendMessage(content);

		// Resize textarea back
		if (inputEl) inputEl.style.height = 'auto';
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			handleSubmit();
		}
	}

	function autoResize() {
		if (inputEl) {
			inputEl.style.height = 'auto';
			inputEl.style.height = Math.min(inputEl.scrollHeight, 200) + 'px';
		}
	}

	async function changeModel(modelId: string) {
		if (!chatState.activeChat) {
			await createNewChat(modelId);
		}
	}

	function formatTokens(n: number): string {
		if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
		return String(n);
	}

	const quickActions = [
		{ label: 'Show Board', prompt: 'Show me the current state of the board' },
		{ label: 'List Tasks', prompt: 'List all my current tasks' },
	];

	async function handleQuickAction(prompt: string) {
		if (chatState.streaming) return;
		if (!chatState.activeChat) {
			await createNewChat();
		}
		await sendMessage(prompt);
	}
</script>

<div class="flex flex-col h-full bg-background">
	<!-- Header -->
	<div class="flex items-center justify-between px-4 py-3 border-b border-border shrink-0">
		<div class="flex items-center gap-2">
			<Sparkles class="h-4 w-4 text-primary" />
			<h3 class="font-semibold text-sm">Pilot Chat</h3>
		</div>
		<div class="flex items-center gap-1.5">
			<span class="text-[10px] text-muted-foreground hidden sm:inline">Model</span>
			<select
				class="select select-sm text-xs h-7 min-w-[100px] max-w-[140px]"
				onchange={(e) => changeModel((e.target as HTMLSelectElement).value)}
			>
				{#each models as model}
					<option value={model.id} selected={chatState.activeChat?.model_id === model.id}>
						{model.name}
					</option>
				{/each}
			</select>
		</div>
	</div>

	<!-- Messages -->
	<div class="flex-1 overflow-y-auto scrollbar-thin px-4 py-4 space-y-4">
		{#if chatState.messages.length === 0 && !chatState.streaming}
			<!-- Empty state -->
			<div class="flex flex-col items-center justify-center h-full text-center">
				<div class="p-4 rounded-full bg-primary/10 mb-4">
					<Bot class="h-8 w-8 text-primary" />
				</div>
				<h3 class="font-semibold mb-1">Chat with Pilot</h3>
				<p class="text-sm text-muted-foreground mb-6 max-w-xs">
					Ask me to create tasks, check progress, or help plan your work.
				</p>
				<div class="flex flex-wrap gap-2 justify-center">
					{#each quickActions as action}
						<button
							onclick={() => handleQuickAction(action.prompt)}
							disabled={chatState.streaming}
							class="px-3 py-1.5 text-xs font-medium rounded-lg bg-muted hover:bg-muted/80 text-foreground border border-border transition-colors disabled:opacity-50"
						>
							{action.label}
						</button>
					{/each}
				</div>
			</div>
		{:else}
			{#each chatState.messages as message (message.id)}
				<div class="flex gap-3 {message.role === 'user' ? '' : ''}">
					<!-- Avatar -->
					<div class="shrink-0 mt-0.5">
						{#if message.role === 'user'}
							<div class="h-7 w-7 rounded-full bg-primary/10 flex items-center justify-center">
								<User class="h-4 w-4 text-primary" />
							</div>
						{:else}
							<div class="h-7 w-7 rounded-full bg-accent flex items-center justify-center">
								<Bot class="h-4 w-4 text-accent-foreground" />
							</div>
						{/if}
					</div>

					<!-- Content -->
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-2 mb-1">
							<span class="text-xs font-medium">{message.role === 'user' ? 'You' : 'Pilot'}</span>
							{#if message.input_tokens || message.output_tokens}
								<span class="text-[10px] text-muted-foreground">
									{formatTokens(message.input_tokens)}in / {formatTokens(message.output_tokens)}out
								</span>
							{/if}
						</div>
						{#if message.role === 'assistant'}
							<div class="text-sm prose prose-sm dark:prose-invert max-w-none break-words chat-markdown">
								{@html renderMarkdown(message.content)}
							</div>
						{:else}
							<div class="text-sm break-words whitespace-pre-wrap">
								{message.content}
							</div>
						{/if}
					</div>
				</div>
			{/each}

			<!-- Streaming indicator -->
			{#if chatState.streaming}
				<div class="flex gap-3">
					<div class="shrink-0 mt-0.5">
						<div class="h-7 w-7 rounded-full bg-accent flex items-center justify-center">
							<Bot class="h-4 w-4 text-accent-foreground" />
						</div>
					</div>
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-2 mb-1">
							<span class="text-xs font-medium">Pilot</span>
							<Loader2 class="h-3 w-3 animate-spin text-primary" />
						</div>
						{#if chatState.streamingContent}
							<div class="text-sm prose prose-sm dark:prose-invert max-w-none break-words chat-markdown">{@html renderMarkdown(chatState.streamingContent)}</div>
						{:else}
							<div class="flex gap-1">
								<span class="h-2 w-2 rounded-full bg-muted-foreground/40 animate-bounce" style="animation-delay: 0ms"></span>
								<span class="h-2 w-2 rounded-full bg-muted-foreground/40 animate-bounce" style="animation-delay: 150ms"></span>
								<span class="h-2 w-2 rounded-full bg-muted-foreground/40 animate-bounce" style="animation-delay: 300ms"></span>
							</div>
						{/if}
					</div>
				</div>
			{/if}
		{/if}

		<div bind:this={messagesEndEl}></div>
	</div>

	<!-- Input -->
	<div class="border-t border-border p-3 shrink-0">
		<form onsubmit={handleSubmit} class="flex items-center gap-2">
			<textarea
				bind:this={inputEl}
				bind:value={inputValue}
				onkeydown={handleKeydown}
				oninput={autoResize}
				placeholder="Ask Pilot anything..."
				rows="1"
				class="input flex-1 text-sm h-9 resize-none min-h-[36px] max-h-[200px]"
				disabled={chatState.streaming}
			></textarea>
			<button
				type="submit"
				disabled={!inputValue.trim() || chatState.streaming}
				class="btn-sm h-9"
			>
				{#if chatState.streaming}
					<Loader2 class="h-4 w-4 animate-spin" />
				{:else}
					<Send class="h-4 w-4" />
				{/if}
			</button>
		</form>
	</div>
</div>

