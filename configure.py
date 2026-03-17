#!/usr/bin/env python3
"""
CNPG REST Server - Build Configuration

Writes config.json (gitignored); image tags are derived from git state at
build time and are never stored in config.json.

Usage:
    python3 configure.py             # interactive — prompts for all values
    python3 configure.py --from-env  # non-interactive, reads env vars
    python3 configure.py --compute-tag  # print computed tag to stdout (Makefile)

    # Specify values directly (skips prompts for those fields)
    python3 configure.py \\
        --registry ghcr.io/myorg \\
        --image cnpg-rest-server

Tag rules (--compute-tag):
    Uncommitted changes present      → latest
    On branch 'main', tag at HEAD    → that git tag
    On branch 'main', no tag at HEAD → latest
    On any other branch              → <branch>-<short-hash>
    Branch names with '/' are sanitised to '-' for Docker tag compatibility.
"""

import json
import os
import subprocess
import sys
import argparse
from pathlib import Path
from typing import Optional

CONFIG_FILE = "config.json"


# ─── Tag computation ─────────────────────────────────────────────────────────

def compute_tag() -> str:
    """Derive the Docker image tag from the current git state.

    Falls back to 'latest' when not inside a git repository (e.g. bare
    workspace, CI without git history, or local dev without git init).
    """

    def run(*cmd) -> Optional[str]:
        """Run a command; return stripped stdout on success, None on error."""
        result = subprocess.run(cmd, capture_output=True, text=True)
        if result.returncode != 0:
            return None
        return result.stdout.strip()

    # Not a git repo at all → latest
    branch = run("git", "rev-parse", "--abbrev-ref", "HEAD")
    if branch is None:
        return "latest"

    short_hash = run("git", "rev-parse", "--short", "HEAD") or "unknown"

    # Uncommitted changes to tracked files → latest.
    # -uno suppresses untracked files so they don't affect the tag.
    if run("git", "status", "--porcelain", "-uno"):
        return "latest"

    if branch == "main":
        tags = run("git", "tag", "--points-at", "HEAD")
        if tags:
            return tags.splitlines()[0]
        return "latest"

    # Non-main branch: sanitise '/' → '-' for Docker tag compatibility
    safe_branch = branch.replace("/", "-")
    return f"{safe_branch}-{short_hash}"


# ─── Config I/O ──────────────────────────────────────────────────────────────

def load_saved_config(output_dir: Path) -> dict:
    """Load config.json for interactive defaults."""
    path = output_dir / CONFIG_FILE
    if path.exists():
        try:
            return json.loads(path.read_text())
        except (json.JSONDecodeError, OSError):
            pass
    return {}


def save_config(config: dict, output_dir: Path) -> Path:
    path = output_dir / CONFIG_FILE
    path.write_text(json.dumps(config, indent=2) + "\n")
    print(f"  Saved:   {path}")
    return path


# ─── Interactive / env-var prompting ─────────────────────────────────────────

def get_env_or_prompt(
    env_var: str,
    prompt: str,
    required: bool = False,
    default: Optional[str] = None,
) -> Optional[str]:
    value = os.environ.get(env_var)
    if value:
        print(f"  {prompt}: {value} (from {env_var})")
        return value

    if sys.stdin.isatty():
        if default:
            user_input = input(f"  {prompt} [{default}]: ").strip()
            return user_input if user_input else default
        else:
            user_input = input(f"  {prompt}: ").strip()
            if required and not user_input:
                print(f"    Error: {prompt} is required")
                sys.exit(1)
            return user_input if user_input else None
    elif required and not default:
        print(f"Error: {env_var} environment variable required in non-interactive mode")
        sys.exit(1)
    return default


# ─── Config collection ───────────────────────────────────────────────────────

def collect_config(args: argparse.Namespace, saved: dict) -> dict:
    saved_server = saved.get("server", {})

    config: dict = {"server": {}}

    print("\n=== Server Target Image ===")
    print("  Tag is computed automatically from git state — do not set it here.")

    config["server"]["registry"] = args.registry or get_env_or_prompt(
        "REGISTRY", "Registry (e.g. ghcr.io/myorg, docker.io/myuser)",
        required=True, default=saved_server.get("registry"),
    )
    config["server"]["image"] = args.image or get_env_or_prompt(
        "IMAGE", "Image name",
        default=saved_server.get("image", "cnpg-rest-server"),
    )

    return config


# ─── Entry point ─────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="Configure build targets for the CNPG REST server image",
    )
    parser.add_argument("--compute-tag", action="store_true",
                        help="Print the git-derived image tag and exit (used by Makefile)")
    parser.add_argument("--from-env", action="store_true",
                        help="Read all values from environment variables (non-interactive)")
    parser.add_argument("--output-dir", type=Path, default=Path("."),
                        help="Directory to write config.json (default: .)")
    parser.add_argument("--registry",
                        help="Target image registry (e.g. ghcr.io/myorg)")
    parser.add_argument("--image",
                        help="Target image name (default: cnpg-rest-server)")

    args = parser.parse_args()

    if args.compute_tag:
        print(compute_tag())
        return

    print("=== CNPG REST Server - Build Configuration ===")

    saved = load_saved_config(args.output_dir)
    config = collect_config(args, saved)

    print()
    save_config(config, args.output_dir)

    tag = compute_tag()
    print(f"\n  Current git tag: {tag}")
    print("\n=== Next Steps ===")
    print("  make build         # Compile the server binary")
    print("  make docker-build  # Build the container image")
    print("  make docker-push   # Build and push the container image")
    print("  make deploy        # Apply Kubernetes manifests")


if __name__ == "__main__":
    main()
