#!/usr/bin/env python3
"""Validate DFM agent docs, skill mirrors, and optional source revisions."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
REQUIRED_AGENT_DOCS = {
    "README.md",
    "architecture.md",
    "data-and-state.md",
    "debugging.md",
    "development-and-tests.md",
    "source-status.md",
    "workers-and-messages.md",
}
REQUIRED_SKILLS = {
    "dfm-core",
    "dfm-blast-radius",
    "dfm-context-research",
    "dfm-decision-capture",
    "dfm-delegation",
    "dfm-incident-investigation",
    "dfm-flow-trace",
    "dfm-implementation-plan",
    "dfm-pr-review",
    "dfm-ticket-implementation",
    "dfm-qa-validation",
    "dfm-pr-remediation",
    "dfm-git-pr-sync",
    "dfm-pr-delivery",
    "dfm-plan-verify",
    "dfm-local-development",
    "dfm-production-access",
    "dfm-work-map",
    "dfm-tdd",
    "dfm-legacy-sam",
    "dfm-sam-nextgen",
    "dfm-shopify-fulfillment",
    "dfm-coesp-smart-license",
}
CURATED_ATL = {"skill-registry.md"}
FORBIDDEN_PUNCTUATION = {"—": "em dash", "“": "left curly quote", "”": "right curly quote"}
LINK_RE = re.compile(r"\[[^\]]+\]\(([^)]+)\)")


def git_head(path: Path) -> str:
    return subprocess.check_output(["git", "-C", str(path), "rev-parse", "HEAD"], text=True).strip()


def check_markdown_links(path: Path, problems: list[str]) -> None:
    text = path.read_text(encoding="utf-8")
    for target in LINK_RE.findall(text):
        target = target.split("#", 1)[0]
        if not target or "://" in target or target.startswith("mailto:"):
            continue
        resolved = (path.parent / target).resolve()
        if not resolved.exists():
            problems.append(f"{path.relative_to(ROOT)}: missing link target {target}")


def curated_markdown() -> list[Path]:
    paths = [ROOT / "AGENTS.md", ROOT / "README.md", ROOT / "CLAUDE.md", ROOT / "THIRD_PARTY_NOTICES.md"]
    paths.extend((ROOT / "docs").rglob("*.md"))
    paths.extend(ROOT / ".atl" / name for name in CURATED_ATL)
    paths.extend((ROOT / "skills").glob("*/SKILL.md"))
    paths.extend((ROOT / "skills").glob("*/references/*.md"))
    return sorted(set(paths))


def require_text(path: Path, needles: list[str], problems: list[str]) -> None:
    if not path.exists():
        problems.append(f"missing source: {path}")
        return
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            problems.append(f"{path}: expected source token {needle!r}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--main", type=Path, help="Current DFM main checkout")
    parser.add_argument("--handbook", type=Path, help="Handbook checkout")
    args = parser.parse_args()
    problems: list[str] = []

    agent_docs = ROOT / "docs"
    present = {path.name for path in agent_docs.glob("*.md")}
    for name in sorted(REQUIRED_AGENT_DOCS - present):
        problems.append(f"missing agent doc: docs/{name}")

    for path in curated_markdown():
        if not path.exists():
            problems.append(f"missing curated file: {path.relative_to(ROOT)}")
            continue
        check_markdown_links(path, problems)
        text = path.read_text(encoding="utf-8")
        for character, label in FORBIDDEN_PUNCTUATION.items():
            if character in text:
                problems.append(f"{path.relative_to(ROOT)}: contains {label}")

    agents_size = (ROOT / "AGENTS.md").stat().st_size
    if agents_size > 4096:
        problems.append(f"AGENTS.md exceeds 4096 bytes: {agents_size}")
    readme_size = (ROOT / "README.md").stat().st_size
    if readme_size > 4096:
        problems.append(f"README.md exceeds 4096 bytes: {readme_size}")
    for path in agent_docs.glob("*.md"):
        if path.stat().st_size > 12000:
            problems.append(f"{path.relative_to(ROOT)} exceeds 12000 bytes")

    canonical = {path.parent.name: path for path in (ROOT / "skills").glob("*/SKILL.md")}
    missing_skills = REQUIRED_SKILLS - set(canonical)
    extra_skills = set(canonical) - REQUIRED_SKILLS
    if missing_skills:
        problems.append(f"missing canonical skills: {', '.join(sorted(missing_skills))}")
    if extra_skills:
        problems.append(f"unexpected canonical skills: {', '.join(sorted(extra_skills))}")
    if (ROOT / ".agents").exists() or (ROOT / ".claude").exists():
        problems.append("legacy skill mirror directories must not exist")
    if not (ROOT / "licenses" / "pstack-MIT.txt").exists():
        problems.append("missing pstack MIT license")
    if not (ROOT / "licenses" / "cursor-team-kit-MIT.txt").exists():
        problems.append("missing cursor-team-kit MIT license")
    if not (ROOT / "licenses" / "mattpocock-skills-MIT.txt").exists():
        problems.append("missing Matt Pocock skills MIT license")

    revisions = json.loads((ROOT / ".atl" / "source-revisions.json").read_text(encoding="utf-8"))
    if args.main:
        main_path = args.main.resolve()
        if git_head(main_path) != revisions["main"]["commit"]:
            problems.append("main checkout does not match .atl/source-revisions.json")
        require_text(main_path / ".python-version", ["3.13.3"], problems)
        require_text(main_path / "requirements.txt", ["django==5.2.17"], problems)
        require_text(main_path / "pyproject.toml", ["[tool.ruff]", "[tool.bandit]", "[tool.pytest.ini_options]"], problems)
        require_text(
            main_path / "digital_fulfillment" / "api" / "urls.py",
            ["pos/provision", "pos/cancel", "sam/pickup-status"],
            problems,
        )
        require_text(
            main_path / "digital_fulfillment" / "api" / "authentication.py",
            ["X-API-Key"],
            problems,
        )
        require_text(
            main_path / "digital_fulfillment" / "sam_nextgen_integration" / "constants.py",
            ["ORDER_TYPE_PHYSICAL_SALE", "ORDER_TYPE_RENTAL_SALE", "ORDER_TYPE_AUTO_SALE"],
            problems,
        )
        require_text(
            main_path / "digital_fulfillment" / "digital_fulfillment" / "settings.py",
            ["sam_nextgen_integration.apps.SamNextgenIntegrationConfig", "DATA_QUEUE_SAMNG_ORDERS", "ORDER_PROCESSING_OBSERVABILITY_ENABLED"],
            problems,
        )
        require_text(
            main_path / "digital_fulfillment" / "sam_integration" / "models.py",
            ["class SAMOutboxEvent", "class SAMRefundLineLifecycleEvent", "class PickupLocationCache"],
            problems,
        )
        require_text(
            main_path / "digital_fulfillment" / "shopify_orders" / "models.py",
            ["class FulfillmentAudit", "class OrderProcessingFailure", "fulfillment_audit_active_uniq"],
            problems,
        )
        require_text(
            main_path / "Makefile",
            ["runchecks:", "djangotest:", "process_sam_nextgen_orders:", "payload_generate:"],
            problems,
        )
        for relative in [
            ".github/copilot-instructions.md",
            ".github/instructions/sam-review.instructions.md",
            ".github/instructions/shopify-review.instructions.md",
            "digital_fulfillment/sam_nextgen_integration/OPERATIONS.md",
            "ops/order-processing-observability/metric-contract.v1.json",
        ]:
            if not (main_path / relative).exists():
                problems.append(f"main missing documented source: {relative}")

    if args.handbook and git_head(args.handbook.resolve()) != revisions["handbook"]["commit"]:
        problems.append("handbook checkout does not match .atl/source-revisions.json")

    if problems:
        print("\n".join(f"- {problem}" for problem in problems), file=sys.stderr)
        return 1

    print(f"Validated {len(present)} agent docs, {len(canonical)} canonical skills, and source contracts.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
