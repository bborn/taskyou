import { AgentWorkflow, type AgentWorkflowEvent, type AgentWorkflowStep } from "agents/workflows";
import { generateText, stepCountIs } from "ai";
import { createAnthropic } from "@ai-sdk/anthropic";
import * as db from "./db";
import { buildSandboxTools, buildTaskExecutionPrompt } from "./agent";
import type { TaskYouAgent } from "./agent";
import type { GitContext } from "./agent";

// Worker env type
interface WorkflowEnv {
	DB: D1Database;
	SANDBOX: DurableObjectNamespace;
	SESSIONS?: KVNamespace;
	STORAGE?: R2Bucket;
	ANTHROPIC_API_KEY?: string;
	[key: string]: unknown;
}

type TaskParams = { taskId: number; userId: string };

type TaskProgress = {
	step: string;
	status: string;
	taskId: number;
	title?: string;
};

export class TaskExecutionWorkflow extends AgentWorkflow<TaskYouAgent, TaskParams, TaskProgress> {
	async run(event: AgentWorkflowEvent<TaskParams>, step: AgentWorkflowStep) {
		const { taskId, userId } = event.payload;
		const env = this.env as unknown as WorkflowEnv;

		// Step 1: Load and mark task as processing
		const task = await step.do("mark-processing", async () => {
			const t = await db.getTask(env.DB, userId, taskId);
			if (!t) throw new Error(`Task ${taskId} not found`);
			await db.updateTask(env.DB, userId, taskId, { status: "processing" });
			return { id: t.id, title: t.title, body: t.body, type: t.type };
		});

		await this.reportProgress({ step: "processing", status: "running", taskId, title: task.title });

		// Step 2: Execute with AI + sandbox
		const result = await step.do("ai-execute", { retries: { limit: 2, delay: "5 seconds", backoff: "linear" } }, async () => {
			const anthropic = createAnthropic({
				apiKey: env.ANTHROPIC_API_KEY,
			});

			// Create sandbox for this task (dynamic import to avoid Vite bundling @cloudflare/sandbox)
			const { getSandbox } = await import("@cloudflare/sandbox");
			const sandbox = getSandbox(env.SANDBOX as any, `task-${taskId}`, {
				normalizeId: true,
				sleepAfter: "15m",
			});

			// Load project and check for GitHub repo
			let gitContext: GitContext | undefined;
			let project: import("$lib/types").Project | null = null;
			const loadedTask = await db.getTask(env.DB, userId, taskId);
			if (loadedTask?.project_id) {
				project = await db.getProjectById(env.DB, loadedTask.project_id);
				if (project?.github_repo && env.SESSIONS) {
					const token = await env.SESSIONS.get(`github-token:${userId}`);
					if (token) {
						const branch = project.github_branch || "main";

						// Clone repo
						const cloneResult = await sandbox.exec(
							`git clone --depth=50 --branch ${branch} https://x-access-token:${token}@github.com/${project.github_repo}.git /workspace`,
							{ cwd: "/" } as any,
						);
						if (cloneResult.success) {
							await sandbox.exec('git config user.name "TaskYou Bot"', { cwd: "/workspace" } as any);
							await sandbox.exec('git config user.email "bot@taskyou.dev"', { cwd: "/workspace" } as any);
							await sandbox.exec(`git checkout -b taskyou/task-${taskId}`, { cwd: "/workspace" } as any);
							await sandbox.exec(
								`git config credential.helper '!f() { echo "username=x-access-token"; echo "password=${token}"; }; f'`,
								{ cwd: "/workspace" } as any,
							);
							gitContext = { token, repo: project.github_repo, defaultBranch: branch };
						}
					}
				}
			}

			const sandboxTools = buildSandboxTools(sandbox, env.DB, taskId, undefined, env.STORAGE, gitContext);

			const prompt = `Execute this task:\nTitle: ${task.title}\n${task.body ? `Details: ${task.body}` : ""}\n${task.type ? `Type: ${task.type}` : ""}\n\nComplete this task thoroughly.${gitContext ? " The repo is already cloned at /workspace. Read existing code before making changes. After making changes, commit, push, and create a PR." : " Use write_file to create output files. For web apps, use serve_app after writing files."}`;

			const response = await generateText({
				model: anthropic("claude-sonnet-4-5-20250929"),
				system: buildTaskExecutionPrompt(project || undefined),
				prompt,
				tools: sandboxTools,
				stopWhen: stepCountIs(15),
			});

			const usage = response.usage as { promptTokens?: number; completionTokens?: number } | undefined;
			return {
				output: response.text,
				inputTokens: usage?.promptTokens || 0,
				outputTokens: usage?.completionTokens || 0,
			};
		});

		await this.reportProgress({ step: "saving", status: "running", taskId });

		// Step 3: Save results and mark done
		await step.do("save-results", async () => {
			await db.updateTask(env.DB, userId, taskId, {
				status: "done",
				output: result.output,
				summary: result.output.slice(0, 200),
			});
			await db.addTaskLog(env.DB, taskId, "output", result.output);
		});

		// Notify agent to sync state
		await this.reportProgress({ step: "complete", status: "done", taskId });

		return result;
	}
}
