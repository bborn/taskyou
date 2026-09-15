import { memo, useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from "react";
import { Check, Copy } from "lucide-react";
import type { ChatMessage } from "../api/types";
import { Markdown } from "./Markdown";
import { cn } from "@/lib/utils";

/**
 * Ported from bb's conversation timeline (apps/app/src/components/thread/
 * timeline/ConversationMessageContent.tsx), because approximating it from
 * screenshots kept missing the structure:
 *
 * - the human's turn is a right-aligned bubble, capped at 70% width
 * - the agent's turn is plain full-width prose with a small inset — no bubble,
 *   no border, no name label, no timestamp
 * - long turns are clamped by LINES (max-h-[15lh]) under a mask-image fade,
 *   not by a character count that cuts mid-sentence
 * - actions live in a fixed-height, absolutely-positioned slot so showing them
 *   never shifts the text
 */

/** bb: COLLAPSED_MESSAGE_FADE_STYLE */
const COLLAPSED_FADE: CSSProperties = {
  maskImage: "linear-gradient(to bottom, black calc(100% - 2.5rem), transparent)",
  WebkitMaskImage: "linear-gradient(to bottom, black calc(100% - 2.5rem), transparent)",
};

export function ChatList({
  messages,
  follow = true,
  emptyHint,
}: {
  messages: ChatMessage[];
  follow?: boolean;
  /** Why there is nothing to show, when "hasn't started yet" would be a lie. */
  emptyHint?: string;
}) {
  const endRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (follow) endRef.current?.scrollIntoView({ block: "nearest" });
  }, [messages[messages.length - 1]?.id, follow]);

  if (messages.length === 0) {
    return (
      <div className="px-1 py-6 text-center text-xs text-muted-foreground">
        {emptyHint ?? "No conversation yet. The executor writes its transcript once it starts."}
      </div>
    );
  }

  return (
    // bb's timeline rows sit at gap-2; anything airier reads as dead space
    // between turns once every turn also carries an action slot.
    <div className="flex flex-col gap-2">
      {messages.map((m) => (
        <Turn key={m.id} message={m} />
      ))}
      <div ref={endRef} />
    </div>
  );
}

/** bb: useIsOverflowing — only meaningful while collapsed.
 *
 * Measured through a ResizeObserver rather than once on mount: the clamp is
 * 15 line-heights, so rotating the phone, opening the drawer or a late font
 * load changes whether the text actually overflows, and a one-shot read leaves
 * a "Show more" that expands nothing (or hides text with no way to see it). */
function useIsOverflowing(
  ref: React.RefObject<HTMLDivElement | null>,
  enabled: boolean,
  measurementKey: string,
): boolean {
  const [overflowing, setOverflowing] = useState(false);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || !enabled) {
      setOverflowing(false);
      return;
    }
    const measure = () => setOverflowing(el.scrollHeight > el.clientHeight + 1);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref, enabled, measurementKey]);
  return overflowing;
}

/** Transcripts inline attachments as `[Image: source: /very/long/path.png]`,
 * which renders as a wall of absolute path — one filled a whole bubble. Show
 * the filename; copying still yields the original text. */
function forDisplay(text: string): string {
  return text.replace(/\[Image:\s*source:\s*([^\]]+)\]/gu, (_match, path: string) => {
    const name = path.trim().split("/").pop();
    return name ? `🖼 ${name}` : "🖼 image";
  });
}

function CollapsibleBody({ text }: { text: string }) {
  const [expanded, setExpanded] = useState(false);
  const bodyRef = useRef<HTMLDivElement>(null);
  const display = forDisplay(text);
  const overflowing = useIsOverflowing(bodyRef, !expanded, display);
  const showToggle = expanded || overflowing;

  return (
    <>
      <div
        ref={bodyRef}
        className={cn("break-words", !expanded && "max-h-[15lh] overflow-hidden")}
        style={!expanded && showToggle ? COLLAPSED_FADE : undefined}
      >
        <Markdown source={display} />
      </div>
      {showToggle && (
        <button
          className="mt-1 text-[11px] text-muted-foreground active:text-foreground"
          onClick={() => setExpanded(!expanded)}
        >
          {expanded ? "Show less" : "Show more"}
        </button>
      )}
    </>
  );
}

