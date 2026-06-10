import { store, useAppState } from "../store";

export function Toasts() {
  const { toasts } = useAppState();
  if (toasts.length === 0) return null;

  return (
    <div className="toasts">
      {toasts.map((toast) => (
        <div
          key={toast.id}
          className={`toast ${toast.kind}`}
          onClick={() => {
            if (toast.taskId) store.openDetail(toast.taskId);
            store.dismissToast(toast.id);
          }}
        >
          <div className="toast-title">{toast.title}</div>
          {toast.body && <div className="toast-body">{toast.body}</div>}
        </div>
      ))}
    </div>
  );
}
