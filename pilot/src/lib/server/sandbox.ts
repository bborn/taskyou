// Cloudflare Sandbox SDK integration
// Each user gets their own isolated sandbox container for task execution
//
// This replaces the Fly Sprites integration from the original pilot.
// Instead of Fly VMs, we use Cloudflare Containers via the Sandbox SDK.
//
// Note: @cloudflare/sandbox is a Cloudflare Workers runtime package.
// It's imported dynamically to avoid build issues with Node.js SSR.

export interface SandboxInfo {
	id: string;
	status: 'pending' | 'creating' | 'running' | 'stopped' | 'error';
}

async function loadSandboxSDK() {
	// Dynamic import to avoid SSR build issues - this only runs in Cloudflare Workers runtime
	const { getSandbox } = await import('@cloudflare/sandbox');
	return { getSandbox };
}

/**
 * Get or create a sandbox for a user.
 * Each user gets a dedicated sandbox identified by their user ID.
 */
export async function getUserSandbox(
	binding: DurableObjectNamespace,
	userId: string,
) {
	const { getSandbox } = await loadSandboxSDK();
	return getSandbox(binding, `user-${userId}`, {
		sleepAfter: '15 minutes',
	});
}

/**
 * Execute a task inside the user's sandbox.
 */
export async function executeInSandbox(
	binding: DurableObjectNamespace,
	userId: string,
	command: string,
	options?: { cwd?: string; env?: Record<string, string> },
) {
	const sandbox = await getUserSandbox(binding, userId);
	return sandbox.exec(command, {
		cwd: options?.cwd || '/workspace',
		env: options?.env,
	});
}

/**
 * Start a long-running process in the sandbox.
 */
export async function startSandboxProcess(
	binding: DurableObjectNamespace,
	userId: string,
	command: string,
	options?: { cwd?: string; env?: Record<string, string> },
) {
	const sandbox = await getUserSandbox(binding, userId);
	return sandbox.startProcess(command, {
		cwd: options?.cwd || '/workspace',
		env: options?.env,
	});
}

/**
 * Get process logs from the sandbox.
 */
export async function getSandboxProcessLogs(
	binding: DurableObjectNamespace,
	userId: string,
	processId: string,
) {
	const sandbox = await getUserSandbox(binding, userId);
	return sandbox.getProcessLogs(processId);
}

/**
 * List all active processes in the user's sandbox.
 */
export async function listSandboxProcesses(
	binding: DurableObjectNamespace,
	userId: string,
) {
	const sandbox = await getUserSandbox(binding, userId);
	return sandbox.listProcesses();
}

/**
 * Kill a process in the sandbox.
 */
export async function killSandboxProcess(
	binding: DurableObjectNamespace,
	userId: string,
	processId: string,
) {
	const sandbox = await getUserSandbox(binding, userId);
	return sandbox.killProcess(processId);
}

/**
 * Destroy a user's sandbox, releasing all resources.
 */
export async function destroySandbox(
	binding: DurableObjectNamespace,
	userId: string,
) {
	const sandbox = await getUserSandbox(binding, userId);
	return sandbox.destroy();
}

/**
 * Handle a terminal WebSocket upgrade request for the user's sandbox.
 */
export async function handleTerminalUpgrade(
	binding: DurableObjectNamespace,
	userId: string,
	request: Request,
) {
	const sandbox = await getUserSandbox(binding, userId);
	return sandbox.terminal(request);
}
