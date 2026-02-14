/// <reference types="@sveltejs/kit" />
/// <reference types="@cloudflare/workers-types" />

declare global {
	namespace App {
		interface Locals {
			user: import('$lib/types').User | null;
			sessionId: string | null;
		}
		interface Platform {
			env: {
				DB: D1Database;
				SANDBOX: DurableObjectNamespace;
				SESSIONS: KVNamespace;
				STORAGE?: R2Bucket;
				GOOGLE_CLIENT_ID?: string;
				GOOGLE_CLIENT_SECRET?: string;
				GITHUB_CLIENT_ID?: string;
				GITHUB_CLIENT_SECRET?: string;
				ANTHROPIC_API_KEY?: string;
				SESSION_SECRET?: string;
				ENVIRONMENT?: string;
			};
			context: ExecutionContext;
		}
	}
}

export {};
