import { useEffect, useRef, useState, useCallback } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { Loader2, AlertCircle, Terminal as TerminalIcon, RefreshCw, Download } from 'lucide-react';
import { tasks } from '@/api/client';
import { cn } from '@/lib/utils';
import '@xterm/xterm/css/xterm.css';

interface TerminalProps {
  taskId: number;
  className?: string;
}

type TerminalState = 'connecting' | 'connected' | 'disconnected' | 'error' | 'no-session' | 'no-ttyd';

export function TaskTerminal({ taskId, className }: TerminalProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<XTerm | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const [state, setState] = useState<TerminalState>('connecting');
  const [error, setError] = useState<string | null>(null);

  const connect = useCallback(async () => {
    if (!containerRef.current) return;

    setState('connecting');
    setError(null);

    try {
      // Get terminal connection info from the API
      const terminalInfo = await tasks.getTerminal(taskId);

      // Clean up any existing terminal
      if (terminalRef.current) {
        terminalRef.current.dispose();
        terminalRef.current = null;
      }
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }

      // Create terminal instance
      const terminal = new XTerm({
        cursorBlink: true,
        fontSize: 14,
        fontFamily: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace',
        theme: {
          background: '#0f172a',
          foreground: '#e2e8f0',
          cursor: '#e2e8f0',
          cursorAccent: '#0f172a',
          selectionBackground: '#334155',
          black: '#1e293b',
          red: '#ef4444',
          green: '#22c55e',
          yellow: '#eab308',
          blue: '#3b82f6',
          magenta: '#a855f7',
          cyan: '#06b6d4',
          white: '#f8fafc',
          brightBlack: '#475569',
          brightRed: '#f87171',
          brightGreen: '#4ade80',
          brightYellow: '#facc15',
          brightBlue: '#60a5fa',
          brightMagenta: '#c084fc',
          brightCyan: '#22d3ee',
          brightWhite: '#ffffff',
        },
      });

      terminalRef.current = terminal;

      // Add fit addon
      const fitAddon = new FitAddon();
      fitAddonRef.current = fitAddon;
      terminal.loadAddon(fitAddon);

      // Open terminal in container
      terminal.open(containerRef.current);
      fitAddon.fit();

      // Connect to ttyd websocket
      const ws = new WebSocket(terminalInfo.websocket_url);
      wsRef.current = ws;

      ws.onopen = () => {
        setState('connected');
        terminal.focus();

        // Send terminal size to ttyd
        // ttyd protocol: byte 1 + JSON for resize (using actual byte values, not ASCII)
        const sendSize = () => {
          const msg = JSON.stringify({ columns: terminal.cols, rows: terminal.rows });
          ws.send(String.fromCharCode(1) + msg);
        };
        sendSize();
        terminal.onResize(sendSize);
      };

      // Handle incoming data from ttyd
      ws.onmessage = (event) => {
        const data = event.data;
        if (typeof data === 'string' && data.length > 0) {
          const cmd = data.charCodeAt(0);
          const payload = data.substring(1);
          if (cmd === 0) {
            // Output message - write to terminal
            terminal.write(payload);
          }
          // cmd 1 = window title, 2 = preferences (ignored)
        }
      };

      // Send input to ttyd
      // ttyd protocol: byte 0 + input data (using actual byte values, not ASCII)
      terminal.onData((data) => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(String.fromCharCode(0) + data);
        }
      });

      ws.onclose = () => {
        if (state !== 'error') {
          setState('disconnected');
        }
      };

      ws.onerror = () => {
        setState('error');
        setError('WebSocket connection failed');
      };
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to connect';
      if (message.includes('no active terminal') || message.includes('not running')) {
        setState('no-session');
      } else if (message.includes('ttyd not installed')) {
        setState('no-ttyd');
      } else {
        setState('error');
        setError(message);
      }
    }
  }, [taskId, state]);

  // Connect on mount and when taskId changes
  useEffect(() => {
    connect();

    return () => {
      if (terminalRef.current) {
        terminalRef.current.dispose();
        terminalRef.current = null;
      }
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    };
  }, [taskId]); // eslint-disable-line react-hooks/exhaustive-deps

  // Handle window resize
  useEffect(() => {
    const handleResize = () => {
      if (fitAddonRef.current && terminalRef.current) {
        fitAddonRef.current.fit();
      }
    };

    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  // Render overlay for non-connected states
  const renderOverlay = () => {
    if (state === 'connected') return null;

    return (
      <div className="absolute inset-0 flex flex-col items-center justify-center bg-slate-900/90 backdrop-blur-sm">
        {state === 'connecting' && (
          <>
            <Loader2 className="h-8 w-8 text-primary animate-spin mb-3" />
            <p className="text-sm text-muted-foreground">Connecting to terminal...</p>
          </>
        )}
        {state === 'no-session' && (
          <>
            <TerminalIcon className="h-8 w-8 text-muted-foreground mb-3" />
            <p className="text-sm text-muted-foreground mb-1">No active terminal session</p>
            <p className="text-xs text-muted-foreground/70">
              The task must be running to view its terminal
            </p>
          </>
        )}
        {state === 'no-ttyd' && (
          <>
            <Download className="h-8 w-8 text-yellow-500 mb-3" />
            <p className="text-sm text-foreground mb-1">ttyd not installed</p>
            <p className="text-xs text-muted-foreground mb-3 text-center max-w-xs">
              Install ttyd to enable live terminal streaming
            </p>
            <code className="px-3 py-1.5 bg-muted rounded text-xs font-mono">
              brew install ttyd
            </code>
          </>
        )}
        {state === 'disconnected' && (
          <>
            <AlertCircle className="h-8 w-8 text-yellow-500 mb-3" />
            <p className="text-sm text-muted-foreground mb-3">Terminal disconnected</p>
            <button
              onClick={connect}
              className="flex items-center gap-2 px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:bg-primary/90 transition-colors"
            >
              <RefreshCw className="h-4 w-4" />
              Reconnect
            </button>
          </>
        )}
        {state === 'error' && (
          <>
            <AlertCircle className="h-8 w-8 text-destructive mb-3" />
            <p className="text-sm text-muted-foreground mb-1">Connection failed</p>
            <p className="text-xs text-destructive mb-3">{error}</p>
            <button
              onClick={connect}
              className="flex items-center gap-2 px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:bg-primary/90 transition-colors"
            >
              <RefreshCw className="h-4 w-4" />
              Retry
            </button>
          </>
        )}
      </div>
    );
  };

  return (
    <div className={cn('relative rounded-lg overflow-hidden bg-slate-900', className)}>
      <div ref={containerRef} className="h-full w-full" />
      {renderOverlay()}
    </div>
  );
}
