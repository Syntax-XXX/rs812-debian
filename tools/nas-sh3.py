import serial, time, sys, re
s = serial.Serial('/dev/ttyUSB0', 115200, timeout=0.2)
s.write(b'\x01'); time.sleep(0.3); s.write(b'3'); time.sleep(1.5); s.write(b'\r'); time.sleep(1)
s.reset_input_buffer()
s.write(sys.argv[1].encode() + b'\r'); time.sleep(float(sys.argv[2]))
out = b''; end = time.time() + 2
while time.time() < end:
    d = s.read(8192)
    if d: out += d; end = time.time() + 1.2
txt = re.sub(r'\x1b\[[0-9;?]*[a-zA-Z]|\x1b[()][0B]', '', out.decode('latin-1', 'replace')).replace('\r', '')
print(txt[-5000:])
