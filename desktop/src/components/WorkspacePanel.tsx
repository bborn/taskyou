import { useEffect, useRef, useState } from "react";
import { FileText, Folder, GitPullRequest, Plus, RefreshCw, TerminalSquare, X, ExternalLink, Search } from "lucide-react";
import { api } from "../api/client";
import type { PanelContent, PanelInstance, PanelProvider, Task } from "../api/types";
import { openExternal } from "../tauri";
import { Markdown } from "./Markdown";
import { PaneMirror } from "./PaneMirror";
import { Button } from "./ui/button";

// Shared content kinds, also covered by the provider parity test.
export const PANEL_RENDERERS = ["shell", "markdown", "text", "files"];
const icons: Record<string, typeof FileText> = { shell: TerminalSquare, pr: GitPullRequest, files: Folder, file: FileText };

export function WorkspacePanel({ task }: { task: Task }) {
  const key = `ty-workspace-selected-${task.id}`;
  const [tabs, setTabs] = useState<PanelInstance[]>([]);
  const [providers, setProviders] = useState<PanelProvider[]>([]);
  const [active, setActive] = useState<string | null>(() => localStorage.getItem(key));
  const [launcher, setLauncher] = useState(false);
  const [query, setQuery] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    let stopped = false;
    const load = async () => {
      try {
        const [next, available] = await Promise.all([api.panels(task.id), api.panelProviders(task.id)]);
        if (stopped) return;
        setTabs(next); setProviders(available);
        setActive((id) => next.some((tab) => tab.id === id) ? id : next[0]?.id ?? null);
      } catch (e) { if (!stopped) setError(String(e)); }
    };
    void load();
    const timer = setInterval(() => void load(), 3000);
    return () => { stopped = true; alive.current = false; clearInterval(timer); };
  }, [task.id]);
  useEffect(() => { if (active) localStorage.setItem(key, active); else localStorage.removeItem(key); }, [key, active]);
  const selected = tabs.find((tab) => tab.id === active);
  async function open(provider: string, resource = "") {
    setBusy(true); setError("");
    try {
      const tab = await api.openPanel(task.id, provider, resource);
      if (!alive.current) return;
      setTabs((items) => items.some((item) => item.id === tab.id) ? items : [...items, tab]);
      setActive(tab.id); setLauncher(false); setQuery("");
    } catch (e) { if (alive.current) setError(e instanceof Error ? e.message : String(e)); }
    finally { if (alive.current) setBusy(false); }
  }
  async function close(tab: PanelInstance) {
    try {
      await api.closePanel(task.id, tab.id);
      if (!alive.current) return;
      setTabs((items) => items.filter((item) => item.id !== tab.id));
      if (active === tab.id) setActive(tabs.find((item) => item.id !== tab.id)?.id ?? null);
    } catch (e) { if (alive.current) setError(String(e)); }
  }
  return <section aria-label="Workspace" className="flex h-full min-h-0 min-w-0 flex-col bg-surface-1" onKeyDown={(e) => {
    if (!e.altKey) return;
    if (e.key === "t") { e.preventDefault(); setLauncher(true); }
    if (e.key === "ArrowRight" || e.key === "ArrowLeft") {
      e.preventDefault(); const index = tabs.findIndex((tab) => tab.id === active);
      setActive(tabs[(index + (e.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length]?.id ?? null); setLauncher(false);
    }
  }}>
    <div className="flex shrink-0 items-center border-b bg-surface-2/40">
      <div role="tablist" aria-label="Workspace tabs" className="flex min-w-0 flex-1 overflow-x-auto">
        {tabs.map((tab) => {
          const Icon = icons[tab.provider_id] ?? FileText;
          return <div key={tab.id} className={`flex shrink-0 items-center border-r ${active === tab.id && !launcher ? "bg-surface-1 text-foreground" : "text-muted-foreground"}`}>
            <button role="tab" aria-selected={active === tab.id && !launcher} className="flex max-w-48 items-center gap-2 px-3 py-3 text-xs" onClick={() => { setActive(tab.id); setLauncher(false); }}>
              <Icon className="size-3.5 shrink-0" /><span className="truncate">{tab.title}</span>
            </button>
            <button aria-label={`Close ${tab.title}`} title="Close tab (keeps sessions running)" className="mr-2 rounded p-1 hover:bg-muted" onClick={() => void close(tab)}><X className="size-3" /></button>
          </div>;
        })}
      </div>
      <Button variant="ghost" size="icon" title="New workspace tab (Alt+T)" aria-label="New workspace tab" onClick={() => { setLauncher(true); setQuery(""); }}><Plus className="size-4" /></Button>
    </div>
    {error && <p role="alert" className="px-4 py-2 text-xs text-destructive">{error}</p>}
    {launcher || !selected ? <div className="overflow-auto p-5">
      <h2 className="mb-1 text-lg font-semibold">Your workspace</h2>
      <p className="mb-5 text-xs text-muted-foreground">Keep tools and files beside the conversation.</p>
      <label className="mb-5 flex items-center gap-2 rounded-md border bg-background px-3 py-2"><Search className="size-4 text-muted-foreground" /><input autoFocus aria-label="Search workspace actions or enter a file path" className="w-full bg-transparent text-sm outline-none" placeholder="Find a tool or enter a file path…" value={query} onChange={(e) => setQuery(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter" && query.trim()) void open("file", query.trim()); }} /></label>
      <h3 className="mb-2 text-xs text-muted-foreground">Actions</h3>
      {providers.filter((p) => p.id !== "file" && (!query || p.title.toLowerCase().includes(query.toLowerCase()))).map((provider) => {
        const Icon = icons[provider.id] ?? FileText;
        return <button key={provider.id} disabled={busy} className="flex w-full items-center gap-3 rounded-md p-3 text-left text-sm hover:bg-muted disabled:opacity-50" onClick={() => void open(provider.id)}><Icon className="size-5 text-muted-foreground" />{provider.title}</button>;
      })}
      {query.trim() && <button disabled={busy} className="mt-3 flex w-full items-center gap-3 rounded border p-3 text-left text-sm" onClick={() => void open("file", query.trim())}><FileText className="size-4" />Open {query}</button>}
    </div> : <div role="tabpanel" aria-label={selected.title} className="flex min-h-0 flex-1 flex-col">
      {selected.provider_id === "shell" ? <PaneMirror task={task} pane="shell" /> : <ResourceView key={selected.id} task={task} tab={selected} open={open} />}
    </div>}
  </section>;
}

function ResourceView({ task, tab, open }: { task: Task; tab: PanelInstance; open: (provider: string, resource?: string) => Promise<void> }) {
  const [content, setContent] = useState<PanelContent | null>(null);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  const prRevision = JSON.stringify(task.pr);
  useEffect(() => {
    let stopped = false;
    setContent(null); setError("");
    api.panelContent(task.id, tab.id).then((data) => { if (!stopped) setContent(data); }).catch((e) => { if (!stopped) setError(e instanceof Error ? e.message : String(e)); });
    return () => { stopped = true; };
  }, [task.id, tab.id, revision, prRevision]);
  const url = content?.url && /^https?:\/\//i.test(content.url) ? content.url : null;
  return <>
    <div className="flex shrink-0 items-center gap-2 border-b px-4 py-2 text-xs text-muted-foreground"><span className="min-w-0 flex-1 truncate">{tab.resource === "." ? "Workspace files" : tab.resource || tab.title}</span><Button variant="ghost" size="icon" className="size-6" aria-label="Refresh panel" onClick={() => setRevision((n) => n + 1)}><RefreshCw className="size-3.5" /></Button>{url && <Button variant="ghost" size="sm" onClick={() => void openExternal(url)}>Open <ExternalLink className="size-3" /></Button>}</div>
    <div className="min-h-0 flex-1 overflow-auto p-4">
      {error ? <p role="alert" className="text-sm text-muted-foreground">{error}</p> : !content ? <p role="status" className="text-sm text-muted-foreground">Loading…</p> : content.kind === "markdown" ? <Markdown source={content.text ?? ""} /> : content.kind === "text" ? <pre className="text-xs leading-relaxed">{content.text}</pre> : content.kind === "files" ? <>
        {tab.resource !== "." && <button className="mb-2 text-xs text-muted-foreground" onClick={() => void open("files", tab.resource.split("/").slice(0, -1).join("/") || ".")}>← Parent directory</button>}
        {!content.entries?.length && <p className="text-sm text-muted-foreground">This directory is empty.</p>}
        {content.entries?.map((entry) => <button key={entry.path} className="flex w-full items-center gap-2 rounded px-2 py-2 text-left text-sm hover:bg-muted" onClick={() => void open(entry.directory ? "files" : "file", entry.path)}>{entry.directory ? <Folder className="size-4 text-muted-foreground" /> : <FileText className="size-4 text-muted-foreground" />}<span className="truncate">{entry.name}</span></button>)}
      </> : <p>Unsupported panel content: {content.kind}</p>}
    </div>
  </>;
}
