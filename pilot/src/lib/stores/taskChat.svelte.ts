// Per-task chat store with WebSocket connection management.
// Independent from the main chat — uses module-level WebSocket (not globalThis)
// because task chat is component-scoped (connect on TaskDetail open, disconnect on close).

import type { AgentChatMessage } from '$lib/types';

let taskWs: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

// Track connection params for reconnect
let currentUserId: string | null = null;
let currentTaskId: number | null = null;

export const taskChatState = $state({
	taskId: null as number | null,
	connected: false,
	messages: [] as AgentChatMessage[],
	streaming: false,
	streamingContent: '',
	error: null as string | null,
	completedMessage: null as AgentChatMessage | null,
});

/** Extract text content from AI SDK UI message format (which uses `parts`) */
function extractTextContent(msg: any): string {
	if (msg.parts && Array.isArray(msg.parts)) {
		return msg.parts
			.filter((p: any) => p.type === 'text')
			.map((p: any) => p.text)
			.join('');
	}
	if (typeof msg.content === 'string') return msg.content;
	return '';
}

/**
 * Connect to a task-scoped agent DO instance via WebSocket.
 * The agent instance name is `{userId}:task-{taskId}`.
 */
export function connectTaskChat(userId: string, taskId: number) {
	// Already connected to this task
	if (taskWs && taskWs.readyState === WebSocket.OPEN && currentTaskId === taskId) {
		return;
	}

	// Clear messages if switching to a different task
	const switchingTask = currentTaskId !== null && currentTaskId !== taskId;

	// Clean up any existing connection
	disconnectTaskChat();

	if (switchingTask) {
		taskChatState.messages = [];
	}

	currentUserId = userId;
	currentTaskId = taskId;
	taskChatState.taskId = taskId;
	taskChatState.error = null;

	const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
	const agentName = encodeURIComponent(`${userId}:task-${taskId}`);
	const url = `${protocol}//${location.host}/agents/taskyou-agent/${agentName}`;

	const ws = new WebSocket(url);
	taskWs = ws;

	ws.addEventListener('open', () => {
		if (taskWs !== ws) return; // Stale socket
		taskChatState.connected = true;
		taskChatState.error = null;
	});

	ws.addEventListener('message', (event) => {
		if (taskWs !== ws) return; // Stale socket
		try {
			const data = JSON.parse(event.data);
			handleTaskChatMessage(data);
		} catch {
			// Non-JSON message, ignore
		}
	});

	ws.addEventListener('close', () => {
		if (taskWs !== ws) return; // Stale socket — don't corrupt current state
		taskChatState.connected = false;
		taskWs = null;

		// Auto-reconnect after 3s if we still have connection params
		if (currentUserId && currentTaskId) {
			reconnectTimer = setTimeout(() => {
				if (currentUserId && currentTaskId) {
					connectTaskChat(currentUserId, currentTaskId);
				}
			}, 3000);
		}
	});

	ws.addEventListener('error', () => {
		if (taskWs !== ws) return; // Stale socket
		taskChatState.error = 'WebSocket connection error';
	});
}

/**
 * Disconnect from the task chat WebSocket and reset state.
 */
export function disconnectTaskChat() {
	currentUserId = null;
	currentTaskId = null;

	if (reconnectTimer) {
		clearTimeout(reconnectTimer);
		reconnectTimer = null;
	}

	if (taskWs) {
		const ws = taskWs;
		taskWs = null; // Clear reference BEFORE closing so close handler ignores
		ws.close();
	}

	taskChatState.taskId = null;
	taskChatState.connected = false;
	taskChatState.streaming = false;
	taskChatState.streamingContent = '';
	taskChatState.error = null;
	taskChatState.completedMessage = null;
	// Note: messages are NOT cleared here — they persist until a new task connects
	// or the server restores them via cf_agent_chat_messages on reconnect
}

/**
 * Handle incoming WebSocket messages for the task chat.
 * Routes protocol messages to update reactive state.
 */
export function handleTaskChatMessage(data: any) {
	// Chat streaming response (AIChatAgent protocol)
	if (data.type === 'cf_agent_use_chat_response') {
		handleChatResponse(data);
		return;
	}

	// Restore persisted chat messages from the agent's DO storage
	if (data.type === 'cf_agent_chat_messages') {
		if (data.messages && Array.isArray(data.messages)) {
			taskChatState.messages = data.messages
				.map((m: any) => ({
					id: m.id || crypto.randomUUID(),
					role: m.role,
					content: extractTextContent(m),
					createdAt: m.createdAt || new Date().toISOString(),
				}))
				.filter((m: any) => m.content.trim() !== '');
		}
		return;
	}

	// Silently ignore other protocol messages
	if (
		data.type === 'cf_agent_identity' ||
		data.type === 'cf_agent_mcp_servers' ||
		data.type === 'cf_agent_state' ||
		data.type === 'cf_agent_state_update'
	) {
		return;
	}
}

function handleChatResponse(data: { id: string; body: string; done: boolean; error?: boolean }) {
	if (data.error) {
		taskChatState.streaming = false;
		taskChatState.streamingContent = '';
		taskChatState.error = data.body || 'Unknown error';
		return;
	}

	if (data.body) {
		try {
			const parsed = JSON.parse(data.body);
			switch (parsed.type) {
				case 'text-delta':
					taskChatState.streamingContent += parsed.delta;
					break;
				case 'error':
					taskChatState.error = parsed.errorText || 'Unknown error';
					taskChatState.streaming = false;
					taskChatState.streamingContent = '';
					return;
			}
		} catch {
			// Body is not JSON — ignore
		}
	}

	if (data.done) {
		if (taskChatState.streamingContent) {
			taskChatState.completedMessage = {
				id: crypto.randomUUID(),
				role: 'assistant',
				content: taskChatState.streamingContent,
				createdAt: new Date().toISOString(),
			};
		}
		taskChatState.streaming = false;
		taskChatState.streamingContent = '';
	}
}

/**
 * Send a chat message to the task-scoped agent via WebSocket.
 * Adds the user message optimistically and sends full history.
 */
export function sendTaskChatMessage(content: string): string | null {
	if (!content.trim()) return null;

	if (!taskWs || taskWs.readyState !== WebSocket.OPEN) {
		taskChatState.error = 'Not connected to task agent';
		return null;
	}

	// Add user message optimistically
	const userMsg: AgentChatMessage = {
		id: crypto.randomUUID(),
		role: 'user',
		content: content.trim(),
		createdAt: new Date().toISOString(),
	};
	taskChatState.messages = [...taskChatState.messages, userMsg];

	// Set streaming state before sending
	taskChatState.streaming = true;
	taskChatState.streamingContent = '';
	taskChatState.error = null;
	taskChatState.completedMessage = null;

	// Send full message history via AIChatAgent protocol
	const requestId = crypto.randomUUID();
	taskWs.send(
		JSON.stringify({
			type: 'cf_agent_use_chat_request',
			id: requestId,
			init: {
				method: 'POST',
				body: JSON.stringify({
					messages: taskChatState.messages.map((m) => ({
						id: m.id,
						role: m.role,
						content: m.content,
						createdAt: m.createdAt,
					})),
				}),
			},
		})
	);

	return requestId;
}
