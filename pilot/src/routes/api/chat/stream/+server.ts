import type { RequestHandler } from './$types';
import { getChat, createMessage, updateChat, listMessages, listTasks, createTask } from '$lib/server/db';

const SYSTEM_PROMPT = `You are Pilot, an AI assistant that helps users manage their tasks and work. You can:
- Create and manage tasks on a kanban board
- Help plan and break down work
- Answer questions about projects and code
- Provide status updates on running tasks

You have access to these tools:
- create_task: Create a new task on the board
- list_tasks: List current tasks with optional status filter
- show_board: Show the current state of the kanban board

Be concise and helpful. When the user asks you to do something, create a task for it.
When discussing tasks, reference them by their ID and title.`;

function buildTools() {
	return [
		{
			name: 'create_task',
			description: 'Create a new task on the kanban board',
			input_schema: {
				type: 'object',
				properties: {
					title: { type: 'string', description: 'Task title' },
					body: { type: 'string', description: 'Task description/instructions' },
					type: { type: 'string', enum: ['code', 'writing', 'thinking'], description: 'Task type' },
				},
				required: ['title'],
			},
		},
		{
			name: 'list_tasks',
			description: 'List current tasks, optionally filtered by status',
			input_schema: {
				type: 'object',
				properties: {
					status: { type: 'string', enum: ['backlog', 'queued', 'processing', 'blocked', 'done', 'failed'] },
				},
			},
		},
		{
			name: 'show_board',
			description: 'Show the current state of the kanban board with task counts per column',
			input_schema: { type: 'object', properties: {} },
		},
	];
}

async function handleToolCall(
	toolName: string,
	toolInput: Record<string, unknown>,
	db: D1Database,
	userId: string,
): Promise<string> {
	switch (toolName) {
		case 'create_task': {
			const task = await createTask(db, userId, {
				title: toolInput.title as string,
				body: (toolInput.body as string) || '',
				type: (toolInput.type as string) || 'code',
			});
			return JSON.stringify({ success: true, task: { id: task.id, title: task.title, status: task.status } });
		}
		case 'list_tasks': {
			const tasks = await listTasks(db, userId, {
				status: toolInput.status as string | undefined,
				includeClosed: true,
			});
			return JSON.stringify(tasks.map(t => ({ id: t.id, title: t.title, status: t.status, type: t.type })));
		}
		case 'show_board': {
			const tasks = await listTasks(db, userId, { includeClosed: true });
			const board: Record<string, number> = {};
			for (const t of tasks) {
				board[t.status] = (board[t.status] || 0) + 1;
			}
			return JSON.stringify({ columns: board, total: tasks.length });
		}
		default:
			return JSON.stringify({ error: `Unknown tool: ${toolName}` });
	}
}

