#!/usr/bin/env python3
"""Read-only mobile regressions against a running TaskYou web UI.

Requires agent-browser and an existing, non-archived fixture task whose title
contains QUERY (at least two words). Use an isolated QA server, for example:
  python3 scripts/qa/check-mobile-web.py http://127.0.0.1:1430 --task-id 1 --query 'mobile qa'
No task data is changed. The browser session is closed even on failure.
"""
import argparse
import json
import subprocess
import uuid
import tempfile
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('url')
parser.add_argument('--task-id', type=int, required=True)
parser.add_argument('--query', required=True)
parser.add_argument('--api-base', help='API origin for mocked reply requests (defaults to UI origin)')
parser.add_argument('--live-task-id', type=int, help='Optional processing/blocked fixture for reply draft tests')
args = parser.parse_args()
assert len(args.query.split()) >= 2, 'Use a multi-word query to catch dropped spaces'
session = 'ty-mobile-test-' + uuid.uuid4().hex[:8]
touch_process = None
attachment_path = None


def browser(*command):
    result = subprocess.run(['agent-browser', '--session', session, '--json', *command],
                            capture_output=True, text=True, timeout=20)
    if result.returncode != 0:
        print(subprocess.run(['agent-browser', '--session', session, 'snapshot', '-i'], capture_output=True, text=True, timeout=5).stdout)
    assert result.returncode == 0, result.stdout + result.stderr
    data = json.loads(result.stdout)
    assert data['success'], data
    return data.get('data', {})


def evaluate(code):
    return browser('eval', code)['result']


def wait(code):
    browser('wait', '--fn', code)


