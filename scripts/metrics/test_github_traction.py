#!/usr/bin/env python3

import importlib.util
import json
import subprocess
import tempfile
import unittest
from datetime import datetime, timezone
from pathlib import Path
from unittest import mock


MODULE_PATH = Path(__file__).with_name("github_traction.py")
SPEC = importlib.util.spec_from_file_location("github_traction", MODULE_PATH)
github_traction = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(github_traction)


class FixtureClient:
    def __init__(self, responses):
        self.responses = responses

    def get(self, endpoint):
        response = self.responses[endpoint]
        if isinstance(response, Exception):
            raise response
        return response

    def get_releases(self, _repository):
        response = self.responses["releases"]
        if isinstance(response, Exception):
            raise response
        return response


def fixture_responses():
    daily_views = [
        {"timestamp": "2026-08-23T00:00:00Z", "count": 10, "uniques": 6},
        {"timestamp": "2026-09-05T00:00:00Z", "count": 103, "uniques": 34},
    ]
    daily_clones = [
        {"timestamp": "2026-08-23T00:00:00Z", "count": 100, "uniques": 50},
        {"timestamp": "2026-09-05T00:00:00Z", "count": 645, "uniques": 200},
    ]
    return {
        "/repos/bborn/taskyou": {"stargazers_count": 58, "forks_count": 5, "topics": ["tasks"]},
        "/repos/bborn/taskyou/traffic/views": {"count": 113, "uniques": 35, "views": daily_views},
        "/repos/bborn/taskyou/traffic/clones": {"count": 745, "uniques": 216, "clones": daily_clones},
        "/repos/bborn/taskyou/traffic/popular/referrers": [
            {"referrer": "taskyou.dev", "count": 18, "uniques": 8},
            {"referrer": "Google", "count": 7, "uniques": 5},
        ],
        "/repos/bborn/taskyou/traffic/popular/paths": [
            {"path": "/bborn/taskyou", "title": "TaskYou", "count": 90, "uniques": 30}
        ],
        "releases": [
            {
                "tag_name": "v0.3.5",
                "published_at": None,
                "draft": True,
                "prerelease": False,
                "assets": [],
            },
            {
                "tag_name": "v0.3.28",
                "published_at": "2026-09-07T00:00:00Z",
                "draft": False,
                "prerelease": False,
                "assets": [{"name": "install.sh", "download_count": 1}],
            }
        ],
    }


class GitHubTractionTest(unittest.TestCase):
    def test_gh_request_has_a_finite_timeout(self):
        with mock.patch.object(
            github_traction.subprocess,
            "run",
            side_effect=subprocess.TimeoutExpired(["gh", "api"], 30),
        ) as run:
            with self.assertRaisesRegex(github_traction.GitHubAPIError, "timed out after 30 seconds"):
                github_traction.GhClient().get("/repos/bborn/taskyou")

        self.assertEqual(run.call_args.kwargs["timeout"], 30)

    def test_snapshot_preserves_window_aggregate_and_daily_counts(self):
        snapshot = github_traction.collect_snapshot(
            FixtureClient(fixture_responses()),
            "bborn/taskyou",
            datetime(2026, 9, 7, 12, 0, tzinfo=timezone.utc),
        )

        views = snapshot["sources"]["traffic_views"]
        self.assertEqual(views["observation"]["aggregate"]["uniques"], 35)
        self.assertEqual(sum(row["uniques"] for row in views["observation"]["daily"]), 40)
        self.assertEqual(views["source_window"]["start"], "2026-08-23")
        self.assertEqual(views["source_window"]["end"], "2026-09-05")
        self.assertIn("Keep snapshots separate", snapshot["snapshot_policy"])

        report = github_traction.render_report(snapshot)
        self.assertIn("35 aggregate unique visitors", report)
        self.assertIn("216 aggregate unique cloners", report)
        self.assertNotIn("40 aggregate unique visitors", report)
        self.assertIn("Latest published release v0.3.28: 1 lifetime asset downloads", report)
        self.assertIn("Consecutive traffic snapshots normally overlap", report)
        self.assertIn("Maintainer and CI activity", report)
        self.assertIn("Visits to taskyou.dev are unmeasured", report)

    def test_errors_are_recorded_without_aborting_or_leaking_tokens(self):
        responses = fixture_responses()
        responses["/repos/bborn/taskyou/traffic/clones"] = github_traction.GitHubAPIError(
            "HTTP 403 bearer ghp_supersecret"
        )
        snapshot = github_traction.collect_snapshot(
            FixtureClient(responses),
            "bborn/taskyou",
            datetime(2026, 9, 7, 12, 0, tzinfo=timezone.utc),
        )

        clones = snapshot["sources"]["traffic_clones"]
        self.assertFalse(clones["available"])
        self.assertEqual(clones["error"], "HTTP 403 [REDACTED]")
        self.assertTrue(snapshot["sources"]["traffic_views"]["available"])

    def test_writes_timestamped_json_and_report(self):
        snapshot = github_traction.collect_snapshot(
            FixtureClient(fixture_responses()),
            "bborn/taskyou",
            datetime(2026, 9, 7, 12, 0, tzinfo=timezone.utc),
        )
        with tempfile.TemporaryDirectory() as directory:
            json_path, report_path = github_traction.write_snapshot(snapshot, Path(directory))
            stored = json.loads(json_path.read_text())

            self.assertEqual(stored["captured_at"], "2026-09-07T12:00:00Z")
            self.assertTrue(report_path.exists())
            self.assertIn("raw", stored["sources"]["repository"])


if __name__ == "__main__":
    unittest.main()
