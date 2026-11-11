# RSRB · RackStation ReBoot – Debian 12 on the Synology RS812

**English** · [Deutsch](README.de.md)

Replaces DSM 6.2 on the **Synology RS812** (Marvell Kirkwood 88F6282, ARMv5, 512 MB RAM, 4 bays)
with plain **Debian 12 "bookworm" (armel)** – **without a USB stick**: the kernel lives in the internal
SPI flash where the DSM kernel used to be, the system on partition 1 of all disks (RAID1).
The DSM data volumes (partition 3) stay untouched and are used as they are.

The result is called **RSRB** (RackStation ReBoot): its own `6.1.176-rsrb` kernel, its own package repository and web UI.

> 🖥️ **Web UI instead of DSM:** [**RackPanel**](https://github.com/Syntax-XXX/rackpanel) – my lightweight management panel
> made for exactly this device (storage, SMART, RAID, shares, users, apps, updates). One line after the installation:
> `bash <(curl -fsSL https://raw.githubusercontent.com/Syntax-XXX/rackpanel/main/install.sh)`
>
> 📦 **Package repository:** [syntax-xxx.github.io/rsrb](https://syntax-xxx.github.io/rsrb/) – RackPanel, a neofetch preset with
> the RSRB logo and the RSRB system identity via `apt` ([sources](https://github.com/Syntax-XXX/rsrb)).

> ⚠️ There is no DSM afterwards. Don't try this without a serial console – it is your way back.

## Result

| | |
|---|---|
| Kernel | two flavors, both fit the 2 MiB of `mtd1`: **`6.1.176-rsrb`** (core – only what a NAS needs is built in) and **`6.1.176-rsrb-full`** (+ memory cgroup, netfilter/NAT, WireGuard, IPv6, NTFS3, CIFS) |
| Device tree | own `kirkwood-rs812.dts` (both LAN ports, PCIe SATA, fan, HDD LEDs) |
| System | Debian 12 armel on `md0` (RAID1 of `sda1`–`sdc1`), swap `md1` (`sda2`–`sdc2`) |
| Data | DSM volumes `md2`–`md4` (ext4) at `/volume1`–`/volume3` |
| Services | SSH, NFS server (v3), `apt`, console login on `ttyS0`, web UI [RackPanel](https://github.com/Syntax-XXX/rackpanel) |
| Quiet | `rs812-fan.service`: fan level follows the hottest disk · disk standby after 20 min |
| Free RAM | ~420 MB (~190 MB under DSM) |
| Throughput | ~30 MB/s over NFS – the limit of CPU/network chip, same as under DSM |

## What we learned about the hardware

- **Bootloader:** Marvell U-Boot 1.1.4 (2011), no USB/`fatload`, but `tftpboot`, `ping`, `setenv`.
  The environment is not stored ("Using default environment") – `saveenv` is never needed.
  Default boot: `bootm F8080000 F8280000` with
  `bootargs=console=ttyS0,115200 ip=off initrd=0x00800040,4M root=/dev/md0 rw …`, `mainlineLinux=no`.
- **SPI flash (m25p32, 4 MB):**

  | mtd | Offset | Size | Contents |
  |---|---|---|---|
  | 0 | 0x000000 | 512 KB | U-Boot ("RedBoot") – **never touch** |
  | 1 | 0x080000 | 2 MB | kernel uImage ("zImage") ← Debian goes here |
  | 2 | 0x280000 | 1.25 MB | DSM ramdisk ("rd.gz") – stays, `bootm` needs a valid header |
  | 3–5 | 0x3C0000 | … | vendor (MACs), config, FIS |

- **Write protection:** the flash is locked via block protect. `flash_unlock` on a partition fails
  (EINVAL). Fix: build `spi-nor.ko` with `CONFIG_MTD_SPI_NOR_SWP_DISABLE=y` and load it only for flashing.
- **No initrd possible** (rd.gz slot too small) → kernel without `BLK_DEV_INITRD`; disk, RAID, ext4 and
  network drivers built in; root via `CONFIG_CMDLINE` (appended):
  `md=0,/dev/sda1,/dev/sdb1,/dev/sdc1 root=/dev/md0` (GPT → no RAID autodetect).
- **Device tree:** mainline `kirkwood-rs411.dts` is called "RS411 RS812", but:
  - the internal SATA controller uses mpp20/21 → **LAN 2 (eth1) dies** → disable `&sata`,
    the disks sit on the PCIe controller 88SX7042 (`&pcie1`) anyway;
  - fan variant `-3` clashes on mpp44 with the HDD LEDs → variant `gpio-fan-100-15-35-1`.
- **DSM superblocks** of `md0`/`md1` are 0.90 big-endian – Debian doesn't recognise them (fine, they get replaced).
  The data RAIDs (1.2) are recognised right away.
- **MACs:** U-Boot sets a Marvell default MAC; the real Synology MACs are in `mtd3` → set them with
  `hwaddress` in `/etc/network/interfaces`.

## Steps

1. **Back up the flash** (under DSM via SSH): `cat /dev/mtdNro > mtdN.bin` for 0–6.
2. **Serial console** on the console port (115200 8N1), e.g. PL2303 → `/dev/ttyUSB0` on the build machine.
3. **Build the kernel:** `kernel/build.sh test`, `kernel/build.sh flash` (core) and/or `kernel/build.sh full`.
4. **Build the root FS:** `rootfs/build-rootfs.sh /srv/rs812-root <out-flash>/modules/lib/modules`
   (adapt `interfaces`, `fstab`, `mdadm.conf`, `exports` to your UUIDs/IPs first).
5. **Test without risk (TFTP + NFS root):** tftpd on the build machine, export a copy of the root FS via NFS.
   Stop the bootloader (`tools/nas-console.py`), then:
   ```
   setenv ipaddr 10.40.32.63; setenv serverip <build-pc>; setenv netmask 255.255.255.0
   setenv mainlineLinux yes; setenv arcNumber 3036
   tftpboot 0x800000 rs812/uImage-test
   setenv bootargs console=ttyS0,115200 root=/dev/nfs rw nfsroot=<build-pc>:/srv/rs812-test,vers=3,tcp ip=10.40.32.63::10.40.32.1:255.255.255.0:rackstation:eth0:off
   bootm 0x800000
   ```
6. **Onto the disks:** in the NFS test Debian run `install/install-to-disks.sh`, copy the root FS via `tar | ssh` to `/mnt/root`.
7. **Test the flash kernel without flashing** (default environment, network only for TFTP):
   `tftpboot 0x2000000 rs812/uImage-flash` → `bootm 0x2000000 F8280000`.
8. **Flash:** in the disk Debian first copy the new kernel's modules to `/lib/modules/`
   (`<out-flash>/modules/lib/modules/6.1.176-rsrb`), then `install/flash-kernel.sh uImage-flash spi-nor-unlock.ko`.
9. Reboot normally.
10. **RSRB packages:** `curl -fsSL https://syntax-xxx.github.io/rsrb/setup.sh | sh`, then
    `apt install rsrb-rs812-full` (web UI, containers, full kernel) or `apt install rsrb-rs812-core` (barebone NAS).
    Kernel updates come with apt; activate one with `rsrb-flash-kernel 6.1.176-rsrb-full` (or `-rsrb`) and a reboot.
    Test a kernel first without flashing: copy `/usr/lib/rsrb/kernel/uImage-*` to your TFTP server and boot it as in step 7.

## Recovery

- U-Boot (mtd0) is never written → stopping the bootloader and loading a kernel via TFTP always works.
- Back to the DSM kernel: write the saved `mtd1.bin` back with `install/flash-kernel.sh`
  (DSM itself must then be reinstalled with Synology Assistant).

## Tools (`tools/`)

| File | Purpose |
|---|---|
| `serial-probe.py` | find out which USB serial adapter is connected to the NAS |
| `nas-console.py` | log the console and stop at "Hit any key to stop autoboot" |
| `nasc.py` | send one command to the U-Boot prompt |
| `nas-boot.py` | send `bootm …` and log the boot |
| `nas-log-passive.py` | only log (normal boot without interfering) |
| `nas-sh.py`, `nas-sh3.py` | run commands in the shell windows of the Debian installer |

## Notes

- A broken disk in a bay delays boot by ~2 minutes (SATA error handling).
- No NFSv4 in the kernel (space) – mount with `vers=3` on clients.
- Shared folders should belong to the anonymous NFS user (`chown 1024:100`), otherwise e.g. Proxmox
  can't write with `all_squash`.
