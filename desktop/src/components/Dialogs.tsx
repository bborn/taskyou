import { useState } from "react";
import { store, useAppState } from "../store";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

export function Dialogs() {
  const { dialog } = useAppState();
  if (!dialog) return null;

  switch (dialog.kind) {
    case "confirm":
      return <ConfirmDialog {...dialog} />;
    case "retry":
      return <RetryDialog taskId={dialog.taskId} />;
    case "status":
      return <StatusDialog taskId={dialog.taskId} />;
    case "help":
      return <HelpDialog />;
  }
}

function ConfirmDialog({
  title,
  message,
  danger,
  onConfirm,
}: {
  title: string;
  message: string;
  danger?: boolean;
  onConfirm: () => void;
}) {
  return (
    <AlertDialog open onOpenChange={(open) => !open && store.setDialog(null)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{message}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            className={danger ? "bg-destructive text-white hover:bg-destructive/90" : ""}
            onClick={() => {
              store.setDialog(null);
              onConfirm();
            }}
          >
            Confirm
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function RetryDialog({ taskId }: { taskId: number }) {
  const { tasks } = useAppState();
  const task = tasks.find((t) => t.id === taskId);
  const [feedback, setFeedback] = useState("");
  const [dangerous, setDangerous] = useState(false);

  async function submit() {
    store.setDialog(null);
    if (dangerous) {
      const { api } = await import("../api/client");
      await api.updateTask(taskId, { permission_mode: "dangerous" }).catch(() => {});
    }
    void store.retryTask(taskId, feedback);
  }

  return (
    <Dialog open onOpenChange={(open) => !open && store.setDialog(null)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Reply to #{taskId}
            {task ? ` — ${task.title}` : ""}
          </DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <Textarea
            autoFocus
            rows={5}
            value={feedback}
            placeholder="Answer the executor's question or give new instructions…"
            onChange={(e) => setFeedback(e.target.value)}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === "Enter") void submit();
            }}
          />
          <div className="flex items-center gap-2">
            <Checkbox
              id="retry-dangerous"
              checked={dangerous}
              onCheckedChange={(v) => setDangerous(v === true)}
            />
            <Label htmlFor="retry-dangerous" className="text-xs font-normal">
              Resume in dangerous mode (skip permission prompts)
            </Label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => store.setDialog(null)}>
            Cancel
          </Button>
          <Button onClick={() => void submit()}>
            Send & resume <span className="kbd ml-1">⌘↩</span>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

const STATUSES = ["backlog", "queued", "processing", "blocked", "done", "archived"];

function StatusDialog({ taskId }: { taskId: number }) {
  const { tasks } = useAppState();
  const task = tasks.find((t) => t.id === taskId);
  const [status, setStatus] = useState<string>(task?.status ?? "backlog");

  return (
    <Dialog open onOpenChange={(open) => !open && store.setDialog(null)}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Change status of #{taskId}</DialogTitle>
        </DialogHeader>
        <Select value={status} onValueChange={setStatus}>
          <SelectTrigger className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {STATUSES.map((s) => (
              <SelectItem key={s} value={s}>
                {s}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <DialogFooter>
          <Button variant="outline" onClick={() => store.setDialog(null)}>
            Cancel
          </Button>
          <Button
            onClick={() => {
              store.setDialog(null);
              void store.setTaskStatus(taskId, status);
            }}
          >
            Apply
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

const HELP: [string, string][] = [
  ["↑↓←→", "Navigate board"],
  ["Enter", "Open task"],
  ["n / ⌘N", "New task"],
  ["e", "Edit task"],
  ["x / X", "Execute / execute dangerously"],
  ["r", "Reply to blocked task"],
  ["c", "Close task"],
  ["a / d", "Archive / delete task"],
  ["t", "Pin task"],
  ["S", "Change status"],
  ["/", "Filter board"],
  ["p or f / ⌘P", "Search tasks"],
  ["o", "Open worktree in editor"],
  ["b / G", "Open branch / PR"],
  ["[ / ]", "Collapse Backlog / Done"],
  ["B P L D", "Jump to column"],
  ["g", "Go to last notification"],
  ["!", "Cycle permission mode"],
  ["s", "Settings"],
  ["R", "Refresh"],
  ["Esc", "Back / close"],
];

function HelpDialog() {
  return (
    <Dialog open onOpenChange={(open) => !open && store.setDialog(null)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
        </DialogHeader>
        <div className="grid grid-cols-[auto_1fr_auto_1fr] gap-x-4 gap-y-1.5 text-[12.5px]">
          {HELP.map(([key, desc]) => (
            <KeyRow key={key} k={key} desc={desc} />
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function KeyRow({ k, desc }: { k: string; desc: string }) {
  return (
    <>
      <span className="kbd justify-self-start">{k}</span>
      <span className="text-muted-foreground">{desc}</span>
    </>
  );
}
