import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { PlacementHost } from "../api/types";
import { useAppState } from "../store";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export function MachineSettings() {
  const { projects, executors } = useAppState();
  const [project, setProject] = useState(projects[0]?.name ?? "");
  const [executor, setExecutor] = useState("automatic");
  const [hosts, setHosts] = useState<PlacementHost[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const selectedProject = projects.some((p) => p.name === project) ? project : projects[0]?.name ?? "";
  useEffect(() => {
    if (!selectedProject) return;
    let current = true;
    setLoading(true); setError(""); setHosts([]);
    void api.placementHosts(selectedProject, executor === "automatic" ? undefined : executor)
      .then(({ hosts }) => { if (current) setHosts(hosts); })
      .catch((e) => { if (current) setError(String(e)); })
      .finally(() => { if (current) setLoading(false); });
    return () => { current = false; };
  }, [selectedProject, executor, refresh]);
  return <section className="space-y-4">
    <div><h2 className="text-lg font-semibold">Machines</h2>
      <p className="mt-1 text-sm text-muted-foreground">See where tasks can run. Available remote machines depend on the project and executor.</p></div>
    <div className="flex flex-wrap gap-2">
      <Select value={selectedProject} onValueChange={setProject}>
        <SelectTrigger onKeyDown={(event) => event.stopPropagation()} aria-label="Project for machines" className="max-w-full"><SelectValue placeholder="Choose a project" /></SelectTrigger>
        <SelectContent onKeyDown={(event) => event.stopPropagation()}>{projects.map((p) => <SelectItem key={p.name} value={p.name}>{p.name}</SelectItem>)}</SelectContent>
      </Select>
      <Select value={executor} onValueChange={setExecutor}>
        <SelectTrigger onKeyDown={(event) => event.stopPropagation()} aria-label="Executor for machines"><SelectValue /></SelectTrigger>
        <SelectContent onKeyDown={(event) => event.stopPropagation()}><SelectItem value="automatic">Default executor</SelectItem>{executors.map((e) => <SelectItem key={e.name} value={e.name}>{e.name}</SelectItem>)}</SelectContent>
      </Select>
      <Button variant="outline" disabled={loading || !selectedProject} onClick={() => setRefresh((n) => n + 1)}>Refresh</Button>
    </div>
    <div className="rounded-lg border p-4"><h3 className="font-medium">TaskYou server</h3><p className="mt-1 text-sm text-muted-foreground">Local execution on the machine running TaskYou.</p></div>
    {loading && <p role="status" className="text-sm text-muted-foreground">Loading remote machines…</p>}
    {error && <p role="alert" className="text-sm text-destructive">Could not load machines: {error}</p>}
    {!selectedProject && <p className="text-sm text-muted-foreground">Add a project to discover its remote machines.</p>}
    {!loading && !error && selectedProject && hosts.length === 0 && <p className="text-sm text-muted-foreground">No remote machines are offered for this project and executor.</p>}
    {hosts.map((host) => <div key={host.target + host.workdir} className="rounded-lg border p-4">
      <h3 className="font-medium">{host.name}</h3><p className="mt-1 break-all text-sm text-muted-foreground">{host.target} · {host.workdir}</p>
      {host.detail && <p className="mt-1 text-sm text-muted-foreground">{host.detail}</p>}
    </div>)}
    <p className="text-sm text-muted-foreground">Remote machines are configured by your placement integration. Select a machine when creating a task or changing its placement.</p>
  </section>;
}
