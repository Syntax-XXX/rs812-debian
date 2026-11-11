#!/bin/bash
# Build the RSRB kernel 6.1.176 for the Synology RS812 (cross-compile on Debian 12 x86_64).
#   apt install gcc-arm-linux-gnueabi bc bison flex libssl-dev libelf-dev make xz-utils u-boot-tools
#   ./build.sh flash   -> uImage for the SPI flash (mtd1, max. 2 MiB), root = md0 on the disks
#   ./build.sh test    -> uImage with NFS root (only for testing via TFTP, does not fit the flash)
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
mkimage -A arm -O linux -T kernel -C none -a 0x00008000 -e 0x00008000 -n "RSRB Kernel $V" -d out-$VAR/zImage-dtb out-$VAR/uImage >/dev/null
rm -rf out-$VAR/modules && $M INSTALL_MOD_PATH=$W/out-$VAR/modules INSTALL_MOD_STRIP=1 modules_install >/dev/null
S=$(stat -c%s out-$VAR/uImage)
echo "uImage: $W/out-$VAR/uImage ($S bytes)"; echo "modules: $W/out-$VAR/modules/lib/modules"
[ "$VAR" = flash ] && { [ "$S" -le 2097152 ] && echo "fits mtd1 ($((2097152-S)) bytes left)" || { echo "TOO LARGE for mtd1 (2 MiB)"; exit 1; }; }
