import { useSyncExternalStore } from "react";

// Phone layout kicks in below Tailwind's `md` breakpoint. The whole mobile
// story hangs off this one query so there is a single place to tune it.
const MOBILE_QUERY = "(max-width: 767px)";

function subscribe(onChange: () => void): () => void {
  const media = window.matchMedia(MOBILE_QUERY);
  media.addEventListener("change", onChange);
  return () => media.removeEventListener("change", onChange);
}

/** True when the viewport is phone-sized. Re-renders on rotation/resize. */
export function useIsMobile(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => window.matchMedia(MOBILE_QUERY).matches,
    () => false,
  );
}
