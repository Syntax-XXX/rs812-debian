import serial, time, sys
s = serial.Serial('/dev/ttyUSB0', 115200, timeout=0.3)
log = open('/root/nas-flashboot.log', 'wb', buffering=0)
end = time.time() + float(sys.argv[1])
while time.time() < end:
    d = s.read(8192)
    if d: log.write(d)
