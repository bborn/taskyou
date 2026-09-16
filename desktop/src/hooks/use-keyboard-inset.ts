import { useEffect, type RefObject } from "react";

/**
 * Ported from bb's useMobileVisualViewportHeight
 * (apps/app/src/components/layout/useMobileVisualViewportHeight.ts).
 *
 * The previous version published a `--ty-keyboard-inset` variable and let one
 * element translate itself upward. That only ever moved the reply composer —
 * the filter sheet, the drawer and every dialog input stayed behind the
 * keyboard, because nothing else consumed the variable.
 *
 * bb's approach is structural instead: resize the whole app shell to the
 * visual viewport, so every descendant lives in a box that ends where the
 * keyboard begins and no individual component has to know about it.
 */

const SHELL_HEIGHT_PROPERTY = "--ty-shell-height";
const SAFE_AREA_BOTTOM_PROPERTY = "--ty-safe-area-bottom";

/** Below this, a viewport change is toolbar/URL-bar jitter, not a keyboard. */
export const KEYBOARD_OPEN_MIN_SHRINK_PX = 80;

/** iOS leaves the visual viewport panned after the keyboard closes; restoring
 * immediately avoids a stranded gap. iPadOS reports a desktop UA, so the
 * touch-point count is the tell. */
export function shouldRestoreIOSViewportOnKeyboardDismissal(
  nav: Pick<Navigator, "maxTouchPoints" | "platform" | "userAgent">,
): boolean {
  const isAppleWebKit = /\bAppleWebKit\//u.test(nav.userAgent);
  const isIOSDevice =
    /\b(?:iPad|iPhone|iPod)\b/u.test(nav.userAgent) ||
    (nav.platform === "MacIntel" && nav.maxTouchPoints > 1);
  return isAppleWebKit && isIOSDevice;
}

export function isKeyboardFocusTarget(target: EventTarget | null): boolean {
  return (
    target instanceof HTMLElement &&
    (target.isContentEditable ||
      target instanceof HTMLInputElement ||
      target instanceof HTMLTextAreaElement ||
      target instanceof HTMLSelectElement)
  );
}

export function useVisualViewportShell(
  shellRef: RefObject<HTMLDivElement | null>,
  enabled: boolean,
): void {
  useEffect(() => {
    const shell = shellRef.current;
    const visualViewport = window.visualViewport;
    if (!shell || !enabled || !visualViewport) return;

    const styleRoot = shell.ownerDocument.body;
    const restoreImmediately = shouldRestoreIOSViewportOnKeyboardDismissal(navigator);

    let frame: number | null = null;
    let applied: { top: number; height: number } | null = null;
    let containingBlockHeight = 0;
    let containingBlockStale = true;
    let heightBeforeKeyboard: number | null = null;
    let insetApplied = false;

    // While the keyboard is up the home indicator is covered, so reserving
    // space for it just leaves a dead gap above the keys.
    function setKeyboardInset(open: boolean) {
      if (open === insetApplied) return;
      insetApplied = open;
      if (open) styleRoot.style.setProperty(SAFE_AREA_BOTTOM_PROPERTY, "0px");
      else styleRoot.style.removeProperty(SAFE_AREA_BOTTOM_PROPERTY);
    }

    function updateKeyboardInset() {
      if (heightBeforeKeyboard === null || !isKeyboardFocusTarget(document.activeElement)) {
        setKeyboardInset(false);
        return;
      }
      setKeyboardInset(
        heightBeforeKeyboard - visualViewport!.height >= KEYBOARD_OPEN_MIN_SHRINK_PX,
      );
    }

    function clearOverride() {
      if (applied === null) return;
      applied = null;
      shell!.style.removeProperty("top");
      shell!.style.removeProperty("height");
      styleRoot.style.removeProperty(SHELL_HEIGHT_PROPERTY);
    }

    function update() {
      frame = null;
      updateKeyboardInset();

      // Pinch-zoom shrinks the visual viewport for reasons unrelated to the
      // keyboard; leave the layout alone.
      if (visualViewport!.scale !== 1) {
        clearOverride();
        return;
      }

      const viewportHeight = Math.round(visualViewport!.height);
      if (containingBlockStale) {
        containingBlockHeight = document.body.clientHeight;
        containingBlockStale = false;
      }

      const panned = visualViewport!.offsetTop > 1 || window.scrollY > 0;
      if (Math.abs(containingBlockHeight - viewportHeight) <= 1 && !panned) {
        clearOverride();
        return;
      }
      if (panned) window.scrollTo(0, 0);

      const top = Math.round(window.scrollY + visualViewport!.offsetTop);
      if (applied !== null && applied.top === top && applied.height === viewportHeight) return;

      applied = { top, height: viewportHeight };
      shell!.style.top = `${top}px`;
      shell!.style.height = `${viewportHeight}px`;
      styleRoot.style.setProperty(SHELL_HEIGHT_PROPERTY, `${viewportHeight}px`);
    }

    function schedule() {
      if (frame !== null) window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(update);
    }
    function scheduleWithContainingBlock() {
      containingBlockStale = true;
      schedule();
    }
    function onViewportScroll() {
      if (applied === null && !isKeyboardFocusTarget(document.activeElement)) return;
      schedule();
    }
    function onFocusIn(event: FocusEvent) {
      if (!isKeyboardFocusTarget(event.target)) return;
      heightBeforeKeyboard ??= visualViewport!.height;
      scheduleWithContainingBlock();
    }
    function onFocusOut(event: FocusEvent) {
      if (!isKeyboardFocusTarget(event.target)) return;
      if (isKeyboardFocusTarget(event.relatedTarget)) return;
      heightBeforeKeyboard = null;
      setKeyboardInset(false);
      if (!restoreImmediately) return;
      if (frame !== null) {
        window.cancelAnimationFrame(frame);
        frame = null;
      }
      clearOverride();
    }

    update();
    visualViewport.addEventListener("resize", schedule);
    visualViewport.addEventListener("scroll", onViewportScroll);
    window.addEventListener("resize", scheduleWithContainingBlock);
    window.addEventListener("orientationchange", scheduleWithContainingBlock);
    document.addEventListener("focusin", onFocusIn);
    document.addEventListener("focusout", onFocusOut);

    return () => {
      visualViewport.removeEventListener("resize", schedule);
      visualViewport.removeEventListener("scroll", onViewportScroll);
      window.removeEventListener("resize", scheduleWithContainingBlock);
      window.removeEventListener("orientationchange", scheduleWithContainingBlock);
      document.removeEventListener("focusin", onFocusIn);
      document.removeEventListener("focusout", onFocusOut);
      if (frame !== null) window.cancelAnimationFrame(frame);
      setKeyboardInset(false);
      clearOverride();
    };
  }, [shellRef, enabled]);
}