export const POST: RequestHandler = async ({ locals, platform, request }) => {
	const user = locals.user;
	if (!user || !platform?.env?.DB) {
		return new Response(JSON.stringify({ error: 'Unauthorized' }), { status: 401 });
	}

	const { chat_id, content } = await request.json() as { chat_id: string; content: string };

	// Verify chat ownership
	const chat = await getChat(platform.env.DB, user.id, chat_id);
	if (!chat) {
		return new Response(JSON.stringify({ error: 'Chat not found' }), { status: 404 });
	}

	// Save user message
	await createMessage(platform.env.DB, { chat_id, role: 'user', content });

	// Update chat title from first message
	const existingMessages = await listMessages(platform.env.DB, chat_id);
	if (existingMessages.filter(m => m.role === 'user').length <= 1) {
		const title = content.slice(0, 60) + (content.length > 60 ? '...' : '');
		await updateChat(platform.env.DB, user.id, chat_id, { title });
	}

	// Get API key
	const apiKey = platform.env.ANTHROPIC_API_KEY;
	if (!apiKey) {
		// Return a helpful error as SSE
		const encoder = new TextEncoder();
		const stream = new ReadableStream({
			start(controller) {
				const errorEvent = `data: ${JSON.stringify({
					type: 'content_block_delta',
					delta: { text: 'ANTHROPIC_API_KEY is not configured. Add it as a secret in wrangler.jsonc or via `wrangler secret put ANTHROPIC_API_KEY`.' }
				})}\n\n`;
				controller.enqueue(encoder.encode(errorEvent));
				controller.enqueue(encoder.encode(`data: [DONE]\n\n`));
				controller.close();
			}
		});
		return new Response(stream, {
			headers: {
				'Content-Type': 'text/event-stream',
				'Cache-Control': 'no-cache',
				'Connection': 'keep-alive',
			},
		});
	}

	// Build message history
	type AnthropicContent = string | Array<{ type: string; text?: string; id?: string; name?: string; input?: unknown; tool_use_id?: string; content?: string }>;
	type AnthropicMessage = { role: string; content: AnthropicContent };

	const history: AnthropicMessage[] = existingMessages
		.filter(m => m.role === 'user' || m.role === 'assistant')
		.map(m => ({ role: m.role, content: m.content }));

	// Call Anthropic API with streaming
	const db = platform.env.DB;
	const modelId = chat.model_id || 'claude-sonnet-4-5-20250929';

	const encoder = new TextEncoder();

	const stream = new ReadableStream({
		async start(controller) {
			try {
				let fullContent = '';
				let inputTokens = 0;
				let outputTokens = 0;

				// Make the API call with tool loop
				let messages: AnthropicMessage[] = [...history];
				let continueLoop = true;

				while (continueLoop) {
					continueLoop = false;

					const response = await fetch('https://api.anthropic.com/v1/messages', {
						method: 'POST',
						headers: {
							'Content-Type': 'application/json',
							'x-api-key': apiKey,
							'anthropic-version': '2023-06-01',
						},
						body: JSON.stringify({
							model: modelId,
							max_tokens: 4096,
							system: SYSTEM_PROMPT,
							messages,
							tools: buildTools(),
							stream: true,
						}),
					});

					if (!response.ok) {
						const errText = await response.text();
						controller.enqueue(encoder.encode(`data: ${JSON.stringify({ type: 'error', error: `API error ${response.status}: ${errText}` })}\n\n`));
						break;
					}

					const reader = response.body?.getReader();
					if (!reader) break;

					let currentToolName = '';
					let currentToolInput = '';
					let currentToolId = '';
					let hasToolUse = false;
					let textBuffer = '';

					const decoder = new TextDecoder();
					let buffer = '';

					while (true) {
						const { done, value } = await reader.read();
						if (done) break;

						buffer += decoder.decode(value, { stream: true });
						const lines = buffer.split('\n');
						buffer = lines.pop() || '';

						for (const line of lines) {
							if (!line.startsWith('data: ')) continue;
							const data = line.slice(6).trim();
							if (data === '' || data === '[DONE]') continue;

							try {
								const event = JSON.parse(data);

								if (event.type === 'content_block_start') {
									if (event.content_block?.type === 'tool_use') {
										hasToolUse = true;
										currentToolName = event.content_block.name;
										currentToolId = event.content_block.id;
										currentToolInput = '';
										controller.enqueue(encoder.encode(`data: ${JSON.stringify({ type: 'tool_use', name: currentToolName })}\n\n`));
									}
								} else if (event.type === 'content_block_delta') {
									if (event.delta?.type === 'text_delta') {
										fullContent += event.delta.text;
										textBuffer += event.delta.text;
										controller.enqueue(encoder.encode(`data: ${JSON.stringify({ type: 'content_block_delta', delta: { text: event.delta.text } })}\n\n`));
									} else if (event.delta?.type === 'input_json_delta') {
										currentToolInput += event.delta.partial_json;
									}
								} else if (event.type === 'message_delta') {
									if (event.usage) {
										outputTokens += event.usage.output_tokens || 0;
									}
								} else if (event.type === 'message_start') {
									if (event.message?.usage) {
										inputTokens += event.message.usage.input_tokens || 0;
									}
								}
							} catch {
								// Skip malformed JSON
							}
						}
					}

					// Handle tool calls
					if (hasToolUse && currentToolName) {
						let toolInput: Record<string, unknown> = {};
						try {
							toolInput = JSON.parse(currentToolInput || '{}');
						} catch { /* empty */ }

						const toolResult = await handleToolCall(currentToolName, toolInput, db, user.id);

						// Add assistant message with tool use and tool result to continue the loop
						messages = [
							...messages,
							{
								role: 'assistant' as const,
								content: [
									...(textBuffer ? [{ type: 'text' as const, text: textBuffer }] : []),
									{ type: 'tool_use' as const, id: currentToolId, name: currentToolName, input: toolInput },
								],
							},
							{
								role: 'user' as const,
								content: [
									{ type: 'tool_result' as const, tool_use_id: currentToolId, content: toolResult },
								],
							},
						];
						textBuffer = '';
						continueLoop = true;
					}
				}

				// Save assistant message
				if (fullContent) {
					const msg = await createMessage(db, {
						chat_id,
						role: 'assistant',
						content: fullContent,
						model_id: modelId,
						input_tokens: inputTokens,
						output_tokens: outputTokens,
					});

					controller.enqueue(encoder.encode(`data: ${JSON.stringify({
						type: 'message_complete',
						message: { id: msg.id, input_tokens: inputTokens, output_tokens: outputTokens, model_id: modelId },
					})}\n\n`));
				}

				controller.enqueue(encoder.encode(`data: [DONE]\n\n`));
			} catch (e) {
				const errorMsg = e instanceof Error ? e.message : 'Unknown error';
				controller.enqueue(encoder.encode(`data: ${JSON.stringify({ type: 'error', error: errorMsg })}\n\n`));
			} finally {
				controller.close();
			}
		},
	});

	return new Response(stream, {
		headers: {
			'Content-Type': 'text/event-stream',
			'Cache-Control': 'no-cache',
			'Connection': 'keep-alive',
		},
	});
};
