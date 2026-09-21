import test from "node:test";
import assert from "node:assert/strict";
import { decidePopState, viewFromSearch, urlForView, syncTargetAfterSkip, type PopStateDecision } from "./mobileNav.ts";
import type { View } from "../store.ts";

// ---------------------------------------------------------------------------
// viewFromSearch: URL <-> view mapping (mirrors the previous inline onPopState
// logic exactly, so behaviour is preserved).
// ---------------------------------------------------------------------------

test("viewFromSearch: empty / no params / unknown params -> board", () => {
  assert.deepEqual(viewFromSearch(""), { kind: "board" });
  assert.deepEqual(viewFromSearch("?"), { kind: "board" });
  assert.deepEqual(viewFromSearch("?foo=bar"), { kind: "board" });
  assert.deepEqual(viewFromSearch("?task=&view="), { kind: "board" });
});

test("viewFromSearch: ?view= settings|routines|plugins", () => {
  assert.deepEqual(viewFromSearch("?view=settings"), { kind: "settings" });
  assert.deepEqual(viewFromSearch("?view=routines"), { kind: "routines" });
  assert.deepEqual(viewFromSearch("?view=plugins"), { kind: "plugins" });
});

test("viewFromSearch: ?view=board|detail|unknown and case variants -> board (default)", () => {
  assert.deepEqual(viewFromSearch("?view=board"), { kind: "board" });
  assert.deepEqual(viewFromSearch("?view=detail"), { kind: "board" });
  assert.deepEqual(viewFromSearch("?view=Settings"), { kind: "board" }); // case-sensitive
  assert.deepEqual(viewFromSearch("?view=SETTINGS"), { kind: "board" });
  assert.deepEqual(viewFromSearch("?view=routines&x=1"), { kind: "routines" }); // tolerate extra params
  assert.deepEqual(viewFromSearch("?view=unknown"), { kind: "board" });
});

test("viewFromSearch: ?task=N positive integer -> detail", () => {
  assert.deepEqual(viewFromSearch("?task=5"), { kind: "detail", taskId: 5 });
  assert.deepEqual(viewFromSearch("?task=12345"), { kind: "detail", taskId: 12345 });
  assert.deepEqual(viewFromSearch("?task=1"), { kind: "detail", taskId: 1 });
  assert.deepEqual(viewFromSearch("?task=5&extra=ignored"), { kind: "detail", taskId: 5 });
});

test("viewFromSearch: ?task non-positive or non-integer -> board", () => {
  assert.deepEqual(viewFromSearch("?task=0"), { kind: "board" });
  assert.deepEqual(viewFromSearch("?task=-3"), { kind: "board" });
  assert.deepEqual(viewFromSearch("?task=abc"), { kind: "board" });
  assert.deepEqual(viewFromSearch("?task=5.5"), { kind: "board" });
  assert.deepEqual(viewFromSearch("?task="), { kind: "board" });
});

test("viewFromSearch: ?task uses Number() parsing (matches the prior inline behaviour)", () => {
  // Number("5e2") === 500 (integer, > 0) -> detail. This documents that the
  // mapping tolerates numeric forms Number() accepts, exactly as the original
  // inline `Number(params.get("task"))` did.
  assert.deepEqual(viewFromSearch("?task=5e2"), { kind: "detail", taskId: 500 });
  assert.deepEqual(viewFromSearch("?task= 7 "), { kind: "detail", taskId: 7 }); // Number trims whitespace
});

test("viewFromSearch: ?task takes precedence over ?view", () => {
  assert.deepEqual(viewFromSearch("?task=5&view=settings"), { kind: "detail", taskId: 5 });
  assert.deepEqual(viewFromSearch("?task=9&view=plugins"), { kind: "detail", taskId: 9 });
  assert.deepEqual(viewFromSearch("?task=1&view=routines"), { kind: "detail", taskId: 1 });
});

// ---------------------------------------------------------------------------
// decidePopState: the three-way branch that the fix hinges on.
// ---------------------------------------------------------------------------

