#!/bin/bash
# Run on the NAS (Debian booted via TFTP/NFS root, see README).
# Replaces ONLY partition 1 (DSM system) and 2 (swap) on sda-sdc. Partition 3 (data) stays untouched.
# Afterwards: copy the root FS (tar over SSH to /mnt/root), complete mdadm.conf, umount.
set -eu
for d in sda sdb sdc; do
  [ "$(cat /sys/block/$d/size)" -gt 7000000000 ] || { echo "Abort: /dev/$d is not the expected 4 TB disk"; exit 1; }
done
grep -qE "sd[abc][12]\[" /proc/mdstat && { echo "Abort: partition 1/2 still active in a RAID"; exit 1; }
wipefs -a /dev/sda1 /dev/sdb1 /dev/sdc1 /dev/sda2 /dev/sdb2 /dev/sdc2
# 0.90 superblock (at the end of the partition) + 4 slots so a 4th disk can join later
mdadm --create /dev/md0 --metadata=0.90 --level=1 --raid-devices=4 --run /dev/sda1 /dev/sdb1 /dev/sdc1 missing < /dev/null
mdadm --create /dev/md1 --metadata=0.90 --level=1 --raid-devices=4 --run /dev/sda2 /dev/sdb2 /dev/sdc2 missing < /dev/null
mkfs.ext4 -q -F -L rs812-root /dev/md0
mkswap -L rs812-swap /dev/md1
mkdir -p /mnt/root && mount /dev/md0 /mnt/root
mdadm --detail --brief /dev/md0 /dev/md1 | sed 's/ name=[^ ]*//'
echo "md0 mounted at /mnt/root – now copy the root FS."
