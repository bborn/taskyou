#!/usr/bin/env python3
"""Capture a bounded, read-only GitHub traction snapshot for TaskYou."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable


DEFAULT_REPOSITORY = "bborn/taskyou"
DEFAULT_OUTPUT_DIR = Path(".taskyou-metrics")
REPOSITORY_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")


class GitHubAPIError(RuntimeError):
    pass


class GhClient:
    """Small seam around `gh api`; authentication stays inside gh."""

    def get(self, endpoint: str) -> Any:
        try:
            result = subprocess.run(
                ["gh", "api", endpoint],
                check=False,
                capture_output=True,
                text=True,
                timeout=30,
            )
        except subprocess.TimeoutExpired as error:
            raise GitHubAPIError("gh api timed out after 30 seconds") from error
        if result.returncode != 0:
            message = result.stderr.strip() or f"gh api exited {result.returncode}"
            raise GitHubAPIError(redact_secrets(message))
        try:
            return json.loads(result.stdout)
        except json.JSONDecodeError as error:
            raise GitHubAPIError(f"gh api returned invalid JSON: {error}") from error

    def get_releases(self, repository: str) -> list[dict[str, Any]]:
        releases: list[dict[str, Any]] = []
        for page in range(1, 101):
            batch = self.get(f"/repos/{repository}/releases?per_page=100&page={page}")
            if not isinstance(batch, list):
                raise GitHubAPIError("releases endpoint returned a non-list response")
            releases.extend(batch)
            if len(batch) < 100:
                return releases
        raise GitHubAPIError("release pagination exceeded 10,000 releases")


def redact_secrets(value: str) -> str:
    patterns = (
        r"(?i)bearer\s+[A-Za-z0-9._~+/=-]+",
        r"gh[pousr]_[A-Za-z0-9_]+",
        r"github_pat_[A-Za-z0-9_]+",
    )
    for pattern in patterns:
        value = re.sub(pattern, "[REDACTED]", value)
    return value


def utc_now() -> datetime:
    return datetime.now(timezone.utc).replace(microsecond=0)


def iso_z(value: datetime) -> str:
    return value.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")


def daily_window(rows: list[dict[str, Any]]) -> dict[str, Any]:
    dates = sorted(
        row["timestamp"][:10]
        for row in rows
        if isinstance(row, dict) and isinstance(row.get("timestamp"), str)
    )
    return {
        "kind": "rolling_14_day_window",
        "start": dates[0] if dates else None,
        "end": dates[-1] if dates else None,
        "dates_from": "returned_daily_series",
    }


def point_in_time_window(captured_at: str) -> dict[str, Any]:
    return {"kind": "point_in_time", "as_of": captured_at}


def available(raw: Any, observation: Any, window: dict[str, Any]) -> dict[str, Any]:
    return {
        "available": True,
        "error": None,
        "source_window": window,
        "observation": observation,
        "raw": raw,
    }


def unavailable(error: Exception, window: dict[str, Any]) -> dict[str, Any]:
    return {
        "available": False,
        "error": redact_secrets(str(error)),
        "source_window": window,
        "observation": None,
        "raw": None,
    }


def safe_fetch(
    fetch: Callable[[], Any],
    normalize: Callable[[Any], Any],
    window: Callable[[Any], dict[str, Any]],
    fallback_window: dict[str, Any],
) -> dict[str, Any]:
    try:
        raw = fetch()
        return available(raw, normalize(raw), window(raw))
    except (GitHubAPIError, OSError, ValueError, TypeError, KeyError) as error:
        return unavailable(error, fallback_window)


def normalize_repository(raw: dict[str, Any]) -> dict[str, Any]:
    return {
        "stars": raw["stargazers_count"],
        "forks": raw["forks_count"],
        "topics": raw.get("topics", []),
    }


def normalize_traffic(raw: dict[str, Any], series_key: str) -> dict[str, Any]:
    return {
        "aggregate": {
            "count": raw["count"],
            "uniques": raw["uniques"],
            "semantics": "window aggregate; do not sum daily uniques or overlapping snapshots",
        },
        "daily": raw.get(series_key, []),
    }


def normalize_releases(raw: list[dict[str, Any]]) -> dict[str, Any]:
    releases = []
    for release in raw:
        releases.append(
            {
                "tag_name": release.get("tag_name"),
                "published_at": release.get("published_at"),
                "draft": bool(release.get("draft")),
                "prerelease": bool(release.get("prerelease")),
                "assets": [
                    {
                        "name": asset.get("name"),
                        "download_count": asset.get("download_count"),
                    }
                    for asset in release.get("assets", [])
                ],
            }
        )
    published = [
        release
        for release in releases
        if release["published_at"] and not release["draft"]
    ]
    latest_published = max(published, key=lambda release: release["published_at"], default=None)
    if latest_published:
        latest_published = {
            **latest_published,
            "asset_downloads_total": sum(
                asset.get("download_count") or 0 for asset in latest_published["assets"]
            ),
        }
    return {
        "releases": releases,
        "latest_published": latest_published,
        "asset_downloads_total": sum(
            asset.get("download_count") or 0
            for release in releases
            for asset in release["assets"]
        ),
        "semantics": "lifetime release asset counters as of captured_at; not installs or users",
    }


def infer_undated_traffic_window(sources: dict[str, Any]) -> dict[str, Any]:
    candidates = []
    for source_name in ("traffic_views", "traffic_clones"):
        source = sources[source_name]
        window = source["source_window"]
        if source["available"] and window.get("start") and window.get("end"):
            candidates.append((window["start"], window["end"]))
    if candidates and all(candidate == candidates[0] for candidate in candidates):
        start, end = candidates[0]
        return {
            "kind": "rolling_14_day_window",
            "start": start,
            "end": end,
            "dates_from": "matching views/clones daily series; this endpoint omits dates",
        }
    return {
        "kind": "rolling_14_day_window",
        "start": None,
        "end": None,
        "dates_from": "unavailable; this endpoint omits dates",
    }


def collect_snapshot(client: Any, repository: str, now: datetime) -> dict[str, Any]:
    captured_at = iso_z(now)
    rolling_fallback = {
        "kind": "rolling_14_day_window",
        "start": None,
        "end": None,
        "dates_from": "unavailable",
    }
    point_window = point_in_time_window(captured_at)
    sources: dict[str, Any] = {}

    sources["repository"] = safe_fetch(
        lambda: client.get(f"/repos/{repository}"),
        normalize_repository,
        lambda _raw: point_window,
        point_window,
    )
    sources["traffic_views"] = safe_fetch(
        lambda: client.get(f"/repos/{repository}/traffic/views"),
        lambda raw: normalize_traffic(raw, "views"),
        lambda raw: daily_window(raw.get("views", [])),
        rolling_fallback,
    )
    sources["traffic_clones"] = safe_fetch(
        lambda: client.get(f"/repos/{repository}/traffic/clones"),
        lambda raw: normalize_traffic(raw, "clones"),
        lambda raw: daily_window(raw.get("clones", [])),
        rolling_fallback,
    )

    inferred_window = infer_undated_traffic_window(sources)
    for name, endpoint in (
        ("traffic_referrers", "popular/referrers"),
        ("traffic_paths", "popular/paths"),
    ):
        sources[name] = safe_fetch(
            lambda endpoint=endpoint: client.get(f"/repos/{repository}/traffic/{endpoint}"),
            lambda raw: raw,
            lambda _raw: inferred_window,
            inferred_window,
        )

    sources["releases"] = safe_fetch(
        lambda: client.get_releases(repository),
        normalize_releases,
        lambda _raw: point_window,
        point_window,
    )

    return {
        "schema_version": 1,
        "captured_at": captured_at,
        "repository": repository,
        "measurement_scope": "read-only GitHub snapshot",
        "snapshot_policy": (
            "Keep snapshots separate. Traffic windows overlap and must not be added together."
        ),
        "sources": sources,
    }


def fmt_window(source: dict[str, Any]) -> str:
    window = source["source_window"]
    if window["kind"] == "point_in_time":
        return f"as of {window['as_of']}"
    if window.get("start") and window.get("end"):
        return f"{window['start']} through {window['end']}"
    return "rolling 14-day window; exact returned dates unavailable"


def render_report(snapshot: dict[str, Any]) -> str:
    sources = snapshot["sources"]
    lines = [
        "# TaskYou GitHub traction snapshot",
        "",
        f"Captured at: {snapshot['captured_at']}",
        f"Repository: {snapshot['repository']}",
        "",
        "## Known from GitHub",
        "",
    ]

    repository = sources["repository"]
    if repository["available"]:
        value = repository["observation"]
        topics = ", ".join(value["topics"]) or "none"
        lines.append(f"- Repository: {value['stars']} stars, {value['forks']} forks; topics: {topics}.")
    else:
        lines.append(f"- Repository metadata unavailable: {repository['error']}")

    for name, label, unique_label in (
        ("traffic_views", "Views", "aggregate unique visitors"),
        ("traffic_clones", "Clones", "aggregate unique cloners"),
    ):
        source = sources[name]
        if source["available"]:
            aggregate = source["observation"]["aggregate"]
            lines.append(
                f"- {label} ({fmt_window(source)}): {aggregate['count']} total events, "
                f"{aggregate['uniques']} {unique_label}."
            )
        else:
            lines.append(f"- {label} unavailable: {source['error']}")

    referrers = sources["traffic_referrers"]
    if referrers["available"]:
        rendered = ", ".join(
            f"{row.get('referrer')} {row.get('count')} total/{row.get('uniques')} unique"
            for row in referrers["observation"]
        ) or "none returned"
        lines.append(f"- Referrers ({fmt_window(referrers)}): {rendered}.")
    else:
        lines.append(f"- Referrers unavailable: {referrers['error']}")

    paths = sources["traffic_paths"]
    if paths["available"]:
        rendered = ", ".join(
            f"{row.get('path')} {row.get('count')} total/{row.get('uniques')} unique"
            for row in paths["observation"]
        ) or "none returned"
        lines.append(f"- Popular paths ({fmt_window(paths)}): {rendered}.")
    else:
        lines.append(f"- Popular paths unavailable: {paths['error']}")

    releases = sources["releases"]
    if releases["available"]:
        value = releases["observation"]
        lines.append(
            f"- Release assets: {value['asset_downloads_total']} lifetime downloads across "
            f"{len(value['releases'])} returned releases, as of capture time."
        )
        latest = value["latest_published"]
        if latest:
            lines.append(
                f"- Latest published release {latest['tag_name']}: "
                f"{latest['asset_downloads_total']} lifetime asset downloads as of capture time."
            )
    else:
        lines.append(f"- Release asset counts unavailable: {releases['error']}")

    unavailable_sources = [name for name, source in sources.items() if not source["available"]]
    lines.extend(
        [
            "",
            "## Interpretation limits",
            "",
            "- GitHub's aggregate unique count is the window-level value. Daily unique counts are retained as raw observations but are not summed.",
            "- Consecutive traffic snapshots normally overlap. This command keeps them separate and does not calculate a cumulative total.",
            "- Clones and release downloads are not installs, users, successful setup, or activation.",
            "- Maintainer and CI activity can appear in views, clones, and download counts. Record known smoke tests beside the report before interpreting a small count as outside adoption.",
            "",
            "## Unknown with current instrumentation",
            "",
            "- Visits to taskyou.dev are unmeasured because the site has no analytics.",
            "- Successful installation and completion of a first TaskYou task are unmeasured.",
            "- Conversion between a GitHub visit, clone or download, installation, and first successful task is unmeasured.",
        ]
    )
    if unavailable_sources:
        lines.extend(
            [
                "",
                "Unavailable in this capture: " + ", ".join(unavailable_sources) + ". See the JSON errors.",
            ]
        )
    lines.extend(
        [
            "",
            "## Possible next measurements (separate approval required)",
            "",
            "- Add a privacy-conscious pageview counter for taskyou.dev to measure visits and outbound install clicks.",
            "- Add an explicitly approved, documented product event for successful setup and first completed task, with a clear opt-out and no task content.",
            "",
        ]
    )
    return "\n".join(lines)


def write_snapshot(snapshot: dict[str, Any], output_dir: Path) -> tuple[Path, Path]:
    output_dir.mkdir(parents=True, exist_ok=True)
    stamp = snapshot["captured_at"].replace(":", "").replace("-", "")
    stem = f"github-traction-{stamp}"
    json_path = output_dir / f"{stem}.json"
    report_path = output_dir / f"{stem}.md"
    json_path.write_text(json.dumps(snapshot, indent=2, sort_keys=True) + "\n")
    report_path.write_text(render_report(snapshot))
    return json_path, report_path


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", default=DEFAULT_REPOSITORY, help="GitHub repository as owner/name")
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=DEFAULT_OUTPUT_DIR,
        help="private ignored output directory (default: .taskyou-metrics)",
    )
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    if not REPOSITORY_RE.fullmatch(args.repo):
        print("error: --repo must be an owner/name repository", file=sys.stderr)
        return 2
    snapshot = collect_snapshot(GhClient(), args.repo, utc_now())
    json_path, report_path = write_snapshot(snapshot, args.output_dir)
    print(render_report(snapshot), end="")
    print(f"Raw snapshot: {json_path}", file=sys.stderr)
    print(f"Report: {report_path}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
