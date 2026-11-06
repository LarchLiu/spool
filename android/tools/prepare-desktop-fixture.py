#!/usr/bin/env python3
"""Generate synthetic Annex-B streams for the offline Android desktop probe."""
import os
from pathlib import Path
import re
import struct
import subprocess

ROOT = Path(__file__).resolve().parents[2]
OUTPUT = ROOT / 'build/android/native-verify/assets/desktop'


def main():
    if not os.environ.get('TMPDIR', '').startswith(str(ROOT / 'build/tmp/')):
        raise SystemExit('Run through sh android/tools/run.sh')
    OUTPUT.mkdir(parents=True, exist_ok=True)
    for stripe in range(2):
        source = f'testsrc2=size=640x180:rate=5,drawbox=x={60 + stripe * 260}:y=40:w=180:h=90:color={"red" if stripe == 0 else "blue"}:t=fill'
        encoded = subprocess.run(['ffmpeg', '-v', 'error', '-f', 'lavfi', '-i', source,
                                  '-frames:v', '8', '-an', '-c:v', 'libx264', '-profile:v', 'high',
                                  '-pix_fmt', 'yuv420p', '-preset', 'ultrafast', '-tune', 'zerolatency',
                                  '-x264-params', 'keyint=4:min-keyint=4:scenecut=0:aud=1:repeat-headers=1:bframes=0',
                                  '-f', 'h264', 'pipe:1'], check=True, capture_output=True).stdout
        starts = [match.start() for match in re.finditer(b'\x00\x00\x00\x01\x09', encoded)]
        if len(starts) != 8:
            raise RuntimeError('Expected 8 H.264 access units')
        starts.append(len(encoded))
        packets = bytearray()
        for frame in range(8):
            access = encoded[starts[frame]:starts[frame + 1]]
            key = any((match.group(1)[0] & 31) == 5 for match in re.finditer(b'\x00\x00\x01(.)', access, re.DOTALL))
            header = struct.pack('>BBIIBBBBH', 1, int(key), frame + 1, 0, stripe, 2, 2, 0, stripe * 180)
            packet = header + access
            packets.extend(struct.pack('>I', len(packet)))
            packets.extend(packet)
        (OUTPUT / f'stripe-{stripe}.bin').write_bytes(packets)
    print('Prepared 8 synthetic frames in 2 independent H.264 stripes.')


if __name__ == '__main__':
    main()
