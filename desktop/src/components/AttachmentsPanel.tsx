import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import type { Attachment } from "../api/types";
import { openExternal } from "../tauri";
import { store } from "../store";
import { Button } from "@/components/ui/button";

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

async function fileToBase64(file: File): Promise<string> {
  const buf = await file.arrayBuffer();
  const bytes = new Uint8Array(buf);
  let bin = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    bin += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(bin);
}

export function AttachmentsPanel({ taskId }: { taskId: number }) {
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [uploading, setUploading] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const reload = useCallback(async () => {
    try {
      setAttachments(await api.listAttachments(taskId));
    } catch {
      // panel is non-critical
    }
  }, [taskId]);

  useEffect(() => {
    void reload();
  }, [reload]);

  async function upload(files: FileList | File[]) {
    setUploading(true);
    try {
      for (const file of Array.from(files)) {
        const data = await fileToBase64(file);
        await api.addAttachment(taskId, file.name, data, file.type || undefined);
      }
      await reload();
    } catch (e) {
      store.toast({
        title: "Attachment upload failed",
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    } finally {
      setUploading(false);
    }
  }

  return (
    <div
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        e.preventDefault();
        if (e.dataTransfer.files.length) void upload(e.dataTransfer.files);
      }}
    >
      <div className="flex flex-col gap-1 text-[12.5px]">
        {attachments.length === 0 && (
          <span className="text-xs text-muted-foreground">No attachments — drop files here</span>
        )}
        {attachments.map((a) => (
          <div key={a.id} className="flex items-center gap-2">
            <a className="text-status-backlog" onClick={() => void openExternal(api.attachmentUrl(a.id))}>
              {a.filename}
            </a>
            <span className="text-muted-foreground">{formatSize(a.size)}</span>
            <Button
              variant="ghost"
              size="icon"
              className="size-5"
              title="Delete attachment"
              onClick={async () => {
                await api.deleteAttachment(a.id).catch(() => {});
                void reload();
              }}
            >
              ✕
            </Button>
          </div>
        ))}
      </div>
      <Button
        variant="outline"
        size="sm"
        className="mt-2"
        disabled={uploading}
        onClick={() => fileInputRef.current?.click()}
      >
        {uploading ? "Uploading…" : "Add files"}
      </Button>
      <input
        ref={fileInputRef}
        type="file"
        multiple
        style={{ display: "none" }}
        onChange={(e) => {
          if (e.target.files?.length) void upload(e.target.files);
          e.target.value = "";
        }}
      />
    </div>
  );
}
