// VS Code-style subsequence fuzzy matching: every character of the needle
// must appear in order; contiguous runs and word-boundary hits score higher.
export function fuzzyScore(needle: string, haystack: string): number {
  if (!needle) return 0;
  const n = needle.toLowerCase();
  const h = haystack.toLowerCase();

  let score = 0;
  let hi = 0;
  let lastMatch = -2;
  for (let ni = 0; ni < n.length; ni++) {
    const c = n[ni];
    const found = h.indexOf(c, hi);
    if (found === -1) return -1;
    score += 1;
    if (found === lastMatch + 1) score += 2; // contiguous bonus
    if (found === 0 || h[found - 1] === " " || h[found - 1] === "-" || h[found - 1] === "_") {
      score += 3; // word-boundary bonus
    }
    lastMatch = found;
    hi = found + 1;
  }
  // Mild penalty for long haystacks so tighter matches rank first.
  return score - h.length / 100;
}

export function fuzzyMatches(needle: string, haystack: string): boolean {
  return fuzzyScore(needle, haystack) >= 0;
}
