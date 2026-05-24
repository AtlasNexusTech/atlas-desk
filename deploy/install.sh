#!/bin/bash
# Atlas Desk Agent — Install systemd service
set -e

BIN="$HOME/.local/bin/atlas-desk-agent"
SERVICE="$HOME/.config/systemd/user/atlas-desk-agent.service"

echo "◆ Atlas Desk Agent — Service Installer"
echo ""

# Copy binary
mkdir -p "$HOME/.local/bin"
cp atlas-desk-agent "$BIN"
chmod +x "$BIN"
echo "✓ Binary: $BIN"

# Install service
mkdir -p "$HOME/.config/systemd/user"
cp deploy/atlas-desk-agent.service "$SERVICE"
echo "✓ Service: $SERVICE"

# Reload + enable
systemctl --user daemon-reload
systemctl --user enable atlas-desk-agent.service
systemctl --user start atlas-desk-agent.service
echo "✓ Service started"

echo ""
echo "Commands:"
echo "  status:  systemctl --user status atlas-desk-agent"
echo "  logs:    journalctl --user -u atlas-desk-agent -f"
echo "  stop:    systemctl --user stop atlas-desk-agent"
echo "  disable: systemctl --user disable atlas-desk-agent"
