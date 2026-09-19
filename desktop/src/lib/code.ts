import { api } from "../api/client";
import type { Task } from "../api/types";
import { openInEditor, openExternal } from "../tauri";

/**
 * Where a task's code is, from the GUI's side.
 *
 * A placed task's worktree is a directory on its host, and `worktree_path` on
 * the task is this machine's path: empty for a task that has only ever run over
 * there, and the stale local worktree for one that was moved. Opening it would
 * show a different checkout of the same project, so the remote case has to be
 * asked about separately — the placement payload carries a `code_uri` that opens
 * the real directory over ssh.
 */
export function runsOnAnotherHost(task: Task): boolean {
  const target = task.placement_target?.trim();
  return !!target && target !== "local";
}

/** Opens the task's worktree in the user's editor, wherever the worktree is. */
export async function openTaskCode(task: Task): Promise<void> {
  if (!runsOnAnotherHost(task)) {
    if (task.worktree_path) await openInEditor(task.worktree_path);
    return;
  }
  const placement = await api.placement(task.id);
  if (placement.code_uri) await openExternal(placement.code_uri);
}

/** True when there is code to open at all, here or on the task's host. */
export function hasTaskCode(task: Task): boolean {
  return runsOnAnotherHost(task) || !!task.worktree_path;
}
