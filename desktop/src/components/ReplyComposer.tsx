import { useLayoutEffect, useRef, useState } from "react";
import { SendHorizonal, Paperclip } from "lucide-react";
import { ApiError, api } from "../api/client";
import { fileToBase64 } from "./AttachmentsPanel";
import type { Attachment, Task } from "../api/types";
import { store } from "../store";
import { useIsCoarsePointer } from "../hooks/use-mobile";
import { Button } from "@/components/ui/button";

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
export function ReplyComposer({ task, onAttach }: { task: Task; onAttach: () => void }) {
  const live = task.status === "processing" || task.status === "blocked";
  const draftKey = `ty-reply-draft-${task.id}`;
  const [message, setMessage] = useState(() => {
    try { return sessionStorage.getItem(draftKey) ?? ""; }
    catch { return ""; }
  });

  const [sending, setSending] = useState(false);
  const filesKey = `ty-reply-files-${task.id}`;
  const [files, setFiles] = useState<Attachment[]>(() => {
    try {
      const saved: unknown = JSON.parse(localStorage.getItem(filesKey) ?? "[]");
      return Array.isArray(saved) ? saved.filter((a): a is Attachment => a && typeof a.id === "number" && typeof a.filename === "string") : [];
    } catch { return []; }
  });
  const [uploading, setUploading] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  function saveFiles(next: Attachment[]) {
    setFiles(next);
    try { localStorage.setItem(filesKey, JSON.stringify(next)); } catch { /* optional persistence */ }
  }
  async function upload(selected: File[]) {
    if (uploading || sending) return;
    setUploading(true);
    let next = [...files];
    for (const file of selected) {
      try {
        if (file.size > 32 * 1024 * 1024) throw new Error("Maximum file size is 32 MB.");
        const a = await api.addAttachment(task.id, file.name, await fileToBase64(file), file.type);
        next = [...next, a];
        saveFiles(next);
      } catch (e) {
        store.toast({ title: `Could not upload ${file.name}`, body: String(e), kind: "error" });
      }
    }
    setUploading(false);
  }
  const attachments = <>
    <input ref={fileRef} type="file" multiple className="hidden" aria-label="Choose attachments"
      onChange={(e) => { const selected = Array.from(e.target.files ?? []); e.target.value = ""; void upload(selected); }} />
    {files.length > 0 && <div className="flex max-h-32 flex-wrap gap-1 overflow-y-auto px-3 py-2" aria-label="Attached files">
      {files.map((file) => <button key={file.id} type="button" className="min-h-11 max-w-full truncate rounded-md border px-2 text-sm" disabled={sending || uploading}
        aria-label={live ? `Remove ${file.filename} from reply` : `Delete ${file.filename}`} onClick={async () => {
          try {
            if (!live) await api.deleteAttachment(file.id);
            saveFiles(files.filter((a) => a.id !== file.id));
          } catch (e) { store.toast({ title: "Could not remove file", body: String(e), kind: "error" }); }
        }}>{file.filename} ×</button>)}
    </div>}
    {uploading && <p role="status" className="px-3 text-sm text-muted-foreground">Uploading files…</p>}
  </>;

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
  // The message the API refused because the agent was mid-turn, held so the
  // user can send it anyway rather than retyping it.
  const [busyText, setBusyText] = useState<string | null>(null);
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

  async function send(text: string, force = false) {
    const body = text.trim();
    if ((!body && files.length === 0) || sending || uploading) return;
    setSending(true);
    try {
      await api.sendInput(task.id, body, force, files.map((a) => a.id));
      saveFiles([]);
      if (text === message) updateMessage("");
      setBusyText(null);
      store.toast({ title: "Sent to the agent", kind: "success" });
      void store.refreshTasks();
    } catch (e) {
      // A busy agent is not a failure: the message is held, and the bar below
      // offers to interrupt. Anything else is worth a toast.
      if (e instanceof ApiError && e.code === "agent_busy") {
        setBusyText(body);
      } else {
        store.toast({
          title: "Could not reach the agent",
          body: e instanceof Error ? e.message : String(e),
          kind: "error",
        });
      }
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
        {attachments}
        <div className="flex gap-2">
        <Button variant="outline" disabled={uploading} className="h-11" onClick={() => fileRef.current?.click()}><Paperclip className="size-4" /> Attach files</Button>
        <Button
          className="h-11 flex-1 text-sm"
          disabled={task.status === "queued" || uploading}
          onClick={() => void store.executeTask(task.id)}
        >
          {task.status === "queued" ? "Queued…" : "Execute"}
        </Button>
        </div>
      </div>
    );
  }

  return (
    <div
      style={liftForKeyboard}
      className="shrink-0 px-3 pt-2 pb-[max(0.625rem,var(--ty-safe-area-bottom,env(safe-area-inset-bottom)))]"
    >
      {busyText !== null && (
        <div className="mb-2 flex items-center gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-[13px]">
          <span className="flex-1 text-muted-foreground">
            The agent is working, so it wasn't interrupted.
          </span>
          <Button
            variant="ghost"
            className="h-8 px-2 text-[13px]"
            disabled={sending}
            onClick={() => void send(busyText, true)}
          >
            Send anyway
          </Button>
          <Button
            variant="ghost"
            className="h-8 px-2 text-[13px] text-muted-foreground"
            onClick={() => setBusyText(null)}
          >
            Wait
          </Button>
        </div>
      )}

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
          onPaste={(e) => {
            if (e.clipboardData.files.length) { e.preventDefault(); void upload(Array.from(e.clipboardData.files)); }
          }}
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
            disabled={sending || uploading || (!message.trim() && files.length === 0)}
            onClick={() => void send(message)}
            aria-label="Send reply"
          >
            <SendHorizonal className="size-4" />
          </Button>
        </div>

        {attachments}
        {/* Controls live inside the card along the bottom edge, where bb keeps
            its attach / model / mic cluster. */}
        <div className="flex items-center gap-1.5 px-2.5 pt-1 pb-2">
          <Button variant="ghost" className="h-11 px-2.5 text-sm text-muted-foreground" disabled={sending || uploading} onClick={() => fileRef.current?.click()}>
            <Paperclip className="size-4" /> Attach files
          </Button>
          <Button variant="ghost" className="h-11 text-sm" onClick={onAttach}>All files</Button>
        </div>
      </div>
    </div>
  );
}