try:
    browser('set', 'device', 'iPhone 14')
    browser('open', args.url)
    touch_process = subprocess.Popen(['node', str(Path(__file__).with_name('enable-mobile-touch.mjs')), session], stdout=subprocess.PIPE, text=True)
    assert touch_process.stdout.readline().strip() == 'Touch emulation ready'
    wait('!!document.querySelector("button svg.lucide-list-filter")')
    browser('click', 'button:has(svg.lucide-list-filter)')
    search = 'input[placeholder="Search title, body, or #123"]'
    browser('wait', search)
    browser('fill', search, '')
    # Deliberately type keystrokes: fill/paste cannot catch whitespace lost
    # between controlled-input renders.
    browser('type', search, args.query)
    actual = evaluate(f'document.querySelector({json.dumps(search)}).value')
    assert actual == args.query, f'Typing lost characters: {actual!r}'
    print('PASS multi-word search preserves typing')
    browser('fill', search, '#' + str(args.task_id))
    wait('document.querySelector("[role=dialog]").innerText.includes("All statuses")')
    counts = evaluate('Array.from(document.querySelectorAll("[role=dialog] button")).map(e=>e.textContent)')
    assert 'All statuses1' in counts, f'ID search count disagrees with result: {counts}'
    assert 'All projects1' in counts, f'ID project count disagrees with result: {counts}'
    print('PASS task-number filter counts')
    browser('click', '[data-slot="dialog-close"]')
    browser('find', 'role', 'button', 'click', '--name', 'New', '--exact')
    browser('wait', '[role="dialog"]')
    assert evaluate('!!document.querySelector("[role=dialog] input[type=file]")'), 'New task has no file picker'
    close_size = evaluate('(()=>{const r=document.querySelector("[data-slot=dialog-close]").getBoundingClientRect();return [r.width,r.height]})()')
    assert min(close_size) >= 44, f'Sheet close target too small: {close_size}'
    print('PASS mobile attachment picker and close target')
    # Portal content mounts after DialogContent's first effect. Its keyboard
    # listener must attach when the panel appears, not only on initial render.
    evaluate('Object.defineProperty(visualViewport,"height",{configurable:true,value:400});visualViewport.dispatchEvent(new Event("resize"))')
    wait('document.querySelector("[role=dialog]").style.bottom === `${document.documentElement.clientHeight-400}px`')
    evaluate('delete visualViewport.height;visualViewport.dispatchEvent(new Event("resize"))')
    wait('document.querySelector("[role=dialog]").style.bottom === ""')
    print('PASS dialog portal attaches and resets keyboard inset')
    for width, height in [(320, 568), (375, 420), (390, 844)]:
        browser('set', 'viewport', str(width), str(height))
        wait('!!document.querySelector("[role=dialog]")')
        browser('scroll', 'down', '2000', '--selector', '[role="dialog"]')
        if width == 320:
            browser('find', 'role', 'button', 'click', '--name', 'Advanced')
        # Scrolling the sheet must expose its primary action at short heights.
        browser('scroll', 'down', '2000', '--selector', '[role="dialog"]')
        assert evaluate('(()=>{const d=document.querySelector("[role=dialog]");return d.scrollWidth<=d.clientWidth+1})()'), f'Horizontal overflow at {width}'
    print('PASS narrow and keyboard-sized sheet layouts')
    # Isolate create/upload/retry behavior from the backend. A failed upload
    # must remain visible, and retry must update the first task, not create two.
    browser('scroll', 'up', '2000', '--selector', '[role="dialog"]')
    browser('fill', '#task-title', 'Attachment recovery check')
    evaluate("""(() => {
      window.__tyUpload = {creates:0, uploads:0, fail:true};
      const original = window.fetch;
      window.__tyOriginalFetch = original;
      window.fetch = (input, init) => {
        const path = new URL(String(input)).pathname;
        const json = (body) => Promise.resolve(new Response(JSON.stringify(body), {headers:{'Content-Type':'application/json'}}));
        if (path === '/api/tasks' && init?.method === 'POST') {
          window.__tyUpload.creates++; return json({id:99999999});
        }
        if (path === '/api/tasks/99999999' && init?.method === 'PATCH') return json({id:99999999});
        if (path === '/api/tasks/99999999/attachments') {
          window.__tyUpload.uploads++;
          return window.__tyUpload.fail ? Promise.reject(new Error('Simulated upload failure')) : json({id:99999999});
        }
        return original(input, init);
      };
    })()""")
    with tempfile.NamedTemporaryFile(suffix='.txt', delete=False) as attachment:
        attachment_path = Path(attachment.name)
        attachment.write(b'Mobile attachment recovery fixture'); attachment.flush()
        browser('upload', '[role="dialog"] input[type="file"]', attachment.name)
    browser('scroll', 'down', '2000', '--selector', '[role="dialog"]')
    browser('find', 'role', 'button', 'click', '--name', 'Create')
    wait('window.__tyUpload.uploads === 1 && !Array.from(document.querySelectorAll("button")).some(b=>b.textContent.includes("Saving"))')
    assert evaluate('!!document.querySelector("[role=dialog] button[aria-label^=Remove]")'), 'Failed attachment lost'
    evaluate('window.__tyUpload.fail = false')
    browser('find', 'role', 'button', 'click', '--name', 'Create')
    wait('!document.querySelector("[role=dialog]")')
    assert evaluate('window.__tyUpload.creates === 1 && window.__tyUpload.uploads === 2'), 'Retry duplicated task or missed upload'
    evaluate('window.fetch = window.__tyOriginalFetch')
    print('PASS failed attachment stays selected and retry creates no duplicate task')

    browser('open', args.url)
    browser('find', 'role', 'button', 'click', '--name', 'Menu', '--exact')
    browser('find', 'role', 'button', 'click', '--name', 'Search everything')
    browser('set', 'viewport', '375', '320')
    browser('wait', '[cmdk-input]')
    assert evaluate('parseFloat(getComputedStyle(document.querySelector("[cmdk-input]")).fontSize) >= 16'), 'Search input can trigger iOS focus zoom'
    assert evaluate('document.querySelector("[cmdk-list]").getBoundingClientRect().bottom <= document.querySelector("[role=dialog]").getBoundingClientRect().bottom'), 'Search results clipped below sheet'
    print('PASS short-viewport global search and input size')

    if args.live_task_id:
        from urllib.parse import urlsplit, urlunsplit
        url = urlsplit(args.url)
        detail = urlunsplit((url.scheme, url.netloc, url.path, 'task=' + str(args.live_task_id), ''))
        browser('set', 'device', 'iPhone 14')
        browser('open', detail)
        reply = 'textarea[placeholder="Reply to the agent…"]'
        browser('wait', reply)
        draft = 'Keep this reply while I check another task.'
        browser('fill', reply, draft)
        browser('press', 'End')
        browser('press', 'Enter')
        assert evaluate(f'document.querySelector({json.dumps(reply)}).value') == draft + '\n', 'Touch Return must insert a newline'
        browser('find', 'role', 'button', 'click', '--name', 'Board', '--exact')
        browser('open', detail)
        browser('wait', reply)
        assert evaluate(f'document.querySelector({json.dumps(reply)}).value') == draft + '\n', 'Reply draft lost on navigation/reload'
        api_base = args.api_base or urlunsplit((url.scheme, url.netloc, '', '', ''))
        endpoint = api_base.rstrip('/') + '/api/tasks/' + str(args.live_task_id) + '/input'
        evaluate("""(() => {
          window.__tyOriginalFetch = window.fetch;
          window.__tyFailReply = true;
          window.fetch = (input, init) => {
            if (String(input) === ENDPOINT) {
              return window.__tyFailReply ? Promise.reject(new Error('Simulated connection loss')) : Promise.resolve(new Response('{"ok":true}', {headers: {'Content-Type': 'application/json'}}));
            }
            return window.__tyOriginalFetch(input, init);
          };
        })()""".replace('ENDPOINT', json.dumps(endpoint)))
        browser('find', 'role', 'button', 'click', '--name', 'Send reply')
        wait('!document.querySelector("textarea").disabled')
        assert evaluate(f'document.querySelector({json.dumps(reply)}).value') == draft + '\n', 'Failed send discarded draft'
        evaluate('window.__tyFailReply = false')
        browser('find', 'role', 'button', 'click', '--name', 'yes', '--exact')
        wait('!document.querySelector("textarea").disabled')
        assert evaluate(f'document.querySelector({json.dumps(reply)}).value') == draft + '\n', 'Quick answer discarded another draft'
        browser('find', 'role', 'button', 'click', '--name', 'Send reply')
        wait('!document.querySelector("textarea").disabled && document.querySelector("textarea").value === ""')
        evaluate('window.fetch = window.__tyOriginalFetch')
        browser('open', detail)
        browser('wait', reply)
        assert evaluate(f'document.querySelector({json.dumps(reply)}).value') == '', 'Sent draft returned'
        print('PASS failed sends retain drafts; quick answers preserve drafts; successful sends clear drafts')
        browser('fill', reply, draft)
        browser('click', reply)
        evaluate(f'document.querySelector({json.dumps(reply)}).select()')
        browser('press', 'Backspace')
        browser('open', detail)
        browser('wait', reply)
        assert evaluate(f'document.querySelector({json.dumps(reply)}).value') == '', 'Cleared draft returned'
        print('PASS touch Return and reply draft persistence/clearing')

finally:
    if attachment_path:
        attachment_path.unlink(missing_ok=True)
    if touch_process:
        touch_process.terminate()
        touch_process.wait(timeout=5)
    browser('close')
