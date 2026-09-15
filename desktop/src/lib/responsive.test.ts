import test from "node:test";
import assert from "node:assert/strict";
import { MOBILE_BREAKPOINT, MOBILE_MEDIA_QUERY, isMobileWidth } from "./responsive.ts";

test("the phone layout ends exactly where Tailwind's md: variants begin", () => {
  // Off-by-one here means CSS and JS disagree for one pixel column: the board
  // would render its phone tabs while the chrome around it uses md: spacing.
  assert.equal(isMobileWidth(MOBILE_BREAKPOINT - 1), true);
  assert.equal(isMobileWidth(MOBILE_BREAKPOINT), false);
  assert.equal(MOBILE_MEDIA_QUERY, `(max-width: ${MOBILE_BREAKPOINT - 1}px)`);
});

test("common phone and desktop widths land on the layout you would expect", () => {
  for (const width of [320, 375, 390, 414, 430, 767]) {
    assert.equal(isMobileWidth(width), true, `${width}px should use the phone layout`);
  }
  for (const width of [768, 820, 1024, 1440]) {
    assert.equal(isMobileWidth(width), false, `${width}px should use the desktop layout`);
  }
});
