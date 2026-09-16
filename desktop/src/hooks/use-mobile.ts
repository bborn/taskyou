import { useSyncExternalStore } from "react";

// Phone layout kicks in below Tailwind's `md` breakpoint. The whole mobile
// story hangs off this one query so there is a single place to tune it.
const MOBILE_QUERY = "(max-width: 767px)";

// Touch sizing keys off capability, not width. A narrow desktop window should
// not sprout 44px hit targets and 15px body text, so anything that exists for
// fingers is gated on width AND pointer — `max-md:pointer-coarse:` in CSS.
const COARSE_QUERY = "(pointer: coarse)";

// useSyncExternalStore resubscribes whenever the subscribe function's identity
// changes, so the per-query closures have to be created once and cached.
const subscribers = new Map<string, (onChange: () => void) => () => void>();
const snapshots = new Map<string, () => boolean>();

function subscriberFor(query: string): (onChange: () => void) => () => void {
  let subscribe = subscribers.get(query);
  if (!subscribe) {
    subscribe = (onChange: () => void) => {
      const media = window.matchMedia(query);
      media.addEventListener("change", onChange);
      return () => media.removeEventListener("change", onChange);
    };
    subscribers.set(query, subscribe);
  }
  return subscribe;
}

function snapshotFor(query: string): () => boolean {
  let snapshot = snapshots.get(query);
  if (!snapshot) {
    snapshot = () => window.matchMedia(query).matches;
    snapshots.set(query, snapshot);
  }
  return snapshot;
}

function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    subscriberFor(query),
    snapshotFor(query),
    () => false,
  );
}

/** True when the viewport is phone-sized. Re-renders on rotation/resize. */
export function useIsMobile(): boolean {
  return useMediaQuery(MOBILE_QUERY);
}

/** True on touch input. Drives affordances that only make sense for fingers:
 * swipe gestures, tap-to-open, and "Enter inserts a newline". */
export function useIsCoarsePointer(): boolean {
  return useMediaQuery(COARSE_QUERY);
}

/** True for a real phone — narrow *and* touch. Both hooks run unconditionally;
 * `&&` here would short-circuit the second one and break the rules of hooks. */
export function useIsTouchPhone(): boolean {
  const narrow = useIsMobile();
  const coarse = useIsCoarsePointer();
  return narrow && coarse;
}
