#!/usr/bin/env python3
"""Measure highlighted-task frames in an isolated QA TUI.

Usage: TY_QA_ROOT=/tmp/ty-qa TY_QA_SID=qa python3 scripts/qa/ty-qa-scroll.py label [busy]
TY_QA_SCROLL_RATE defaults to 30; TY_QA_SCROLL_STEPS defaults to 300.
"""
import argparse
import json
import os
import re
import sqlite3
import statistics
import subprocess
import threading
import time

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("label")
parser.add_argument("mode", nargs="?", choices=["busy"])
args = parser.parse_args()
root = os.environ.get("TY_QA_ROOT", "/tmp/ty-qa")
target = "task-ui-" + os.environ.get("TY_QA_SID", "qa") + ":tui"
rate = float(os.environ.get("TY_QA_SCROLL_RATE", "30"))
steps = int(os.environ.get("TY_QA_SCROLL_STEPS", "300"))
if rate <= 0 or steps < 2:
    raise SystemExit("Rate must be positive and steps must be at least two")
if args.mode == "busy" and not os.path.realpath(root).startswith(("/private/tmp/", "/tmp/")):
    raise SystemExit("Busy-mode fixtures must live under /tmp")
env = dict(os.environ, TMUX_TMPDIR=root + "/tmux")
env.pop("TMUX", None)
# Selected cards have a bold ID; ordinary cards style the ID without bold.
pattern = re.compile(rb"\x1b\[1m(?:\x1b\[[0-9;]*m)*[^#\n\x1b]*#(\d+)")


def tmux(*command):
    return subprocess.check_output(
        ["tmux", *command], env=env, stderr=subprocess.DEVNULL, timeout=5
    )


def selected():
    match = pattern.search(tmux("capture-pane", "-p", "-e", "-t", target))
    return int(match[1]) if match else None


for _ in range(600):
    initial = selected()
    if initial:
        break
    time.sleep(0.02)
else:
    raise SystemExit("No selected card; launch the QA TUI first")

with sqlite3.connect("file:" + root + "/tasks.db?mode=ro", uri=True) as database:
    ids = [row[0] for row in database.execute(
        "SELECT id FROM tasks WHERE status='backlog' AND deleted_at IS NULL "
        "ORDER BY pinned DESC,created_at DESC,id DESC"
    )]
    active = database.execute(
        "SELECT id FROM tasks WHERE status='processing' AND deleted_at IS NULL LIMIT 1"
    ).fetchone()
if initial not in ids:
    raise SystemExit("Focus Backlog before running this test")
if ids.index(initial) + steps >= len(ids):
    raise SystemExit("Seed more ordinary backlog tasks for the requested scroll distance")
if args.mode == "busy" and active is None:
    raise SystemExit("Busy mode requires an isolated processing fixture task")

stop = threading.Event()


def write_logs():
    with sqlite3.connect(root + "/tasks.db", timeout=5) as database:
        while not stop.is_set():
            database.execute(
                "INSERT INTO task_logs(task_id,line_type,content,created_at) "
                "VALUES(?,'output',?,datetime('now'))",
                (active[0], "QA log " + str(time.monotonic())),
            )
            database.commit()
            stop.wait(0.02)


def measure(direction, delta):
    start_id = selected()
    start_index = ids.index(start_id)
    sent, frames = {}, []
    done = threading.Event()
    errors = []

    def expected(n):
        return ids[start_index + delta * n]

    def send_keys():
        try:
            start = time.perf_counter()
            for n in range(1, steps + 1):
                delay = start + (n - 1) / rate - time.perf_counter()
                if delay > 0:
                    time.sleep(delay)
                sent[expected(n)] = time.perf_counter()
                tmux("send-keys", "-t", target, direction)
        except Exception as error:
            errors.append(error)
        finally:
            done.set()

    sender = threading.Thread(target=send_keys)
    sender.start()
    previous = start_id
    start = time.perf_counter()
    while time.perf_counter() - start < steps / rate + 10:
        task_id, now = selected(), time.perf_counter()
        if task_id is not None and task_id != previous:
            frames.append({
                "id": task_id,
                "time": now,
                "latency_ms": (now - sent[task_id]) * 1000 if task_id in sent else None,
            })
            previous = task_id
        if done.is_set() and (errors or task_id == expected(steps)):
            break
        time.sleep(0.003)
    sender.join()
    if errors:
        raise errors[0]
    if task_id != expected(steps):
        raise RuntimeError("Input queue did not reach the expected final task")
    latency = sorted(frame["latency_ms"] for frame in frames if frame["latency_ms"] is not None)
    if not latency:
        raise RuntimeError("No selection frames observed")
    gaps = [(frames[i]["time"] - frames[i - 1]["time"]) * 1000 for i in range(1, len(frames))]
    result = {
        "direction": direction, "keys": steps, "rate": rate,
        "visible_selections": len(frames), "final_id": task_id, "expected": expected(steps),
        "median_ms": round(statistics.median(latency), 2),
        "p95_ms": round(latency[int(len(latency) * 0.95)], 2),
        "max_ms": round(max(latency), 2),
        "max_frame_gap_ms": round(max(gaps, default=0), 2),
    }
    print(json.dumps(result), flush=True)
    return {**result, "frames": frames}


writer = threading.Thread(target=write_logs, daemon=True) if args.mode == "busy" else None
if writer:
    writer.start()
try:
    results = [measure("Down", 1)]
    time.sleep(0.5)
    results.append(measure("Up", -1))
    with open(root + "/" + os.path.basename(args.label) + ".json", "w") as output:
        json.dump(results, output, indent=2)
finally:
    stop.set()
    if writer:
        writer.join(timeout=6)
