import { useMemo, useState } from "react";
import type { Task } from "../api/types";
import { fuzzyScore } from "../lib/fuzzy";
import { store, useAppState } from "../store";
import {
  CommandDialog,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Badge } from "@/components/ui/badge";

const STATUS_PRIORITY: Record<string, number> = {
  blocked: 0,
  processing: 1,
  queued: 2,
  backlog: 3,
  done: 4,
  archived: 5,
};

const STATUS_BADGE: Record<string, string> = {
  backlog: "border-status-backlog/50 text-status-backlog",
  queued: "border-amber-300/50 text-amber-300",
  processing: "border-status-processing/50 text-status-processing",
  blocked: "border-status-blocked/50 text-status-blocked",
  done: "text-muted-foreground",
  archived: "text-muted-foreground",
};

export function Palette() {
  const { tasks } = useAppState();
  const [query, setQuery] = useState("");

  const results = useMemo(() => {
    const q = query.trim();
    let matched: { task: Task; score: number }[];
    if (!q) {
      matched = tasks.filter((t) => t.status !== "archived").map((task) => ({ task, score: 0 }));
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

  return (
    <CommandDialog
      open
      onOpenChange={(open) => !open && store.setPalette(false)}
      shouldFilter={false}
      title="Jump to task"
      description="Search tasks by title or #id"
    >
      <CommandInput
        placeholder="Jump to task — type a title or #id"
        value={query}
        onValueChange={setQuery}
      />
      <CommandList>
        <CommandEmpty>No matching tasks</CommandEmpty>
        {results.map((task) => (
          <CommandItem
            key={task.id}
            value={String(task.id)}
            onSelect={() => {
              store.setPalette(false);
              store.openDetail(task.id);
            }}
          >
            <Badge variant="outline" className={`h-4.5 px-1.5 text-[10px] ${STATUS_BADGE[task.status] ?? ""}`}>
              {task.status}
            </Badge>
            <span className="font-mono text-[11px] text-muted-foreground">#{task.id}</span>
            <span className="truncate">{task.title}</span>
          </CommandItem>
        ))}
      </CommandList>
    </CommandDialog>
  );
}