test("decidePopState: navPending suppresses re-navigation even on a stale URL (the fix)", () => {
  // Exact race scenario: a drawer item just changed the view, closeDrawer()
  // fired history.back(), and popstate arrives with the *pre-drawer* URL still
  // showing (the view<->URL effect hasn't rewritten it yet) and the drawer
  // already marked closed. navPending must make us skip, regardless of what
  // the stale URL would otherwise re-navigate to.
  const stale = ["", "?view=settings", "?view=routines", "?view=plugins", "?task=5", "?task=5&view=settings", "?view=board"];
  for (const search of stale) {
    assert.equal(
      decidePopState({ navPending: true, drawerOpen: false, search }).action,
      "skip",
      `navPending should skip re-navigation for stale search=${JSON.stringify(search)}`,
    );
  }
});

test("decidePopState: navPending wins over an open drawer", () => {
  // closeDrawer() always sets drawerOpen=false before back() commits, so in
  // practice navPending+drawerOpen never co-occur; but the branch order must
  // still put navPending first so the housekeeping popstate is consumed.
  assert.equal(decidePopState({ navPending: true, drawerOpen: true, search: "?view=settings" }).action, "skip");
});

test("decidePopState: open drawer (system Back) -> closeDrawer, ignoring the URL", () => {
  assert.equal(decidePopState({ navPending: false, drawerOpen: true, search: "" }).action, "closeDrawer");
  assert.equal(decidePopState({ navPending: false, drawerOpen: true, search: "?view=settings" }).action, "closeDrawer");
  assert.equal(decidePopState({ navPending: false, drawerOpen: true, search: "?task=9" }).action, "closeDrawer");
  assert.equal(decidePopState({ navPending: false, drawerOpen: true, search: "?view=plugins" }).action, "closeDrawer");
});

test("decidePopState: genuine Back / deep-link -> navigate to the URL's view", () => {
  assert.deepEqual(decidePopState({ navPending: false, drawerOpen: false, search: "" }), { action: "navigate", view: { kind: "board" } });
  assert.deepEqual(decidePopState({ navPending: false, drawerOpen: false, search: "?view=settings" }), { action: "navigate", view: { kind: "settings" } });
  assert.deepEqual(decidePopState({ navPending: false, drawerOpen: false, search: "?view=routines" }), { action: "navigate", view: { kind: "routines" } });
  assert.deepEqual(decidePopState({ navPending: false, drawerOpen: false, search: "?view=plugins" }), { action: "navigate", view: { kind: "plugins" } });
  assert.deepEqual(decidePopState({ navPending: false, drawerOpen: false, search: "?task=7" }), { action: "navigate", view: { kind: "detail", taskId: 7 } });
  assert.deepEqual(decidePopState({ navPending: false, drawerOpen: false, search: "?view=board" }), { action: "navigate", view: { kind: "board" } });
  assert.deepEqual(decidePopState({ navPending: false, drawerOpen: false, search: "?task=0" }), { action: "navigate", view: { kind: "board" } });
});

test("decidePopState: is deterministic and side-effect free (same inputs -> same output)", () => {
  const ctx = { navPending: false, drawerOpen: false, search: "?view=routines" } as const;
  assert.deepEqual(decidePopState(ctx), decidePopState(ctx));
  assert.deepEqual(decidePopState({ ...ctx }), decidePopState({ ...ctx }));
});

// ---------------------------------------------------------------------------
// urlForView: the URL (path + search) that mirrors a view.
// ---------------------------------------------------------------------------

test("urlForView: board -> bare path; detail -> ?task=N; settings/routines/plugins -> ?view=kind", () => {
  assert.equal(urlForView({ kind: "board" }, "/"), "/");
  assert.equal(urlForView({ kind: "detail", taskId: 5 }, "/"), "/?task=5");
  assert.equal(urlForView({ kind: "settings" }, "/"), "/?view=settings");
  assert.equal(urlForView({ kind: "routines" }, "/"), "/?view=routines");
  assert.equal(urlForView({ kind: "plugins" }, "/"), "/?view=plugins");
  // Path is preserved as-is (the app keeps whatever the current path is).
  assert.equal(urlForView({ kind: "board" }, "/foo"), "/foo");
  assert.equal(urlForView({ kind: "detail", taskId: 9 }, "/foo"), "/foo?task=9");
});

test("urlForView: round-trips with viewFromSearch for every view", () => {
  const vs: View[] = [
    { kind: "board" },
    { kind: "detail", taskId: 12 },
    { kind: "settings" },
    { kind: "routines" },
    { kind: "plugins" },
  ];
  for (const v of vs) {
    const url = urlForView(v, "/");
    const search = url.slice("/".length); // the part after the path
    assert.deepEqual(viewFromSearch(search), v, `round-trip failed for ${JSON.stringify(v)} (search=${search})`);
  }
});

