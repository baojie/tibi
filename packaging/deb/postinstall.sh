#!/bin/sh
set -e
if ! getent group tibi >/dev/null; then
    addgroup --system tibi
fi
if ! getent passwd tibi >/dev/null; then
    adduser --system --ingroup tibi --home /var/lib/tibi --no-create-home \
        --gecos "tibi image host and pastebin" --shell /usr/sbin/nologin tibi
fi
install -d -o tibi -g tibi -m 0750 /var/lib/tibi
chown root:tibi /etc/tibi/server.toml
chmod 0640 /etc/tibi/server.toml
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload >/dev/null || true
fi
cat <<'MSG'
tibi installed. Next steps:
  1. edit /etc/tibi/server.toml (base_url at least)
  2. sudo systemctl enable --now tibi
  3. sudo -u tibi tibi admin token create --name me --scopes upload,delete,read_private --config /etc/tibi/server.toml
MSG
