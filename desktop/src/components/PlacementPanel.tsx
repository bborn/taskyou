import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { Placement } from "../api/types";
import { store } from "../store";
import { Button } from "./ui/button";
import { Input } from "./ui/input";

export function PlacementPanel({ taskId }: { taskId: number }) {
  const [placement, setPlacement] = useState<Placement | null>(null);
  const [editing, setEditing] = useState(false);
  const [target, setTarget] = useState("local");
  const [workdir, setWorkdir] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    const refresh = () => api.placement(taskId).then((p) => {
      if (active) setPlacement(p);
    }).catch((e: Error) => { if (active) setError(e.message); });
    void refresh();
    const timer = setInterval(() => void refresh(), 5000);
    return () => { active = false; clearInterval(timer); };
  }, [taskId]);

  async function move(e: React.FormEvent) {
    e.preventDefault(); setBusy(true); setError(""); setMessage("");
    try {
      const result = await api.placeTask(taskId, target.trim(), workdir.trim());
      setMessage(result.messages.join("\n"));
      setPlacement(await api.placement(taskId));
      setEditing(false);
      void store.refreshTasks();
    } catch (e) { setError(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(false); }
  }
  return (
    <section aria-label="Task placement" className="mt-4 rounded-md border bg-surface-1 p-3 text-xs">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h3 className="font-semibold">Runs on {placement?.target || "this machine"}</h3>
          <p className="mt-1 text-muted-foreground">{placement?.health.state || "Loading placement…"}{placement && !placement.decided ? " · not yet placed" : ""}</p>
        </div>
        <Button size="sm" variant="outline" disabled={busy} onClick={() => {
          setTarget(placement?.target || "local"); setWorkdir(placement?.workdir || "");
          setEditing(!editing); setMessage(""); setError("");
        }}>{editing ? "Cancel" : "Change host"}</Button>
      </div>
      {placement?.reason && <p className="mt-2 text-muted-foreground">{placement.reason}</p>}
      {(placement?.remote_worktree || placement?.workdir) && <p className="mt-1 break-all font-mono text-muted-foreground">{placement.remote_worktree || placement.workdir}</p>}
      {placement?.health.last_seen && <p className="mt-1 text-muted-foreground">Last observed {new Date(placement.health.last_seen).toLocaleString()}</p>}
      {placement?.health.problem && <p className="mt-1 text-amber-400">{placement.health.problem}</p>}
      {editing && <form onSubmit={(e) => void move(e)} className="mt-3 space-y-3">
        <label className="block">SSH destination or local<Input aria-label="SSH destination or local" className="mt-1" value={target} onChange={(e) => setTarget(e.target.value)} required disabled={busy} /></label>
        <label className="block">Remote checkout directory<Input aria-label="Remote checkout directory" className="mt-1" value={workdir} onChange={(e) => setWorkdir(e.target.value)} placeholder="~/projects/app" disabled={busy} /></label>
        <p className="text-muted-foreground">Moving carries code through Git and asks for a handoff. Git-ignored files stay on the source machine. The next run starts at the destination.</p>
        <Button type="submit" size="sm" disabled={busy}>{busy ? "Moving task…" : "Move task"}</Button>
      </form>}
      {error && <p role="alert" className="mt-2 whitespace-pre-wrap text-red-400">{error}</p>}
      {message && <p role="status" className="mt-2 whitespace-pre-wrap text-muted-foreground">{message}</p>}
    </section>
  );
}
