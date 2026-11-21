#!/usr/bin/env python3
"""Compute the nightly benchmark metrics and compare them with the baseline.

The nightly job runs the static engine over the committed corpus, which needs
no network and no model and is therefore deterministic. This script turns the
result CSV into a small metrics document, compares it with the committed
baseline (bench/results/baseline_metrics.json) and reports the delta.

Usage:

    bench-metrics.py RESULT.csv [--out metrics.json] [--baseline baseline.json]
                          [--max-regression 0.02] [--summary summary.md]

The exit status is 0 when the run is within the threshold and 1 when the
accuracy regressed by more than --max-regression.
"""

from __future__ import annotations

import argparse
import csv
import json
import os
import sys
from datetime import datetime, timezone
from typing import Any

SCHEMA_VERSION = "1"
ENGINE = "bandit"


def _truthy(value: str | None) -> bool:
    return (value or "").strip().lower() == "true"


def compute(csv_path: str) -> dict[str, Any]:
    """Compute the metrics of a benchmark result CSV.

    The corpus is paired: every row holds a vulnerable snippet ("before") and
    its fixed counterpart ("after"). The ground truth of the "before" column
    is therefore always "vulnerable", which means the precision cannot be
    computed from it — a false positive is a *patched* snippet that the
    engine flagged, and it is counted in the "after" column.
    """
    with open(csv_path, newline="", encoding="utf-8") as handle:
        rows = list(csv.DictReader(handle))

    before = {"decided": 0, "correct": 0}
    after = {"decided": 0, "correct": 0}
    pair_correct = 0
    flagged_before = 0
    flagged_after = 0
    true_positive = 0
    false_positive = 0

    for row in rows:
        if _truthy(row.get("bandit_completed_before")):
            before["decided"] += 1
            if _truthy(row.get("bandit_correct_before")):
                before["correct"] += 1
        if _truthy(row.get("bandit_completed_after")):
            after["decided"] += 1
            if _truthy(row.get("bandit_correct_after")):
                after["correct"] += 1
        if _truthy(row.get("bandit_pair_correct")):
            pair_correct += 1
        if row.get("bandit_classification_before") == "vulnerable":
            flagged_before += 1
            if row.get("expected_before") == "vulnerable":
                true_positive += 1
            else:
                false_positive += 1
        if row.get("bandit_classification_after") == "vulnerable":
            flagged_after += 1
            # A patched snippet that the engine flagged is a false
            # positive: the code is already fixed.
            false_positive += 1

    decided = before["decided"] + after["decided"]
    correct = before["correct"] + after["correct"]
    accuracy = (correct / decided) if decided else 0.0
    # The precision is over the flagged snippets of both columns: the
    # vulnerable ones it caught, and the patched ones it wrongly flagged.
    flagged = flagged_before + flagged_after
    precision = (true_positive / flagged) if flagged else 0.0
    recall = (true_positive / before["decided"]) if before["decided"] else 0.0
    fpr = (false_positive / after["decided"]) if after["decided"] else 0.0

    return {
        "schema_version": SCHEMA_VERSION,
        "engine": ENGINE,
        "rows": len(rows),
        "samples": decided,
        "decided": decided,
        "correct": correct,
        "accuracy": round(accuracy, 6),
        "precision": round(precision, 6),
        "recall": round(recall, 6),
        "false_positive_rate": round(fpr, 6),
        "flagged_before": flagged_before,
        "flagged_after": flagged_after,
        "false_positives": false_positive,
        "pair_correct": pair_correct,
        "before": {
            "decided": before["decided"],
            "correct": before["correct"],
            "accuracy": round(before["correct"] / before["decided"], 6)
            if before["decided"]
            else 0.0,
        },
        "after": {
            "decided": after["decided"],
            "correct": after["correct"],
            "accuracy": round(after["correct"] / after["decided"], 6)
            if after["decided"]
            else 0.0,
        },
    }


