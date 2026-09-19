import { api } from "../api/client";
import type { Task } from "../api/types";
import { store } from "../store";
import { inTauri, openInEditor, openExternal } from "../tauri";

/**
 * Where a task's code is, from the GUI's side.
 *
 * A placed task's worktree is a directory on its host, and `worktree_path` on
 * the task is this machine's path: empty for a task that has only ever run over
 * there, and the stale local worktree for one that was moved. Opening it would
 * show a different checkout of the same project, so the remote case has to be
 * asked about separately — the placement payload carries the worktree on the
 * host and a `code_uri` that opens it over ssh.
 */
export function runsOnAnotherHost(task: Task): boolean {
  const target = task.placement_target?.trim();
  return !!target && target !== "local";
}

/** True when there is code to open at all, here or on the task's host. */
export function hasTaskCode(task: Task): boolean {
  return runsOnAnotherHost(task) || !!task.worktree_path;
}

/**
 * Opens the task's worktree in the user's editor, wherever the worktree is.
 *
 * Rejects with something worth showing the user: "nothing happened" is the one
 * outcome this must not have, and the remote path has real reasons to fail — the
 * task may not have a worktree on its host yet (it gets one on its first run
 * there), the coordinator may be unreachable, or no installed editor may be able
 * to open a directory over ssh.
 */
export async function openTaskCode(task: Task): Promise<void> {
  if (!runsOnAnotherHost(task)) {
    if (!task.worktree_path) throw new Error(`Task #${task.id} has no worktree yet.`);
    await openInEditor(task.worktree_path);
    return;
  }

  const placement = await api.placement(task.id);
  const host = placement.target || task.placement_target || "its host";
  if (!placement.remote_worktree) {
    throw new Error(
      `Task #${task.id} does not have a worktree on ${host} yet — it gets one the first time it runs there.`,
    );
  }
  // In the desktop app the editor is launched with its own CLI, which is what
  // makes a fork like Cursor work. In the browser there is no process to launch,
  // so the URI goes to the OS as a scheme handler.
  if (inTauri()) {
    await openInEditor(placement.remote_worktree, placement.target);
    return;
  }
  if (!placement.code_uri) {
    throw new Error(`The worktree is on ${host} at ${placement.remote_worktree}.`);
  }
  await openExternal(placement.code_uri);
}

/**
 * openTaskCode for a click or a keypress: the same call, with the failure shown
 * as a toast rather than dropped as an unhandled rejection.
 */
export function openTaskCodeReporting(task: Task): void {
  void openTaskCode(task).catch((e: unknown) => {
    store.toast({
      title: "Could not open the worktree",
      body: e instanceof Error ? e.message : String(e),
      kind: "error",
    });
  });
}
