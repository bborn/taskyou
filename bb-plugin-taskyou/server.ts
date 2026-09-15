import { defineRpcContract, type BbPluginApi } from "@get-bb/plugin-sdk";
import { z } from "zod";
import {
  actionResult,
  boardSchema,
  detailSchema,
  idInput,
  taskFields,
  taskSchema,
} from "./contracts";
import {
  DEFAULT_API_URL,
  TaskYouApi,
  streamBoard,
  validateApiUrl,
  waitForReconnect,
} from "./api";
export type {
  Task,
  Project,
  TaskType,
  ExecutorInfo,
  LogLine,
} from "./contracts";

export const rpcContract = defineRpcContract({
  board: { input: z.null(), output: boardSchema },
  detail: { input: idInput, output: detailSchema },
  create: {
    input: z.object({ ...taskFields, execute: z.boolean() }).strict(),
    output: taskSchema,
  },
  update: {
    input: z.object(taskFields).partial().extend(idInput.shape).strict(),
    output: taskSchema,
  },
  execute: { input: idInput, output: actionResult },
  retry: {
    input: idInput.extend({ feedback: z.string().max(200_000) }),
    output: actionResult,
  },
  close: { input: idInput, output: actionResult },
});

export default async function plugin(bb: BbPluginApi) {
  const settings = bb.settings.define({
    apiUrl: {
      type: "string",
      label: "TaskYou API URL (reachable from the bb server)",
      default: DEFAULT_API_URL,
      experimental_schema: z.string().refine((value) => {
        try {
          validateApiUrl(value);
          return true;
        } catch {
          return false;
        }
      }, "Use an absolute HTTP(S) URL without credentials, query, or fragment."),
    },
  });
  const lifetime = new AbortController();
  let endpoint = new AbortController();
  let baseUrl = (await settings.get()).apiUrl;
  function client() {
    return new TaskYouApi(
      baseUrl,
      AbortSignal.any([lifetime.signal, endpoint.signal]),
    );
  }
  function publish(reason: string) {
    if (!lifetime.signal.aborted)
      bb.realtime.publish("taskyou-changed", { reason });
  }
  settings.onChange((next) => {
    endpoint.abort();
    endpoint = new AbortController();
    baseUrl = next.apiUrl;
    publish("settings");
  });
  bb.onDispose(() => {
    lifetime.abort();
    endpoint.abort();
  });

  async function mutate<T>(work: () => Promise<T>): Promise<T> {
    const generation = endpoint;
    const result = await work();
    if (!generation.signal.aborted) publish("mutation");
    return result;
  }
  bb.rpc.register(rpcContract, {
    board: () => client().board(),
    detail: ({ id }) => client().detail(id),
    create: (input) => mutate(() => client().create(input)),
    update: ({ id, ...patch }) => mutate(() => client().update(id, patch)),
    execute: ({ id }) => mutate(() => client().action(id, "execute")),
    retry: ({ id, feedback }) =>
      mutate(() => client().action(id, "retry", { feedback })),
    close: ({ id }) => mutate(() => client().action(id, "close")),
  });

  bb.background.service("taskyou-events", {
    async start(signal) {
      const stopped = AbortSignal.any([signal, lifetime.signal]);
      while (!stopped.aborted) {
        const generation = endpoint;
        const attempt = AbortSignal.any([stopped, generation.signal]);
        try {
          await streamBoard(baseUrl, attempt, () => {
            if (!attempt.aborted) publish("board");
          });
        } catch (error) {
          if (!attempt.aborted)
            bb.log.warn(
              `TaskYou live updates disconnected; reconnecting: ${error instanceof Error ? error.message : "connection failed"}`,
            );
        }
        await waitForReconnect(attempt);
      }
    },
  });
}
