import React from "react";
import ReactDOM from "react-dom/client";
import { Agentation } from "agentation";
import App from "./App";
import "./index.css";
import "@xterm/xterm/css/xterm.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
    {/* Visual feedback toolbar (dev only) — annotations sync to the coding
        agent via agentation-mcp on :4747 when it's running. */}
    {import.meta.env.DEV && <Agentation endpoint="http://localhost:4747" />}
  </React.StrictMode>,
);
