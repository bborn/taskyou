// Post-build: Wrap SvelteKit worker with custom entry point
// - Adds routeAgentRequest() for agent WebSocket/HTTP handling
// - Re-exports TaskYouAgent and TaskExecutionWorkflow for wrangler DO/Workflow bindings
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

// Add agent imports at the top of the file
const agentImports = `
import { routeAgentRequest } from "agents";
`;

content = agentImports + content;

// Wrap the existing fetch handler with agent routing
content = content.replace(
	'async fetch(req, env2, ctx) {',
	`async fetch(req, env2, ctx) {
    // Route agent WebSocket/HTTP requests before SvelteKit
    const agentResponse = await routeAgentRequest(req, env2);
    if (agentResponse) return agentResponse;`
);

// Add DO and Workflow class re-exports at the end
content += `
// Re-export agent classes for Durable Object and Workflow bindings
export { TaskYouAgent } from "./src/lib/server/agent.ts";
export { TaskExecutionWorkflow } from "./src/lib/server/workflow.ts";
`;

writeFileSync(workerFile, content);
console.log('✓ Patched worker-entry.js with routeAgentRequest + agent exports');