// ---------------------------------------------------------------------------
// syncTargetAfterSkip: the URL the skip branch should pushState to re-establish
// the in-flight view's URL, or null when it already matches (no-op).
// ---------------------------------------------------------------------------

test("syncTargetAfterSkip: returns the target URL when the current URL is stale; null when it matches", () => {
  // View-changing tap (board -> settings) with the pre-drawer "/" URL left by
  // the back-traversal: must push ?view=settings.
  assert.equal(syncTargetAfterSkip({ view: { kind: "settings" }, pathname: "/", currentSearch: "" }), "/?view=settings");
  assert.equal(syncTargetAfterSkip({ view: { kind: "routines" }, pathname: "/", currentSearch: "" }), "/?view=routines");
  assert.equal(syncTargetAfterSkip({ view: { kind: "plugins" }, pathname: "/", currentSearch: "" }), "/?view=plugins");
  assert.equal(syncTargetAfterSkip({ view: { kind: "detail", taskId: 5 }, pathname: "/", currentSearch: "" }), "/?task=5");
  assert.equal(syncTargetAfterSkip({ view: { kind: "board" }, pathname: "/", currentSearch: "?view=settings" }), "/");
  // Non-view-changing close: URL already matches the view -> no-op (null).
  assert.equal(syncTargetAfterSkip({ view: { kind: "board" }, pathname: "/", currentSearch: "" }), null);
  assert.equal(syncTargetAfterSkip({ view: { kind: "settings" }, pathname: "/", currentSearch: "?view=settings" }), null);
  assert.equal(syncTargetAfterSkip({ view: { kind: "detail", taskId: 3 }, pathname: "/", currentSearch: "?task=3" }), null);
});

test("syncTargetAfterSkip: case where the pre-drawer URL differs from the new view but matches after push", () => {
  // detail -> drawer -> settings: pre-drawer search="?task=5", new view=settings.
  assert.equal(syncTargetAfterSkip({ view: { kind: "settings" }, pathname: "/", currentSearch: "?task=5" }), "/?view=settings");
  // settings -> drawer -> board: pre-drawer search="?view=settings", new view=board -> "/".
  assert.equal(syncTargetAfterSkip({ view: { kind: "board" }, pathname: "/", currentSearch: "?view=settings" }), "/");
});

// ---------------------------------------------------------------------------
// Timeline regression: a minimal pure model of the App/MobileDrawer
// history+popstate+view machinery, driven by decidePopState + syncTargetAfterSkip,
// that models BOTH deterministic scheduler orderings the fix must survive:
//   - "P-before-E" (the bug report's model, scheduler@0.27.0 MessageChannel vs
//     history traversal): back()'s popstate P dequeues before the view<->URL
//     passive-effect flush E.
//   - "E-before-P" (observed in Chromium 153 / Playwright here): the effect
//     pushState fires before the back-traversal popstate, stranding that push
//     in the forward stack; the skip branch must re-push to truncate it.
// Both must land on the chosen view with URLs in sync and no stranded entries.
// ---------------------------------------------------------------------------

function urlPath(view: View): string {
  // The search-ish part the simulator stores (path "/" implicit).
  switch (view.kind) {
    case "board": return "";
    case "detail": return `?task=${view.taskId}`;
    case "settings": return "?view=settings";
    case "routines": return "?view=routines";
    case "plugins": return "?view=plugins";
  }
}

