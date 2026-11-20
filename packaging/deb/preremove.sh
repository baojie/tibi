#!/bin/sh
set -e
if [ -d /run/systemd/system ] && [ "$1" = remove ]; then
    systemctl stop tibi.service >/dev/null 2>&1 || true
    systemctl disable tibi.service >/dev/null 2>&1 || true
fi
