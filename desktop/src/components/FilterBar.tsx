import { useEffect, useMemo, useRef, useState } from "react";
import { X } from "lucide-react";
import { fuzzyScore } from "../lib/fuzzy";
import { store, useAppState } from "../store";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

/**
 * Board filter, mirroring the TUI's behavior:
 * - free text matches title/body, `#id` selects a task, `[project]` scopes to a project
 * - typing an unclosed `[` opens a fuzzy project dropdown (↑/↓ navigate, Tab/Enter accept)
 * - Enter (no dropdown) applies the filter and blurs so board navigation resumes
 * - Esc clears and closes; Backspace on empty closes; Backspace after `]` deletes the chip
 */
export function FilterBar() {
  const { filter, projects } = useAppState();
  const inputRef = useRef<HTMLInputElement>(null);
  const [dropdownIndex, setDropdownIndex] = useState(0);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  // Dropdown is active while the last "[" has no closing "]".
  const dropdownQuery = useMemo(() => {
    const lastBracket = filter.lastIndexOf("[");
    if (lastBracket < 0) return null;
    const after = filter.slice(lastBracket + 1);
    return after.includes("]") ? null : after;
  }, [filter]);

  const dropdownProjects = useMemo(() => {
    if (dropdownQuery === null) return [];
    if (dropdownQuery === "") return projects.slice(0, 10);
    return projects
      .map((p) => ({ p, score: fuzzyScore(dropdownQuery, p.name) }))
      .filter((r) => r.score >= 0)
      .sort((a, b) => b.score - a.score)
      .slice(0, 10)
      .map((r) => r.p);
  }, [dropdownQuery, projects]);

  const showDropdown = dropdownQuery !== null && dropdownProjects.length > 0;

  useEffect(() => {
    setDropdownIndex(0);
  }, [dropdownQuery]);

  function acceptProject(name: string) {
    const lastBracket = filter.lastIndexOf("[");
    const prefix = lastBracket > 0 ? filter.slice(0, lastBracket) : "";
    store.setFilter(`${prefix}[${name}] `);
    inputRef.current?.focus();
  }

  function clearAndClose() {
    store.setFilter("");
    store.setFilterOpen(false);
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Escape") {
      e.stopPropagation();
      clearAndClose();
      return;
    }
    if (showDropdown && (e.key === "Tab" || e.key === "Enter")) {
      e.preventDefault();
      acceptProject(dropdownProjects[dropdownIndex].name);
      return;
    }
    if (showDropdown && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
      e.preventDefault();
      setDropdownIndex((i) => {
        const n = dropdownProjects.length;
        return e.key === "ArrowDown" ? (i + 1) % n : (i - 1 + n) % n;
      });
      return;
    }
    if (e.key === "Enter") {
      // Apply and hand keyboard control back to the board; filter stays active.
      inputRef.current?.blur();
      if (!filter) store.setFilterOpen(false);
      return;
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      // Resume board navigation (TUI parity).
      inputRef.current?.blur();
      return;
    }
    if (e.key === "Backspace") {
      if (filter === "") {
        clearAndClose();
        return;
      }
      // Delete a whole [project] chip when the caret sits right after "]".
      const pos = inputRef.current?.selectionStart ?? filter.length;
      if (pos > 0 && filter[pos - 1] === "]") {
        const open = filter.lastIndexOf("[", pos - 1);
        if (open >= 0) {
          e.preventDefault();
          let end = pos;
          if (filter[end] === " ") end++;
          store.setFilter(filter.slice(0, open) + filter.slice(end));
          requestAnimationFrame(() => inputRef.current?.setSelectionRange(open, open));
        }
      }
    }
  }

  return (
    <div className="relative border-b bg-surface-1">
      <div className="flex items-center gap-2 px-4 py-2">
        <span className="text-xs text-muted-foreground">Filter</span>
        <Input
          id="board-filter"
          ref={inputRef}
          className="h-7 flex-1"
          value={filter}
          placeholder="text, #id, or [project — type [ for suggestions"
          onChange={(e) => store.setFilter(e.target.value)}
          onKeyDown={onKeyDown}
        />
        <Button variant="ghost" size="icon" className="size-7" title="Clear filter (Esc)" onClick={clearAndClose}>
          <X className="size-4" />
        </Button>
      </div>

      {showDropdown && (
        <div className="absolute left-16 top-full z-30 mt-1 w-64 overflow-hidden rounded-md border bg-popover py-1 shadow-md">
          {dropdownProjects.map((p, i) => (
            <div
              key={p.name}
              className={cn(
                "flex items-center gap-2 px-3 py-1.5 text-[12.5px]",
                i === dropdownIndex && "bg-accent text-accent-foreground",
              )}
              onMouseEnter={() => setDropdownIndex(i)}
              onMouseDown={(e) => {
                e.preventDefault();
                acceptProject(p.name);
              }}
            >
              <span
                className="inline-block size-2 rounded-full"
                style={{ background: p.color || "var(--muted-foreground)" }}
              />
              <span>{p.name}</span>
              <span className="ml-auto text-[10px] text-muted-foreground">{p.task_count} tasks</span>
            </div>
          ))}
          <div className="border-t px-3 pt-1 text-[10px] text-muted-foreground">
            ↑↓ navigate · Tab/Enter select
          </div>
        </div>
      )}
    </div>
  );
}
