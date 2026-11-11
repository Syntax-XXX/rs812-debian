# Send one command to the NAS bootloader and show the reply:  python3 nasc.py "printenv" [wait seconds]
import serial, sys, time
s = serial.Serial('/dev/ttyUSB0', 115200, timeout=0.2)
s.reset_input_buffer()
s.write(sys.argv[1].encode() + b'\r')
end = time.time() + float(sys.argv[2] if len(sys.argv) > 2 else 3)
out = b''
while time.time() < end:
    d = s.read(4096)
    if d:
        out += d
        end = max(end, time.time() + 0.8)
    if out.rstrip().endswith(b'Marvell>>') and len(out) > len(sys.argv[1]) + 12:
        break
s.close()
txt = out.decode('latin-1', 'replace').replace('\r', '')
open('/root/nas-console.log', 'a').write(txt)
print(txt)
