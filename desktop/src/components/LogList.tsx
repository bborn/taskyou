import { useEffect, useRef } from "react";
import type { LogLine } from "../api/types";

const TYPE_ICONS: Record<string, string> = {
  system: "⚙",
  text: "💬",
  tool: "🔧",
  error: "✖",
  question: "❓",
  user: "👤",
  output: "›",
};

export function LogList({ logs, follow = true }: { logs: LogLine[]; follow?: boolean }) {
  const endRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (follow) endRef.current?.scrollIntoView({ block: "nearest" });
  }, [logs.length, follow]);

  if (logs.length === 0) {
    return <div className="text-xs text-muted-foreground">No activity yet</div>;
  }

  return (
    <div className="flex flex-col gap-0.5 font-mono text-[11.5px]">
      {logs.map((log) => (
        <div
          key={log.id}
          className={`flex items-baseline gap-2 ${
            log.line_type === "error"
              ? "text-status-blocked"
              : log.line_type === "question"
                ? "text-amber-300"
                : log.line_type === "system"
                  ? "text-muted-foreground"
                  : log.line_type === "tool"
                    ? "text-status-backlog"
                    : "text-foreground/90"
          }`}
        >
          <span className="shrink-0 text-muted-foreground">
            {new Date(log.created_at).toLocaleTimeString([], {
              hour: "2-digit",
              minute: "2-digit",
            })}
          </span>
          <span className="shrink-0">{TYPE_ICONS[log.line_type] ?? "·"}</span>
          <span className="whitespace-pre-wrap break-words">{log.content}</span>
        </div>
      ))}
      <div ref={endRef} />
    </div>
  );
}
