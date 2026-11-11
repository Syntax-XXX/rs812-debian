import serial, sys, time
dev = sys.argv[1]
for baud in (115200, 9600):
    try:
        s = serial.Serial(dev, baud, timeout=0.3)
    except Exception as e:
        print(dev, baud, 'Fehler', e); continue
    s.reset_input_buffer()
    s.write(b'\r')
    time.sleep(1.5)
    data = s.read(4096)
    s.close()
    txt = data.decode('latin-1', 'replace').replace('\r', '')
    printable = sum(c.isprintable() or c == '\n' for c in txt)
    print(f'--- {dev} @ {baud}: {len(data)} Bytes, lesbar {printable}/{len(txt)}')
    print(txt[-300:])
