import { useState } from "react";
import { SendHorizonal } from "lucide-react";
import { api } from "../api/client";
import type { Task } from "../api/types";
import { store } from "../store";
import { useIsCoarsePointer } from "../hooks/use-mobile";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";

/** One-tap answers for the two things an agent asks for most often. */
const QUICK = ["yes", "continue"];

/** The phone's stand-in for the terminal: types straight into the agent's
 * session via POST /api/tasks/{id}/input. xterm needs a keyboard and ~80
 * columns; from a phone the thing worth doing is answering the agent. */
export function ReplyComposer({ task }: { task: Task }) {
  const [message, setMessage] = useState("");
  const [sending, setSending] = useState(false);
  const touch = useIsCoarsePointer();

  const live = task.status === "processing" || task.status === "blocked";

  async function send(text: string) {
    const body = text.trim();
    if (!body || sending) return;
    setSending(true);
    try {
      await api.sendInput(task.id, body);
      setMessage("");
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

  // On iOS the soft keyboard doesn't resize the layout viewport, so a bottom
  // anchored bar would sit underneath it. useKeyboardInset measures it.
  const liftForKeyboard = { transform: "translateY(calc(-1 * var(--ty-keyboard-inset, 0px)))" };

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
      className="shrink-0 border-t bg-surface-1 px-3 pt-2.5 pb-[max(0.625rem,var(--ty-safe-area-bottom,env(safe-area-inset-bottom)))]"
    >
      <div className="mb-2 flex gap-1.5">
        {QUICK.map((word) => (
          <Button
            key={word}
            variant="outline"
            className="h-9 px-3.5 text-[13px]"
            disabled={sending}
            onClick={() => void send(word)}
          >
            {word}
          </Button>
        ))}
        <Button
          variant="outline"
          className="ml-auto h-9 px-3.5 text-[13px]"
          disabled={sending}
          onClick={() => store.setDialog({ kind: "retry", taskId: task.id })}
        >
          Retry…
        </Button>
      </div>
      <div className="flex items-end gap-2">
        {/* 16px text: iOS Safari zooms the page when focusing anything smaller. */}
        <Textarea
          rows={1}
          value={message}
          disabled={sending}
          placeholder="Reply to the agent…"
          enterKeyHint={touch ? "enter" : "send"}
          className="max-h-40 min-h-11 resize-none text-base md:text-base"
          onChange={(e) => setMessage(e.target.value)}
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
        <Button
          size="icon"
          className="size-11 shrink-0"
          disabled={sending || !message.trim()}
          onClick={() => void send(message)}
          aria-label="Send reply"
        >
          <SendHorizonal className="size-4" />
        </Button>
      </div>
    </div>
  );
}