/** bb: MessageActionBar — a fixed-height slot with the row absolutely placed
 * inside it, so revealing actions never reflows the message. Hover-revealed on
 * a pointer device, always visible on a phone (no hover to reveal with). */
function ActionSlot({ text, align }: { text: string; align: "start" | "end" }) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const id = window.setTimeout(() => setCopied(false), 2000);
    return () => window.clearTimeout(id);
  }, [copied]);

  if (!text) return null;

  return (
    // bb: h-5 on a pointer device, h-7 for a touch target. The slot keeps its
    // height whether or not the button is visible, so revealing it never
    // reflows the message — but it must not reserve a touch-sized row on
    // desktop, which is what made the turns look so far apart.
    <div className={cn("relative h-7 w-full md:h-5", align === "end" && "pr-[11px]")}>
      <div className={cn("absolute top-0 flex items-center gap-2", align === "end" ? "right-[11px]" : "-ml-1.5 left-0")}>
        <button
          type="button"
          aria-label="Copy message"
          onClick={() => {
            void navigator.clipboard
              .writeText(text)
              .then(() => setCopied(true))
              .catch(() => {});
          }}
          className="inline-flex size-7 items-center justify-center text-muted-foreground active:text-foreground md:size-5 md:opacity-0 md:transition-opacity md:group-hover/message:opacity-100"
        >
          {copied ? <Check className="size-4 md:size-3" /> : <Copy className="size-4 md:size-3" />}
        </button>
      </div>
    </div>
  );
}

const Turn = memo(function Turn({ message }: { message: ChatMessage }) {
  const machinery =
    message.tools.length > 0 || message.thinking > 0 ? (
      <div className="text-[11px] text-muted-foreground">
        {message.thinking > 0 && `thought ×${message.thinking}`}
        {message.thinking > 0 && message.tools.length > 0 && " · "}
        {message.tools.length > 0 && summariseTools(message.tools)}
      </div>
    ) : null;

  // bb: the human's turn is a right-aligned bubble capped at 70%.
  if (message.role === "user") {
    return (
      <div className="w-full select-text">
        <div className="group/message ml-auto flex w-fit max-w-[85%] flex-col items-end md:max-w-[70%]">
          <div className="flex w-fit max-w-full flex-col items-end">
            <div className="max-w-full rounded-xl border bg-surface-2 px-4 py-2.5 text-sm leading-relaxed text-foreground">
              {message.text ? (
                <CollapsibleBody text={message.text} />
              ) : (
                <p className="text-muted-foreground">Sent attachments</p>
              )}
            </div>
            <ActionSlot text={message.text} align="end" />
          </div>
        </div>
      </div>
    );
  }

  // bb: the agent's turn is plain prose, full width, with a small inset.
  return (
    <div className="group/message w-full px-2 text-sm leading-relaxed font-normal select-text">
      {message.text ? (
        <CollapsibleBody text={message.text} />
      ) : (
        <span className="text-xs text-muted-foreground italic">
          {message.tools.length > 0 ? "Worked without commentary" : "(empty)"}
        </span>
      )}
      {machinery}
      <ActionSlot text={message.text} align="start" />
    </div>
  );
});

/** ["Read","Read","Bash"] -> "Read ×2 · Bash" */
function summariseTools(tools: string[]): string {
  const counts = new Map<string, number>();
  for (const t of tools) counts.set(t, (counts.get(t) ?? 0) + 1);
  return [...counts.entries()].map(([name, n]) => (n > 1 ? `${name} ×${n}` : name)).join(" · ");
}
