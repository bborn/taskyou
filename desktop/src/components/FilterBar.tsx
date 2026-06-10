import { useEffect, useRef } from "react";
import { store, useAppState } from "../store";

export function FilterBar() {
  const { filter } = useAppState();
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  return (
    <div className="filterbar">
      <span style={{ color: "var(--text-dim)" }}>Filter</span>
      <input
        ref={inputRef}
        value={filter}
        placeholder="text, #id, or [project]"
        onChange={(e) => store.setFilter(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            store.setFilter("");
            store.setFilterOpen(false);
          } else if (e.key === "Enter") {
            inputRef.current?.blur();
          }
        }}
      />
      <button
        className="icon-btn"
        onClick={() => {
          store.setFilter("");
          store.setFilterOpen(false);
        }}
      >
        ✕
      </button>
    </div>
  );
}
