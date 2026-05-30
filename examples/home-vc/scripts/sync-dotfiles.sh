#!/bin/bash
# Sync dotfiles from a git repo on container start
set -e

DOTFILES_REPO="${DOTFILES_REPO:-}"
if [ -z "$DOTFILES_REPO" ]; then
  echo "DOTFILES_REPO not set, skipping dotfiles sync"
  exit 0
fi

if [ -d "$HOME/.dotfiles" ]; then
  cd "$HOME/.dotfiles" && git pull --ff-only
else
  git clone "$DOTFILES_REPO" "$HOME/.dotfiles"
fi

# Apply dotfiles (simple symlink approach)
if [ -f "$HOME/.dotfiles/install.sh" ]; then
  bash "$HOME/.dotfiles/install.sh"
fi
