import { z } from "zod";

export const taskSchema = z.object({
  id: z.number().int().positive(),
  title: z.string(),
  body: z.string(),
  status: z.enum([
    "backlog",
    "queued",
    "processing",
    "blocked",
    "done",
    "archived",
  ]),
  type: z.string(),
  project: z.string(),
  executor: z.string(),
  pinned: z.boolean(),
  tags: z.string(),
  permission_mode: z.string(),
  branch_name: z.string(),
  worktree_path: z.string().optional(),
  has_executor: z.boolean(),
  pr_url: z.string(),
  summary: z.string().optional(),
  stand: z.string().optional(),
  created_at: z.string(),
  updated_at: z.string(),
});
export const projectSchema = z.object({
  id: z.number().int(),
  name: z.string(),
  path: z.string(),
  color: z.string(),
});
export const taskTypeSchema = z.object({
  id: z.number().int(),
  name: z.string(),
  label: z.string(),
});
export const executorSchema = z.object({
  name: z.string(),
  available: z.boolean(),
  default: z.boolean(),
});
export const logSchema = z.object({
  id: z.number().int(),
  line_type: z.string(),
  content: z.string(),
  created_at: z.string(),
});

export type Task = z.infer<typeof taskSchema>;
export type Project = z.infer<typeof projectSchema>;
export type TaskType = z.infer<typeof taskTypeSchema>;
export type ExecutorInfo = z.infer<typeof executorSchema>;
export type LogLine = z.infer<typeof logSchema>;

export const taskFields = {
  title: z.string().trim().min(1).max(2000),
  body: z.string().max(200_000),
  project: z.string().max(1000),
  type: z.string().max(1000),
  executor: z.string().max(1000),
};
export const idInput = z
  .object({ id: z.number().int().positive().safe() })
  .strict();
export const actionResult = z.object({ ok: z.boolean() });
export const detailSchema = z.object({
  task: taskSchema,
  logs: z.array(logSchema),
});
export const boardSchema = z.object({
  tasks: z.array(taskSchema),
  projects: z.array(projectSchema),
  types: z.array(taskTypeSchema),
  executors: z.array(executorSchema),
  truncated: z.boolean(),
});
