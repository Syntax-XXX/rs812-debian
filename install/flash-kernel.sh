#!/bin/bash
# Kernel-uImage in den SPI-Flash (mtd1 "zImage") schreiben – auf der NAS unter Debian ausführen.
# Der Flash-Chip (m25p32) ist per Block-Protect schreibgeschützt. Linux hebt das nur auf, wenn spi-nor mit
# CONFIG_MTD_SPI_NOR_SWP_DISABLE=y gebaut ist -> dafür ein eigenes spi-nor.ko bereitlegen (siehe README).
#   ./flash-kernel.sh uImage spi-nor-unlock.ko
set -eu
IMG=${1:?uImage}; KO=${2:?spi-nor.ko mit SWP_DISABLE}
n=$(stat -c%s "$IMG"); [ "$n" -le 2097152 ] || { echo "Image zu groß für mtd1"; exit 1; }
rmmod mtdblock 2>/dev/null || true; rmmod spi_nor 2>/dev/null || true
insmod "$KO"; sleep 2
grep -q '"zImage"' /proc/mtd || { echo "mtd1 'zImage' nicht gefunden"; exit 1; }
flash_lock --islocked /dev/mtd1 | grep -q unlocked || { echo "Flash noch gesperrt – Abbruch"; exit 1; }
before0=$(sha256sum < /dev/mtd0)
flashcp -v "$IMG" /dev/mtd1
[ "$(head -c "$n" /dev/mtd1 | sha256sum)" = "$(sha256sum < "$IMG")" ] && echo "Kernel verifiziert" || { echo "FEHLER: Rücklesen stimmt nicht!"; exit 1; }
[ "$(sha256sum < /dev/mtd0)" = "$before0" ] && echo "Bootloader (mtd0) unverändert"
