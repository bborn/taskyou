import { AIChatAgent } from "@cloudflare/ai-chat";
import { createAnthropic } from "@ai-sdk/anthropic";
import { streamText, generateText, convertToModelMessages, tool, stepCountIs } from "ai";
import { z } from "zod";
import * as db from "./db";

// Worker env type for agent context (not SvelteKit platform)
interface AgentEnv {
	DB: D1Database;
	TASKYOU_AGENT: DurableObjectNamespace;
	TASK_WORKFLOW: unknown; // Workflow binding for TaskExecutionWorkflow
	SANDBOX: DurableObjectNamespace;
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

			// Task-scoped instance: provide task context + sandbox tools
			const taskId = this.getTaskId();
			if (taskId !== null) {
				const userId = this.getUserId();
				const task = await db.getTask(this.env.DB, userId, taskId);
				if (!task) {
					throw new Error(`Task ${taskId} not found`);
				}

				let project: import("$lib/types").Project | null = null;
				if (task.project_id) {
					project = await db.getProjectById(this.env.DB, task.project_id);
				}

				const { getSandbox } = await import("@cloudflare/sandbox");
				const sandbox = getSandbox(this.env.SANDBOX as any, `task-${taskId}`, {
					normalizeId: true,
					sleepAfter: "15m",
				});

				const sandboxTools = buildSandboxTools(sandbox, this.env.DB, taskId, undefined, this.env.STORAGE);

				const result = streamText({
					model: anthropic("claude-sonnet-4-5-20250929"),
					system: this.buildTaskChatSystemPrompt(task, project),
					messages: modelMessages,
					tools: sandboxTools,
					onFinish,
					stopWhen: stepCountIs(10),
				});

				return result.toUIMessageStreamResponse();
			}

			// Main chat instance: orchestrator tools
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

