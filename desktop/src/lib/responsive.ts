import { useEffect, useState } from "react";

/**
 * Width below which the shell switches to its phone layout: one board column
 * at a time, a collapsed terminal, and stacked form fields. Kept equal to
 * Tailwind's `md` breakpoint so the CSS `md:` variants and the JS checks below
 * always flip on the same pixel.
 */
export const MOBILE_BREAKPOINT = 768;

export const MOBILE_MEDIA_QUERY = `(max-width: ${MOBILE_BREAKPOINT - 1}px)`;

export function isMobileWidth(width: number): boolean {
  return width < MOBILE_BREAKPOINT;
}

/** Subscribes to the phone-layout media query. */
export function useIsMobile(): boolean {
  const [mobile, setMobile] = useState(
    () => typeof window !== "undefined" && window.matchMedia(MOBILE_MEDIA_QUERY).matches,
  );

  useEffect(() => {
    const media = window.matchMedia(MOBILE_MEDIA_QUERY);
    const apply = () => setMobile(media.matches);
    apply(); // width may have changed between first render and this effect
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, []);

  return mobile;
}

/**
 * True for touch-primary devices. Used for affordances that have no touch
 * equivalent (drag-and-drop, hover-only hints) rather than for layout — a
 * touchscreen laptop still gets the desktop board.
 */
export function isCoarsePointer(): boolean {
  return typeof window !== "undefined" && window.matchMedia("(pointer: coarse)").matches;
}
