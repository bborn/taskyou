import type { Chat, Message } from '$lib/types';
import { chats as chatsApi, messages as messagesApi } from '$lib/api/client';

export const chatState = $state({
	chats: [] as Chat[],
	activeChat: null as Chat | null,
	messages: [] as Message[],
	loading: false,
	streaming: false,
	streamingContent: '',
});

export async function fetchChats() {
	try {
		chatState.chats = await chatsApi.list();
	} catch (e) {
		console.error('Failed to fetch chats:', e);
	}
}

export async function selectChat(chat: Chat) {
	chatState.activeChat = chat;
	chatState.loading = true;
	try {
		chatState.messages = await messagesApi.list(chat.id);
	} catch (e) {
		console.error('Failed to fetch messages:', e);
	} finally {
		chatState.loading = false;
	}
}

export async function createNewChat(modelId?: string): Promise<Chat> {
	const chat = await chatsApi.create({ model_id: modelId });
	chatState.chats = [chat, ...chatState.chats];
	chatState.activeChat = chat;
	chatState.messages = [];
	return chat;
}

export async function deleteCurrentChat() {
	if (!chatState.activeChat) return;
	await chatsApi.delete(chatState.activeChat.id);
	chatState.chats = chatState.chats.filter(c => c.id !== chatState.activeChat!.id);
	chatState.activeChat = null;
	chatState.messages = [];
}

export async function sendMessage(content: string) {
	if (!chatState.activeChat || !content.trim()) return;

	const chatId = chatState.activeChat.id;

	// Add user message optimistically
	const userMsg: Message = {
		id: crypto.randomUUID(),
		chat_id: chatId,
		role: 'user',
		content: content.trim(),
		input_tokens: 0,
		output_tokens: 0,
		created_at: new Date().toISOString(),
	};
	chatState.messages = [...chatState.messages, userMsg];

	// Auto-title from first message
	if (chatState.messages.filter(m => m.role === 'user').length === 1) {
		const title = content.trim().slice(0, 60) + (content.length > 60 ? '...' : '');
		chatState.activeChat = { ...chatState.activeChat, title };
		chatState.chats = chatState.chats.map(c =>
			c.id === chatId ? { ...c, title } : c
		);
	}

	// Stream LLM response
	chatState.streaming = true;
	chatState.streamingContent = '';

	try {
		const response = await fetch('/api/chat/stream', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ chat_id: chatId, content: content.trim() }),
			credentials: 'include',
		});

		if (!response.ok) {
			throw new Error(`Chat failed: ${response.statusText}`);
		}

		const reader = response.body?.getReader();
		const decoder = new TextDecoder();
		let fullContent = '';

		if (reader) {
			while (true) {
				const { done, value } = await reader.read();
				if (done) break;

				const chunk = decoder.decode(value, { stream: true });
				const lines = chunk.split('\n');

				for (const line of lines) {
					if (line.startsWith('data: ')) {
						const data = line.slice(6);
						if (data === '[DONE]') continue;

						try {
							const event = JSON.parse(data);
							if (event.type === 'content_block_delta' && event.delta?.text) {
								fullContent += event.delta.text;
								chatState.streamingContent = fullContent;
							} else if (event.type === 'message_complete') {
								// Final message with token counts
								if (event.message) {
									const assistantMsg: Message = {
										id: event.message.id || crypto.randomUUID(),
										chat_id: chatId,
										role: 'assistant',
										content: fullContent,
										input_tokens: event.message.input_tokens || 0,
										output_tokens: event.message.output_tokens || 0,
										model_id: event.message.model_id,
										created_at: new Date().toISOString(),
									};
									chatState.messages = [...chatState.messages, assistantMsg];
								}
							} else if (event.type === 'tool_use') {
								// Show tool call in stream
								chatState.streamingContent = fullContent + `\n\n*Using tool: ${event.name}...*`;
							} else if (event.type === 'error') {
								fullContent += `\n\n**Error:** ${event.error}`;
								chatState.streamingContent = fullContent;
							}
						} catch {
							// Skip malformed JSON
						}
					}
				}
			}
		}

		// If no message_complete event, add the message manually
		if (fullContent && !chatState.messages.some(m => m.content === fullContent && m.role === 'assistant')) {
			const assistantMsg: Message = {
				id: crypto.randomUUID(),
				chat_id: chatId,
				role: 'assistant',
				content: fullContent,
				input_tokens: 0,
				output_tokens: 0,
				created_at: new Date().toISOString(),
			};
			chatState.messages = [...chatState.messages, assistantMsg];
		}
	} catch (e) {
		console.error('Streaming failed:', e);
		const errorMsg: Message = {
			id: crypto.randomUUID(),
			chat_id: chatId,
			role: 'assistant',
			content: `**Error:** ${e instanceof Error ? e.message : 'Failed to get response'}. Check that ANTHROPIC_API_KEY is configured.`,
			input_tokens: 0,
			output_tokens: 0,
			created_at: new Date().toISOString(),
		};
		chatState.messages = [...chatState.messages, errorMsg];
	} finally {
		chatState.streaming = false;
		chatState.streamingContent = '';
	}
}
