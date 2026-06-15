#!/bin/sh
# Returns the git describe output from the project directory.
# Falls back to commit SHA if no tags exist, or "unknown" if not a git repo.
git -C "$PROJECT_DIR" describe --tags --always 2>/dev/null || echo "unknown"
