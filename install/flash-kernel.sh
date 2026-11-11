#!/bin/bash
# Write a kernel uImage to the SPI flash (mtd1 "zImage") – run on the NAS under Debian.
# The flash chip (m25p32) is write-protected via block protect. Linux only lifts that when spi-nor is
# built with CONFIG_MTD_SPI_NOR_SWP_DISABLE=y -> provide such a spi-nor.ko (see README).
#   ./flash-kernel.sh uImage spi-nor-unlock.ko
set -eu
IMG=${1:?uImage}; KO=${2:?spi-nor.ko with SWP_DISABLE}
n=$(stat -c%s "$IMG"); [ "$n" -le 2097152 ] || { echo "image too large for mtd1"; exit 1; }
rmmod mtdblock 2>/dev/null || true; rmmod spi_nor 2>/dev/null || true
insmod "$KO"; sleep 2
grep -q '"zImage"' /proc/mtd || { echo "mtd1 'zImage' not found"; exit 1; }
flash_lock --islocked /dev/mtd1 | grep -q unlocked || { echo "flash still locked – abort"; exit 1; }
before0=$(sha256sum < /dev/mtd0)
flashcp -v "$IMG" /dev/mtd1
[ "$(head -c "$n" /dev/mtd1 | sha256sum)" = "$(sha256sum < "$IMG")" ] && echo "kernel verified" || { echo "ERROR: read-back does not match!"; exit 1; }
[ "$(sha256sum < /dev/mtd0)" = "$before0" ] && echo "bootloader (mtd0) unchanged"
