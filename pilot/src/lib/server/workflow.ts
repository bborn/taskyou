import { AgentWorkflow, type AgentWorkflowEvent, type AgentWorkflowStep } from "agents/workflows";
import { generateText, tool, stepCountIs } from "ai";
import { createAnthropic } from "@ai-sdk/anthropic";
import { z } from "zod";
import * as db from "./db";
import type { TaskYouAgent } from "./agent";

// Worker env type
interface WorkflowEnv {
	DB: D1Database;
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

		// Step 2: Execute with AI
		const result = await step.do("ai-execute", { retries: { limit: 2, delay: "5 seconds", backoff: "linear" } }, async () => {
			const anthropic = createAnthropic({
				apiKey: env.ANTHROPIC_API_KEY,
			});

			const prompt = `You are an AI assistant executing a task.
Task: ${task.title}
${task.body ? `Details: ${task.body}` : ""}
${task.type ? `Type: ${task.type}` : ""}

Execute this task thoroughly. Use the available tools as needed.
When you're done, provide a clear summary of what was accomplished.`;

			const response = await generateText({
				model: anthropic("claude-sonnet-4-5-20250929"),
				system: "You are a task execution agent. Complete the given task using the tools available. Be thorough but concise.",
				prompt,
				tools: {
					write_output: tool({
						description: "Write the task output/result",
						inputSchema: z.object({ content: z.string() }),
						execute: async (args) => ({ written: true, length: args.content.length }),
					}),
					update_progress: tool({
						description: "Report progress on the task",
						inputSchema: z.object({ note: z.string() }),
						execute: async (args) => {
							await db.addTaskLog(env.DB, taskId, "text", args.note);
							return { logged: true };
						},
					}),
				},
				stopWhen: stepCountIs(10),
			});

			const usage = response.usage;
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
