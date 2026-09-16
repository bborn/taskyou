import { useLayoutEffect, useRef, useState } from "react";
import { SendHorizonal } from "lucide-react";
import { api } from "../api/client";
import type { Task } from "../api/types";
import { store } from "../store";
import { useIsCoarsePointer } from "../hooks/use-mobile";
import { Button } from "@/components/ui/button";

/** One-tap answers for the two things an agent asks for most often. */
const QUICK = ["yes", "continue"];

/**
 * Shell ported from bb's promptbox (apps/app/src/components/promptbox/
 * PromptBoxInternal.tsx + ComposerEditorSlot.tsx): one bordered rounded card
 * that owns the input AND its controls, rather than a row of buttons floating
 * above a bare textarea.
 *
 * bb's editor is TipTap and this is a plain textarea, so what is copied is the
 * shell and its geometry: the card, the input region's padding with pr-14
 * reserved for an overlaid send button, the 68px floor and 50dvh ceiling, and
 * the controls sitting inside the card along the bottom edge.
 */
export function ReplyComposer({ task }: { task: Task }) {
  const draftKey = `ty-reply-draft-${task.id}`;
  const [message, setMessage] = useState(() => {
    try { return sessionStorage.getItem(draftKey) ?? ""; }
    catch { return ""; }
  });

  // Write on each edit, rather than on unmount: mobile tabs can be discarded
  // without running cleanup. A successful send (or clearing the field) removes it.
  function updateMessage(value: string) {
    setMessage(value);
    try {
      if (value) sessionStorage.setItem(draftKey, value);
      else sessionStorage.removeItem(draftKey);
    } catch {
      // Storage may be unavailable; composing and sending must still work.
    }
  }
  const [sending, setSending] = useState(false);
  const touch = useIsCoarsePointer();
  const inputRef = useRef<HTMLTextAreaElement>(null);

  // Grow with the text, the way bb's editor does, instead of scrolling a fixed
  // region — on a phone a scrolled box hides the start of what you just typed.
  // Height is driven imperatively so the CSS floor (68px) and ceiling
  // (50dvh - 3rem) still clamp it, with overflow taking over past the ceiling.
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${el.scrollHeight}px`;
  }, [message]);

  const live = task.status === "processing" || task.status === "blocked";

  async function send(text: string) {
    const body = text.trim();
    if (!body || sending) return;
    setSending(true);
    try {
      await api.sendInput(task.id, body);
      // A quick answer must not discard a different reply being composed.
      if (text === message) updateMessage("");
      store.toast({ title: "Sent to the agent", kind: "success" });
      void store.refreshTasks();
    } catch (e) {
      store.toast({
        title: "Could not reach the agent",
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    } finally {
      setSending(false);
    }
  }

  // No lift here any more: the shell itself is resized to the visual viewport
  // while the keyboard is up, so this bar is already inside a box that ends
  // above the keys. Translating it too would double-count the inset.
  const liftForKeyboard = undefined;

  if (!live) {
    return (
      <div
        style={liftForKeyboard}
        className="shrink-0 border-t bg-surface-1 px-3 pt-3 pb-[max(0.75rem,var(--ty-safe-area-bottom,env(safe-area-inset-bottom)))]"
      >
        <Button
          className="h-11 w-full text-sm"
          disabled={task.status === "queued"}
          onClick={() => void store.executeTask(task.id)}
        >
          {task.status === "queued" ? "Queued…" : "Execute"}
        </Button>
      </div>
    );
  }

  return (
    <div
      style={liftForKeyboard}
      className="shrink-0 px-3 pt-2 pb-[max(0.625rem,var(--ty-safe-area-bottom,env(safe-area-inset-bottom)))]"
    >
      {/* bb: group/promptbox relative w-full rounded-xl border bg-background */}
      <div className="relative w-full rounded-xl border bg-background shadow-sm">
        {/* bb's editor scroll region: pr-14 reserves the send button's column
            so long text never runs under it. */}
        <textarea
          ref={inputRef}
          value={message}
          disabled={sending}
          placeholder="Reply to the agent…"
          enterKeyHint={touch ? "enter" : "send"}
          style={{ minHeight: "68px", maxHeight: "calc(50dvh - 3rem)" }}
          className="w-full resize-none overflow-y-auto bg-transparent px-4 pt-3 pr-14 pb-1 text-sm leading-relaxed outline-none max-md:text-base"
          onChange={(e) => updateMessage(e.target.value)}
          onKeyDown={(e) => {
            // A touch keyboard has no usable Shift+Enter, so Enter-to-send
            // would make multi-line replies impossible. There, Return inserts
            // a newline and the send button is the only way to send.
            if (touch) return;
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              void send(message);
            }
          }}
        />

        {/* bb: absolute right-[13px] top-2 z-20 flex items-center */}
        <div className="absolute top-2 right-[13px] z-20 flex items-center">
          <Button
            size="icon"
            className="size-9 max-md:size-10"
            disabled={sending || !message.trim()}
            onClick={() => void send(message)}
            aria-label="Send reply"
          >
            <SendHorizonal className="size-4" />
          </Button>
        </div>

        {/* Controls live inside the card along the bottom edge, where bb keeps
            its attach / model / mic cluster. */}
        <div className="flex items-center gap-1.5 px-2.5 pt-1 pb-2">
          {QUICK.map((word) => (
            <Button
              key={word}
              variant="ghost"
              className="h-8 px-2 text-[13px] text-muted-foreground max-md:h-10 max-md:px-2.5"
              disabled={sending}
              onClick={() => void send(word)}
            >
              {word}
            </Button>
          ))}
          <Button
            variant="ghost"
            className="ml-auto h-8 px-2 text-[13px] text-muted-foreground max-md:h-10 max-md:px-2.5"
            disabled={sending}
            onClick={() => store.setDialog({ kind: "retry", taskId: task.id })}
          >
            Retry…
          </Button>
        </div>
      </div>
    </div>
  );
}
