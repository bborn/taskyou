import type { Chat, Message, AgentChatMessage } from '$lib/types';
import { chats as chatsApi, messages as messagesApi } from '$lib/api/client';
import { sendChatViaWebSocket } from './agent-ws';
import { agentState, agentChatStream, restoredMessages, setOnMessagesRestored } from './agent.svelte';

export const chatState = $state({
	chats: [] as Chat[],
	activeChat: null as Chat | null,
	messages: [] as Message[],
	// Agent chat messages (from WebSocket)
	agentMessages: [] as AgentChatMessage[],
	loading: false,
});

// Derived streaming state from agent store (single source of truth)
export function isStreaming() {
	return agentChatStream.streaming;
}

export function getStreamingContent() {
	return agentChatStream.streamingContent;
}

export async function fetchChats() {
	try {
		chatState.chats = await chatsApi.list();
	} catch (e) {
		console.error('Failed to fetch chats:', e);
	}
}

export function selectChat(chat: Chat) {
	chatState.activeChat = chat;
	chatState.messages = [];
	chatState.agentMessages = [];
	// Agent DO handles persistence — messages restored via cf_agent_chat_messages on WS connect
}

export async function createNewChat(modelId?: string): Promise<Chat> {
	const chat = await chatsApi.create({ model_id: modelId });
	chatState.chats = [chat, ...chatState.chats];
	chatState.activeChat = chat;
	chatState.messages = [];
	chatState.agentMessages = [];
	return chat;
}

export async function deleteChat(chatId: string) {
	await chatsApi.delete(chatId);
	chatState.chats = chatState.chats.filter(c => c.id !== chatId);
	if (chatState.activeChat?.id === chatId) {
		chatState.activeChat = null;
		chatState.messages = [];
		chatState.agentMessages = [];
	}
}

/**
 * Load persisted messages from the agent's DO storage.
 * Called automatically when cf_agent_chat_messages is received via WebSocket.
 */
export function loadRestoredMessages() {
	if (restoredMessages.length > 0) {
		chatState.agentMessages = [...restoredMessages];
	}
}

// NOTE: setOnMessagesRestored(loadRestoredMessages) must be called from +page.svelte
// to survive Vite tree-shaking. See memory note on tree-shaking.

/**
 * Send a message to the agent via WebSocket (AIChatAgent protocol).
 * The agent handles persistence, streaming, and tool calling.
 */
export async function sendAgentChatMessage(content: string, _userId: string) {
	if (!content.trim() || agentChatStream.streaming) return;

	if (!agentState.connected) {
		const errorMsg: AgentChatMessage = {
			id: crypto.randomUUID(),
			role: 'assistant',
			content: '**Error:** Not connected to agent. Please wait for connection...',
			createdAt: new Date().toISOString(),
		};
		chatState.agentMessages = [...chatState.agentMessages, errorMsg];
		return;
	}

	if (!chatState.activeChat) {
		const errorMsg: AgentChatMessage = {
			id: crypto.randomUUID(),
			role: 'assistant',
			content: '**Error:** No active chat. Please create or select a chat first.',
			createdAt: new Date().toISOString(),
		};
		chatState.agentMessages = [...chatState.agentMessages, errorMsg];
		return;
	}

	// Add user message optimistically
	const userMsg: AgentChatMessage = {
		id: crypto.randomUUID(),
		role: 'user',
		content: content.trim(),
		createdAt: new Date().toISOString(),
	};
	chatState.agentMessages = [...chatState.agentMessages, userMsg];
	if (chatState.agentMessages.filter(m => m.role === 'user').length === 1 && chatState.activeChat) {
		const title = content.trim().slice(0, 60) + (content.length > 60 ? '...' : '');
		chatState.activeChat = { ...chatState.activeChat, title };
		chatState.chats = chatState.chats.map(c =>
			c.id === chatState.activeChat!.id ? { ...c, title } : c
		);
	}

	// Set streaming state before sending
	agentChatStream.streaming = true;
	agentChatStream.streamingContent = '';
	agentChatStream.lastError = null;
	agentChatStream.completedMessage = null;

	// Send all messages via WebSocket using AIChatAgent protocol
	const requestId = sendChatViaWebSocket(chatState.agentMessages);

	if (!requestId) {
		agentChatStream.streaming = false;
		const errorMsg: AgentChatMessage = {
			id: crypto.randomUUID(),
			role: 'assistant',
			content: '**Error:** Failed to send message. WebSocket not connected.',
			createdAt: new Date().toISOString(),
		};
		chatState.agentMessages = [...chatState.agentMessages, errorMsg];
	}
}