function simulate(
  startView: View,
  tapView: View,
  decide: (ctx: { drawerOpen: boolean; navPending: boolean; search: string }) => PopStateDecision,
  ordering: "P-before-E" | "E-before-P" = "P-before-E",
): { view: View; stack: string[] } {
  const stack: { url: string; drawer?: boolean }[] = [{ url: urlPath(startView) }];
  let cursor = 0;
  let view: View = startView;
  let drawerOpen = false;
  let navPending = false;
  // Index of the drawer's synthetic history entry; the back() closeDrawer
  // issues traverses to the entry UNDER it (drawerIndex - 1), not cursor-1, so
  // an effect pushState that landed on top of the drawer entry first (the
  // E-before-P ordering) is correctly jumped over.
  let drawerIndex = -1;
  const PATH = "/"; // simulator pathname
  const currentSearch = () => stack[cursor].url;
  const currentUrl = () => stack[cursor].url;
  const push = (url: string, drawer?: boolean) => { stack.splice(cursor + 1); stack.push({ url, drawer }); cursor = stack.length - 1; };
  // The view<->URL effect (and the skip branch) use urlForView; here we store the
  // search part only (path "/" implicit), so map full->search.
  const toSearch = (full: string) => full.slice(PATH.length);
  const effectFlush = () => {
    const target = urlForView(view, PATH);
    if (PATH + currentSearch() !== target) push(toSearch(target));
  };
  const skipSync = () => {
    const t = syncTargetAfterSkip({ view, pathname: PATH, currentSearch: currentSearch() });
    if (t !== null) push(toSearch(t));
  };
  const backAndPop = () => {
    // history.back() traverses to the entry under the drawer entry (the entry
    // that was current when the drawer was opened). Models the async traversal:
    // move cursor there and fire onPopState.
    cursor = drawerIndex - 1;
    onPopState();
  };
  const onPopState = () => {
    const d = decide({ drawerOpen, navPending, search: currentSearch() });
    if (d.action === "skip") { navPending = false; drawerOpen = false; skipSync(); return; }
    if (d.action === "closeDrawer") { drawerOpen = false; return; }
    view = d.view; // navigate branch (clobber, in the buggy variant)
    const target = urlForView(view, PATH);
    if (PATH + currentSearch() !== target) push(toSearch(target)); // effect re-runs on navigate
  };

  // openDrawer
  drawerOpen = true; push(currentUrl(), true);
  drawerIndex = cursor;
  // go(action): action sets the view; closeDrawer sets drawerOpen=false, flags
  // navPending, and calls history.back().
  view = tapView;
  drawerOpen = false;
  if (stack[cursor].drawer) { navPending = true; }
  if (ordering === "P-before-E") {
    backAndPop();       // popstate (skip -> skipSync pushes the new view url)
    effectFlush();      // view<->URL effect: now a no-op (URL already matches)
  } else {
    effectFlush();      // effect pushState on top of the still-current drawer entry
    backAndPop();       // back-traversal jumps to the pre-drawer entry; skip -> skipSync truncates the stranded push
  }
  return { view, stack: stack.map((e) => e.url) };
}

// The pre-fix onPopState: no navPending handling — always re-navigated from
// whatever URL was on top of the stack when popstate fired.
const buggyDecide = (ctx: { drawerOpen: boolean; navPending: boolean; search: string }): PopStateDecision => {
  if (ctx.drawerOpen) return { action: "closeDrawer" };
  return { action: "navigate", view: viewFromSearch(ctx.search) };
};

test("regression: tapping each view-changing drawer item keeps the selection — P-before-E (bug report's model)", () => {
  const taps: View[] = [
    { kind: "settings" },
    { kind: "routines" },
    { kind: "plugins" },
    { kind: "detail", taskId: 5 },
    { kind: "board" },
  ];
  for (const tap of taps) {
    const { view, stack } = simulate({ kind: "board" }, tap, decidePopState, "P-before-E");
    assert.equal(view.kind, tap.kind, `tapping ${tap.kind} from Board should land on ${tap.kind}, got ${view.kind}`);
    if (tap.kind === "detail") assert.equal((view as { kind: "detail"; taskId: number }).taskId, 5);
    assert.deepEqual(stack, ["", urlPath(tap)], `stack should be [pre-drawer, <new view url>] for ${tap.kind}`);
  }
});

test("regression: tapping each view-changing drawer item keeps the selection — E-before-P (this Chromium 153)", () => {
  const taps: View[] = [
    { kind: "settings" },
    { kind: "routines" },
    { kind: "plugins" },
    { kind: "detail", taskId: 5 },
    { kind: "board" },
  ];
  for (const tap of taps) {
    const { view, stack } = simulate({ kind: "board" }, tap, decidePopState, "E-before-P");
    assert.equal(view.kind, tap.kind, `tapping ${tap.kind} should land on ${tap.kind}, got ${view.kind}`);
    if (tap.kind === "detail") assert.equal((view as { kind: "detail"; taskId: number }).taskId, 5);
    assert.deepEqual(stack, ["", urlPath(tap)], `no stranded drawer/stranded effect entry for ${tap.kind} (got ${JSON.stringify(stack)})`);
  }
});

