// WebSocket send helpers for the agent connection.
// The WebSocket itself is created in +page.svelte and stored on globalThis.__agentWs.

import type { AgentChatMessage } from '$lib/types';

function getWs(): WebSocket | null {
	const G = globalThis as any;
	return G.__agentWs?.ws ?? null;
}

export function sendChatViaWebSocket(messages: AgentChatMessage[]): string | null {
	const ws = getWs();
	if (!ws || ws.readyState !== WebSocket.OPEN) {
		console.error('[agent-ws] WebSocket not connected');
		return null;
	}

	const requestId = crypto.randomUUID();

	ws.send(JSON.stringify({
		type: 'cf_agent_use_chat_request',
		id: requestId,
		init: {
			method: 'POST',
			body: JSON.stringify({
				messages: messages.map(m => ({
					id: m.id,
					role: m.role,
					content: m.content,
					createdAt: m.createdAt,
				})),
			}),
		},
	}));

	return requestId;
}

export function sendAgentMessage(message: unknown) {
	const ws = getWs();
	if (ws && ws.readyState === WebSocket.OPEN) {
		ws.send(JSON.stringify(message));
	}
}

export function isConnected(): boolean {
	const G = globalThis as any;
	return G.__agentWs?.connected ?? false;
}