/** Set on the sheet panel itself, and read by its max-height. Mirrors bb's
 * `--bb-drawer-keyboard-inset`: the element that moves also declares how much
 * shorter it has to be. */
export const SHEET_KEYBOARD_INSET_PROPERTY = "--ty-sheet-keyboard-inset";

/** bb's second layer: a bottom sheet sets its OWN `bottom` to the measured
 * overlap. The shell resize handles the page; this handles a panel that is
 * fixed to the viewport bottom and would otherwise sit under the keys. */
export function measureKeyboardOverlap(
  layoutViewportHeight: number,
  visualViewportHeight: number,
  visualViewportOffsetTop: number,
): number {
  const overlap = Math.round(
    layoutViewportHeight - (visualViewportHeight + visualViewportOffsetTop),
  );
  return overlap >= KEYBOARD_OPEN_MIN_SHRINK_PX ? overlap : 0;
}

export function useSheetKeyboardInset(
  panel: HTMLElement | null,
  open: boolean,
): void {
  useEffect(() => {
    const visualViewport = window.visualViewport;
    if (!panel || !open || !visualViewport) return;

    let frame: number | null = null;
    function reset() {
      panel!.style.bottom = "";
      panel!.style.removeProperty(SHEET_KEYBOARD_INSET_PROPERTY);
    }
    function apply() {
      frame = null;
      if (visualViewport!.scale !== 1) {
        reset();
        return;
      }
      const overlap = measureKeyboardOverlap(
        panel!.ownerDocument.documentElement.clientHeight,
        visualViewport!.height,
        visualViewport!.offsetTop,
      );
      if (overlap === 0) {
        reset();
        return;
      }
      // Both, as bb does. `bottom` lifts the panel clear of the keys; the
      // property shrinks its max-height by the same amount. Lifting alone just
      // pushes the panel's own content off the top of the screen — which is
      // what was cutting the project list off mid-row.
      panel!.style.bottom = `${overlap}px`;
      panel!.style.setProperty(SHEET_KEYBOARD_INSET_PROPERTY, `${overlap}px`);
    }
    function schedule() {
      if (frame !== null) window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(apply);
    }

    apply();
    visualViewport.addEventListener("resize", schedule);
    visualViewport.addEventListener("scroll", schedule);
    return () => {
      visualViewport.removeEventListener("resize", schedule);
      visualViewport.removeEventListener("scroll", schedule);
      if (frame !== null) window.cancelAnimationFrame(frame);
      reset();
    };
  }, [panel, open]);
}
