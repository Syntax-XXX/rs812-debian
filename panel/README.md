# RackStation-Panel

Verwaltungsoberfläche für die RS812 unter Debian: die originale **CasaOS-UI v0.4.5** (Apache-2.0) mit einem eigenen, schlanken Go-Backend für ARMv5 (etwa 5 MB RAM).

- **Startseite:** CasaOS-Widgets (CPU, RAM, Speicher, Netz live über socket.io) und Kacheln für die Homelab-Dienste (`link-apps.json`)
- **Dateien:** CasaOS-Dateimanager für /volume1–3 und /home
- **Verwaltung:** eigenes Fenster mit Übersicht, Platten & RAID (SMART), NFS-Freigaben, Diensten, Updates (apt), Protokollen und Neustart/Ausschalten

## Bauen
```
git clone https://github.com/IceWhaleTech/CasaOS-UI vendor/casaos-ui && cd vendor/casaos-ui
git checkout 400a22c && git apply ../../ui-patch/casaos-ui-rs812.patch
pnpm install && NODE_OPTIONS=--openssl-legacy-provider pnpm build
cd ../.. && ./deploy.sh ui      # baut das Backend (GOARCH=arm GOARM=5) und spielt beides auf die NAS
```
Dienst: `rs812-panel.service` → `/usr/local/bin/rs812-panel -listen :80`, Daten in `/var/lib/rs812-panel`.
