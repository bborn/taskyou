// Pure decision logic for the mobile drawer <~> history <~> view wiring that
// `desktop/src/App.tsx` drives with side effects (history, store, React state).
// Extracted so the drawer's navigation edge cases can be unit-tested without a
// DOM or the React 19 passive-effect scheduler — the timing race this guards
// against is otherwise invisible to the repo's `node --test` harness.
//
// The bug it fixes: tapping a view-changing drawer item (Settings/Routines/
// Plugins) runs the store action, then `closeDrawer()` calls `history.back()`
// to pop the drawer's synthetic `{drawer:true}` entry. That `back()` enqueues a
// `popstate` which fires *before* the view<->URL passive effect has rewritten
// the URL to the newly chosen view, so a naive `onPopState` reads the
// pre-drawer URL and re-navigates, reverting the user's selection. The fix is
// to flag the popstate that our own `back()` triggered and skip re-navigation
// for it — while still popping the drawer entry, so a later system Back
// returns to the pre-drawer view in a single press.
import type { View } from "../store";

export type PopStateDecision =
  // The popstate came from `closeDrawer()`'s own `history.back()` — housekeeping
  // that popped the drawer entry, not a user navigation. Do NOT re-navigate, or
  // an in-flight view change made by the drawer item that triggered the close
  // will be clobbered by the stale pre-drawer URL.
  | { action: "skip" }
  // The drawer is open and the user pressed system Back: dismiss it and stay.
  | { action: "closeDrawer" }
  // A genuine Back (or a deep-link reload): sync the view to the URL that is
  // now on top of the history stack.
  | { action: "navigate"; view: View };

/** Decode `window.location.search` into the view it represents. Empty/unknown
 * → board; `?task=N` (N a positive integer) → detail; `?view=settings|routines|
 * plugins` → the matching view. `task` takes precedence over `view`. This
 * mirrors the previous inline logic in `App.tsx`'s `onPopState` exactly. */
export function viewFromSearch(search: string): View {
  const params = new URLSearchParams(search);
  const task = Number(params.get("task"));
  if (Number.isInteger(task) && task > 0) return { kind: "detail", taskId: task };
  const view = params.get("view");
  if (view === "settings") return { kind: "settings" };
  if (view === "routines") return { kind: "routines" };
  if (view === "plugins") return { kind: "plugins" };
  return { kind: "board" };
}

/** Build the URL (path + search) that mirrors `view`, relative to `pathname`.
 * Same rule the view<->URL effect uses: board → bare path; detail →
 * `?task=<id>`; settings/routines/plugins → `?view=<kind>`. */
export function urlForView(view: View, pathname: string): string {
  switch (view.kind) {
    case "board":
      return pathname;
    case "detail":
      return `${pathname}?task=${view.taskId}`;
    case "settings":
    case "routines":
    case "plugins":
      return `${pathname}?view=${view.kind}`;
  }
}

/** Decide what `onPopState` should do. `drawerOpen` is the ref-read live value
 * (popstate handlers close over stale React state), and `navPending` is the
 * flag `closeDrawer` sets immediately before its own `history.back()`. */
export function decidePopState(ctx: {
  drawerOpen: boolean;
  navPending: boolean;
  search: string;
}): PopStateDecision {
  if (ctx.navPending) return { action: "skip" };
  if (ctx.drawerOpen) return { action: "closeDrawer" };
  return { action: "navigate", view: viewFromSearch(ctx.search) };
}

/** The URL string the skip branch should pushState to re-establish the
 * in-flight view's URL after a view-changing drawer tap, or `null` if the URL
 * already matches (no push needed). This is what makes the fix robust under
 * BOTH scheduler orderings:
 *
 *   - P-then-E (bug report's model): back() pops the drawer entry → popstate
 *     (skip) lands on the pre-drawer URL → this returns the new view's URL →
 *     pushState establishes it; the view<->URL effect is then a no-op.
 *   - E-then-P (this Chromium 153): the effect pushStates the new URL while
 *     the drawer entry is still current; the back-traversal then jumps the
 *     cursor to the pre-drawer entry, stranding that push in the forward
 *     stack — this returns the new view's URL and the skip-branch pushState
 *     truncates the stranded entry, re-establishing the URL on top.
 *
 * `currentSearch` is `window.location.search` at skip time; `pathname` is
 * `window.location.pathname`; `view` is the in-flight view (read via a ref, so
 * it's the latest committed value). Returns `null` when `pathname + currentSearch`
 * already equals `urlForView(view, pathname)`, i.e. no work to do — which is
 * the common case for non-view-changing drawer closes (scrim/escape/drag),
 * keeping them a no-op.
 */
export function syncTargetAfterSkip(ctx: {
  view: View;
  pathname: string;
  currentSearch: string;
}): string | null {
  const target = urlForView(ctx.view, ctx.pathname);
  return ctx.pathname + ctx.currentSearch !== target ? target : null;
}
