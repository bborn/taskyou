import { memo, useEffect, useRef, useState } from "react";
import { Check, ChevronDown, ChevronRight, Copy } from "lucide-react";
import type { ChatMessage } from "../api/types";
import { Markdown } from "./Markdown";
import { cn } from "@/lib/utils";

/** Only clamp what is genuinely unreadable. The opening turn is the whole
 * ticket — hundreds of lines — but a normal reply is shown in full. */
const CLAMP_CHARS = 1400;

export function ChatList({ messages, follow = true }: { messages: ChatMessage[]; follow?: boolean }) {
  const endRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (follow) endRef.current?.scrollIntoView({ block: "nearest" });
  }, [messages[messages.length - 1]?.id, follow]);

  if (messages.length === 0) {
    return (
      <div className="px-1 py-6 text-center text-xs text-muted-foreground">
        No conversation yet. The executor writes its transcript once it starts.
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-5">
      {messages.map((m) => (
        <Turn key={m.id} message={m} />
      ))}
      <div ref={endRef} />
    </div>
  );
}

/** Cut at a word boundary — slicing mid-word reads like corruption. */
function clamp(text: string): string {
  const cut = text.slice(0, CLAMP_CHARS);
  const lastBreak = cut.lastIndexOf("\n");
  const lastSpace = cut.lastIndexOf(" ");
  const at = lastBreak > CLAMP_CHARS * 0.6 ? lastBreak : lastSpace > 0 ? lastSpace : cut.length;
  return `${cut.slice(0, at)}…`;
}

/**
 * Flowing prose, not chat bubbles.
 *
 * Cards with a name, a timestamp and a button on every turn put more chrome on
 * screen than conversation. bb shows the agent's words plainly and marks the
 * human's with a rule down the side; that reads at arm's length, and it is what
 * this imitates.
 */
const Turn = memo(function Turn({ message }: { message: ChatMessage }) {
  const long = message.text.length > CLAMP_CHARS;
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const mine = message.role === "user";
  const text = long && !expanded ? clamp(message.text) : message.text;

  async function copy() {
    try {
      await navigator.clipboard.writeText(message.text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard needs a secure context; nothing useful to say if it fails
    }
  }

  return (
    <div className={cn("select-text", mine && "border-l-2 border-status-backlog/70 pl-3")}>
      {text ? (
        <div className={cn(mine && "text-muted-foreground")}>
          <Markdown source={text} />
        </div>
      ) : (
        <span className="text-xs text-muted-foreground italic">
          {message.tools.length > 0 ? "Worked without commentary" : "(empty)"}
        </span>
      )}

      {long && (
        <button
          className="mt-1 flex items-center gap-1 text-[11px] text-muted-foreground active:text-foreground"
          onClick={() => setExpanded(!expanded)}
        >
          {expanded ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
          {expanded ? "Show less" : "Show more"}
        </button>
      )}

      {/* One muted line for the machinery and the only action worth a tap.
          Touch has no hover, so the copy affordance is always present but
          deliberately quiet. */}
      <div className="mt-1.5 flex items-center gap-2 text-[11px] text-muted-foreground">
        {(message.tools.length > 0 || message.thinking > 0) && (
          <span className="min-w-0 truncate">
            {message.thinking > 0 && `thought ×${message.thinking}`}
            {message.thinking > 0 && message.tools.length > 0 && " · "}
            {message.tools.length > 0 && summariseTools(message.tools)}
          </span>
        )}
        {message.text !== "" && (
          <button
            onClick={() => void copy()}
            aria-label="Copy message"
            className="ml-auto flex size-6 shrink-0 items-center justify-center rounded opacity-60 active:bg-surface-2 active:opacity-100"
          >
            {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          </button>
        )}
      </div>
    </div>
  );
});

/** ["Read","Read","Bash"] -> "Read ×2 · Bash" */
function summariseTools(tools: string[]): string {
  const counts = new Map<string, number>();
  for (const t of tools) counts.set(t, (counts.get(t) ?? 0) + 1);
  return [...counts.entries()].map(([name, n]) => (n > 1 ? `${name} ×${n}` : name)).join(" · ");
}
