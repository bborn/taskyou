import { useEffect, useMemo, useRef, useState } from "react";
import type { Task } from "../api/types";
import { fuzzyScore } from "../lib/fuzzy";
import { store, useAppState } from "../store";

const STATUS_PRIORITY: Record<string, number> = {
  blocked: 0,
  processing: 1,
  queued: 2,
  backlog: 3,
  done: 4,
  archived: 5,
};

export function Palette() {
  const { tasks } = useAppState();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const results = useMemo(() => {
    const q = query.trim();
    let matched: { task: Task; score: number }[];
    if (!q) {
      matched = tasks
        .filter((t) => t.status !== "archived")
        .map((task) => ({ task, score: 0 }));
    } else {
      matched = tasks
        .map((task) => {
          const idMatch = `#${task.id}` === q || String(task.id) === q;
          const score = idMatch ? 1000 : fuzzyScore(q, task.title);
          return { task, score };
        })
        .filter((r) => r.score >= 0);
    }
    matched.sort((a, b) => {
      if (b.score !== a.score) return b.score - a.score;
      const sp = (STATUS_PRIORITY[a.task.status] ?? 9) - (STATUS_PRIORITY[b.task.status] ?? 9);
      if (sp !== 0) return sp;
      return Date.parse(b.task.updated_at) - Date.parse(a.task.updated_at);
    });
    return matched.slice(0, 30).map((r) => r.task);
  }, [tasks, query]);

  useEffect(() => {
    setActive(0);
  }, [query]);

  function select(task: Task) {
    store.setPalette(false);
    store.openDetail(task.id);
  }

  return (
    <div className="overlay" onMouseDown={() => store.setPalette(false)}>
      <div className="modal palette" onMouseDown={(e) => e.stopPropagation()}>
        <input
          ref={inputRef}
          value={query}
          placeholder="Jump to task — type a title or #id"
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown") {
              e.preventDefault();
              setActive((a) => Math.min(results.length - 1, a + 1));
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              setActive((a) => Math.max(0, a - 1));
            } else if (e.key === "Enter" && results[active]) {
              select(results[active]);
            } else if (e.key === "Escape") {
              store.setPalette(false);
            }
          }}
        />
        <div className="palette-results">
          {results.length === 0 && <div className="column-empty">No matching tasks</div>}
          {results.map((task, i) => (
            <div
              key={task.id}
              className={`palette-item ${i === active ? "active" : ""}`}
              onMouseEnter={() => setActive(i)}
              onClick={() => select(task)}
            >
              <span className={`status-badge ${task.status}`}>{task.status}</span>
              <span className="card-id">#{task.id}</span>
              <span className="title">{task.title}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
