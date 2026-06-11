import { useCallback, useEffect, useState } from "react";
import { Play, RefreshCw, ScrollText, CalendarClock } from "lucide-react";
import { api } from "../api/client";
import type { Routine } from "../api/types";
import { shortDuration } from "../lib/board";
import { store } from "../store";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

const STATUS_BADGE: Record<string, string> = {
  ok: "border-status-processing/50 text-status-processing",
  failed: "border-status-blocked/50 text-status-blocked",
  running: "border-amber-400/50 text-amber-500 dark:text-amber-300",
};

function lastRunSummary(routine: Routine): string {
  const run = routine.last_run;
  if (!run) return "never run";
  const when = shortDuration(Math.max(0, Date.now() - Date.parse(run.started_at)));
  if (run.status === "running") return `running for ${when}`;
  return `${run.status} · ${when} ago`;
}

/** Fleet-health view over all routines (parity with the TUI's `u` view),
 * plus run-now and log viewing on top of the same API. */
export function RoutinesView() {
  const [routines, setRoutines] = useState<Routine[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [log, setLog] = useState<{ title: string; text: string } | null>(null);

  const reload = useCallback(async () => {
    try {
      setRoutines(await api.listRoutines());
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    void reload();
    // Poll while visible so triggered runs show their outcome.
    const id = setInterval(() => void reload(), 5000);
    return () => clearInterval(id);
  }, [reload]);

  async function runNow(name: string) {
    try {
      await api.runRoutine(name);
      store.toast({ title: `Started routine ${name}`, kind: "info" });
      await reload();
    } catch (e) {
      store.toast({
        title: `Failed to start ${name}`,
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    }
  }

  async function viewLog(routine: Routine) {
    const run = routine.last_run;
    if (!run) return;
    try {
      const resp = await api.routineRunLog(routine.name, run.id);
      setLog({
        title: `${routine.name} — run #${run.id} (${run.status})${resp.note ? ` — ${resp.note}` : ""}`,
        text: resp.log || "(empty log)",
      });
    } catch (e) {
      store.toast({
        title: "Failed to load log",
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    }
  }

  return (
    <div className="flex-1 overflow-y-auto px-6 py-5">
      <div className="mb-3 flex items-center gap-2">
        <h2 className="text-[13px] font-semibold uppercase tracking-wider text-muted-foreground">
          Routines
        </h2>
        <span className="text-xs text-muted-foreground">
          unattended agent runs · defined in ~/.config/task/routines
        </span>
        <div className="flex-1" />
        <Button variant="ghost" size="icon" className="size-7" title="Refresh" onClick={() => void reload()}>
          <RefreshCw className="size-4" />
        </Button>
      </div>

      {error && <div className="text-sm text-destructive">{error}</div>}
      {routines && routines.length === 0 && (
        <div className="rounded-lg border px-4 py-8 text-center text-sm text-muted-foreground">
          No routines yet — create one with{" "}
          <code className="kbd">ty routines new &lt;name&gt;</code>
        </div>
      )}

      {routines && routines.length > 0 && (
        <div className="divide-y rounded-lg border">
          {routines.map((routine) => (
            <div key={routine.name} className="flex items-center gap-3 px-3 py-2.5 text-[12.5px]">
              <span className="font-medium">{routine.name}</span>
              {routine.disabled && (
                <Badge variant="outline" className="h-4.5 px-1.5 text-[10px] text-muted-foreground">
                  disabled
                </Badge>
              )}
              {routine.project && (
                <span className="text-[11px] text-muted-foreground">→ {routine.project}</span>
              )}
              <Badge variant="outline" className="h-4.5 px-1.5 text-[10px]">
                {routine.model}
              </Badge>
              {routine.schedule && (
                <span className="flex items-center gap-1 text-[11px] text-muted-foreground">
                  <CalendarClock className="size-3" />
                  {routine.schedule.detail}
                </span>
              )}

              <div className="flex-1" />

              {routine.last_run && (
                <Badge
                  variant="outline"
                  className={`h-4.5 px-1.5 text-[10px] ${STATUS_BADGE[routine.last_run.status] ?? ""}`}
                >
                  {routine.last_run.status}
                </Badge>
              )}
              <span className="w-32 text-right text-[11px] tabular-nums text-muted-foreground">
                {lastRunSummary(routine)}
              </span>
              <Button
                variant="ghost"
                size="icon"
                className="size-6"
                title="View latest run log"
                disabled={!routine.last_run}
                onClick={() => void viewLog(routine)}
              >
                <ScrollText className="size-3.5" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                className="size-6"
                title={routine.disabled ? "Routine is disabled" : "Run now"}
                disabled={routine.disabled || routine.last_run?.status === "running"}
                onClick={() => void runNow(routine.name)}
              >
                <Play className="size-3.5" />
              </Button>
            </div>
          ))}
        </div>
      )}

      <Dialog open={log !== null} onOpenChange={(open) => !open && setLog(null)}>
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle className="text-sm">{log?.title}</DialogTitle>
          </DialogHeader>
          <pre className="max-h-[60vh] select-text overflow-auto rounded-md border bg-surface-2 p-3 font-mono text-[11.5px] leading-relaxed whitespace-pre-wrap">
            {log?.text}
          </pre>
        </DialogContent>
      </Dialog>
    </div>
  );
}
