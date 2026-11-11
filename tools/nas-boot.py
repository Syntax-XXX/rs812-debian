import serial, time, sys
s = serial.Serial('/dev/ttyUSB0', 115200, timeout=0.2)
s.write((sys.argv[2] if len(sys.argv) > 2 else 'bootm 0x800000 0x1100000').encode() + b'\r')
log = open('/root/nas-debian-boot.log', 'wb', buffering=0)
end = time.time() + float(sys.argv[1])
while time.time() < end:
    d = s.read(4096)
    if d: log.write(d)
s.close()
