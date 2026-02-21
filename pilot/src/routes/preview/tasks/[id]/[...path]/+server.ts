import type { RequestHandler } from './$types';

// GET /preview/tasks/:id/* — serve task files from R2 with correct MIME types
// This enables iframe previews where relative paths (style.css, script.js) just work.
export const GET: RequestHandler = async ({ params, platform }) => {
	const taskId = params.id;
	const filePath = params.path || 'index.html';

	const storage = platform!.env.STORAGE as R2Bucket;
	const r2Key = `tasks/${taskId}/${filePath}`;
	const object = await storage.get(r2Key);

	if (!object) {
		// Try index.html for directory-style requests
		if (!filePath.includes('.')) {
			const indexKey = `tasks/${taskId}/${filePath}/index.html`;
			const indexObject = await storage.get(indexKey);
			if (indexObject) {
				return new Response(indexObject.body, {
					headers: {
						'Content-Type': 'text/html; charset=utf-8',
						'Cache-Control': 'no-cache',
					},
				});
			}
		}
		return new Response('Not found', { status: 404 });
	}

	const contentType = object.httpMetadata?.contentType || guessMime(filePath);
	return new Response(object.body, {
		headers: {
			'Content-Type': `${contentType}; charset=utf-8`,
			'Cache-Control': 'no-cache',
		},
	});
};

function guessMime(path: string): string {
	const ext = path.split('.').pop()?.toLowerCase() || '';
	const types: Record<string, string> = {
		html: 'text/html', htm: 'text/html', css: 'text/css',
		js: 'application/javascript', mjs: 'application/javascript',
		json: 'application/json', svg: 'image/svg+xml',
		png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg',
		gif: 'image/gif', webp: 'image/webp', ico: 'image/x-icon',
		woff: 'font/woff', woff2: 'font/woff2', ttf: 'font/ttf',
		md: 'text/markdown', txt: 'text/plain', xml: 'application/xml',
		py: 'text/plain', rb: 'text/plain', sh: 'text/plain',
	};
	return types[ext] || 'application/octet-stream';
}
