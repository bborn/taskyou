import { useEffect, useState } from "react";
import { Check, Trash2 } from "lucide-react";
import {
  GROUP_BY_OPTIONS,
  SORT_OPTIONS,
  type ListGroupBy,
  type ListSort,
} from "../lib/list";
import { store, useAppState } from "../store";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

// The two list controls, matching the TUI's `O` and `V`.
//
// Escape is deliberately NOT handled by Radix here (onEscapeKeyDown is
// prevented): Radix closes on Escape by flipping our own store state, which the
// app's window-level handler then reads as "no dialog open" and falls through
// to clearing the filter — one keypress doing two things. App.tsx owns Escape.
//
// Row height is deliberately absent from Arrange: "how should this look" is a
// design decision we make, not a preference to hand over. Grouping and sort are
// genuinely per-moment choices — group by project today, by status tomorrow —
// which is why those are offered and row height is not.

function OptionRow<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: readonly T[];
  onChange: (next: T) => void;
}) {
  return (
    <div className="flex items-center gap-3">
      <span className="w-20 shrink-0 text-xs text-muted-foreground">{label}</span>
      <div className="flex flex-wrap gap-1">
        {options.map((opt) => (
          <button
            key={opt}
            type="button"
            onClick={() => onChange(opt)}
            className={cn(
              "rounded-md px-2 py-1 text-xs capitalize transition-colors",
              opt === value
                ? "bg-primary text-primary-foreground"
                : "bg-surface-2 text-muted-foreground hover:text-foreground",
            )}
          >
            {opt}
          </button>
        ))}
      </div>
    </div>
  );
}

export function ArrangeMenu() {
  const { arrangeOpen, listOptions } = useAppState();

  return (
    <Dialog open={arrangeOpen} onOpenChange={(open) => store.setArrangeOpen(open)}>
      <DialogContent className="sm:max-w-md" onEscapeKeyDown={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle>Arrange list</DialogTitle>
          <DialogDescription>How the list groups and orders tasks.</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3 py-1">
          <OptionRow
            label="Group by"
            value={listOptions.groupBy}
            options={GROUP_BY_OPTIONS}
            onChange={(groupBy: ListGroupBy) => store.setListOptions({ ...listOptions, groupBy })}
          />
          <OptionRow
            label="Sort"
            value={listOptions.sort}
            options={SORT_OPTIONS}
            onChange={(sort: ListSort) => store.setListOptions({ ...listOptions, sort })}
          />
        </div>
      </DialogContent>
    </Dialog>
  );
}

export function ViewsMenu() {
  const { viewsOpen, savedViews, activeView, filter } = useAppState();
  const [name, setName] = useState("");

  useEffect(() => {
    if (viewsOpen) {
      setName("");
      void store.refreshViews();
    }
  }, [viewsOpen]);

  const canSave = filter.trim() !== "" && name.trim() !== "";

  return (
    <Dialog open={viewsOpen} onOpenChange={(open) => store.setViewsOpen(open)}>
      <DialogContent className="sm:max-w-lg" onEscapeKeyDown={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle>Saved views</DialogTitle>
          <DialogDescription>
            A view is a name plus a filter query. The same queries work in the TUI and in{" "}
            <code className="font-mono text-[11px]">ty list --view</code>.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-1">
          {savedViews.length === 0 && (
            <p className="py-2 text-sm text-muted-foreground">
              No saved views yet. Filter the board, then save it below.
            </p>
          )}
          {savedViews.map((view) => (
            <div
              key={view.id}
              className={cn(
                "group flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 hover:bg-surface-2",
                view.name === activeView && "bg-surface-2",
              )}
              onClick={() => void store.applyView(view.name)}
            >
              <Check
                className={cn(
                  "size-3.5 shrink-0",
                  view.name === activeView ? "text-primary" : "text-transparent",
                )}
              />
              <span className="shrink-0 text-sm font-medium">{view.name}</span>
              <code className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">
                {view.query || "(everything)"}
              </code>
              <Button
                variant="ghost"
                size="icon"
                className="size-6 opacity-0 transition-opacity group-hover:opacity-100"
                title={`Delete "${view.name}"`}
                onClick={(e) => {
                  e.stopPropagation();
                  void store.deleteView(view.name);
                }}
              >
                <Trash2 className="size-3.5" />
              </Button>
            </div>
          ))}
        </div>

        <div className="flex items-center gap-2 border-t pt-3">
          <Input
            value={name}
            placeholder={filter.trim() ? "Save current filter as…" : "Set a filter first"}
            disabled={filter.trim() === ""}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && canSave) void store.saveCurrentAsView(name.trim());
            }}
            className="h-8"
          />
          <Button
            size="sm"
            disabled={!canSave}
            onClick={() => void store.saveCurrentAsView(name.trim())}
          >
            Save
          </Button>
        </div>
        {filter.trim() && (
          <p className="text-[11px] text-muted-foreground">
            Current filter: <code className="font-mono">{filter}</code>
          </p>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** The arrangement, always visible above the list — a setting you cannot see is
 * one you forget you changed. Mirrors the TUI's options bar. */
export function ListToolbar() {
  const { listOptions, activeView } = useAppState();
  return (
    <div className="flex items-center gap-2 px-2 text-[11px] text-muted-foreground">
      {activeView && (
        <span className="rounded bg-primary px-1.5 py-0.5 font-medium text-primary-foreground">
          {activeView}
        </span>
      )}
      <button
        type="button"
        className="transition-colors hover:text-foreground"
        onClick={() => store.setArrangeOpen(true)}
        title="Arrange list (O)"
      >
        group: <span className="text-foreground">{listOptions.groupBy}</span>
        {"   "}sort: <span className="text-foreground">{listOptions.sort}</span>
      </button>
      <span className="flex-1" />
      <button
        type="button"
        className="transition-colors hover:text-foreground"
        onClick={() => store.setViewsOpen(true)}
        title="Saved views (V)"
      >
        views
      </button>
      <button
        type="button"
        className="transition-colors hover:text-foreground"
        onClick={() => store.setBoardMode("board")}
        title="Back to the board (v)"
      >
        board
      </button>
    </div>
  );
}
