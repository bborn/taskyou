import { useEffect, useState } from "react";
import { AnimatePresence } from "motion/react";
import { Search, X } from "lucide-react";
import type { Column } from "../lib/board";
import { store, useAppSelector } from "../store";
import { CardSlot } from "./Board";
import { cn } from "@/lib/utils";
import { Input } from "@/components/ui/input";

/** "Needs you" comes first: the point of the phone is unblocking work you
 * can't see, not browsing the backlog. */
const FILTERS = [
  { key: "blocked", label: "Needs you" },
  { key: "processing", label: "Running" },
  { key: "backlog", label: "Backlog" },
  { key: "done", label: "Done" },
] as const;

type FilterKey = (typeof FILTERS)[number]["key"];

const ACCENT: Record<FilterKey, string> = {
  blocked: "border-status-blocked/60 bg-status-blocked/15 text-status-blocked",
  processing: "border-status-processing/60 bg-status-processing/15 text-status-processing",
  backlog: "border-status-backlog/60 bg-status-backlog/15 text-status-backlog",
  done: "border-border bg-surface-3 text-foreground",
};

const EMPTY: Record<FilterKey, string> = {
  blocked: "Nothing waiting on you.",
  processing: "Nothing running right now.",
  backlog: "Backlog is empty.",
  done: "Nothing finished yet.",
};

/** Cards rendered before a "show more" button; Done alone can hold hundreds. */
const RENDER_CAP = 50;

/** Phone board: one scrolling list at a time instead of four side-by-side
 * columns. Cards are the desktop component, so the layouts can't drift. */
export function MobileBoard({ columns }: { columns: Column[] }) {
  const projects = useAppSelector((s) => s.projects);
  const latestLogs = useAppSelector((s) => s.latestLogs);
  const filter = useAppSelector((s) => s.filter);
  const [active, setActive] = useState<FilterKey>("blocked");
  const [searchOpen, setSearchOpen] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const [landed, setLanded] = useState(false);

  const count = (key: FilterKey) => columns.find((c) => c.status === key)?.tasks.length ?? 0;
  const tasks = columns.find((c) => c.status === active)?.tasks ?? [];
  const visible = showAll ? tasks : tasks.slice(0, RENDER_CAP);

  // First load: an empty "Needs you" is good news, but land on the work that
  // is actually moving instead of an empty list.
  useEffect(() => {
    if (landed || columns.every((c) => c.tasks.length === 0)) return;
    setLanded(true);
    if (count("blocked") === 0) {
      setActive(count("processing") > 0 ? "processing" : "backlog");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [landed, columns]);

  function choose(key: FilterKey) {
    setActive(key);
    setShowAll(false);
  }

  function closeSearch() {
    setSearchOpen(false);
    store.setFilter("");
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center gap-1.5 border-b px-3 py-2">
        {/* Chips scroll on their own so the search button stays pinned. */}
        <div className="flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto [scrollbar-width:none]">
          {FILTERS.map((f) => (
            <button
              key={f.key}
              onClick={() => choose(f.key)}
              className={cn(
                "flex h-9 shrink-0 items-center gap-1.5 rounded-full border px-3.5 text-[13px] font-medium transition-colors",
                active === f.key ? ACCENT[f.key] : "border-transparent bg-surface-2 text-muted-foreground",
              )}
            >
              {f.label}
              <span className="text-[11px] tabular-nums opacity-70">{count(f.key)}</span>
            </button>
          ))}
        </div>
        <button
          onClick={() => (searchOpen || filter ? closeSearch() : setSearchOpen(true))}
          aria-label={searchOpen || filter ? "Close search" : "Search tasks"}
          className={cn(
            "flex size-9 shrink-0 items-center justify-center rounded-full",
            searchOpen || filter ? "bg-surface-3 text-foreground" : "text-muted-foreground",
          )}
        >
          {searchOpen || filter ? <X className="size-4" /> : <Search className="size-4" />}
        </button>
      </div>

      {(searchOpen || filter !== "") && (
        <div className="shrink-0 px-3 pt-2">
          {/* 16px text: iOS Safari zooms the page when focusing anything smaller. */}
          <Input
            autoFocus
            value={filter}
            className="h-10 text-base md:text-base"
            placeholder="Filter — text, #123, [project]"
            onChange={(e) => store.setFilter(e.target.value)}
          />
        </div>
      )}

      <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto overscroll-contain px-3 pt-2 pb-[max(1.5rem,env(safe-area-inset-bottom))]">
        {tasks.length === 0 ? (
          <div className="px-4 py-12 text-center text-sm text-muted-foreground">
            {filter ? "No tasks match that filter." : EMPTY[active]}
          </div>
        ) : (
          <AnimatePresence initial={false}>
            {visible.map((task) => (
              <CardSlot
                key={task.id}
                task={task}
                selected={false}
                tapToOpen
                projectColor={projects.find((p) => p.name === task.project)?.color || "var(--muted-foreground)"}
                latest={latestLogs[String(task.id)]}
              />
            ))}
          </AnimatePresence>
        )}
        {tasks.length > visible.length && (
          <button
            className="rounded-lg py-3 text-center text-[13px] text-muted-foreground active:bg-surface-2"
            onClick={() => setShowAll(true)}
          >
            {tasks.length - visible.length} more…
          </button>
        )}
      </div>
    </div>
  );
}
