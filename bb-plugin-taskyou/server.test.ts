import {
  createServer,
  type IncomingMessage,
  type ServerResponse,
} from "node:http";
import type { AddressInfo } from "node:net";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  createFakePluginHost,
  experimental_scanPublicSdkOnly,
} from "@get-bb/plugin-sdk/testing";
import plugin from "./server";
import {
  BoardEventParser,
  TaskYouApi,
  TASKS_PER_STATUS,
  validateApiUrl,
} from "./api";
import type { Task } from "./contracts";

it("ships without private bb workspace imports", () => {
  const scan = experimental_scanPublicSdkOnly(
    fileURLToPath(new URL(".", import.meta.url)),
    {
      allow: [
        /^react(?:-dom)?(?:\/.*)?$/,
        /^@radix-ui\//,
        /^@hugeicons\//,
        /^@testing-library\//,
        /^(class-variance-authority|clsx|tailwind-merge|vaul)$/,
        /^vitest\//,
        /^@\/(components|lib)\//,
      ],
    },
  );
  expect(scan.violations).toEqual([]);
  expect(scan.privateDependencies).toEqual([]);
});

const task: Task = {
  id: 1,
  title: "Fix queue",
  body: "Reproduce the race",
  status: "backlog",
  type: "",
  project: "workflow",
  executor: "codex",
  pinned: false,
  tags: "",
  permission_mode: "default",
  branch_name: "",
  has_executor: false,
  pr_url: "",
  created_at: "2026-09-15T12:00:00Z",
  updated_at: "2026-09-15T12:00:00Z",
};
const cleanups: Array<() => Promise<void>> = [];
afterEach(async () => {
  for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
});

