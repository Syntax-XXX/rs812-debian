# RSRB · RackStation ReBoot – Debian 12 auf der Synology RS812

[English](README.md) · **Deutsch**

Ersetzt DSM 6.2 auf der **Synology RS812** (Marvell Kirkwood 88F6282, ARMv5, 512 MB RAM, 4 Schächte)
durch ein normales **Debian 12 „bookworm“ (armel)** – **ohne USB-Stick**: Der Kernel liegt im internen
SPI-Flash an der Stelle des DSM-Kernels, das System auf Partition 1 aller Platten (RAID1).
Die Daten-Volumes aus DSM (Partition 3) bleiben unverändert und werden weiterverwendet.

Das Ergebnis heißt **RSRB** (RackStation ReBoot): eigener Kernel `6.1.176-rsrb`, eigene Paketquelle und Weboberfläche.

> 🖥️ **Weboberfläche statt DSM:** [**RackPanel**](https://github.com/Syntax-XXX/rackpanel) – mein schlankes Management-Panel
> für genau dieses Gerät (Speicher, SMART, RAID, Freigaben, Benutzer, Apps, Updates). Nach der Installation hier mit einer Zeile drauf:
> `bash <(curl -fsSL https://raw.githubusercontent.com/Syntax-XXX/rackpanel/main/install.sh)`
>
> 📦 **Paketquelle:** [syntax-xxx.github.io/rsrb](https://syntax-xxx.github.io/rsrb/) – RackPanel, neofetch-Preset mit RSRB-Logo
> und RSRB-Systemidentität per `apt` ([Quellen](https://github.com/Syntax-XXX/rsrb)).

> ⚠️ Danach gibt es kein DSM mehr. Ohne serielle Konsole nicht nachmachen – sie ist der Rettungsweg.

## Ergebnis

| | |
|---|---|
| Kernel | zwei Flavors, beide passen in die 2 MiB von `mtd1`: **`6.1.176-rsrb`** (Core – nur, was ein NAS braucht, auf Tempo getunt: der schnellste) und **`6.1.176-rsrb-full`** (+ memory-cgroup für Container-Limits, Netfilter/NAT, WireGuard, IPv6, NTFS3, CIFS – die memory-cgroup kostet ~20 % Schreibtempo) |
| Gerätebeschreibung | eigene `kirkwood-rs812.dts` (beide LAN-Ports, PCIe-SATA, Lüfter, HDD-LEDs, Mikrocontroller-UART, Ricoh-RTC) |
| System | Debian 12 armel auf `md0` (RAID1 aus `sda1`–`sdc1`), Swap `md1` (`sda2`–`sdc2`) |
| Daten | DSM-Volumes `md2`–`md4` (ext4) unter `/volume1`–`/volume3` |
| Dienste | SSH, NFS-Server (v3), `apt`, Konsolen-Login auf `ttyS0`, Weboberfläche [RackPanel](https://github.com/Syntax-XXX/rackpanel) |
| Front-Panel | `rs812-panel.service`: Platten-LEDs (grün, blinken bei Zugriff, orange bei SMART-Fehler), Status-LED grün / orange blinkend bei Problemen, Power-LED, echte Lüfterdrehzahl |
| Leise | `rs812-fan.service`: Lüfterstufe nach wärmster Platte · Platten-Standby nach 20 min |
| RAM frei | ~420 MB (unter DSM ~190 MB) |
| Boot | ~40 s bis zum Login (Kernel 3,7 s) |
| Durchsatz | NFS von einem Server mit Intel-Karte: **~50 MB/s lesen, ~37 MB/s schreiben** (vor dem Tuning 38/27) · iperf3 ~940 Mbit/s senden, ~555 Mbit/s empfangen · Platte ~163 MB/s lesen, ~97 MB/s schreiben – alles CPU-gebunden, gemessen am 12.11.2026 |

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
  - Lüfter-Variante `-3` kollidiert auf mpp44 mit den HDD-LEDs → Variante `gpio-fan-100-15-35-1`, **ohne deren
    Alarm-GPIO**: Auf der RS812 wechselt mpp35 im Takt des Lüfters (Impulse), jede Flanke war ein Interrupt plus
    ein uevent – udev kam nie zur Ruhe, der Boot wartete 2 Minuten und der Kernel schaltete den IRQ irgendwann ab.
- **DSM-Superblöcke** von `md0`/`md1` sind 0.90 in Big-Endian – Debian erkennt sie nicht (egal, werden ersetzt).
  Die Daten-RAIDs (1.2) erkennt Debian sofort.
- **Performance:** Der eine ARMv5-Kern mit 1,6 GHz ist überall die Grenze – ARMv5 hat keine cache-kohärente DMA,
  jeder Platten- und Netzwerkpuffer braucht Cache-Pflege durch die CPU (165 MB/s Direct-Read von Platte = 91 % CPU).
  Gemessene Gewinne (A/B auf der Hardware, Client pve1 mit Intel igb, NFS Ende zu Ende):

  | Änderung | NFS schreiben | NFS lesen |
  |---|---|---|
  | vorher (6.1.176-4) | 27,5 MB/s | 38,5 MB/s |
  | + keine memory-cgroup (Core), Härtung/Debug aus (`INIT_ON_ALLOC`, `HARDENED_USERCOPY`, `DEBUG_LIST`, Stack-Protector, PSI), `PREEMPT_NONE`, `HZ=100` | 31,9 | 50,1 |
  | + `-O2` statt `-Os` für TCP/IPv4, Ethernet-Treiber, md, ext4, jbd2, mm (+ Module nfsd/sunrpc) | **36,6** | **50,0** |

  `INIT_ON_ALLOC` allein hat jede Page-Cache-Seite und jeden Netzwerkpuffer genullt; die memory-cgroup verbucht
  jede Seite (`cgroup.memory=nokmem,nosocket` hilft nicht). `-O2` überall wäre 110 KB zu groß für den Flash; die
  gewählten Verzeichnisse bringen dasselbe und passen (8 KB frei). Gesetzt in `build.sh` (`O2_DIRS`).
  Laufzeit-Tuning in `rsrb-rs812-hw`: RX-Interrupt-Coalescing 100 µs statt 250 µs (Empfangen +20 %) und 4 MiB
  Read-Ahead auf den Daten-Arrays (Datei-Lesen +8–13 %). Größere RX-Ringe brachten nichts. Ringgrößen nicht zur
  Laufzeit ändern (`ethtool -G`): unter 6.1 führte das unter Last zu TX-Queue-Timeouts. Ein Client mit
  USB-2.0-Netzwerkadapter (oder ein USB-C-Dock, das seine schnellen Leitungen für einen Monitor braucht) begrenzt auf ~300 Mbit/s.
- **Link nach Neustart:** Nach manchen Kernel-Wechseln sah der 88E1116R-PHY den Switch, schloss das Gigabit-Training
  aber nie ab; `ethtool -r` behebt es, deshalb startet `rsrb-rs812-hw` die Aushandlung neu, wenn 10 s nach ifup kein Link da ist.
- **Front-Panel:** LEDs, Summer und Power-Taste hängen an einem Mikrocontroller an UART1 (`/dev/ttyS1`, 9600 8N1).
  Codes wie in DSMs eigenen Skripten: `4` Power-LED an, `6` aus, `7` Status-LED aus, `8` grün, `9` grün blinkend,
  `:` orange, `;` orange blinkend; `1` schaltet aus (sendet der Kernel-Treiber `synology,power-off`). Die 2×4
  Platten-LEDs sind GPIOs (`synology:green|amber:hddN`). mpp35 trägt die Drehzahl-Impulse des Lüfters (2 pro
  Umdrehung) – daran erkennt DSMs `FanStatusMapping`, dass er läuft; eine Alarmleitung ist es nicht.
- **RTC:** ein Ricoh RS5C372A auf I²C 0x32 (`i2cdetect`; DSMs `synobios` hat `rtc_ricoh_*`), nicht der Seiko
  S-35390A auf 0x30, den der Mainline-RS411-Tree einschaltet.
- **MACs:** U-Boot setzt eine Marvell-Default-MAC; die echten Synology-MACs stehen in `mtd3` → in
  `/etc/network/interfaces` per `hwaddress` setzen.

## Ablauf

1. **Flash sichern** (unter DSM per SSH): `cat /dev/mtdNro > mtdN.bin` für 0–6.
2. **Serielle Konsole** an den Konsolen-Port (115200 8N1), z. B. PL2303 → `/dev/ttyUSB0` am Build-Rechner.
3. **Kernel bauen:** `kernel/build.sh test`, `kernel/build.sh flash` (Core) und/oder `kernel/build.sh full`.
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
8. **Flashen:** unter dem Platten-Debian zuerst die Module des neuen Kernels nach `/lib/modules/` kopieren
   (`<out-flash>/modules/lib/modules/6.1.176-rsrb`), dann `install/flash-kernel.sh uImage-flash spi-nor-unlock.ko`.
9. Normal neu starten.
10. **RSRB-Pakete:** `curl -fsSL https://syntax-xxx.github.io/rsrb/setup.sh | sh`, dann
    `apt install rsrb-rs812-full` (Weboberfläche, Container, Full-Kernel) oder `apt install rsrb-rs812-core` (Barebone-NAS).
    Kernel-Updates kommen per apt; aktivieren mit `rsrb-flash-kernel 6.1.176-rsrb-full` (oder `-rsrb`) und Neustart.
    Einen Kernel vorher ohne Flashen testen: `/usr/lib/rsrb/kernel/uImage-*` auf den TFTP-Server kopieren und wie in Schritt 7 starten.

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
