import { useEffect } from "react";

/** How much the visual viewport must shrink before we call it a keyboard.
 * Mobile browsers collapse their own URL bar by ~60px as you scroll; without
 * a deadzone that jitter reads as the keyboard opening and closing. */
const KEYBOARD_DEADZONE_PX = 80;

const INSET_VAR = "--ty-keyboard-inset";
const SAFE_BOTTOM_VAR = "--ty-safe-area-bottom";

function isIOS(): boolean {
  const { userAgent, platform, maxTouchPoints } = window.navigator;
  // iPadOS reports a desktop UA, so fall back to the touch-point tell.
  const webkit = /\bAppleWebKit\//u.test(userAgent);
  const ios =
    /\b(?:iPad|iPhone|iPod)\b/u.test(userAgent) ||
    (platform === "MacIntel" && maxTouchPoints > 1);
  return webkit && ios;
}

/**
 * Publishes the on-screen keyboard's height as `--ty-keyboard-inset` so bottom
 * anchored UI (the reply composer, bottom sheets) can lift clear of it.
 *
 * `interactive-widget=resizes-content` in index.html handles this for us on
 * Android/Chrome, but iOS Safari leaves the layout viewport alone and only
 * moves the *visual* viewport — so there we have to measure it ourselves.
 *
 * Also zeroes `--ty-safe-area-bottom` on iOS while the keyboard is up: the
 * home indicator is covered by the keyboard, so reserving space for it just
 * leaves a dead gap above the keys.
 */
export function useKeyboardInset(): void {
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;

    const root = document.documentElement;
    const ios = isIOS();
    let frame = 0;

    function apply() {
      frame = 0;
      const viewport = window.visualViewport;
      if (!viewport) return;

      // While pinch-zoomed the visual viewport is smaller for reasons that
      // have nothing to do with the keyboard. Leave the layout alone.
      if (viewport.scale !== 1) {
        root.style.removeProperty(INSET_VAR);
        root.style.removeProperty(SAFE_BOTTOM_VAR);
        return;
      }

      const hidden = Math.round(
        window.innerHeight - (viewport.height + viewport.offsetTop),
      );
      const inset = hidden >= KEYBOARD_DEADZONE_PX ? hidden : 0;

      if (inset === 0) {
        root.style.removeProperty(INSET_VAR);
        root.style.removeProperty(SAFE_BOTTOM_VAR);
        return;
      }
      root.style.setProperty(INSET_VAR, `${inset}px`);
      if (ios) root.style.setProperty(SAFE_BOTTOM_VAR, "0px");
    }

    function schedule() {
      if (frame) return;
      frame = requestAnimationFrame(apply);
    }

    vv.addEventListener("resize", schedule);
    vv.addEventListener("scroll", schedule);
    apply();

    return () => {
      if (frame) cancelAnimationFrame(frame);
      vv.removeEventListener("resize", schedule);
      vv.removeEventListener("scroll", schedule);
      root.style.removeProperty(INSET_VAR);
      root.style.removeProperty(SAFE_BOTTOM_VAR);
    };
  }, []);
}
