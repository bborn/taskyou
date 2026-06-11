import { useState } from "react";
import { CheckCircle2, XCircle, RefreshCw } from "lucide-react";
import type { EnvironmentReport } from "../api/types";
import { checkEnvironment } from "../tauri";
import { Button } from "@/components/ui/button";

function Row({ ok, label, detail }: { ok: boolean; label: string; detail?: string }) {
  return (
    <div className="flex items-center gap-2.5 text-[13px]">
      {ok ? (
        <CheckCircle2 className="size-4 shrink-0 text-status-processing" />
      ) : (
        <XCircle className="size-4 shrink-0 text-status-blocked" />
      )}
      <span className="font-medium">{label}</span>
      {detail && <span className="truncate text-muted-foreground">{detail}</span>}
    </div>
  );
}

function InstallHint({ children }: { children: string }) {
  return (
    <code
      className="block select-text rounded-md border bg-surface-2 px-3 py-2 font-mono text-xs"
      title="Click to copy"
      onClick={() => void navigator.clipboard.writeText(children)}
    >
      {children}
    </code>
  );
}

/** First-run gate: taskyou executes agents inside tmux via executor CLIs;
 * neither can be bundled, so check for them and explain instead of failing
 * mysteriously later. */
export function SetupCheck({
  report: initial,
  onReady,
  onSkip,
}: {
  report: EnvironmentReport;
  onReady: () => void;
  onSkip: () => void;
}) {
  const [report, setReport] = useState(initial);
  const [checking, setChecking] = useState(false);

  const tmuxOk = report.tmux !== null;
  const foundExecutors = report.executors.filter((e) => e.path !== null);
  const executorOk = foundExecutors.length > 0;

  async function recheck() {
    setChecking(true);
    try {
      const fresh = await checkEnvironment();
      setReport(fresh);
      if (fresh.tmux && fresh.executors.some((e) => e.path)) onReady();
    } finally {
      setChecking(false);
    }
  }

  return (
    <div className="flex h-full items-center justify-center bg-background">
      <div className="w-[480px] rounded-xl border bg-card p-6 shadow-lg">
        <h1 className="text-base font-semibold">Almost ready</h1>
        <p className="mt-1 text-[12.5px] text-muted-foreground">
          TaskYou runs AI agents inside tmux. Two tools need to be installed on this machine:
        </p>

        <div className="mt-4 flex flex-col gap-3">
          <Row ok={tmuxOk} label="tmux" detail={report.tmux_version ?? report.tmux ?? "not found"} />
          {!tmuxOk && <InstallHint>brew install tmux</InstallHint>}

          <Row
            ok={executorOk}
            label="An executor CLI"
            detail={
              executorOk
                ? foundExecutors.map((e) => e.name).join(", ")
                : "none found (claude, codex, gemini, …)"
            }
          />
          {!executorOk && <InstallHint>npm install -g @anthropic-ai/claude-code</InstallHint>}
        </div>

        <div className="mt-5 flex items-center justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={onSkip}>
            Continue anyway
          </Button>
          <Button size="sm" disabled={checking} onClick={() => void recheck()}>
            <RefreshCw className={`size-3.5 ${checking ? "animate-spin" : ""}`} />
            Re-check
          </Button>
        </div>
      </div>
    </div>
  );
}
