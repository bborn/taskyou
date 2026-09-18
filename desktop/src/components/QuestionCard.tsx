import { useRef, useState, type KeyboardEvent } from "react";
import { MessageCircleQuestion, SendHorizonal } from "lucide-react";
import { ApiError, api } from "../api/client";
import type { PendingQuestion, Task } from "../api/types";
import { store } from "../store";
import { buildAnswer, sendsOnTap } from "../lib/question";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";

/**
 * The question a blocked task's agent asked with options (taskyou_needs_input),
 * answered with a tap: an option button sends it, multi_choice builds a
 * selection first, and "Other" is there when the agent allowed its own words.
 *
 * The answer goes to POST /api/tasks/{id}/answer, which delivers it through the
 * same one-paste path as a typed reply. Tap targets are 44px on a phone.
 */
export function QuestionCard({
  task,
  question,
  onAnswered,
  className,
}: {
  task: Task;
  question: PendingQuestion;
  onAnswered: (id: number) => void;
  className?: string;
}) {
  const [picked, setPicked] = useState<number[]>([]);
  const [other, setOther] = useState("");
  const [sending, setSending] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);
  const multi = question.kind === "multi_choice";
  const confirm = question.kind === "confirm";

  async function send(choices: number[], text: string) {
    const built = buildAnswer(question, choices, text);
    if ("error" in built) {
      store.toast({ title: built.error, kind: "error" });
      return;
    }
    if (sending) return;
    setSending(true);
    try {
      const res = await api.answerQuestion(task.id, built.answer);
      onAnswered(question.id);
      store.toast({ title: "Answered", body: res.answer, kind: "success" });
      void store.refreshTasks();
    } catch (e) {
      const code = e instanceof ApiError ? e.code : undefined;
      if (code === "question_changed" || code === "no_question") {
        // Answered elsewhere, or the agent asked again: show what is current.
        onAnswered(question.id);
        store.toast({ title: "That question has moved on", body: "It was answered elsewhere or replaced.", kind: "info" });
        void store.refreshTasks();
      } else {
        store.toast({
          title: code === "agent_busy" ? "The agent is still working" : "Could not send the answer",
          body: e instanceof Error ? e.message : String(e),
          kind: "error",
        });
      }
    } finally {
      setSending(false);
    }
  }

  function toggle(n: number) {
    setPicked((p) => (p.includes(n) ? p.filter((x) => x !== n) : [...p, n]));
  }

  // Arrow keys walk the options, as j/k do in the TUI.
  function onListKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp" && e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    const items = Array.from(listRef.current?.querySelectorAll<HTMLElement>("[data-option]") ?? []);
    const at = items.indexOf(document.activeElement as HTMLElement);
    if (at < 0 || items.length === 0) return;
    e.preventDefault();
    const step = e.key === "ArrowDown" || e.key === "ArrowRight" ? 1 : -1;
    items[(at + step + items.length) % items.length]?.focus();
  }

  return (
    <section
      aria-label="Question from the agent"
      className={cn("rounded-xl border border-status-blocked/40 bg-surface-1 p-3 shadow-sm", className)}
    >
      <div className="mb-2.5 flex items-start gap-2">
        <MessageCircleQuestion className="mt-0.5 size-4 shrink-0 text-status-blocked" />
        <p className="text-sm leading-snug font-medium whitespace-pre-wrap">{question.question}</p>
      </div>

      <div
        ref={listRef}
        role="group"
        aria-label="Answers"
        onKeyDown={onListKeyDown}
        className={cn("flex gap-1.5", confirm ? "flex-row" : "flex-col")}
      >
        {question.options.map((opt, i) => {
          const n = i + 1;
          if (multi) {
            const on = picked.includes(n);
            return (
              <label
                key={n}
                className={cn(
                  "flex min-h-10 cursor-pointer items-start gap-2.5 rounded-lg border px-3 py-2 text-left max-md:min-h-11",
                  on ? "border-primary/60 bg-primary/10" : "hover:bg-accent",
                )}
              >
                <Checkbox
                  data-option
                  checked={on}
                  disabled={sending}
                  onCheckedChange={() => toggle(n)}
                  className="mt-0.5"
                  aria-label={opt.label}
                />
                <OptionText n={n} label={opt.label} description={opt.description} />
              </label>
            );
          }
          return (
            <Button
              key={n}
              data-option
              variant={confirm && n === 1 ? "default" : "outline"}
              disabled={sending}
              onClick={() => sendsOnTap(question) && void send([n], "")}
              className={cn(
                "h-auto min-h-10 whitespace-normal max-md:min-h-11",
                confirm ? "flex-1" : "justify-start px-3 py-2 text-left",
              )}
            >
              {confirm ? opt.label : <OptionText n={n} label={opt.label} description={opt.description} />}
            </Button>
          );
        })}
      </div>

      {(multi || question.allow_other) && (
        <form
          className="mt-2.5 flex items-center gap-1.5"
          onSubmit={(e) => {
            e.preventDefault();
            void send(multi ? picked : [], other);
          }}
        >
          {question.allow_other && (
            <Input
              value={other}
              disabled={sending}
              onChange={(e) => setOther(e.target.value)}
              placeholder={multi ? "Anything else? (optional)" : "Or answer in your own words…"}
              aria-label="Answer in your own words"
              className="h-10 flex-1 max-md:h-11 max-md:text-base"
            />
          )}
          <Button
            type="submit"
            disabled={sending || (multi ? picked.length === 0 && !other.trim() : !other.trim())}
            className={cn("h-10 max-md:h-11", !question.allow_other && "flex-1")}
          >
            <SendHorizonal className="size-4" />
            {multi && picked.length > 0 ? `Send ${picked.length}` : "Send"}
          </Button>
        </form>
      )}
    </section>
  );
}

function OptionText({ n, label, description }: { n: number; label: string; description?: string }) {
  return (
    <span className="flex min-w-0 flex-col gap-0.5">
      <span className="text-sm font-medium">
        {/* The number mirrors the TUI's keys; a screen reader wants the label. */}
        <span aria-hidden className="mr-1.5 font-mono text-xs text-muted-foreground">{n}</span>
        {label}
      </span>
      {description && <span className="text-xs font-normal text-muted-foreground">{description}</span>}
    </span>
  );
}
