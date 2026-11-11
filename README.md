# Debian 12 auf der Synology RackStation RS812

Ersetzt DSM 6.2 auf der **Synology RS812** (Marvell Kirkwood 88F6282, ARMv5, 512 MB RAM, 4 Schächte)
durch ein normales **Debian 12 „bookworm“ (armel)** – **ohne USB-Stick**: Der Kernel liegt im internen
SPI-Flash an der Stelle des DSM-Kernels, das System auf Partition 1 aller Platten (RAID1).
Die Daten-Volumes aus DSM (Partition 3) bleiben unverändert und werden weiterverwendet.

> 🖥️ **Weboberfläche statt DSM:** [**RackPanel**](https://github.com/Syntax-XXX/rackpanel) – mein schlankes Management-Panel
> für genau dieses Gerät (Speicher, SMART, RAID, Freigaben, Benutzer, Apps, Updates). Nach der Installation hier mit einer Zeile drauf:
> `bash <(curl -fsSL https://raw.githubusercontent.com/Syntax-XXX/rackpanel/main/install.sh)`

> ⚠️ Danach gibt es kein DSM mehr. Ohne serielle Konsole nicht nachmachen – sie ist der Rettungsweg.

## Ergebnis

| | |
|---|---|
| Kernel | 6.1.176 (eigene, schlanke Konfiguration, XZ, 2,08 MB – passt in die 2 MiB von `mtd1`) |
| Gerätebeschreibung | eigene `kirkwood-rs812.dts` (beide LAN-Ports, PCIe-SATA, Lüfter, HDD-LEDs) |
| System | Debian 12 armel auf `md0` (RAID1 aus `sda1`–`sdc1`), Swap `md1` (`sda2`–`sdc2`) |
| Daten | DSM-Volumes `md2`–`md4` (ext4) unter `/volume1`–`/volume3` |
| Dienste | SSH, NFS-Server (v3), `apt`, Konsolen-Login auf `ttyS0`, Weboberfläche [RackPanel](https://github.com/Syntax-XXX/rackpanel) |
| Leise | `rs812-fan.service`: Lüfterstufe nach wärmster Platte · Platten-Standby nach 20 min |
| RAM frei | ~420 MB (unter DSM ~190 MB) |
| Durchsatz | ~30 MB/s per NFS – die Grenze von CPU/Netzwerkchip, unter DSM gleich |

## Hardware-Erkenntnisse

- **Bootloader:** Marvell U-Boot 1.1.4 (2011), kein USB/`fatload`, aber `tftpboot`, `ping`, `setenv`.
  Umgebung ist nicht gespeichert („Using default environment“) – nie `saveenv` nötig.
  Standard-Start: `bootm F8080000 F8280000` mit
  `bootargs=console=ttyS0,115200 ip=off initrd=0x00800040,4M root=/dev/md0 rw …`, `mainlineLinux=no`.
- **SPI-Flash (m25p32, 4 MB):**

  | mtd | Offset | Größe | Inhalt |
  |---|---|---|---|
  | 0 | 0x000000 | 512 KB | U-Boot („RedBoot“) – **nie anfassen** |
  | 1 | 0x080000 | 2 MB | Kernel-uImage („zImage“) ← hier kommt Debian rein |
  | 2 | 0x280000 | 1,25 MB | DSM-Ramdisk („rd.gz“) – bleibt, `bootm` braucht einen gültigen Header |
  | 3–5 | 0x3C0000 | … | vendor (MACs), Config, FIS |

- **Schreibschutz:** Der Flash ist per Block-Protect gesperrt. `flash_unlock` auf eine Partition schlägt fehl
  (EINVAL). Lösung: `spi-nor.ko` mit `CONFIG_MTD_SPI_NOR_SWP_DISABLE=y` bauen und nur zum Flashen laden.
- **Kein Initrd möglich** (rd.gz-Platz zu klein) → Kernel ohne `BLK_DEV_INITRD`; Platten-, RAID-, ext4- und
  Netzwerktreiber fest eingebaut; Root über `CONFIG_CMDLINE` (angehängt):
  `md=0,/dev/sda1,/dev/sdb1,/dev/sdc1 root=/dev/md0` (GPT → kein RAID-Autodetect).
- **Device Tree:** Mainline `kirkwood-rs411.dts` heißt zwar „RS411 RS812“, aber:
  - der interne SATA-Controller belegt mpp20/21 → **LAN 2 (eth1) fällt aus** → `&sata` deaktivieren,
    die Platten hängen ohnehin am PCIe-Controller 88SX7042 (`&pcie1`);
  - Lüfter-Variante `-3` kollidiert auf mpp44 mit den HDD-LEDs → Variante `gpio-fan-100-15-35-1`.
- **DSM-Superblöcke** von `md0`/`md1` sind 0.90 in Big-Endian – Debian erkennt sie nicht (egal, werden ersetzt).
  Die Daten-RAIDs (1.2) erkennt Debian sofort.
- **MACs:** U-Boot setzt eine Marvell-Default-MAC; die echten Synology-MACs stehen in `mtd3` → in
  `/etc/network/interfaces` per `hwaddress` setzen.

## Ablauf

1. **Flash sichern** (unter DSM per SSH): `cat /dev/mtdNro > mtdN.bin` für 0–6.
2. **Serielle Konsole** an den Konsolen-Port (115200 8N1), z. B. PL2303 → `/dev/ttyUSB0` am Build-Rechner.
3. **Kernel bauen:** `kernel/build.sh test` und `kernel/build.sh flash`.
4. **Root-FS bauen:** `rootfs/build-rootfs.sh /srv/rs812-root <out-flash>/modules/lib/modules`
   (`interfaces`, `fstab`, `mdadm.conf`, `exports` vorher an die eigenen UUIDs/IPs anpassen).
5. **Testen ohne Risiko (TFTP + NFS-Root):** tftpd auf dem Build-Rechner, Root-FS-Kopie per NFS exportieren.
   Bootloader anhalten (`tools/nas-console.py`), dann:
   ```
   setenv ipaddr 10.40.32.63; setenv serverip <build-pc>; setenv netmask 255.255.255.0
   setenv mainlineLinux yes; setenv arcNumber 3036
   tftpboot 0x800000 rs812/uImage-test
   setenv bootargs console=ttyS0,115200 root=/dev/nfs rw nfsroot=<build-pc>:/srv/rs812-test,vers=3,tcp ip=10.40.32.63::10.40.32.1:255.255.255.0:rackstation:eth0:off
   bootm 0x800000
   ```
6. **Auf die Platten:** im NFS-Test-Debian `install/install-to-disks.sh`, Root-FS per `tar | ssh` nach `/mnt/root`.
7. **Flash-Kernel testen, ohne zu flashen** (Standard-Umgebung, nur Netz für TFTP):
   `tftpboot 0x2000000 rs812/uImage-flash` → `bootm 0x2000000 F8280000`.
8. **Flashen:** unter dem Platten-Debian `install/flash-kernel.sh uImage-flash spi-nor-unlock.ko`.
9. Normal neu starten – fertig.

## Rettung

- U-Boot (mtd0) wird nie beschrieben → Bootloader anhalten und per TFTP einen Kernel laden geht immer.
- DSM-Kernel zurück: gesichertes `mtd1.bin` mit `install/flash-kernel.sh` zurückschreiben
  (DSM selbst muss danach per Synology Assistant neu installiert werden).

## Werkzeuge (`tools/`)

| Datei | Zweck |
|---|---|
| `serial-probe.py` | herausfinden, welcher USB-Seriell-Adapter an der NAS hängt |
| `nas-console.py` | Konsole mitschreiben und bei „Hit any key to stop autoboot“ anhalten |
| `nasc.py` | einen Befehl an den U-Boot-Prompt schicken |
| `nas-boot.py` | `bootm …` senden und den Startvorgang mitschreiben |
| `nas-log-passive.py` | nur mitschreiben (normaler Start ohne Eingriff) |
| `nas-sh.py`, `nas-sh3.py` | Befehle in den Shell-Fenstern des Debian-Installers ausführen |

## Hinweise

- Eine defekte Platte im Schacht verzögert den Start um ~2 Minuten (SATA-Fehlerbehandlung).
- Kein NFSv4 im Kernel (Platz) – Clients mit `vers=3` einbinden.
- Freigabe-Ordner sollten dem anonymen NFS-Benutzer gehören (`chown 1024:100`), sonst kann z. B. Proxmox
  bei `all_squash` nicht schreiben.