	getTaskId(): number | null {
		const decoded = decodeURIComponent(this.name);
		const parts = decoded.split(':');
		if (parts.length < 2) return null;
		const segment = parts[1];
		if (segment.startsWith('task-')) {
			const id = parseInt(segment.slice(5), 10);
			return isNaN(id) ? null : id;
		}
		return null;
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
				description: "Execute a task using an AI agent with a sandbox environment. The task must already exist. The agent writes files, runs commands, and can serve web apps in the sandbox. If the task's project has a GitHub repo, the sandbox clones it first.",
				inputSchema: z.object({
					task_id: z.number().describe("Task ID to execute"),
				}),
				execute: async ({ task_id }) => {
					const apiKey = self.env.ANTHROPIC_API_KEY;
					if (!apiKey) return { error: "ANTHROPIC_API_KEY not configured" };

					await self.syncTasks();
					const result = await executeTask({
						db: self.env.DB,
						sandbox: self.env.SANDBOX,
						sessions: self.env.SESSIONS,
						storage: self.env.STORAGE,
						apiKey,
						userId: self.getUserId(),
						taskId: task_id,
					});
					await self.syncTasks();
					return 'error' in result ? result : { executed: true, output: result.output };
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

	// Workflow lifecycle callbacks — relay progress to connected frontend clients
	async onWorkflowProgress(_name: string, _id: string, progress: unknown) {
		this.broadcast(JSON.stringify({ type: 'workflow-progress', workflowId: _id, progress }));
		await this.syncTasks();
	}

	async onWorkflowComplete(_name: string, _id: string, result?: unknown) {
		this.broadcast(JSON.stringify({ type: 'workflow-complete', workflowId: _id, result }));
		await this.syncTasks();
	}

	async onWorkflowError(_name: string, _id: string, error: string) {
		this.broadcast(JSON.stringify({ type: 'workflow-error', workflowId: _id, error }));
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
2. Execute it using run_task — this spawns an AI agent with a sandbox environment
3. The agent writes real files, runs commands, and can serve web apps
4. The board updates in real-time as the task progresses

The sandbox environment gives each task its own Linux container with a real filesystem.
Tasks can produce files (code, documents) and web apps with live preview URLs.
Be concise and action-oriented. Create the task, run it, and let the user know it's underway.`;
	}

	private buildTaskChatSystemPrompt(task: import("$lib/types").Task, project: import("$lib/types").Project | null): string {
		let prompt = `You are Pilot, an AI assistant helping with a specific task in its sandbox environment.
You have access to the task's Linux sandbox with tools to read/write files and run commands.

## Current Task
- **Title**: ${task.title}
- **Status**: ${task.status}
- **Type**: ${task.type}`;

		if (task.body) {
			prompt += `\n- **Details**: ${task.body}`;
		}

		if (project?.instructions) {
			prompt += `\n\n## Project Instructions\n${project.instructions}`;
		}

		prompt += `\n
## Capabilities
- Use read_file to inspect files in the sandbox at /workspace
- Use write_file to create or modify files
- Use run_command to execute shell commands (build, test, install packages, etc.)
- Use serve_app to start a web server and get a preview URL

The sandbox may already contain files from a previous task execution. Explore first with run_command or read_file before making changes.
Be concise and helpful. Focus on the task at hand.`;

		return prompt;
	}

	private getUserId(): string {
		// The agent instance name is URL-encoded "{userId}:{chatId}" — decode and extract userId
		const decoded = decodeURIComponent(this.name);
		return decoded.split(':')[0];
	}
}

// ── Task execution (used by both agent run_task tool and API route) ──

export async function executeTask(opts: {
	db: D1Database;
	sandbox: DurableObjectNamespace;
	sessions: KVNamespace;
	storage?: R2Bucket;
	apiKey: string;
	userId: string;
	taskId: number;
}): Promise<{ output: string } | { error: string }> {
	const { db: database, sandbox: sandboxNs, sessions, storage, apiKey, userId, taskId } = opts;

	const task = await db.getTask(database, userId, taskId);
	if (!task) return { error: "Task not found" };

	await db.updateTask(database, userId, taskId, { status: "processing" });
	await db.addTaskLog(database, taskId, "system", `Starting task: ${task.title}`);

	try {
		const { getSandbox } = await import("@cloudflare/sandbox");
		const sandbox = getSandbox(sandboxNs as any, `task-${taskId}`, {
			normalizeId: true,
			sleepAfter: "15m",
		});

		// Load project and check for GitHub repo
		let gitContext: GitContext | undefined;
		let project: import("$lib/types").Project | null = null;
		if (task.project_id) {
			project = await db.getProjectById(database, task.project_id);
			if (project?.github_repo) {
				const token = await sessions.get(`github-token:${userId}`);
				if (!token) {
					await db.updateTask(database, userId, taskId, { status: "failed", output: "GitHub not connected" });
					return { error: "GitHub not connected. Connect your GitHub account in project settings." };
				}

				const branch = project.github_branch || "main";
				await db.addTaskLog(database, taskId, "system", `Cloning ${project.github_repo} (branch: ${branch})...`);

				const cloneResult = await sandbox.exec(
					`git clone --depth=50 --branch ${branch} https://x-access-token:${token}@github.com/${project.github_repo}.git /workspace`,
					{ cwd: "/" } as any,
				);
				if (!cloneResult.success) {
					await db.updateTask(database, userId, taskId, { status: "failed", output: `Clone failed: ${cloneResult.stderr}` });
					return { error: `Failed to clone repo: ${cloneResult.stderr}` };
				}

				await sandbox.exec('git config user.name "TaskYou Bot"', { cwd: "/workspace" } as any);
				await sandbox.exec('git config user.email "bot@taskyou.dev"', { cwd: "/workspace" } as any);
				const taskBranch = `taskyou/task-${taskId}`;
				await sandbox.exec(`git checkout -b ${taskBranch}`, { cwd: "/workspace" } as any);
				await sandbox.exec(
					`git config credential.helper '!f() { echo "username=x-access-token"; echo "password=${token}"; }; f'`,
					{ cwd: "/workspace" } as any,
				);

				await db.addTaskLog(database, taskId, "system", `Cloned. Working on branch ${taskBranch}`);
				gitContext = { token, repo: project.github_repo, defaultBranch: branch };
			}
		}

		const sandboxTools = buildSandboxTools(sandbox, database, taskId, undefined, storage, gitContext);
		const anthropic = createAnthropic({ apiKey });
		const response = await generateText({
			model: anthropic("claude-sonnet-4-5-20250929"),
			system: buildTaskExecutionPrompt(project || undefined),
			prompt: `Execute this task:\nTitle: ${task.title}\n${task.body ? `Details: ${task.body}` : ""}\n${task.type ? `Type: ${task.type}` : ""}\n\nComplete this task thoroughly.${gitContext ? " The repo is already cloned at /workspace. Read existing code before making changes. After making changes, commit, push, and create a PR." : " Use write_file to create output files. For web apps, use serve_app after writing files."}`,
			tools: sandboxTools,
			stopWhen: stepCountIs(15),
			onStepFinish: async (event) => {
				try {
					for (const tc of event.toolCalls || []) {
						const args = JSON.stringify(tc.args) || '';
						await db.addTaskLog(database, taskId, "tool", `${tc.toolName}(${args.slice(0, 200)})`);
					}
					for (const tr of event.toolResults || []) {
						const result = JSON.stringify(tr.result) || '';
						await db.addTaskLog(database, taskId, "output", result.slice(0, 500));
					}
					if (event.text) {
						await db.addTaskLog(database, taskId, "text", event.text.slice(0, 500));
					}
				} catch (logErr) {
					console.error("[executeTask] onStepFinish logging error:", logErr);
				}
			},
		});

		await db.updateTask(database, userId, taskId, {
			status: "done",
			output: response.text,
			summary: response.text.slice(0, 200),
		});
		await db.addTaskLog(database, taskId, "output", response.text);
		return { output: response.text };
	} catch (execErr) {
		console.error("[executeTask] Execution failed:", execErr);
		const errMsg = execErr instanceof Error ? execErr.message : String(execErr);
		await db.updateTask(database, userId, taskId, {
			status: "failed",
			output: `Execution error: ${errMsg}`,
		});
		await db.addTaskLog(database, taskId, "error", errMsg);
		return { error: `Execution failed: ${errMsg}` };
	}
}

// ── Sandbox tools shared between agent and workflow ──

function guessMimeType(path: string): string {
	const ext = path.split(".").pop()?.toLowerCase() || "";
	const mimeMap: Record<string, string> = {
		html: "text/html", htm: "text/html", css: "text/css", js: "application/javascript",
		ts: "text/typescript", json: "application/json", md: "text/markdown",
		py: "text/x-python", rb: "text/x-ruby", sh: "text/x-shellscript",
		txt: "text/plain", svg: "image/svg+xml", xml: "application/xml",
		yaml: "text/yaml", yml: "text/yaml", toml: "text/toml",
	};
	return mimeMap[ext] || "text/plain";
}

export type GitContext = {
	token: string;
	repo: string; // "owner/repo"
	defaultBranch: string;
};

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function buildSandboxTools(
	sandbox: any,
	database: D1Database,
	taskId: number,
	hostname?: string,
	storage?: R2Bucket,
	gitContext?: GitContext,
) {
	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	const tools: Record<string, any> = {
		write_file: tool({
			description: "Write a file to the task sandbox. Use this to create deliverables — code, HTML, documents, etc.",
			inputSchema: z.object({
				path: z.string().describe("File path relative to /workspace (e.g. 'index.html', 'src/app.js')"),
				content: z.string().describe("File content"),
			}),
			execute: async ({ path, content }) => {
				const fullPath = `/workspace/${path}`;
				await sandbox.writeFile(fullPath, content, { encoding: "utf-8" });
				const mimeType = guessMimeType(path);
				const sizeBytes = new TextEncoder().encode(content).length;
				await db.addTaskFile(database, taskId, path, mimeType, sizeBytes);
				// Persist to R2 so files survive sandbox shutdown
				if (storage) {
					await storage.put(`tasks/${taskId}/${path}`, content, {
						httpMetadata: { contentType: mimeType },
					});
				}
				return { written: true, path, size: sizeBytes };
			},
		}),
		read_file: tool({
			description: "Read a file from the task sandbox",
			inputSchema: z.object({
				path: z.string().describe("File path relative to /workspace"),
			}),
			execute: async ({ path }) => {
				const file = await sandbox.readFile(`/workspace/${path}`, { encoding: "utf-8" });
				return { content: file.content, path };
			},
		}),
		run_command: tool({
			description: "Run a shell command in the task sandbox (e.g. npm install, python script.py, build commands)",
			inputSchema: z.object({
				command: z.string().describe("Shell command to execute"),
				cwd: z.string().optional().describe("Working directory (default: /workspace)"),
			}),
			execute: async ({ command, cwd }) => {
				const result = await sandbox.exec(command, { cwd: cwd || "/workspace" } as any);
				return {
					success: result.success,
					exitCode: result.exitCode,
					stdout: result.stdout?.slice(0, 5000),
					stderr: result.stderr?.slice(0, 2000),
				};
			},
		}),
		serve_app: tool({
			description: "Start a web server in the sandbox and expose it via a preview URL. Use after writing HTML/JS/CSS files.",
			inputSchema: z.object({
				port: z.number().optional().describe("Port to serve on (default: 8080)"),
				directory: z.string().optional().describe("Directory to serve (default: /workspace)"),
			}),
			execute: async ({ port, directory }) => {
				const servePort = port || 8080;
				const serveDir = directory || "/workspace";
				await sandbox.startProcess(`npx serve -l ${servePort} ${serveDir}`, {
					processId: `serve-${servePort}`,
					cwd: "/workspace",
				});
				const exposed = await sandbox.exposePort(servePort, {
					...(hostname ? { hostname } : {}),
				} as any);
				const previewUrl = exposed.url;
				await db.updateTask(database, "", taskId, { preview_url: previewUrl } as any);
				return { preview_url: previewUrl, port: servePort };
			},
		}),
	};

	// Add git tools when working on a cloned repo
	if (gitContext) {
		tools.create_pull_request = tool({
			description: "Commit all changes, push to GitHub, and create a pull request. Call this after making your code changes.",
			inputSchema: z.object({
				title: z.string().describe("PR title"),
				body: z.string().optional().describe("PR description"),
			}),
			execute: async ({ title, body }) => {
				// Get current branch
				const branchResult = await sandbox.exec("git rev-parse --abbrev-ref HEAD", { cwd: "/workspace" } as any);
				const branch = branchResult.stdout?.trim();
				if (!branch) return { error: "Could not determine current branch" };

				// Stage and commit
				await sandbox.exec("git add -A", { cwd: "/workspace" } as any);
				const commitResult = await sandbox.exec(
					`git commit -m "${title.replace(/"/g, '\\"')}"`,
					{ cwd: "/workspace" } as any,
				);
				if (!commitResult.success && !commitResult.stderr?.includes("nothing to commit")) {
					return { error: `Commit failed: ${commitResult.stderr}` };
				}

				// Push
				const pushResult = await sandbox.exec(`git push -u origin ${branch}`, { cwd: "/workspace" } as any);
				if (!pushResult.success) {
					return { error: `Push failed: ${pushResult.stderr}` };
				}

				// Create PR via GitHub API
				const [owner, repo] = gitContext.repo.split("/");
				const prResponse = await fetch(`https://api.github.com/repos/${owner}/${repo}/pulls`, {
					method: "POST",
					headers: {
						Authorization: `Bearer ${gitContext.token}`,
						"Content-Type": "application/json",
						"User-Agent": "TaskYou-Pilot",
					},
					body: JSON.stringify({
						title,
						body: body || `Created by TaskYou (task #${taskId})`,
						head: branch,
						base: gitContext.defaultBranch,
					}),
				});

				if (!prResponse.ok) {
					const errText = await prResponse.text();
					return { error: `Failed to create PR: ${errText}` };
				}

				const pr = await prResponse.json() as { html_url: string; number: number };
				return { success: true, pr_url: pr.html_url, pr_number: pr.number, branch };
			},
		});
	}

	return tools;
}

export function buildTaskExecutionPrompt(project?: import("$lib/types").Project): string {
	const hasRepo = !!project?.github_repo;

	let prompt = `You are a task execution agent with a sandbox environment.
You have access to a real Linux container with a filesystem.`;

	if (hasRepo) {
		prompt += `

You are working on an existing codebase cloned from GitHub (${project!.github_repo}).
The code is at /workspace on branch taskyou/task-*.

Guidelines:
- Use read_file and run_command to understand the existing code before making changes
- Make focused, minimal changes to accomplish the task
- Use run_command to run existing tests if available
- After making changes, use create_pull_request to commit, push, and open a PR
- Do NOT use write_file for files that already exist — use run_command with sed or similar, or read then write`;
	} else {
		prompt += `

When executing tasks, always produce output files:
- Use write_file to create deliverables (code, documents, reports)
- For web apps: write HTML/CSS/JS files, use run_command if a build step is needed, then call serve_app
- Use run_command for build steps (npm install, npm run build, etc.)
- Every task should produce at least one output file`;
	}

	if (project?.instructions) {
		prompt += `\n\nProject instructions:\n${project.instructions}`;
	}

	prompt += `\n\nBe thorough but concise. Write clean, well-structured code.`;

	return prompt;
}
