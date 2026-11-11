# NAS-Konsole mitschreiben und Bootloader anhalten (sendet nur Ctrl-C, sobald die Abbruch-Meldung kommt)
import serial, time, re, sys
s = serial.Serial('/dev/ttyUSB0', 115200, timeout=0.1)
log = open('/root/nas-console.log', 'ab', buffering=0)
buf = ''
end = time.time() + float(sys.argv[1] if len(sys.argv) > 1 else 300)
stopped = False
while time.time() < end:
    d = s.read(1024)
    if d:
        log.write(d)
        buf = (buf + d.decode('latin-1', 'replace'))[-4000:]
        if not stopped and re.search(r'Hit any key to stop autoboot', buf):
            for _ in range(8):
                s.write(b'\x03'); time.sleep(0.05)
            s.write(b'\r')
            stopped = True
            log.write(b'\n### [Ctrl-C gesendet]\n')
        if stopped and re.search(r'(RedBoot|Marvell)>\s*$|=>\s*$', buf):
            log.write(b'\n### [Bootloader-Prompt erreicht]\n')
            break
s.close()
