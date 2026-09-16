#!/usr/bin/env python3
"""Real iOS Safari keyboard checks; standard library + runner-installed Appium.

Runs only on an ephemeral GitHub runner. No daemon, executor, real tasks,
credentials, external services, or local machine UI are used.
"""
import base64
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


def request(method, url, data=None, timeout=45):
    body = None if data is None else json.dumps(data).encode()
    req = urllib.request.Request(url, data=body, method=method,
                                 headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as res:
            result = json.load(res)
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f'{method} {url}: {exc.read().decode()[:2000]}') from exc
    return result


def wait_for(fn, description, seconds=30):
    deadline = time.monotonic() + seconds
    last = None
    while time.monotonic() < deadline:
        try:
            value = fn()
            if value:
                return value
        except (RuntimeError, OSError) as exc:
            last = exc
        time.sleep(0.5)
    raise AssertionError(f'Timed out: {description}; {last}')


def main():
    assert os.environ.get('GITHUB_ACTIONS') == 'true', 'Run through GitHub Actions, not on the user desktop'
    artifacts = Path(os.environ['QA_ARTIFACTS'])
    artifacts.mkdir(parents=True, exist_ok=True)
    processes, streams = [], []
    session = None
    checks = []
    appium = 'http://127.0.0.1:4723'
    site = 'http://127.0.0.1:8484'

    def start(command, name, env=None):
        log = (artifacts / f'{name}.log').open('w')
        streams.append(log)
        process = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT, env=env)
        processes.append(process)
        return process

    def wd(method, route, data=None):
        return request(method, f'{appium}/session/{session}{route}', data)['value']

    def js(script, *args):
        return wd('POST', '/execute/sync', {'script': script, 'args': list(args)})

    def element(selector):
        data = wd('POST', '/element', {'using': 'css selector', 'value': selector})
        return data['element-6066-11e4-a52e-4f735466cecf']

    def click(selector):
        ident = wait_for(lambda: element(selector), selector)
        wd('POST', f'/element/{ident}/click', {})

    def native_field(kind, label):
        # Native accessibility avoids Appium's web-to-screen calibration, which
        # can fail after Safari changes its toolbar or a sheet animates in.
        context = wd('GET', '/context')
        wd('POST', '/context', {'name': 'NATIVE_APP'})
        try:
            (artifacts / 'native-fields.xml').write_text(wd('GET', '/source'))
            predicate = f"type == '{kind}' AND (label == '{label}' OR value == '{label}')"
            target = wait_for(lambda: wd('POST', '/element', {
                'using': '-ios predicate string', 'value': predicate}), f'native {label}')
            ident = target['element-6066-11e4-a52e-4f735466cecf']
            wd('POST', f'/element/{ident}/click', {})
        finally:
            wd('POST', '/context', {'name': context})

    def evidence(name):
        print(f'Capturing {name}', flush=True)
        png = wd('GET', '/screenshot')
        (artifacts / f'{name}.png').write_bytes(base64.b64decode(png))
        geometry = js('''return {url:location.href, ua:navigator.userAgent,
          viewport:{height:visualViewport.height,top:visualViewport.offsetTop,scale:visualViewport.scale},
          innerHeight,clientHeight:document.documentElement.clientHeight,
          active:document.activeElement?.outerHTML,
          dialogs:[...document.querySelectorAll('[role="dialog"]')].map(e=>({rect:e.getBoundingClientRect().toJSON(),bottom:e.style.bottom}))};''')
        (artifacts / f'{name}.json').write_text(json.dumps(geometry, indent=2))
        return geometry

    def keyboard_visible():
        return wd('GET', '/appium/device/is_keyboard_shown')

    def assert_input_visible(selector):
        assert js('''const e=document.querySelector(arguments[0]); if(!e)return false;
          const r=e.getBoundingClientRect(),v=visualViewport;
          return document.activeElement===e && r.top>=v.offsetTop-2 && r.bottom<=v.offsetTop+v.height+2
            && r.left>=-2 && r.right<=innerWidth+2 && Math.abs(v.scale-1)<.02;''', selector), 'Expected input is not focused, is clipped, or Safari zoomed'

    try:
        with tempfile.TemporaryDirectory(prefix='taskyou-ios-') as temp:
            env = dict(os.environ, WORKTREE_DB_PATH=f'{temp}/tasks.db')
            start([sys.argv[1], 'serve', '--host', '127.0.0.1', '--port', '8484'], 'server', env)
            wait_for(lambda: request('GET', site + '/api/tasks') is not None, 'isolated API')
            for title in ['Mobile checkout: keep the order summary visible', 'Review account settings on small screens', 'Polish attachment upload feedback']:
                request('POST', site + '/api/tasks', {'title': title, 'body': 'Verify the mobile layout and keyboard interactions.', 'execute': False})
            devices = json.loads(subprocess.check_output(['xcrun', 'simctl', 'list', 'devices', 'available', '-j']))
            choices = [(runtime, d) for runtime, ds in devices['devices'].items()
                       if 'iOS' in runtime for d in ds if d['name'].startswith('iPhone') and d.get('isAvailable')]
            assert choices, 'Runner has no preinstalled iPhone simulator; do not download a runtime'
            sdk = subprocess.check_output(['xcrun', '--sdk', 'iphonesimulator', '--show-sdk-version'], text=True).strip()
            choices = [(r, d) for r, d in choices if r.endswith('iOS-' + sdk.replace('.', '-'))]
            assert choices, f'No installed simulator matching selected Xcode SDK {sdk}'
            runtime, device = sorted(choices, key=lambda pair: (pair[1]['name'] != 'iPhone 16', pair[1]['name']))[0]
            print(f'Starting {device["name"]} on {runtime}', flush=True)
            (artifacts / 'device.json').write_text(json.dumps({'runtime': runtime, 'device': device}, indent=2))
            start(['appium', '--address', '127.0.0.1', '--port', '4723', '--log-level', 'info'], 'appium')
            wait_for(lambda: request('GET', appium + '/status'), 'Appium', 120)
            if device['state'] != 'Booted':
                subprocess.run(['xcrun', 'simctl', 'boot', device['udid']], check=True, timeout=30)
            subprocess.run(['xcrun', 'simctl', 'bootstatus', device['udid'], '-b'], check=True, timeout=150)
            print('Simulator boot complete; starting Safari automation', flush=True)
            result = request('POST', appium + '/session', {'capabilities': {'alwaysMatch': {
                'platformName': 'iOS', 'browserName': 'Safari',
                'appium:automationName': 'XCUITest', 'appium:udid': device['udid'],
                'appium:platformVersion': sdk,
                'appium:deviceName': device['name'], 'appium:nativeWebTap': True,
                'appium:connectHardwareKeyboard': False,
                'appium:forceSimulatorSoftwareKeyboardPresence': True,
                'appium:isHeadless': True, 'appium:newCommandTimeout': 90,
                'appium:webviewAtomWaitTimeout': 20000,
                'appium:usePreinstalledWDA': True,
                'appium:prebuiltWDAPath': os.environ['QA_WDA_PATH'],
            }}}, timeout=300)
            session = result['value']['sessionId']
            print('Safari session ready', flush=True)
            wd('POST', '/url', {'url': site})
            wait_for(lambda: js('return !!document.querySelector("button svg.lucide-list-filter")'), 'board')
            before = evidence('01-board')['viewport']['height']
            click('button:has(svg.lucide-list-filter)')
            search = 'input[placeholder="Search title, body, or #123"]'
            click(search)
            wait_for(keyboard_visible, 'actual iOS software keyboard')
            wait_for(lambda: js('return visualViewport.height') < before - 80, 'keyboard reduces visible viewport')
            evidence('02-filter-keyboard')
            assert_input_visible(search)
            checks.append('Filter input remains above the real software keyboard without focus zoom')
            ident = element(search)
            wd('POST', f'/element/{ident}/value', {'text': 'Mobile checkout'})
            wait_for(lambda: js('return document.querySelector(arguments[0]).value', search) == 'Mobile checkout', 'multiword input')
            evidence('03-filter-text')
            click('[role="dialog"] [data-slot="dialog-close"]')
            wait_for(lambda: not keyboard_visible(), 'keyboard dismissal')
            wait_for(lambda: js('return !document.querySelector("[role=dialog]")'), 'filter dismissed')
            assert js('return document.body.innerText.includes("Mobile checkout")'), 'Filtered task missing'
            checks.append('Multiword filter and keyboard dismissal')
            # Reload clears transient filters and restores the normal board.
            wd('POST', '/url', {'url': site})
            wait_for(lambda: js('return !![...document.querySelectorAll("button")].find(e=>e.textContent.trim()==="New")'), 'New task button')
            new_id = js('return [...document.querySelectorAll("button")].find(e=>e.textContent.trim()==="New")')
            wd('POST', f'/element/{new_id["element-6066-11e4-a52e-4f735466cecf"]}/click', {})
            native_field('XCUIElementTypeTextField', 'What needs doing?')
            wait_for(keyboard_visible, 'task title keyboard')
            evidence('04-new-task-keyboard')
            assert_input_visible('#task-title')
            checks.append('New-task title stays visible with software keyboard')
            native_field('XCUIElementTypeTextView', 'Description (markdown)')
            wait_for(keyboard_visible, 'description keyboard')
            evidence('05-description-keyboard')
            assert_input_visible('#task-body')
            checks.append('Description can receive focus above software keyboard')
            click('[role="dialog"] [data-slot="dialog-close"]')
            wait_for(lambda: not keyboard_visible(), 'final keyboard dismissal')
            evidence('06-dismissed')
            checks.append('Form dismisses without creating or executing a task')
            print(json.dumps({'passed': checks}, indent=2), flush=True)
    except Exception as exc:
        if session:
            try:
                evidence('failure')
            except Exception as capture_error:
                print(f'Could not capture failure: {capture_error}', flush=True)
        (artifacts / 'result.json').write_text(json.dumps({'passed': checks, 'error': str(exc)}, indent=2))
        raise
    else:
        (artifacts / 'result.json').write_text(json.dumps({'passed': checks}, indent=2))
    finally:
        if session:
            try:
                wd('DELETE', '')
            except Exception:
                pass
        for process in reversed(processes):
            process.terminate()
            try:
                process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        for stream in streams:
            stream.close()


if __name__ == '__main__':
    main()
