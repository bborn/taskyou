// Post-build: Wrap SvelteKit worker with custom entry point
// - Adds routeAgentRequest() for agent WebSocket/HTTP handling
// - Adds proxyToSandbox() for sandbox preview URL routing
// - Re-exports TaskYouAgent, TaskExecutionWorkflow, and Sandbox for wrangler bindings
// - SvelteKit handles everything else (auth, CRUD, static assets)

import { readFileSync, writeFileSync, existsSync } from 'fs';

const workerFile = 'worker-entry.js';

if (!existsSync(workerFile)) {
	console.error(`✗ ${workerFile} not found - did vite build run?`);
	process.exit(1);
}

let content = readFileSync(workerFile, 'utf8');

// Check if already patched
if (content.includes('routeAgentRequest')) {
	console.log('✓ worker-entry.js already patched');
	process.exit(0);
}

// Add agent + sandbox imports at the top of the file
const imports = `
import { routeAgentRequest } from "agents";
import { proxyToSandbox } from "@cloudflare/sandbox";
`;

content = imports + content;

// Wrap the existing fetch handler with sandbox proxy + agent routing
content = content.replace(
	'async fetch(req, env2, ctx) {',
	`async fetch(req, env2, ctx) {
    // Route agent WebSocket/HTTP requests before SvelteKit
    const agentResponse = await routeAgentRequest(req, env2);
    if (agentResponse) return agentResponse;

    // Proxy sandbox preview URLs (after agent routing)
    // Only relevant with custom domains for preview URL subdomains
    try {
      const sandboxResponse = await proxyToSandbox(req, env2);
      if (sandboxResponse && sandboxResponse.status !== 404) return sandboxResponse;
    } catch (e) {
      // Sandbox proxy not available — continue to SvelteKit
    }`
);

// Add DO, Workflow, and Sandbox class re-exports at the end
content += `
// Re-export agent classes for Durable Object, Workflow, and Container bindings
export { TaskYouAgent } from "./src/lib/server/agent.ts";
export { TaskExecutionWorkflow } from "./src/lib/server/workflow.ts";
export { Sandbox } from "@cloudflare/sandbox";
`;

writeFileSync(workerFile, content);
console.log('✓ Patched worker-entry.js with proxyToSandbox + routeAgentRequest + exports');
