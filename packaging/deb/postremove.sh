#!/bin/sh
set -e
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload >/dev/null || true
fi
# Data in /var/lib/tibi and the tibi user are kept: they hold your uploads.
