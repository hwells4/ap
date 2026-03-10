#!/bin/sh
# Install ap skill for Claude Code
set -e

SKILL_DIR="$HOME/.claude/skills/ap"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

mkdir -p "$SKILL_DIR/references"
cp "$SCRIPT_DIR/SKILL.md" "$SKILL_DIR/SKILL.md"
cp "$SCRIPT_DIR/references/contract.md" "$SKILL_DIR/references/contract.md"

echo "ap skill installed to $SKILL_DIR"
