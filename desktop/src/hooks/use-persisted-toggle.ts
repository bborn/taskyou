import { useState } from "react";

/** Remember disclosure state per browser, without making storage a requirement. */
export function usePersistedToggle(key: string, initial: boolean) {
  const [value, setValue] = useState(() => {
    try {
      const saved = localStorage.getItem(key);
      return saved === null ? initial : saved === "true";
    } catch {
      return initial;
    }
  });
  function update(next: boolean) {
    setValue(next);
    try { localStorage.setItem(key, String(next)); } catch { /* Storage may be disabled. */ }
  }
  return [value, update] as const;
}
