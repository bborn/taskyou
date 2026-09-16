import { useEffect } from "react";
import { AnimatePresence, motion, type PanInfo } from "motion/react";
import {
  KanbanSquare,
  Moon,
  MonitorSmartphone,
  Plus,
  Repeat,
  Search,
  Settings2,
  Sun,
} from "lucide-react";
import logoUrl from "../assets/logo.png";
import { store, useAppSelector } from "../store";
import { cn } from "@/lib/utils";

/** bb's drawer width, and it holds up: wide enough for a label plus an icon,
 * narrow enough that the board behind it still reads as "still there". */
const WIDTH = "min(76vw, 320px)";

/** Release commits the drawer when it has been dragged a third of the way, or
 * flicked hard enough that the intent is obvious from velocity alone. */
const COMMIT_FRACTION = 0.33;
const FLING_VELOCITY = 450;

const EASE = [0.32, 0.72, 0, 1] as const;

type ViewKind = "board" | "detail" | "settings" | "routines";

export function MobileDrawer({
  open,
  onClose,
  view,
}: {
  open: boolean;
  onClose: () => void;
  view: ViewKind;
}) {
  const theme = useAppSelector((s) => s.theme);
  const permissionMode = useAppSelector((s) => s.permissionMode);

  // A hardware keyboard (iPad, or a desktop window dragged narrow) should be
  // able to dismiss this the same way every other overlay does.
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
      }
    }
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [open, onClose]);

  function go(action: () => void) {
    action();
    onClose();
  }

  function onDragEnd(_: unknown, info: PanInfo) {
    const width = Math.min(window.innerWidth * 0.76, 320);
    const dragged = -info.offset.x / width;
    if (dragged >= COMMIT_FRACTION || -info.velocity.x >= FLING_VELOCITY) onClose();
  }

  const items: {
    key: string;
    icon: typeof KanbanSquare;
    label: string;
    active: boolean;
    onClick: () => void;
  }[] = [
    {
      key: "new",
      icon: Plus,
      label: "New task",
      active: false,
      onClick: () => go(() => store.setForm({ kind: "new" })),
    },
    {
      key: "board",
      icon: KanbanSquare,
      label: "Board",
      active: view === "board" || view === "detail",
      onClick: () => go(() => store.openBoard()),
    },
    {
      key: "search",
      icon: Search,
      label: "Search everything",
      active: false,
      onClick: () => go(() => store.setPalette(true)),
    },
    {
      key: "routines",
      icon: Repeat,
      label: "Routines",
      active: view === "routines",
      onClick: () => go(() => store.openRoutines()),
    },
    {
      key: "settings",
      icon: Settings2,
      label: "Settings",
      active: view === "settings",
      onClick: () => go(() => store.openSettings()),
    },
  ];

  const ThemeIcon = theme === "light" ? Sun : theme === "dark" ? Moon : MonitorSmartphone;

  return (
    <AnimatePresence>
      {open && (
        <>
          <motion.div
            className="fixed inset-0 z-40 bg-black/40"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.22, ease: EASE }}
            onClick={onClose}
          />
          <motion.nav
            aria-label="Menu"
            className="fixed inset-y-0 left-0 z-50 flex touch-pan-y flex-col border-r bg-background pt-[env(safe-area-inset-top)] pb-[max(0.75rem,env(safe-area-inset-bottom))] pl-[env(safe-area-inset-left)]"
            style={{ width: WIDTH }}
            initial={{ x: "-100%" }}
            animate={{ x: 0 }}
            exit={{ x: "-100%" }}
            transition={{ duration: 0.22, ease: EASE }}
            drag="x"
            dragConstraints={{ left: 0, right: 0 }}
            dragElastic={{ left: 1, right: 0 }}
            dragMomentum={false}
            onDragEnd={onDragEnd}
          >
            <div className="flex h-12 shrink-0 items-center gap-2 px-4">
              <img src={logoUrl} alt="" className="size-5 rounded" />
              <span className="text-sm font-semibold tracking-tight">TaskYou</span>
            </div>

            <div className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto overscroll-contain px-2 py-2">
              {items.map((item) => (
                <button
                  key={item.key}
                  onClick={item.onClick}
                  className={cn(
                    "flex h-11 shrink-0 items-center gap-3 rounded-lg px-3 text-left text-sm active:bg-surface-2",
                    item.active ? "bg-surface-2 font-medium text-foreground" : "text-muted-foreground",
                  )}
                >
                  <item.icon className="size-4 shrink-0" />
                  {item.label}
                </button>
              ))}
            </div>

            {/* Both of these are desktop-only today, which is most of why the
                phone couldn't do everything the desktop could. */}
            <div className="shrink-0 border-t px-2 pt-2">
              <button
                onClick={() => store.cyclePermissionMode()}
                className="flex h-11 w-full items-center gap-3 rounded-lg px-3 text-left text-sm text-muted-foreground active:bg-surface-2"
              >
                <span
                  className={cn(
                    "size-2 shrink-0 rounded-full",
                    permissionMode === "dangerous"
                      ? "bg-destructive"
                      : permissionMode === "auto"
                        ? "bg-status-processing"
                        : "bg-muted-foreground",
                  )}
                />
                Permissions
                <span className="ml-auto text-xs text-foreground">
                  {permissionMode === "" ? "default" : permissionMode}
                </span>
              </button>
              <button
                onClick={() => {
                  const order = ["system", "light", "dark"] as const;
                  store.setTheme(order[(order.indexOf(theme) + 1) % order.length]);
                }}
                className="flex h-11 w-full items-center gap-3 rounded-lg px-3 text-left text-sm text-muted-foreground active:bg-surface-2"
              >
                <ThemeIcon className="size-4 shrink-0" />
                Theme
                <span className="ml-auto text-xs text-foreground">{theme}</span>
              </button>
            </div>
          </motion.nav>
        </>
      )}
    </AnimatePresence>
  );
}
