import { useState } from "react";
import { MessageCircleQuestion } from "lucide-react";
import { ApiError, api } from "../api/client";
import type { Task } from "../api/types";
import type { QuestionOption } from "../lib/question";
import { store } from "../store";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";

/**
 * The answers a blocked task's agent offered with its question, as buttons. A
 * tap sends the label through the same input endpoint as a typed reply, so the
 * agent gets it exactly as if it had been typed. Anything else goes in the
 * reply box as before.
 */
export function QuestionOptions({
  task,
  question,
  options,
  onSent,
  className,
}: {
  task: Task;
  question: string;
  options: QuestionOption[];
  onSent: () => void;
  className?: string;
}) {
  const [sending, setSending] = useState(false);

  async function pick(label: string) {
    if (sending) return;
    setSending(true);
    try {
      await api.sendInput(task.id, label);
      onSent();
      store.toast({ title: "Sent to the agent", body: label, kind: "success" });
      void store.refreshTasks();
    } catch (e) {
      const busy = e instanceof ApiError && e.code === "agent_busy";
      store.toast({
        title: busy ? "The agent is still working" : "Could not reach the agent",
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    } finally {
      setSending(false);
    }
  }

  return (
    <section
      aria-label="Question from the agent"
      className={cn("rounded-xl border border-status-blocked/40 bg-surface-1 p-3", className)}
    >
      <div className="mb-2.5 flex items-start gap-2">
        <MessageCircleQuestion className="mt-0.5 size-4 shrink-0 text-status-blocked" />
        <p className="text-sm leading-snug font-medium whitespace-pre-wrap">{question}</p>
      </div>
      <div className="flex flex-col gap-1.5">
        {options.map((opt) => (
          <Button
            key={opt.label}
            variant="outline"
            disabled={sending}
            onClick={() => void pick(opt.label)}
            className="h-auto min-h-10 justify-start px-3 py-2 text-left whitespace-normal max-md:min-h-11"
          >
            <span className="flex min-w-0 flex-col gap-0.5">
              <span className="text-sm font-medium">{opt.label}</span>
              {opt.description && (
                <span className="text-xs font-normal text-muted-foreground">{opt.description}</span>
              )}
            </span>
          </Button>
        ))}
      </div>
    </section>
  );
}