def compare(current: dict[str, Any], baseline: dict[str, Any] | None) -> dict[str, Any]:
    """Compare the current metrics with the baseline."""
    if not baseline:
        return {"has_baseline": False}
    base_accuracy = float(baseline.get("accuracy", 0.0))
    now = float(current.get("accuracy", 0.0))
    return {
        "has_baseline": True,
        "baseline_accuracy": base_accuracy,
        "current_accuracy": now,
        "delta": round(now - base_accuracy, 6),
        "baseline_correct": baseline.get("correct"),
        "current_correct": current.get("correct"),
        "baseline_generated_at": baseline.get("generated_at"),
    }


def render_summary(current: dict[str, Any], delta: dict[str, Any], limit: float) -> str:
    """Render the markdown summary of the run."""
    lines = [
        "## Nightly benchmark (static engine)",
        "",
        "| metric | value |",
        "|---|---|",
        f"| engine | `{current['engine']}` |",
        f"| rows | {current['rows']} |",
        f"| decided samples | {current['decided']} |",
        f"| correct | {current['correct']} |",
        f"| accuracy | {current['accuracy'] * 100:.2f}% |",
        f"| precision | {current['precision'] * 100:.2f}% |",
        f"| recall | {current['recall'] * 100:.2f}% |",
        f"| false-positive rate on the patched corpus | {current['false_positive_rate'] * 100:.2f}% |",
        f"| accuracy (vulnerable snippets) | {current['before']['accuracy'] * 100:.2f}% |",
        f"| accuracy (fixed snippets) | {current['after']['accuracy'] * 100:.2f}% |",
        f"| false positives on the fixed corpus | {current['false_positives']} |",
        "",
    ]
    if delta.get("has_baseline"):
        direction = "down" if delta["delta"] < 0 else "up"
        lines += [
            "### Delta against the committed baseline",
            "",
            f"- baseline accuracy: {delta['baseline_accuracy'] * 100:.2f}%",
            f"- current accuracy: {delta['current_accuracy'] * 100:.2f}%",
            f"- delta: {delta['delta'] * 100:+.2f} percentage points ({direction})",
            f"- regression threshold: {limit * 100:.2f} percentage points",
            "",
        ]
    else:
        lines += ["> No baseline was found; this run only reports its own numbers.", ""]
    return "\n".join(lines)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("csv", help="the benchmark result CSV")
    parser.add_argument("--out", help="write the metrics document here")
    parser.add_argument("--baseline", help="the committed baseline metrics document")
    parser.add_argument(
        "--max-regression",
        type=float,
        default=0.02,
        help="the accuracy drop that fails the job (default: 0.02, two percentage points)",
    )
    parser.add_argument("--summary", help="write the markdown summary here")
    args = parser.parse_args(argv)

    current = compute(args.csv)

    baseline: dict[str, Any] | None = None
    if args.baseline and os.path.exists(args.baseline):
        with open(args.baseline, encoding="utf-8") as handle:
            baseline = json.load(handle)

    delta = compare(current, baseline)

    document = dict(current)
    document["delta"] = delta
    document["generated_at"] = datetime.now(timezone.utc).replace(microsecond=0).isoformat()
    if args.out:
        os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
        with open(args.out, "w", encoding="utf-8") as handle:
            json.dump(document, handle, indent=2, sort_keys=True)
            handle.write("\n")

    summary = render_summary(current, delta, args.max_regression)
    if args.summary:
        os.makedirs(os.path.dirname(os.path.abspath(args.summary)), exist_ok=True)
        with open(args.summary, "w", encoding="utf-8") as handle:
            handle.write(summary)
    print(summary)

    if delta.get("has_baseline") and delta["delta"] < -args.max_regression:
        print(
            f"accuracy regressed by {abs(delta['delta']) * 100:.2f} percentage points, "
            f"which is more than the {args.max_regression * 100:.2f} point threshold",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
