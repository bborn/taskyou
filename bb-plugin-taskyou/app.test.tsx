// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { loadPluginApp, renderSlot } from "@get-bb/plugin-sdk/testing/app";
import type { PluginRpcTestHandlers } from "@get-bb/plugin-sdk/testing/app";
import type { rpcContract, Task } from "./server";

type RpcHandlers = {
  -readonly [K in keyof PluginRpcTestHandlers<
    typeof rpcContract
  >]: PluginRpcTestHandlers<typeof rpcContract>[K];
};

const app = await loadPluginApp(() => import("./app"));
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function task(overrides: Partial<Task> = {}): Task {
  return {
    id: 1,
    title: "Ship the board",
    body: "Keep CLI access",
    status: "backlog",
    project: "Workflow",
    type: "feature",
    executor: "codex",
    pinned: false,
    tags: "",
    permission_mode: "",
    branch_name: "",
    has_executor: false,
    pr_url: "",
    created_at: "2026-09-15",
    updated_at: "2026-09-15",
    ...overrides,
  };
}

function fixture(initial: Task[] = [task()]) {
  let tasks = initial;
  const catalog = {
    projects: [{ id: 1, name: "Workflow", path: "/workflow", color: "" }],
    types: [{ id: 1, name: "feature", label: "Feature" }],
    executors: [{ name: "codex", available: true, default: true }],
    truncated: false,
  };
  const rpc: RpcHandlers = {
    board: vi.fn(() => ({ tasks, ...catalog })),
    detail: vi.fn(({ id }) => ({
      task: tasks.find((t) => t.id === id)!,
      logs: [
        {
          id: 1,
          line_type: "system",
          content: "Executor started",
          created_at: "2026-09-15",
        },
      ],
    })),
    create: vi.fn((input) => {
      const created = task({
        ...input,
        id: tasks.length + 1,
        status: input.execute ? "queued" : "backlog",
      });
      tasks = [...tasks, created];
      return created;
    }),
    update: vi.fn(({ id, ...fields }) => {
      tasks = tasks.map((t) => (t.id === id ? { ...t, ...fields } : t));
      return tasks.find((t) => t.id === id)!;
    }),
    execute: vi.fn(({ id }) => {
      tasks = tasks.map((t) => (t.id === id ? { ...t, status: "queued" } : t));
      return { ok: true };
    }),
    retry: vi.fn(({ id }) => {
      tasks = tasks.map((t) => (t.id === id ? { ...t, status: "queued" } : t));
      return { ok: true };
    }),
    close: vi.fn(({ id }) => {
      tasks = tasks.map((t) => (t.id === id ? { ...t, status: "done" } : t));
      return { ok: true };
    }),
  };
  return {
    rpc,
    setTasks: (next: Task[]) => {
      tasks = next;
    },
  };
}

const mount = (rpc: PluginRpcTestHandlers<typeof rpcContract>) =>
  renderSlot(
    app.navPanels[0]!,
    { subPath: "" },
    { rpc, settings: { apiUrl: "http://localhost:7433" } },
  );
async function openTask(title = "Ship the board") {
  fireEvent.click(
    await screen.findByRole("button", { name: new RegExp(title) }),
  );
  await screen.findByRole("button", { name: "Edit task" });
  return within(screen.getByRole("dialog"));
}

