import { z } from "zod";
import {
  taskSchema,
  projectSchema,
  taskTypeSchema,
  executorSchema,
  logSchema,
  type Task,
} from "./contracts";

export const DEFAULT_API_URL = "http://127.0.0.1:8484";
export const TASKS_PER_STATUS = 200;
const REQUEST_TIMEOUT_MS = 15_000;
const STREAM_IDLE_TIMEOUT_MS = 45_000;
const MAX_RESPONSE_BYTES = 8 * 1024 * 1024;

export function validateApiUrl(value: string): string {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new Error("TaskYou API URL must be an absolute HTTP or HTTPS URL.");
  }
  if (
    !["http:", "https:"].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash
  ) {
    throw new Error(
      "TaskYou API URL must use HTTP(S), without credentials, a query, or a fragment.",
    );
  }
  return url.href.replace(/\/+$/, "");
}

async function readJson(response: Response): Promise<unknown> {
  const reader = response.body?.getReader();
  if (!reader) throw new Error("TaskYou returned an empty response.");
  let bytes = 0;
  let text = "";
  const decoder = new TextDecoder();
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      bytes += value.byteLength;
      if (bytes > MAX_RESPONSE_BYTES)
        throw new Error(
          "TaskYou response is too large. Use the TaskYou browser UI or CLI for this task.",
        );
      text += decoder.decode(value, { stream: true });
    }
    text += decoder.decode();
    try {
      return JSON.parse(text);
    } catch {
      throw new Error(
        "TaskYou returned invalid JSON. Check the API URL points to ty serve.",
      );
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

export class TaskYouApi {
  readonly baseUrl: string;
  constructor(
    baseUrl: string,
    private readonly signal: AbortSignal,
    private readonly timeoutMs = REQUEST_TIMEOUT_MS,
  ) {
    this.baseUrl = validateApiUrl(baseUrl);
  }

  async request(
    method: "GET" | "POST" | "PATCH",
    path: string,
    body?: unknown,
  ): Promise<unknown> {
    const signal = AbortSignal.any([
      this.signal,
      AbortSignal.timeout(this.timeoutMs),
    ]);
    try {
      const response = await fetch(`${this.baseUrl}${path}`, {
        method,
        redirect: "error",
        signal,
        headers: {
          Accept: "application/json",
          ...(body === undefined ? {} : { "Content-Type": "application/json" }),
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      const data = await readJson(response);
      if (!response.ok) {
        const parsed = z.object({ error: z.string() }).safeParse(data);
        throw new Error(
          `TaskYou ${method} ${path} failed (${response.status})${parsed.success ? `: ${parsed.data.error.slice(0, 1000)}` : "."}`,
        );
      }
      return data;
    } catch (error) {
      if (this.signal.aborted)
        throw new Error(
          "TaskYou connection changed or the plugin stopped. Refresh and try again.",
        );
      if (signal.aborted)
        throw new Error(
          `TaskYou request timed out. Check ty serve at ${this.baseUrl}.`,
        );
      if (error instanceof TypeError)
        throw new Error(
          `Cannot reach TaskYou at ${this.baseUrl}. Start ty serve on the bb server host or configure the API URL. Redirects are not supported.`,
        );
      throw error;
    }
  }

  private async list<T>(path: string, schema: z.ZodType<T>): Promise<T[]> {
    return z.array(schema).parse((await this.request("GET", path)) ?? []);
  }

  async board() {
    const statuses = [
      "backlog",
      "queued",
      "processing",
      "blocked",
      "done",
    ] as const;
    const [columns, projects, types, executors] = await Promise.all([
      Promise.all(
        statuses.map((status) =>
          this.list(
            `/api/tasks?status=${status}&limit=${TASKS_PER_STATUS + 1}`,
            taskSchema,
          ),
        ),
      ),
      this.list("/api/projects", projectSchema),
      this.list("/api/types", taskTypeSchema),
      this.list("/api/executors", executorSchema),
    ]);
    return {
      tasks: columns.flatMap((tasks) => tasks.slice(0, TASKS_PER_STATUS)),
      projects,
      types,
      executors,
      truncated: columns.some((tasks) => tasks.length > TASKS_PER_STATUS),
    };
  }

  async detail(id: number) {
    return z
      .object({
        task: taskSchema,
        logs: z
          .array(logSchema)
          .nullable()
          .transform((logs) => logs ?? []),
      })
      .parse(await this.request("GET", `/api/tasks/${id}`));
  }

  async create(input: unknown): Promise<Task> {
    return taskSchema.parse(await this.request("POST", "/api/tasks", input));
  }

  async update(id: number, patch: Record<string, unknown>): Promise<Task> {
    // The existing API cannot clear a task type. Omit blank values so editing
    // an untyped task's title/body remains possible.
    const body = { ...patch };
    if (body.type === "") delete body.type;
    return taskSchema.parse(
      await this.request("PATCH", `/api/tasks/${id}`, body),
    );
  }

  async action(
    id: number,
    action: "execute" | "retry" | "close",
    body: object = {},
  ) {
    const result = await this.request(
      "POST",
      `/api/tasks/${id}/${action}`,
      body,
    );
    // Close is idempotent; an already-done task returns a message, not `ok`.
    z.union([
      z.object({ ok: z.literal(true) }),
      z.object({ message: z.literal("task already done") }),
    ]).parse(result);
    return { ok: true };
  }
}

/** Parse complete SSE records, including records split across network chunks. */
export class BoardEventParser {
  private buffer = "";
  push(chunk: string, changed: () => void) {
    this.buffer += chunk;
    if (this.buffer.length > 64 * 1024)
      throw new Error("TaskYou event stream record is too large.");
    let boundary: RegExpExecArray | null;
    while ((boundary = /\r?\n\r?\n/.exec(this.buffer))) {
      const record = this.buffer.slice(0, boundary.index);
      this.buffer = this.buffer.slice(boundary.index + boundary[0].length);
      let event = "";
      let hasData = false;
      for (const line of record.split(/\r?\n/)) {
        if (line.startsWith("event:")) event = line.slice(6).trim();
        if (line.startsWith("data:")) hasData = true;
      }
      if (event === "board" && hasData) changed();
    }
  }
}

/** One stream attempt. The service handles reconnects and endpoint changes. */
export async function streamBoard(
  baseUrl: string,
  signal: AbortSignal,
  changed: () => void,
) {
  const idle = new AbortController();
  const combined = AbortSignal.any([signal, idle.signal]);
  let timer: ReturnType<typeof setTimeout>;
  const resetTimeout = () => {
    clearTimeout(timer);
    timer = setTimeout(() => idle.abort(), STREAM_IDLE_TIMEOUT_MS);
    timer.unref?.();
  };
  resetTimeout();
  try {
    const response = await fetch(
      `${validateApiUrl(baseUrl)}/api/board/stream?signal=true`,
      {
        headers: { Accept: "text/event-stream" },
        redirect: "error",
        signal: combined,
      },
    );
    if (
      !response.ok ||
      !response.headers.get("content-type")?.includes("text/event-stream") ||
      !response.body
    ) {
      await response.body?.cancel();
      throw new Error(`TaskYou event stream unavailable (${response.status}).`);
    }
    const reader = response.body.getReader();
    const parser = new BoardEventParser();
    const decoder = new TextDecoder();
    try {
      while (!combined.aborted) {
        const { done, value } = await reader.read();
        if (done) break;
        resetTimeout();
        if (!combined.aborted)
          parser.push(decoder.decode(value, { stream: true }), changed);
      }
    } finally {
      await reader.cancel().catch(() => {});
      reader.releaseLock();
    }
  } finally {
    clearTimeout(timer!);
  }
}

export function waitForReconnect(
  signal: AbortSignal,
  delayMs = 2000,
): Promise<void> {
  if (signal.aborted) return Promise.resolve();
  return new Promise((resolve) => {
    const finish = () => {
      clearTimeout(timer);
      signal.removeEventListener("abort", finish);
      resolve();
    };
    const timer = setTimeout(finish, delayMs);
    timer.unref?.();
    signal.addEventListener("abort", finish, { once: true });
  });
}
