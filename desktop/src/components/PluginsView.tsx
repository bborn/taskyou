import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Check,
  Download,
  ExternalLink,
  Loader2,
  RefreshCw,
  Search,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import { api } from "../api/client";
import type { CatalogPlugin } from "../api/types";
import { store } from "../store";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

type Scope = "all" | "installed" | "available";

const SCOPES: { key: Scope; label: string }[] = [
  { key: "all", label: "All" },
  { key: "installed", label: "Installed" },
  { key: "available", label: "Available" },
];

/**
 * The plugin browser: search the catalog, read what a plugin does, install or
 * remove it in one click. Parity with the TUI's `m` view and `ty plugins`, over
 * the same /api/plugins endpoints — nobody should have to discover that a repo
 * of plugins exists and then go to a terminal to get one.
 */
export function PluginsView() {
  const [query, setQuery] = useState("");
  const [scope, setScope] = useState<Scope>("all");
  const [plugins, setPlugins] = useState<CatalogPlugin[] | null>(null);
  const [stale, setStale] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The id currently installing or being removed, so its own button spins and a
  // second click can't start the same clone twice.
  const [busy, setBusy] = useState<string | null>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  const load = useCallback(
    async (opts: { refresh?: boolean } = {}) => {
      try {
        const resp = await api.pluginCatalog({ q: query, scope, refresh: opts.refresh });
        setPlugins(resp.plugins);
        setStale(resp.stale);
        setError(null);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [query, scope],
  );

  // Debounced so typing doesn't fire a request per keystroke.
  useEffect(() => {
    const id = setTimeout(() => void load(), plugins === null ? 0 : 150);
    return () => clearTimeout(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load]);

  useEffect(() => {
    searchRef.current?.focus();
  }, []);

  async function install(plugin: CatalogPlugin) {
    setBusy(plugin.id);
    try {
      const resp = await api.installPlugin({ id: plugin.id });
      if (!resp.ok) {
        store.toast({ title: `Could not install ${plugin.id}`, body: resp.error, kind: "error" });
        return;
      }
      store.toast({
        title: `${resp.updated ? "Updated" : "Installed"} ${(resp.plugins ?? [plugin.id]).join(", ")}`,
        kind: "success",
      });
      await load();
    } catch (e) {
      store.toast({
        title: `Could not install ${plugin.id}`,
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    } finally {
      setBusy(null);
    }
  }

  async function remove(plugin: CatalogPlugin) {
    store.setDialog({
      kind: "confirm",
      title: `Remove ${plugin.name}?`,
      message: "Its directory and everything in it will be deleted.",
      danger: true,
      onConfirm: () => void (async () => {
        setBusy(plugin.id);
        try {
          const resp = await api.removePlugin(plugin.id);
          if (!resp.ok) {
            store.toast({ title: `Could not remove ${plugin.id}`, body: resp.error, kind: "error" });
            return;
          }
          store.toast({ title: `Removed ${plugin.id}`, kind: "info" });
          await load();
        } catch (e) {
          store.toast({
            title: `Could not remove ${plugin.id}`,
            body: e instanceof Error ? e.message : String(e),
            kind: "error",
          });
        } finally {
          setBusy(null);
        }
      })(),
    });
  }

  const counts = useMemo(() => {
    const installed = (plugins ?? []).filter((p) => p.installed).length;
    return { total: plugins?.length ?? 0, installed };
  }, [plugins]);

  return (
    <div className="flex-1 overflow-y-auto px-4 py-5 md:px-6">
      <div className="mb-3 flex items-center gap-2">
        <h2 className="text-[13px] font-semibold uppercase tracking-wider text-muted-foreground">
          Plugins
        </h2>
        <span className="hidden text-xs text-muted-foreground md:inline">
          workflows, hooks, actions and services · {counts.installed} of {counts.total} installed
        </span>
        <div className="flex-1" />
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          title="Re-fetch the catalog"
          onClick={() => void load({ refresh: true })}
        >
          <RefreshCw className="size-4" />
        </Button>
      </div>

      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div className="relative min-w-[14rem] flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            ref={searchRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search plugins — name, what it does, a tag…"
            className="h-8 pl-8 text-[13px]"
          />
        </div>
        <div className="flex items-center gap-1">
          {SCOPES.map((s) => (
            <Button
              key={s.key}
              variant={scope === s.key ? "secondary" : "ghost"}
              size="sm"
              className="h-8 px-2.5 text-xs"
              onClick={() => setScope(s.key)}
            >
              {s.label}
            </Button>
          ))}
        </div>
      </div>

      {stale && (
        <div className="mb-3 flex items-center gap-1.5 text-xs text-muted-foreground">
          <TriangleAlert className="size-3.5" />
          Showing the catalog shipped with ty — refresh to fetch the latest.
        </div>
      )}
      {error && <div className="text-sm text-destructive">{error}</div>}

      {plugins && plugins.length === 0 && (
        <div className="rounded-lg border px-4 py-8 text-center text-sm text-muted-foreground">
          {query
            ? `Nothing matches “${query}”.`
            : "No plugins to show in this view."}
        </div>
      )}

      {plugins && plugins.length > 0 && (
        <div className="grid gap-2.5 md:grid-cols-2">
          {plugins.map((plugin) => (
            <PluginCard
              key={`${plugin.id}:${plugin.in_catalog}`}
              plugin={plugin}
              busy={busy === plugin.id}
              onInstall={() => void install(plugin)}
              onRemove={() => void remove(plugin)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function PluginCard({
  plugin,
  busy,
  onInstall,
  onRemove,
}: {
  plugin: CatalogPlugin;
  busy: boolean;
  onInstall: () => void;
  onRemove: () => void;
}) {
  return (
    <div className="flex flex-col gap-2 rounded-lg border p-3">
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-[13px] font-medium">{plugin.name}</span>
            {plugin.installed && (
              <Badge variant="outline" className="h-4.5 gap-1 px-1.5 text-[10px] text-status-processing">
                <Check className="size-2.5" /> installed
              </Badge>
            )}
            {plugin.category && (
              <Badge variant="outline" className="h-4.5 px-1.5 text-[10px] text-muted-foreground">
                {plugin.category}
              </Badge>
            )}
          </div>
          <code className="text-[11px] text-muted-foreground">{plugin.id}</code>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {plugin.homepage && (
            <Button
              variant="ghost"
              size="icon"
              className="size-7"
              title="Open the plugin's page"
              asChild
            >
              <a href={plugin.homepage} target="_blank" rel="noreferrer">
                <ExternalLink className="size-3.5" />
              </a>
            </Button>
          )}
          {plugin.installed && (
            <Button
              variant="ghost"
              size="icon"
              className="size-7 text-muted-foreground hover:text-destructive"
              title="Remove"
              disabled={busy}
              onClick={onRemove}
            >
              <Trash2 className="size-3.5" />
            </Button>
          )}
          {/* An uncatalogued local plugin has no source to reinstall from, so it
              gets no install button — only removal. */}
          {plugin.in_catalog && (
            <Button
              variant={plugin.installed ? "ghost" : "secondary"}
              size="sm"
              className="h-7 px-2 text-xs"
              disabled={busy}
              onClick={onInstall}
            >
              {busy ? (
                <Loader2 className="size-3.5 animate-spin" />
              ) : (
                <Download className="size-3.5" />
              )}
              {plugin.installed ? "Update" : "Install"}
            </Button>
          )}
        </div>
      </div>

      <p className="text-[12.5px] leading-snug text-muted-foreground">{plugin.description}</p>

      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted-foreground">
        {(plugin.provides ?? []).map((p) => (
          <Badge key={p} variant="ghost" className="h-4 px-1 text-[10px]">
            {p}
          </Badge>
        ))}
        {plugin.author && <span>by {plugin.author}</span>}
        {(plugin.tags ?? []).length > 0 && <span>{(plugin.tags ?? []).join(" · ")}</span>}
      </div>

      {(plugin.requires ?? []).length > 0 && (
        <div className="flex items-start gap-1.5 text-[11px] text-amber-600 dark:text-amber-400">
          <TriangleAlert className="mt-0.5 size-3 shrink-0" />
          <span>needs {(plugin.requires ?? []).join("; ")}</span>
        </div>
      )}
    </div>
  );
}
