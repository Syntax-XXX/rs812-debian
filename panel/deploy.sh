#!/bin/bash
# Backend (und optional Oberfläche mit "ui") bauen und auf die RS812 spielen
set -euo pipefail
NAS=${NAS:-root@10.40.32.63}; SSH="ssh -o ConnectTimeout=8 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR $NAS"
cd "$(dirname "$0")/server"
GOOS=linux GOARCH=arm GOARM=5 go build -trimpath -ldflags "-s -w" -o rs812-panel-armv5 .
if [ "${1:-}" = ui ]; then
  W=../vendor/casaos-ui/build/sysroot/var/lib/casaos/www
  tar -C $W -czf - . | $SSH 'rm -rf /usr/share/rs812-panel/www.new && mkdir -p /usr/share/rs812-panel/www.new && tar -C /usr/share/rs812-panel/www.new -xzf - && rm -rf /usr/share/rs812-panel/www.old && mv /usr/share/rs812-panel/www /usr/share/rs812-panel/www.old && mv /usr/share/rs812-panel/www.new /usr/share/rs812-panel/www && rm -rf /usr/share/rs812-panel/www.old'
  echo "Oberfläche aktualisiert"
fi
cat rs812-panel-armv5 | $SSH 'cat > /usr/local/bin/rs812-panel.new && chmod 755 /usr/local/bin/rs812-panel.new && mv /usr/local/bin/rs812-panel.new /usr/local/bin/rs812-panel && systemctl restart rs812-panel && sleep 3 && systemctl is-active rs812-panel'
