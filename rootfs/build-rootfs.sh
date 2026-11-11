#!/bin/bash
# Debian 12 (armel) Root-Dateisystem für die RS812 bauen.
#   apt install debootstrap qemu-user-static binfmt-support
#   ./build-rootfs.sh /srv/rs812-root /pfad/zu/out-flash/modules/lib/modules
# Passwort für root/admin wird abgefragt; SSH-Schlüssel aus ~/.ssh/*.pub werden übernommen.
set -euo pipefail
R=${1:?Zielordner}; MODS=${2:?Modulordner (…/lib/modules)}; HERE=$(cd "$(dirname "$0")" && pwd)
read -rsp "Passwort für root und admin: " PW; echo
debootstrap --arch=armel --foreign --variant=minbase --include=systemd-sysv,udev,openssh-server,mdadm,nfs-kernel-server,smartmontools,hdparm,ifupdown,iproute2,iputils-ping,sudo,curl,rsync,htop,ca-certificates,systemd-timesyncd,kmod,procps,less,nano,cron,e2fsprogs,dbus,lm-sensors,net-tools,mtd-utils,ethtool bookworm "$R" http://deb.debian.org/debian
cp /usr/bin/qemu-arm-static "$R/usr/bin/"
chroot "$R" /debootstrap/debootstrap --second-stage
# Module nach usr/lib (Debian 12: /lib ist ein Verweis auf usr/lib – nicht überschreiben!)
mkdir -p "$R/usr/lib/modules" && cp -a "$MODS"/. "$R/usr/lib/modules/"
cp -a "$HERE/files/." "$R/"
mv "$R/etc/mdadm/mdadm.conf.example" "$R/etc/mdadm/mdadm.conf"
mkdir -p "$R/volume1" "$R/volume2" "$R/volume3"
echo rackstation > "$R/etc/hostname"; printf '127.0.0.1 localhost\n127.0.1.1 rackstation\n' > "$R/etc/hosts"
chroot "$R" /bin/bash -c "ln -sf /usr/share/zoneinfo/Europe/Berlin /etc/localtime; useradd -m -s /bin/bash -G sudo admin; systemctl enable ssh serial-getty@ttyS0 systemd-timesyncd nfs-server rs812-fan; depmod -a \$(ls /usr/lib/modules | head -1)"
printf 'root:%s\nadmin:%s\n' "$PW" "$PW" | chroot "$R" chpasswd
for d in "$R/root/.ssh" "$R/home/admin/.ssh"; do mkdir -p "$d"; cat ~/.ssh/*.pub > "$d/authorized_keys" 2>/dev/null || true; done
chroot "$R" chown -R admin:admin /home/admin/.ssh
rm -f "$R/usr/bin/qemu-arm-static"
echo "Fertig: $R  (vor dem Einspielen: etc/network/interfaces, etc/fstab, etc/mdadm/mdadm.conf, etc/exports anpassen)"