test("regression: WITHOUT the navPending guard the exact bug reproduces under both orderings", () => {
  for (const ordering of ["P-before-E", "E-before-P"] as const) {
    assert.equal(simulate({ kind: "board" }, { kind: "settings" }, buggyDecide, ordering).view.kind, "board", `settings reverts to board under ${ordering}`);
    assert.equal(simulate({ kind: "board" }, { kind: "routines" }, buggyDecide, ordering).view.kind, "board", `routines reverts under ${ordering}`);
    assert.equal(simulate({ kind: "board" }, { kind: "plugins" }, buggyDecide, ordering).view.kind, "board", `plugins reverts under ${ordering}`);
    assert.equal(simulate({ kind: "board" }, { kind: "settings" }, decidePopState, ordering).view.kind, "settings", `fix keeps settings under ${ordering}`);
    assert.equal(simulate({ kind: "board" }, { kind: "routines" }, decidePopState, ordering).view.kind, "routines", `fix keeps routines under ${ordering}`);
    assert.equal(simulate({ kind: "board" }, { kind: "plugins" }, decidePopState, ordering).view.kind, "plugins", `fix keeps plugins under ${ordering}`);
  }
});

test("regression: tapping Settings from a task's detail keeps Settings (stale ?task=5 URL) under both orderings", () => {
  assert.equal(simulate({ kind: "detail", taskId: 5 }, { kind: "settings" }, buggyDecide, "P-before-E").view.kind, "detail");
  assert.equal(simulate({ kind: "detail", taskId: 5 }, { kind: "settings" }, buggyDecide, "E-before-P").view.kind, "detail");
  assert.equal(simulate({ kind: "detail", taskId: 5 }, { kind: "settings" }, decidePopState, "P-before-E").view.kind, "settings");
  assert.equal(simulate({ kind: "detail", taskId: 5 }, { kind: "settings" }, decidePopState, "E-before-P").view.kind, "settings");
});

test("regression: tapping Board from Settings keeps Board (stale ?view=settings URL) under both orderings", () => {
  assert.equal(simulate({ kind: "settings" }, { kind: "board" }, buggyDecide, "P-before-E").view.kind, "settings");
  assert.equal(simulate({ kind: "settings" }, { kind: "board" }, buggyDecide, "E-before-P").view.kind, "settings");
  assert.equal(simulate({ kind: "settings" }, { kind: "board" }, decidePopState, "P-before-E").view.kind, "board");
  assert.equal(simulate({ kind: "settings" }, { kind: "board" }, decidePopState, "E-before-P").view.kind, "board");
});

test("property: system Back after a drawer navigation returns to the pre-drawer view in one press (no stranded entry) — both orderings", () => {
  for (const ordering of ["P-before-E", "E-before-P"] as const) {
    // Board -> Settings, then a genuine system Back must reach Board in one press.
    const { view, stack } = simulate({ kind: "board" }, { kind: "settings" }, decidePopState, ordering);
    assert.deepEqual(stack, ["", "?view=settings"], `stack after Settings tap under ${ordering}`);
    assert.equal(view.kind, "settings");
    // Genuine Back: cursor 1 -> 0, onPopState navigates to viewFromSearch("")=board.
    const stack2: { url: string; drawer?: boolean }[] = stack.map((u) => ({ url: u }));
    let cursor = 1;
    let view2: View = view;
    const onPop = () => {
      const d = decidePopState({ drawerOpen: false, navPending: false, search: stack2[cursor].url });
      if (d.action === "navigate") { view2 = d.view; }
    };
    cursor--; onPop();
    assert.equal(cursor, 0);
    assert.equal(stack2[cursor].url, "");
    assert.equal(view2.kind, "board", `single Back reaches Board under ${ordering}`);
  }
});

test("property: user Back while the drawer is open dismisses it and stays on the current view", () => {
  // Independently of navPending: when the drawer is open and the user presses
  // system Back, the drawer-open branch must dismiss the drawer and NOT
  // re-navigate. (navPending is false here — it's only set by closeDrawer.)
  const d = decidePopState({ navPending: false, drawerOpen: true, search: "" });
  assert.equal(d.action, "closeDrawer");
  const d2 = decidePopState({ navPending: false, drawerOpen: true, search: "?view=plugins" });
  assert.equal(d2.action, "closeDrawer"); // URL ignored, drawer reclaims Back
});