async function fixture(
  handler: (req: IncomingMessage, res: ServerResponse) => void | Promise<void>,
) {
  const server = createServer((req, res) => {
    void Promise.resolve(handler(req, res)).catch(() => {
      res.statusCode = 500;
      res.end();
    });
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  cleanups.push(
    () =>
      new Promise<void>((resolve, reject) => {
        server.close((error) => (error ? reject(error) : resolve()));
        server.closeAllConnections();
      }),
  );
  return `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
}
async function host(apiUrl: string) {
  const { bb, harness } = createFakePluginHost({
    pluginId: "taskyou",
    settings: { apiUrl },
  });
  await plugin(bb);
  cleanups.push(() => harness.lifecycle.dispose());
  return harness;
}
function json(res: ServerResponse, value: unknown, status = 200) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(value));
}
async function body(req: IncomingMessage) {
  let text = "";
  for await (const chunk of req) text += chunk.toString();
  return text ? JSON.parse(text) : null;
}

describe("TaskYou RPC adapter", () => {
  it("loads all active statuses and done independently, normalizing empty metadata arrays", async () => {
    const requested: string[] = [];
    const url = await fixture((req, res) => {
      requested.push(req.url!);
      const target = new URL(req.url!, "http://fixture");
      if (target.pathname === "/api/tasks") {
        const status = target.searchParams.get("status")!;
        json(
          res,
          status === "backlog" || status === "done"
            ? [{ ...task, status }]
            : null,
        );
      } else json(res, null);
    });
    const harness = await host(url);
    expect(await harness.behavior.callRpc("board", null)).toEqual({
      tasks: [task, { ...task, status: "done" }],
      projects: [],
      types: [],
      executors: [],
      truncated: false,
    });
    expect(requested.filter((path) => path.startsWith("/api/tasks"))).toEqual(
      expect.arrayContaining([
        "/api/tasks?status=backlog&limit=201",
        "/api/tasks?status=queued&limit=201",
        "/api/tasks?status=processing&limit=201",
        "/api/tasks?status=blocked&limit=201",
        "/api/tasks?status=done&limit=201",
      ]),
    );
    expect(requested).not.toContain(expect.stringContaining("archived"));
    expect(harness.inspection.sdk.calls).toEqual([]);
  });

  it("reports truncation without hiding active work behind completed tasks", async () => {
    const url = await fixture((req, res) => {
      const target = new URL(req.url!, "http://fixture");
      if (target.searchParams.get("status") === "done") {
        json(
          res,
          Array.from({ length: TASKS_PER_STATUS + 1 }, (_, i) => ({
            ...task,
            id: i + 2,
            status: "done",
          })),
        );
      } else
        json(
          res,
          target.searchParams.get("status") === "backlog" ? [task] : [],
        );
    });
    const harness = await host(url);
    const result = (await harness.behavior.callRpc("board", null)) as {
      tasks: Task[];
      truncated: boolean;
    };
    expect(result.truncated).toBe(true);
    expect(result.tasks).toHaveLength(TASKS_PER_STATUS + 1);
    expect(result.tasks[0]).toEqual(task);
  });

  it("maps create/edit/queue/retry/close to TaskYou and broadcasts successful writes", async () => {
    const requests: unknown[] = [];
    const url = await fixture(async (req, res) => {
      const input = await body(req);
      requests.push({ method: req.method, path: req.url, body: input });
      if (req.method === "PATCH" || req.url === "/api/tasks")
        json(res, { ...task, ...input });
      else if (req.url?.endsWith("/close"))
        json(res, { message: "task already done" });
      else json(res, { ok: true });
    });
    const harness = await host(url);
    const input = {
      title: "A task",
      body: "",
      project: "workflow",
      type: "",
      executor: "codex",
      execute: true,
    };
    await harness.behavior.callRpc("create", input);
    await harness.behavior.callRpc("update", {
      id: 1,
      title: "Edited",
      type: "",
    });
    expect(await harness.behavior.callRpc("execute", { id: 1 })).toEqual({
      ok: true,
    });
    expect(
      await harness.behavior.callRpc("retry", {
        id: 1,
        feedback: "Try another approach",
      }),
    ).toEqual({ ok: true });
    expect(await harness.behavior.callRpc("close", { id: 1 })).toEqual({
      ok: true,
    });
    expect(requests).toEqual([
      { method: "POST", path: "/api/tasks", body: input },
      { method: "PATCH", path: "/api/tasks/1", body: { title: "Edited" } },
      { method: "POST", path: "/api/tasks/1/execute", body: {} },
      {
        method: "POST",
        path: "/api/tasks/1/retry",
        body: { feedback: "Try another approach" },
      },
      { method: "POST", path: "/api/tasks/1/close", body: {} },
    ]);
    expect(harness.inspection.realtimeSignals).toHaveLength(5);
    expect(
      harness.inspection.realtimeSignals.every(
        (event) => event.channel === "taskyou-changed",
      ),
    ).toBe(true);
    expect(harness.inspection.sdk.calls).toEqual([]);
  });

  it("normalizes empty detail logs and preserves task errors", async () => {
    const url = await fixture((req, res) => {
      if (req.url === "/api/tasks/1") json(res, { task, logs: null });
      else json(res, { error: "task not found" }, 404);
    });
    const harness = await host(url);
    expect(await harness.behavior.callRpc("detail", { id: 1 })).toEqual({
      task,
      logs: [],
    });
    await expect(harness.behavior.callRpc("detail", { id: 2 })).rejects.toThrow(
      "task not found",
    );
  });

  it("rejects invalid inputs before HTTP and does not broadcast failed mutations", async () => {
    let requests = 0;
    const url = await fixture((_req, res) => {
      requests++;
      json(res, { error: "task is already queued or processing" }, 409);
    });
    const harness = await host(url);
    await expect(
      harness.behavior.callRpc("execute", { id: "../../settings" }),
    ).rejects.toThrow();
    await expect(
      harness.behavior.callRpc("execute", { id: 1, url: "/api/settings" }),
    ).rejects.toThrow();
    expect(requests).toBe(0);
    await expect(
      harness.behavior.callRpc("execute", { id: 1 }),
    ).rejects.toThrow("already queued");
    expect(harness.inspection.realtimeSignals).toHaveLength(0);
  });

  it("rejects credentials, unsupported schemes, query and fragment in settings", async () => {
    const harness = await host("http://127.0.0.1:8484");
    for (const apiUrl of [
      "file:///tmp/tasks.db",
      "http://me:secret@localhost",
      "http://localhost?token=abc",
      "http://localhost#fragment",
      "relative/path",
    ]) {
      await expect(harness.behavior.setSettings({ apiUrl })).rejects.toThrow();
    }
    expect(validateApiUrl("https://example.test/taskyou/")).toBe(
      "https://example.test/taskyou",
    );
  });

  it("refuses redirects and reports connection failures and timeouts", async () => {
    let redirected = false;
    const target = await fixture((_req, res) => {
      redirected = true;
      json(res, { ok: true });
    });
    const url = await fixture((_req, res) => {
      res.writeHead(302, { Location: target });
      res.end();
    });
    const harness = await host(url);
    await expect(
      harness.behavior.callRpc("execute", { id: 1 }),
    ).rejects.toThrow("Redirects are not supported");
    expect(redirected).toBe(false);
    const stalled = await fixture(() => {});
    await expect(
      new TaskYouApi(stalled, new AbortController().signal, 20).detail(1),
    ).rejects.toThrow("timed out");
    await expect(
      new TaskYouApi("http://127.0.0.1:1", new AbortController().signal).detail(
        1,
      ),
    ).rejects.toThrow("Cannot reach TaskYou");
  });

  it("rejects malformed API payloads rather than reporting successful writes", async () => {
    const url = await fixture((req, res) => {
      if (req.url?.endsWith("/execute")) json(res, { ok: false });
      else {
        res.writeHead(200, { "Content-Type": "text/html" });
        res.end("<html>wrong server</html>");
      }
    });
    const harness = await host(url);
    await expect(
      harness.behavior.callRpc("execute", { id: 1 }),
    ).rejects.toThrow();
    await expect(harness.behavior.callRpc("detail", { id: 1 })).rejects.toThrow(
      "invalid JSON",
    );
    expect(harness.inspection.realtimeSignals).toHaveLength(0);
  });

  it("cancels in-flight RPC when the configured server changes", async () => {
    let requested = false;
    const oldUrl = await fixture(() => {
      requested = true;
    });
    const nextUrl = await fixture((_req, res) =>
      json(res, { task: { ...task, title: "Other server" }, logs: [] }),
    );
    const harness = await host(oldUrl);
    const pending = harness.behavior.callRpc("detail", { id: 1 });
    const rejected = expect(pending).rejects.toThrow("connection changed");
    await vi.waitFor(() => expect(requested).toBe(true));
    await harness.behavior.setSettings({ apiUrl: nextUrl });
    await rejected;
    expect(await harness.behavior.callRpc("detail", { id: 1 })).toMatchObject({
      task: { title: "Other server" },
    });
  });
});

describe("live updates", () => {
  it("parses fragmented SSE records and ignores heartbeats and partial records", () => {
    const parser = new BoardEventParser();
    const changed = vi.fn();
    for (const text of [
      "event: boa",
      "rd\r",
      "\ndata: {}\r\n\r",
      "\nevent: heartbeat\ndata: {}\n\nevent: board\n",
      "data: {}\n\n",
      "event: board\ndata: {}\n",
    ])
      parser.push(text, changed);
    expect(changed).toHaveBeenCalledTimes(2);
  });

  it("stops during reconnect backoff without waiting for another attempt", async () => {
    let requests = 0;
    const url = await fixture((_req, res) => {
      requests++;
      json(res, { error: "offline" }, 503);
    });
    const harness = await host(url);
    const service = harness.behavior.runService("taskyou-events");
    await vi.waitFor(() =>
      expect(
        harness.inspection.logEntries.some((entry) =>
          entry.message.includes("reconnecting"),
        ),
      ).toBe(true),
    );
    const started = Date.now();
    service.controller.abort();
    await service.done;
    expect(Date.now() - started).toBeLessThan(1000);
    expect(requests).toBe(1);
  });

  it("reconnects on EOF, switches endpoints immediately and disposes its active stream", async () => {
    const connections: ServerResponse[] = [];
    let closed = 0;
    const oldUrl = await fixture((req, res) => {
      expect(req.url).toBe("/api/board/stream?signal=true");
      res.writeHead(200, { "Content-Type": "text/event-stream" });
      res.flushHeaders();
      connections.push(res);
      res.on("close", () => {
        closed++;
      });
    });
    const nextConnections: ServerResponse[] = [];
    let nextClosed = false;
    const nextUrl = await fixture((_req, res) => {
      res.writeHead(200, { "Content-Type": "text/event-stream" });
      res.flushHeaders();
      nextConnections.push(res);
      res.on("close", () => {
        nextClosed = true;
      });
    });
    const harness = await host(oldUrl);
    const service = harness.behavior.runService("taskyou-events");
    await vi.waitFor(() => expect(connections).toHaveLength(1));
    connections[0]!.write("event: boa");
    connections[0]!.write("rd\ndata: {}\n\n");
    await vi.waitFor(() =>
      expect(harness.inspection.realtimeSignals).toHaveLength(1),
    );
    connections[0]!.end();
    await vi.waitFor(() => expect(connections).toHaveLength(2), {
      timeout: 3500,
    });
    await harness.behavior.setSettings({ apiUrl: nextUrl });
    await vi.waitFor(() => expect(nextConnections).toHaveLength(1));
    await vi.waitFor(() => expect(closed).toBe(2));
    const before = harness.inspection.realtimeSignals.length;
    connections[1]!.write("event: board\ndata: {}\n\n");
    nextConnections[0]!.write("event: board\ndata: {}\n\n");
    await vi.waitFor(() =>
      expect(harness.inspection.realtimeSignals).toHaveLength(before + 1),
    );
    await harness.lifecycle.dispose();
    await service.done;
    await vi.waitFor(() => expect(nextClosed).toBe(true));
    const after = harness.inspection.realtimeSignals.length;
    nextConnections[0]!.write("event: board\ndata: {}\n\n");
    expect(harness.inspection.realtimeSignals).toHaveLength(after);
  });
});
