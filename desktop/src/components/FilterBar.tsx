import { useEffect, useRef } from "react";
import { X } from "lucide-react";
import { store, useAppState } from "../store";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export function FilterBar() {
  const { filter } = useAppState();
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  function close() {
    store.setFilter("");
    store.setFilterOpen(false);
  }

  return (
    <div className="flex items-center gap-2 border-b border-white/[0.06] bg-white/[0.02] px-4 py-2">
      <span className="text-xs text-muted-foreground">Filter</span>
      <Input
        ref={inputRef}
        className="h-7 flex-1"
        value={filter}
        placeholder="text, #id, or [project]"
        onChange={(e) => store.setFilter(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape") close();
          else if (e.key === "Enter") inputRef.current?.blur();
        }}
      />
      <Button variant="ghost" size="icon" className="size-7" onClick={close}>
        <X className="size-4" />
      </Button>
    </div>
  );
}
