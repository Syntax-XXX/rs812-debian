#!/bin/bash
# Kernel 6.1.176 für die Synology RS812 bauen (Cross-Compile auf Debian 12 x86_64).
#   apt install gcc-arm-linux-gnueabi bc bison flex libssl-dev libelf-dev make xz-utils u-boot-tools
#   ./build.sh flash   -> uImage für den SPI-Flash (mtd1, max. 2 MiB), Root = md0 auf den Platten
#   ./build.sh test    -> uImage mit NFS-Root (nur zum Testen per TFTP, passt nicht in den Flash)
set -euo pipefail
V=6.1.176; VAR=${1:-flash}; HERE=$(cd "$(dirname "$0")" && pwd); W=${WORK:-$HERE/build}
mkdir -p "$W" && cd "$W"
[ -d linux-$V ] || { curl -fsSO https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-$V.tar.xz; tar -xf linux-$V.tar.xz; }
cp "$HERE/kirkwood-rs812.dts" linux-$V/arch/arm/boot/dts/
grep -q kirkwood-rs812.dtb linux-$V/arch/arm/boot/dts/Makefile || sed -i 's/\tkirkwood-rs411.dtb \\/\tkirkwood-rs411.dtb \\\n\tkirkwood-rs812.dtb \\/' linux-$V/arch/arm/boot/dts/Makefile
mkdir -p out-$VAR
cp "$HERE/rs812-$([ "$VAR" = flash ] && echo flash || echo nfsroot-test).config" out-$VAR/.config
M="make -C linux-$V ARCH=arm CROSS_COMPILE=arm-linux-gnueabi- O=$W/out-$VAR"
$M olddefconfig >/dev/null
$M -j"$(nproc)" zImage kirkwood-rs812.dtb modules
cat out-$VAR/arch/arm/boot/zImage out-$VAR/arch/arm/boot/dts/kirkwood-rs812.dtb > out-$VAR/zImage-dtb
mkimage -A arm -O linux -T kernel -C none -a 0x00008000 -e 0x00008000 -n "RS812 Debian $V" -d out-$VAR/zImage-dtb out-$VAR/uImage >/dev/null
rm -rf out-$VAR/modules && $M INSTALL_MOD_PATH=$W/out-$VAR/modules INSTALL_MOD_STRIP=1 modules_install >/dev/null
S=$(stat -c%s out-$VAR/uImage)
echo "uImage: $W/out-$VAR/uImage ($S Bytes)"; echo "Module: $W/out-$VAR/modules/lib/modules"
[ "$VAR" = flash ] && { [ "$S" -le 2097152 ] && echo "passt in mtd1 (Rest $((2097152-S)) Bytes)" || { echo "ZU GROSS für mtd1 (2 MiB)"; exit 1; }; }
