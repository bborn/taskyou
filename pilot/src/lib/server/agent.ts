import { AIChatAgent } from "@cloudflare/ai-chat";
import { createAnthropic } from "@ai-sdk/anthropic";
import { streamText, generateText, convertToModelMessages, tool, stepCountIs } from "ai";
import { z } from "zod";
import * as db from "./db";

// Worker env type for agent context (not SvelteKit platform)
interface AgentEnv {
	DB: D1Database;
	TASKYOU_AGENT: DurableObjectNamespace;
	TASK_WORKFLOW?: any;
	SESSIONS: KVNamespace;
	STORAGE?: R2Bucket;
	ANTHROPIC_API_KEY?: string;
	[key: string]: unknown;
}

// State synced to all connected frontend clients in real-time
export type AgentState = {
	tasks: Array<{
		id: number;
		title: string;
		status: string;
		type: string;
		project_id: string;
		updated_at: string;
	}>;
	activeProject: string | null;
	lastSync: string;
};

export class TaskYouAgent extends AIChatAgent<AgentEnv, AgentState> {
	initialState: AgentState = {
		tasks: [],
		activeProject: null,
		lastSync: "",
	};

	onError(error: unknown) {
		console.error("[TaskYouAgent] onError:", error);
		return error instanceof Error ? error : new Error(String(error));
	}

	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	async onChatMessage(onFinish: any, options?: any) {
		try {
			if (!this.messages.length) {
				throw new Error("No messages to process");
			}

			const apiKey = this.env.ANTHROPIC_API_KEY;
			if (!apiKey) {
				throw new Error("ANTHROPIC_API_KEY not configured");
			}

			const anthropic = createAnthropic({ apiKey });
			const modelMessages = await convertToModelMessages(this.messages as any);

			const result = streamText({
				model: anthropic("claude-sonnet-4-5-20250929"),
				system: this.buildSystemPrompt(),
				messages: modelMessages,
				tools: this.getTools(),
				onFinish,
				stopWhen: stepCountIs(5),
			});

			return result.toUIMessageStreamResponse();
		} catch (err) {
			console.error("[TaskYouAgent] onChatMessage error:", err);
			throw err;
		}
	}