describe("TaskYou board", () => {
  it("groups queued and processing together, and filters by search/project", async () => {
    const { rpc } = fixture([
      task(),
      task({ id: 2, title: "Waiting", body: "", status: "queued" }),
      task({
        id: 3,
        title: "Running",
        body: "",
        status: "processing",
        project: "Other",
      }),
    ]);
    mount(rpc);
    await screen.findByRole("button", { name: /Ship the board/ });
    const progress = within(
      screen.getByRole("region", { name: "In progress" }),
    );
    expect(progress.getByText("Queued")).toBeTruthy();
    expect(progress.getByText("Processing")).toBeTruthy();
    fireEvent.change(
      screen.getByRole("combobox", { name: "Filter by project" }),
      { target: { value: "Workflow" } },
    );
    expect(screen.queryByText("Running")).toBeNull();
    fireEvent.change(screen.getByRole("textbox", { name: "Search tasks" }), {
      target: { value: "keep CLI" },
    });
    expect(screen.getByText("Ship the board")).toBeTruthy();
    expect(screen.queryByText("Waiting")).toBeNull();
  });

  it("creates in backlog by default using TaskYou catalog choices", async () => {
    const { rpc } = fixture([]);
    mount(rpc);
    await waitFor(() =>
      expect(
        (screen.getByRole("button", { name: "New task" }) as HTMLButtonElement)
          .disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByRole("button", { name: "New task" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Title" }), {
      target: { value: "New idea" },
    });
    fireEvent.change(screen.getByRole("combobox", { name: "Project" }), {
      target: { value: "Workflow" },
    });
    fireEvent.change(screen.getByRole("combobox", { name: "Task type" }), {
      target: { value: "feature" },
    });
    fireEvent.change(screen.getByRole("combobox", { name: "Executor" }), {
      target: { value: "codex" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create task" }));
    await waitFor(() =>
      expect(rpc.create).toHaveBeenCalledWith({
        title: "New idea",
        body: "",
        project: "Workflow",
        type: "feature",
        executor: "codex",
        execute: false,
      }),
    );
    expect(
      await screen.findByRole("button", { name: /New idea/ }),
    ).toBeTruthy();
  });

  it("retains a failed creation and requires explicit queue selection", async () => {
    const { rpc } = fixture([]);
    rpc.create = vi
      .fn()
      .mockRejectedValueOnce(new Error("TaskYou is offline"))
      .mockImplementation((input) => task({ ...input, status: "queued" }));
    mount(rpc);
    await waitFor(() =>
      expect(
        (screen.getByRole("button", { name: "New task" }) as HTMLButtonElement)
          .disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByRole("button", { name: "New task" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Title" }), {
      target: { value: "Retain this" },
    });
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: "Queue for execution after saving",
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Create and queue" }));
    expect(await screen.findByRole("alert")).toHaveProperty(
      "textContent",
      "TaskYou is offline",
    );
    expect(screen.getByRole("textbox", { name: "Title" })).toHaveProperty(
      "value",
      "Retain this",
    );
    fireEvent.click(screen.getByRole("button", { name: "Create and queue" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(rpc.create).toHaveBeenLastCalledWith(
      expect.objectContaining({ title: "Retain this", execute: true }),
    );
  });

  it("edits the existing task without queueing it", async () => {
    const { rpc } = fixture();
    mount(rpc);
    const detail = await openTask();
    expect(detail.getByText("Executor started")).toBeTruthy();
    fireEvent.click(detail.getByRole("button", { name: "Edit task" }));
    expect(screen.getByRole("textbox", { name: "Description" })).toHaveProperty(
      "value",
      "Keep CLI access",
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Title" }), {
      target: { value: "Updated board" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() =>
      expect(rpc.update).toHaveBeenCalledWith(
        expect.objectContaining({
          id: 1,
          title: "Updated board",
          body: "Keep CLI access",
          executor: "codex",
        }),
      ),
    );
    expect(rpc.execute).not.toHaveBeenCalled();
    expect(
      await screen.findByRole("button", { name: /Updated board/ }),
    ).toBeTruthy();
  });

  it("disables commands while executing and closes a task through the API", async () => {
    const { rpc } = fixture();
    let finish!: (result: { ok: boolean }) => void;
    rpc.execute = vi.fn(
      () =>
        new Promise<{ ok: boolean }>((resolve) => {
          finish = resolve;
        }),
    );
    mount(rpc);
    const detail = await openTask();
    fireEvent.click(detail.getByRole("button", { name: "Execute" }));
    await waitFor(() => expect(rpc.execute).toHaveBeenCalledWith({ id: 1 }));
    expect(detail.getByRole("button", { name: "Close task" })).toHaveProperty(
      "disabled",
      true,
    );
    expect(detail.getByRole("button", { name: "Edit task" })).toHaveProperty(
      "disabled",
      true,
    );
    await act(async () => finish({ ok: true }));
    fireEvent.click(detail.getByRole("button", { name: "Close task" }));
    await waitFor(() => expect(rpc.close).toHaveBeenCalledWith({ id: 1 }));
    await waitFor(() =>
      expect(screen.getByRole("dialog").textContent).toContain("· Done ·"),
    );
  });

  it("retains retry feedback after failure and retries with that feedback", async () => {
    const { rpc } = fixture([task({ status: "blocked" })]);
    rpc.retry = vi
      .fn()
      .mockRejectedValueOnce(new Error("Try later"))
      .mockResolvedValue({ ok: true });
    mount(rpc);
    const detail = await openTask();
    fireEvent.change(detail.getByRole("textbox", { name: "Retry feedback" }), {
      target: { value: "Use the API" },
    });
    fireEvent.click(detail.getByRole("button", { name: "Retry task" }));
    expect(await screen.findByRole("alert")).toHaveProperty(
      "textContent",
      "Try later",
    );
    expect(
      detail.getByRole("textbox", { name: "Retry feedback" }),
    ).toHaveProperty("value", "Use the API");
    fireEvent.click(detail.getByRole("button", { name: "Retry task" }));
    await waitFor(() => expect(rpc.retry).toHaveBeenCalledTimes(2));
    expect(rpc.retry).toHaveBeenLastCalledWith({
      id: 1,
      feedback: "Use the API",
    });
  });

  it("shows connection guidance and recovers with Try again", async () => {
    const { rpc } = fixture();
    const healthy = rpc.board;
    rpc.board = vi
      .fn()
      .mockRejectedValueOnce(new Error("Connection refused"))
      .mockImplementation(healthy);
    mount(rpc);
    expect((await screen.findByRole("alert")).textContent).toContain(
      "reachable from the bb server",
    );
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(
      await screen.findByRole("button", { name: /Ship the board/ }),
    ).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("reconciles missed events on reconnect and coalesces event bursts", async () => {
    const { rpc, setTasks } = fixture();
    const slot = mount(rpc);
    await screen.findByRole("button", { name: /Ship the board/ });
    setTasks([task({ title: "Changed elsewhere" })]);
    await slot.behavior.setRealtimeConnectionState("reconnecting");
    await slot.behavior.setRealtimeConnectionState("connected");
    expect(
      await screen.findByRole("button", { name: /Changed elsewhere/ }),
    ).toBeTruthy();
    const before = vi.mocked(rpc.board).mock.calls.length;
    setTasks([task({ title: "Burst update" })]);
    await slot.behavior.emitRealtime("taskyou-changed", {});
    await slot.behavior.emitRealtime("taskyou-changed", {});
    await slot.behavior.emitRealtime("taskyou-changed", {});
    expect(
      await screen.findByRole("button", { name: /Burst update/ }),
    ).toBeTruthy();
    expect(rpc.board).toHaveBeenCalledTimes(before + 1);
  });

  it("ignores a late detail response after a different task is opened", async () => {
    const { rpc } = fixture([task(), task({ id: 2, title: "Second task" })]);
    let finish!: (value: { task: Task; logs: [] }) => void;
    rpc.detail = vi.fn(({ id }: { id: number }) =>
      id === 1
        ? new Promise<{ task: Task; logs: [] }>((resolve) => {
            finish = resolve;
          })
        : { task: task({ id: 2, title: "Second task" }), logs: [] },
    );
    mount(rpc);
    fireEvent.click(
      await screen.findByRole("button", { name: /Ship the board/ }),
    );
    await waitFor(() => expect(rpc.detail).toHaveBeenCalledWith({ id: 1 }));
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Close" }),
    );
    fireEvent.click(screen.getByRole("button", { name: /Second task/ }));
    await screen.findByRole("heading", { name: "Second task" });
    await act(async () => finish({ task: task(), logs: [] }));
    expect(
      within(screen.getByRole("dialog")).getByRole("heading", {
        name: "Second task",
      }),
    ).toBeTruthy();
  });

  it("closes editors when the server changes its TaskYou endpoint", async () => {
    const { rpc } = fixture();
    const slot = mount(rpc);
    const detail = await openTask();
    fireEvent.click(detail.getByRole("button", { name: "Edit task" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Title" }), {
      target: { value: "Belongs to old endpoint" },
    });
    await slot.behavior.emitRealtime("taskyou-changed", { reason: "settings" });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(rpc.update).not.toHaveBeenCalled();
  });

  it("disposes queued refreshes and polling when the panel unmounts", async () => {
    const { rpc } = fixture();
    const slot = mount(rpc);
    await screen.findByRole("button", { name: /Ship the board/ });
    vi.useFakeTimers();
    await slot.behavior.emitRealtime("taskyou-changed", {});
    slot.lifecycle.unmount();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(rpc.board).toHaveBeenCalledTimes(1);
  });
});
