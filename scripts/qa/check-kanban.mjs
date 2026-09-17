// Run with: node scripts/qa/check-kanban.mjs (after installing desktop dependencies).
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire(new URL('../../desktop/package.json', import.meta.url));
const ts = require('typescript');
const { readFileSync } = require('node:fs');
const { resolve, dirname } = require('node:path');
const modules = new Map();
function load(path) {
  if (modules.has(path)) return modules.get(path).exports;
  const module = { exports: {} };
  modules.set(path, module);
  const { outputText } = ts.transpileModule(readFileSync(path, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } });
  new Function('require', 'module', 'exports', outputText)((name) => load(resolve(dirname(path), name + '.ts')), module, module.exports);
  return module.exports;
}
const { buildKanbanColumns } = load(new URL('../../desktop/src/lib/kanban.ts', import.meta.url).pathname);
const task = (id, project, status, pinned = false) => ({ id, project, status, pinned, title: `Task ${id}`, created_at: '2026-01-01', updated_at: '2026-01-01' });
const tasks = [task(1,'done','blocked',true),task(2,'app','queued'),task(3,'done','done'),task(4,'app','archived'),task(5,'','backlog')];
const project = buildKanbanColumns(tasks,{groupBy:'project',sort:'urgency'});
assert.deepEqual(project.flatMap(c=>c.tasks).map(t=>t.id).sort(),[1,2,3,5]);
assert.deepEqual(project.find(c=>c.project==='done').tasks.map(t=>t.id),[1,3]);
assert.ok(project.every(c=>c.status==='')); // project names cannot become status drop targets
assert.ok(project.some(c=>c.label==='No project'));
assert.equal(new Set(project.map(c=>c.key)).size,project.length);
const status = buildKanbanColumns(tasks,{groupBy:'status',sort:'urgency'});
assert.equal(status.length,4);
assert.deepEqual(status.find(c=>c.status==='backlog').tasks.map(t=>t.id).sort(),[2,5]);
assert.equal(buildKanbanColumns(tasks,{groupBy:'none',sort:'title'}).length,1);
assert.equal(buildKanbanColumns([],{groupBy:'project',sort:'urgency'})[0].tasks.length,0);
assert.equal(tasks.length,5);
console.log('Kanban grouping checks passed');