	private getTools() {
		const self = this;

		return {
			create_task: tool({
				description: "Create a new task on the board",
				inputSchema: z.object({
					title: z.string().describe("Task title"),
					body: z.string().optional().describe("Task description/details"),
					type: z.enum(["code", "writing", "thinking"]).optional().describe("Task type"),
					project_id: z.string().optional().describe("Project ID to assign to"),
				}),
				execute: async ({ title, body, type, project_id }) => {
					const task = await db.createTask(self.env.DB, self.getUserId(), {
						title, body, type, project_id,
					});
					await self.syncTasks();
					return { id: task.id, title: task.title, status: task.status };
				},
			}),
			list_tasks: tool({
				description: "List tasks, optionally filtered by status or project",
				inputSchema: z.object({
					status: z.string().optional().describe("Filter by status: backlog, queued, processing, blocked, done, failed"),
					project_id: z.string().optional().describe("Filter by project ID"),
				}),
				execute: async ({ status, project_id }) => {
					const tasks = await db.listTasks(self.env.DB, self.getUserId(), {
						status, project_id, includeClosed: true,
					});
					return tasks.map((t) => ({
						id: t.id, title: t.title, status: t.status,
						type: t.type, project_id: t.project_id, created_at: t.created_at,
					}));
				},
			}),
			update_task: tool({
				description: "Update a task's title, body, status, or type",
				inputSchema: z.object({
					task_id: z.number().describe("Task ID to update"),
					title: z.string().optional(),
					body: z.string().optional(),
					status: z.enum(["backlog", "queued", "processing", "blocked", "done", "failed"]).optional(),
					type: z.string().optional(),
				}),
				execute: async ({ task_id, ...data }) => {
					const task = await db.updateTask(self.env.DB, self.getUserId(), task_id, data);
					await self.syncTasks();
					return task
						? { id: task.id, title: task.title, status: task.status }
						: { error: "Task not found" };
				},
			}),
			delete_task: tool({
				description: "Delete a task from the board",
				inputSchema: z.object({
					task_id: z.number().describe("Task ID to delete"),
				}),
				execute: async ({ task_id }) => {
					const deleted = await db.deleteTask(self.env.DB, self.getUserId(), task_id);
					await self.syncTasks();
					return { deleted };
				},
			}),
			run_task: tool({
				description: "Execute a task using an AI workflow agent. The task must already exist. Spawns a background workflow that processes the task autonomously.",
				inputSchema: z.object({
					task_id: z.number().describe("Task ID to execute"),
				}),
				execute: async ({ task_id }) => {
					await db.updateTask(self.env.DB, self.getUserId(), task_id, { status: "queued" });
					await self.syncTasks();

					try {
						const workflowId = await self.runWorkflow("TASK_WORKFLOW", {
							taskId: task_id,
							userId: self.getUserId(),
						});
						return { queued: true, workflowId };
					} catch (err) {
						// Workflow binding may not be available in local dev — fall back to direct execution
						console.warn("[run_task] Workflow unavailable, executing inline:", err);
						await db.updateTask(self.env.DB, self.getUserId(), task_id, { status: "processing" });
						await self.syncTasks();

						const task = await db.getTask(self.env.DB, self.getUserId(), task_id);
						if (!task) return { error: "Task not found" };

						const apiKey = self.env.ANTHROPIC_API_KEY;
						if (!apiKey) return { error: "ANTHROPIC_API_KEY not configured" };

						const anthropic = createAnthropic({ apiKey });
						const response = await generateText({
							model: anthropic("claude-sonnet-4-5-20250929"),
							prompt: `Execute this task:\nTitle: ${task.title}\n${task.body ? `Details: ${task.body}` : ""}\n\nComplete this task and provide the result.`,
							stopWhen: stepCountIs(1),
						});

						await db.updateTask(self.env.DB, self.getUserId(), task_id, {
							status: "done",
							output: response.text,
							summary: response.text.slice(0, 200),
						});
						await self.syncTasks();
						return { executed: true, output: response.text };
					}
				},
			}),
			show_board: tool({
				description: "Show the current kanban board summary with task counts per column",
				inputSchema: z.object({}),
				execute: async () => {
					const tasks = await db.listTasks(self.env.DB, self.getUserId(), { includeClosed: true });
					return {
						backlog: tasks.filter((t) => t.status === "backlog").length,
						running: tasks.filter((t) => ["queued", "processing"].includes(t.status)).length,
						blocked: tasks.filter((t) => t.status === "blocked").length,
						done: tasks.filter((t) => t.status === "done").length,
						failed: tasks.filter((t) => t.status === "failed").length,
						total: tasks.length,
					};
				},
			}),
			list_projects: tool({
				description: "List all projects",
				inputSchema: z.object({}),
				execute: async () => {
					const projects = await db.listProjects(self.env.DB, self.getUserId());
					return projects.map((p) => ({
						id: p.id, name: p.name, color: p.color,
					}));
				},
			}),
		};
	}

	// Sync tasks from D1 -> agent state -> all connected clients
	async syncTasks() {
		const tasks = await db.listTasks(this.env.DB, this.getUserId(), { includeClosed: true });
		this.setState({
			...this.state,
			tasks: tasks.map((t) => ({
				id: t.id,
				title: t.title,
				status: t.status,
				type: t.type,
				project_id: t.project_id,
				updated_at: t.updated_at,
			})),
			lastSync: new Date().toISOString(),
		});
	}

	async onWorkflowProgress(_name: string, _id: string, progress: unknown) {
		this.broadcast(JSON.stringify({ type: "workflow-progress", workflowId: _id, progress }));
		await this.syncTasks();
	}

	async onWorkflowComplete(_name: string, _id: string, result?: unknown) {
		this.broadcast(JSON.stringify({ type: "workflow-complete", workflowId: _id, result }));
		await this.syncTasks();
	}

	async onWorkflowError(_name: string, _id: string, error: string) {
		this.broadcast(JSON.stringify({ type: "workflow-error", workflowId: _id, error }));
		await this.syncTasks();
	}

	private buildSystemPrompt(): string {
		return `You are Pilot, the orchestrator for the TaskYou platform.
You manage tasks and delegate execution to specialized workflow agents.

You can:
- Create tasks on the kanban board
- Execute tasks by spawning AI workflow agents via run_task
- List tasks, show board summary, list projects
- Update or delete tasks

When a user asks you to do something (write a poem, fix a bug, draft an email, etc.):
1. Create a task for it using create_task
2. Execute it using run_task — this spawns a background AI workflow agent
3. The workflow agent handles the work autonomously (queued → processing → done)
4. The board updates in real-time as the workflow progresses

You are the orchestrator — you decide what needs doing and delegate to workflow agents.
Be concise and action-oriented. Create the task, run it, and let the user know it's underway.`;
	}

	private getUserId(): string {
		// The agent instance name is URL-encoded "{userId}:{chatId}" — decode and extract userId
		const decoded = decodeURIComponent(this.name);
		return decoded.split(':')[0];
	}
}
