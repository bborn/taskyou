// Svelte 5 reactive state for agent connection.
// WebSocket logic lives in Dashboard.svelte (inline) and agent-ws.ts.
// This file manages reactive state updates.

import type { AgentChatMessage } from '$lib/types';

// Messages restored from agent DO storage on WebSocket connect
export let restoredMessages: AgentChatMessage[] = [];

// Callback for when messages are restored (set by chat store to avoid circular dep)
let onMessagesRestored: (() => void) | null = null;
export function setOnMessagesRestored(cb: () => void) { onMessagesRestored = cb; }

// Callback for when agent syncs tasks (triggers fetchTasks in tasks store)
let onTasksUpdated: (() => void) | null = null;
export function setOnTasksUpdated(cb: () => void) { onTasksUpdated = cb; }

/** Extract text content from AI SDK UI message format (which uses `parts`) */
function extractTextContent(msg: any): string {
	// AI SDK UI messages have `parts` array with type/text entries
	if (msg.parts && Array.isArray(msg.parts)) {
		return msg.parts
			.filter((p: any) => p.type === 'text')
			.map((p: any) => p.text)
			.join('');
	}
	// Fallback: plain content string
	if (typeof msg.content === 'string') return msg.content;
	return '';
}

export const agentState = $state({
	connected: false,
	tasks: [] as Array<{
		id: number;
		title: string;
		status: string;
		type: string;
		project_id: string;
		updated_at: string;
	}>,
	lastSync: '',
});

export const workflowState = $state({
	activeWorkflows: {} as Record<string, { status: string; progress?: any; result?: any; error?: string }>,
});

export const agentChatStream = $state({
	streaming: false,
	streamingContent: '',
	lastError: null as string | null,
	completedMessage: null as AgentChatMessage | null,
});

/**
 * Handle incoming WebSocket messages and route to reactive state.
 * Called by Dashboard.svelte's inline WebSocket handler.
 */
export function handleAgentMessage(data: any) {
	if (data.type === '_connected') {
		agentState.connected = true;
		return;
	}
	if (data.type === '_disconnected') {
		agentState.connected = false;
		return;
	}

	// Handle state sync from agent
	if (data.type === 'cf_agent_state' || data.type === 'cf_agent_state_update') {
		const state = data.state || data;
		if (state.tasks) {
			agentState.tasks = state.tasks;
			onTasksUpdated?.();
		}
		if (state.lastSync) {
			agentState.lastSync = state.lastSync;
		}
		return;
	}

	// Handle chat streaming response (AIChatAgent protocol)
	if (data.type === 'cf_agent_use_chat_response') {
		handleChatResponse(data);
		return;
	}

	// Handle workflow lifecycle messages
	if (data.type === 'workflow-progress') {
		const id = data.workflowId;
		if (id) {
			workflowState.activeWorkflows[id] = {
				status: 'running',
				progress: data.progress,
			};
		}
		return;
	}
	if (data.type === 'workflow-complete') {
		const id = data.workflowId;
		if (id) {
			workflowState.activeWorkflows[id] = {
				status: 'complete',
				result: data.result,
			};
		}
		return;
	}
	if (data.type === 'workflow-error') {
		const id = data.workflowId;
		if (id) {
			workflowState.activeWorkflows[id] = {
				status: 'error',
				error: data.error,
			};
		}
		return;
	}

	// Restore persisted chat messages from the agent's DO storage
	if (data.type === 'cf_agent_chat_messages') {
		if (data.messages && Array.isArray(data.messages)) {
			restoredMessages = data.messages
				.map((m: any) => ({
					id: m.id || crypto.randomUUID(),
					role: m.role,
					content: extractTextContent(m),
					createdAt: m.createdAt || new Date().toISOString(),
				}))
				.filter((m: any) => m.content.trim() !== '');
			onMessagesRestored?.();
		}
		return;
	}

	// Silently ignore protocol messages
	if (data.type === 'cf_agent_identity' || data.type === 'cf_agent_mcp_servers') {
		return;
	}
}

/**
 * Reset chat-related streaming state when switching chats.
 * Called before connecting to a new agent instance.
 */
export function resetChatState() {
	agentChatStream.streaming = false;
	agentChatStream.streamingContent = '';
	agentChatStream.completedMessage = null;
	agentChatStream.lastError = null;
	agentState.connected = false;
	restoredMessages = [];
}

function handleChatResponse(data: { id: string; body: string; done: boolean; error?: boolean; continuation?: boolean }) {
	if (data.error) {
		agentChatStream.streaming = false;
		agentChatStream.streamingContent = '';
		agentChatStream.lastError = data.body || 'Unknown error';
		return;
	}

	if (data.body) {
		try {
			const parsed = JSON.parse(data.body);
			switch (parsed.type) {
				case 'text-delta':
					agentChatStream.streamingContent += parsed.delta;
					break;
				case 'error':
					agentChatStream.lastError = parsed.errorText || 'Unknown error';
					agentChatStream.streaming = false;
					agentChatStream.streamingContent = '';
					return;
				// Ignore: start, start-step, text-start, text-end, finish-step, finish
			}
		} catch {
			// Body is not JSON — might be empty string or old SSE format
		}
	}

	if (data.done) {
		if (agentChatStream.streamingContent) {
			agentChatStream.completedMessage = {
				id: crypto.randomUUID(),
				role: 'assistant',
				content: agentChatStream.streamingContent,
				createdAt: new Date().toISOString(),
			};
		}
		agentChatStream.streaming = false;
		agentChatStream.streamingContent = '';
	}
}
