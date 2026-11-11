#!/bin/bash
# Auf der NAS ausführen (Debian per TFTP/NFS-Root gestartet, siehe README).
# Ersetzt NUR Partition 1 (DSM-System) und 2 (Swap) auf sda-sdc. Partition 3 (Daten) bleibt unangetastet.
# Danach: Root-FS einspielen (tar über SSH nach /mnt/root), mdadm.conf ergänzen, umount.
set -eu
for d in sda sdb sdc; do
  [ "$(cat /sys/block/$d/size)" -gt 7000000000 ] || { echo "Abbruch: /dev/$d ist nicht die erwartete 4-TB-Platte"; exit 1; }
done
grep -qE "sd[abc][12]\[" /proc/mdstat && { echo "Abbruch: Partition 1/2 noch in einem RAID aktiv"; exit 1; }
wipefs -a /dev/sda1 /dev/sdb1 /dev/sdc1 /dev/sda2 /dev/sdb2 /dev/sdc2
# 0.90-Superblock (am Ende der Partition) + 4 Plätze, damit eine 4. Platte später dazukann
mdadm --create /dev/md0 --metadata=0.90 --level=1 --raid-devices=4 --run /dev/sda1 /dev/sdb1 /dev/sdc1 missing < /dev/null
mdadm --create /dev/md1 --metadata=0.90 --level=1 --raid-devices=4 --run /dev/sda2 /dev/sdb2 /dev/sdc2 missing < /dev/null
mkfs.ext4 -q -F -L rs812-root /dev/md0
mkswap -L rs812-swap /dev/md1
mkdir -p /mnt/root && mount /dev/md0 /mnt/root
mdadm --detail --brief /dev/md0 /dev/md1 | sed 's/ name=[^ ]*//'
echo "md0 unter /mnt/root eingehängt – jetzt Root-FS einspielen."
