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
    return <div className="empty-hint">No activity yet</div>;
  }

  return (
    <div className="loglist">
      {logs.map((log) => (
        <div key={log.id} className={`logline t-${log.line_type}`}>
          <span className="time">
            {new Date(log.created_at).toLocaleTimeString([], {
              hour: "2-digit",
              minute: "2-digit",
            })}
          </span>
          <span className="time">{TYPE_ICONS[log.line_type] ?? "·"}</span>
          <span className="content">{log.content}</span>
        </div>
      ))}
      <div ref={endRef} />
    </div>
  );
}
