import serial, time, sys, re
s = serial.Serial('/dev/ttyUSB0', 115200, timeout=0.2)
def send(b, wait=1.0):
    s.write(b); time.sleep(wait)
if sys.argv[1] == 'switch':
    send(b'\x01', 0.3); send(b'2', 1.5); send(b'\r', 1.0)
cmd = sys.argv[2] if len(sys.argv) > 2 else ''
s.reset_input_buffer()
send(cmd.encode() + b'\r', float(sys.argv[3]) if len(sys.argv) > 3 else 4)
out = b''
end = time.time() + 2
while time.time() < end:
    d = s.read(8192)
    if d: out += d; end = time.time() + 1.2
s.close()
txt = re.sub(r'\x1b\[[0-9;?]*[a-zA-Z]|\x1b[()][0B]', '', out.decode('latin-1', 'replace')).replace('\r', '')
print(txt[-6000:])
