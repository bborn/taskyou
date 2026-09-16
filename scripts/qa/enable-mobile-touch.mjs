// agent-browser's device preset sets size and UA, but currently leaves the
// pointer fine. Enable real Chrome touch emulation so Return/gesture tests
// exercise the mobile code path. Node 22+; no third-party dependencies.
import { execFileSync } from 'node:child_process';
const session = process.argv[2];
if (!session) throw new Error('Usage: node enable-mobile-touch.mjs SESSION');
const endpoint = execFileSync('agent-browser', ['--session', session, 'get', 'cdp-url'], {encoding:'utf8'}).trim();
const ws = new WebSocket(endpoint);
await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; });
let id = 0;
const pending = new Map();
ws.onmessage = ({data}) => {
  const message = JSON.parse(data);
  const handler = pending.get(message.id);
  if (handler) { pending.delete(message.id); message.error ? handler.reject(message.error) : handler.resolve(message.result); }
};
function send(method, params = {}, sessionId) {
  return new Promise((resolve, reject) => {
    const requestId = ++id;
    pending.set(requestId, {resolve, reject});
    ws.send(JSON.stringify({id: requestId, method, params, sessionId}));
  });
}
try {
  const {targetInfos} = await send('Target.getTargets');
  for (const page of targetInfos.filter(t => t.type === 'page')) {
    const {sessionId} = await send('Target.attachToTarget', {targetId:page.targetId, flatten:true});
    await send('Emulation.setTouchEmulationEnabled', {enabled:true, maxTouchPoints:5}, sessionId);

  }
  console.log("Touch emulation ready");
} catch (error) { ws.close(); throw error; }
// CDP restores defaults on detach; retain the connection for the test run.
process.on("SIGTERM", () => { ws.close(); process.exit(0); });
