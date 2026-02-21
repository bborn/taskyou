<script lang="ts">
	import { tick } from 'svelte';
	import { Send, Bot, User, Loader2, MessageSquare } from 'lucide-svelte';
	import { taskChatState, connectTaskChat, disconnectTaskChat, sendTaskChatMessage } from '$lib/stores/taskChat.svelte';
	import type { Task } from '$lib/types';
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

	interface Props {
		taskId: number;
		task: Task;
		userId: string;
	}

	let { taskId, task, userId }: Props = $props();

	let inputValue = $state('');
	let inputEl: HTMLTextAreaElement;
	let messagesEndEl: HTMLDivElement;

	// Connect/disconnect WebSocket when taskId changes
	$effect(() => {
		const id = taskId;
		const uid = userId;
		if (id && uid) {
			connectTaskChat(uid, id);
		}
		return () => {
			disconnectTaskChat();
		};
	});

	// Watch for completed messages from stream and append to messages
	$effect(() => {
		const msg = taskChatState.completedMessage;
		if (msg) {
			taskChatState.messages = [...taskChatState.messages, msg];
			taskChatState.completedMessage = null;
		}
	});

	// Watch for errors and show as assistant message
	$effect(() => {
		const err = taskChatState.error;
		if (err) {
			taskChatState.messages = [...taskChatState.messages, {
				id: crypto.randomUUID(),
				role: 'assistant',
				content: `**Error:** ${err}`,
				createdAt: new Date().toISOString(),
			}];
			taskChatState.error = null;
		}
	});

	// Auto-scroll when messages change or streaming content updates
	$effect(() => {
		if (taskChatState.messages.length || taskChatState.streamingContent) {
			tick().then(() => {
				messagesEndEl?.scrollIntoView({ behavior: 'smooth' });
			});
		}
	});

	function handleSubmit(e?: SubmitEvent) {
		e?.preventDefault();
		const content = inputValue.trim();
		if (!content || taskChatState.streaming) return;

		inputValue = '';
		sendTaskChatMessage(content);

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
			inputEl.style.height = Math.min(inputEl.scrollHeight, 120) + 'px';
		}
	}
</script>

<div class="flex flex-col h-full bg-background">
	<!-- Header -->
	<div class="flex items-center justify-between px-3 py-2 border-b border-border shrink-0">
		<div class="flex items-center gap-2">
			<MessageSquare class="h-3.5 w-3.5 text-primary" />
			<span class="text-xs font-medium">Task Chat</span>
		</div>
		<div class="flex items-center gap-1.5">
			<span
				class="h-1.5 w-1.5 rounded-full {taskChatState.connected ? 'bg-green-500' : 'bg-muted-foreground/40'}"
			></span>
			<span class="text-[10px] text-muted-foreground">
				{taskChatState.connected ? 'Connected' : 'Disconnected'}
			</span>
		</div>
	</div>

	<!-- Messages -->
	<div class="flex-1 overflow-y-auto scrollbar-thin px-3 py-3 space-y-3">
		{#if taskChatState.messages.length === 0 && !taskChatState.streaming}
			<!-- Empty state -->
			<div class="flex flex-col items-start justify-end h-full px-1 pb-1">
				<p class="text-[12px] text-muted-foreground/60 leading-relaxed">
					Chat with the agent about this task. It has access to the task's sandbox.
				</p>
			</div>
		{:else}
			{#each taskChatState.messages as message (message.id)}
				<div class="flex gap-2">
					<!-- Avatar -->
					<div class="shrink-0 mt-0.5">
						{#if message.role === 'user'}
							<div class="h-6 w-6 rounded-full bg-primary/10 flex items-center justify-center">
								<User class="h-3 w-3 text-primary" />
							</div>
						{:else}
							<div class="h-6 w-6 rounded-full bg-accent flex items-center justify-center">
								<Bot class="h-3 w-3 text-accent-foreground" />
							</div>
						{/if}
					</div>

					<!-- Content -->
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-2 mb-0.5">
							<span class="text-[11px] font-medium">{message.role === 'user' ? 'You' : 'Agent'}</span>
						</div>
						{#if message.role === 'assistant'}
							<div class="text-xs prose prose-xs dark:prose-invert max-w-none break-words chat-markdown">
								{@html renderMarkdown(message.content)}
							</div>
						{:else}
							<div class="text-xs break-words whitespace-pre-wrap">
								{message.content}
							</div>
						{/if}

						<!-- Tool invocations -->
						{#if message.toolInvocations?.length}
							<div class="mt-1.5 space-y-0.5">
								{#each message.toolInvocations as tool}
									<div class="text-[10px] text-muted-foreground bg-muted/50 rounded px-1.5 py-0.5">
										<span class="font-mono">{tool.toolName}</span>
										{#if tool.state === 'result'}
											<span class="text-green-500 ml-1">done</span>
										{:else}
											<span class="text-blue-500 ml-1">running...</span>
										{/if}
									</div>
								{/each}
							</div>
						{/if}
					</div>
				</div>
			{/each}

			<!-- Streaming indicator -->
			{#if taskChatState.streaming}
				<div class="flex gap-2">
					<div class="shrink-0 mt-0.5">
						<div class="h-6 w-6 rounded-full bg-accent flex items-center justify-center">
							<Bot class="h-3 w-3 text-accent-foreground" />
						</div>
					</div>
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-2 mb-0.5">
							<span class="text-[11px] font-medium">Agent</span>
							<Loader2 class="h-3 w-3 animate-spin text-primary" />
						</div>
						{#if taskChatState.streamingContent}
							<div class="text-xs prose prose-xs dark:prose-invert max-w-none break-words chat-markdown">
								{@html renderMarkdown(taskChatState.streamingContent)}
							</div>
						{:else}
							<div class="flex gap-1">
								<span class="h-1.5 w-1.5 rounded-full bg-muted-foreground/40 animate-bounce" style="animation-delay: 0ms"></span>
								<span class="h-1.5 w-1.5 rounded-full bg-muted-foreground/40 animate-bounce" style="animation-delay: 150ms"></span>
								<span class="h-1.5 w-1.5 rounded-full bg-muted-foreground/40 animate-bounce" style="animation-delay: 300ms"></span>
							</div>
						{/if}
					</div>
				</div>
			{/if}
		{/if}

		<div bind:this={messagesEndEl}></div>
	</div>

	<!-- Input -->
	<div class="border-t border-border p-2 shrink-0">
		<form onsubmit={handleSubmit} class="flex items-center gap-1.5">
			<textarea
				bind:this={inputEl}
				bind:value={inputValue}
				onkeydown={handleKeydown}
				oninput={autoResize}
				placeholder="Ask about this task..."
				rows="1"
				class="input flex-1 text-xs h-8 resize-none min-h-[32px] max-h-[120px]"
				disabled={taskChatState.streaming || !taskChatState.connected}
			></textarea>
			<button
				type="submit"
				disabled={!inputValue.trim() || taskChatState.streaming || !taskChatState.connected}
				class="btn-sm h-8"
			>
				{#if taskChatState.streaming}
					<Loader2 class="h-3.5 w-3.5 animate-spin" />
				{:else}
					<Send class="h-3.5 w-3.5" />
				{/if}
			</button>
		</form>
	</div>
</div>
