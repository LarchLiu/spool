#!/usr/bin/env python3
"""Use the same installed Robolectric font bytes for native and browser UI checks."""
import hashlib
import json
import math
import struct
from pathlib import Path
from zipfile import ZipFile

ROOT = Path(__file__).resolve().parents[2]
CACHE = ROOT / 'build/cache/android-ui-fonts'
JARS = sorted((ROOT / 'build/cache/gradle/caches/modules-2/files-2.1/org.robolectric/nativeruntime-dist-compat/1.0.18').glob('*/*.jar'))
if len(JARS) != 1:
    raise SystemExit('Run the pinned Gradle native UI tests first to resolve Robolectric fonts.')

FACES = {100: 'Thin', 300: 'Light', 400: 'Regular', 500: 'Medium', 700: 'Bold', 900: 'Black'}
CACHE.mkdir(parents=True, exist_ok=True)
manifest = {'source': 'org.robolectric:nativeruntime-dist-compat:1.0.18', 'faces': []}


def standalone_face(collection, index):
    """Repack a TTC face without changing its glyph, metric or shaping tables."""
    base = struct.unpack_from('>I', collection, 12 + index * 4)[0]
    version, count = struct.unpack_from('>4sH', collection, base)
    tables = []
    for i in range(count):
        tag, _, offset, length = struct.unpack_from('>4sIII', collection, base + 12 + i * 16)
        if tag == b'DSIG':
            continue
        data = bytearray(collection[offset:offset + length])
        if tag == b'head':
            data[8:12] = b'\0' * 4
        tables.append((tag, data))
    tables.sort()
    count = len(tables)
    selector = int(math.log2(count))
    header = bytearray(struct.pack('>4sHHHH', version, count, 16 * 2**selector, selector, count * 16 - 16 * 2**selector))
    body = bytearray()
    head = None
    for tag, data in tables:
        length = len(data)
        data += b'\0' * (-length % 4)
        checksum = sum(struct.unpack('>' + 'I' * (len(data) // 4), data)) & 0xffffffff
        offset = 12 + count * 16 + len(body)
        header += struct.pack('>4sIII', tag, checksum, offset, length)
        if tag == b'head':
            head = offset
        body += data
    result = header + body
    if head is not None:
        checksum = sum(struct.unpack('>' + 'I' * (len(result) // 4), result)) & 0xffffffff
        struct.pack_into('>I', result, head + 8, (0xb1b0afba - checksum) & 0xffffffff)
    return bytes(result)


with ZipFile(JARS[0]) as archive:
    for weight, name in FACES.items():
        for italic in (False, True):
            filename = 'Roboto-' + ('Italic' if name == 'Regular' and italic else name + ('Italic' if italic else '')) + '.ttf'
            data = archive.read('fonts/' + filename)
            target = CACHE / filename
            if not target.exists() or target.read_bytes() != data:
                target.write_bytes(data)
            manifest['faces'].append({'file': filename, 'weight': weight, 'italic': italic, 'sha256': hashlib.sha256(data).hexdigest()})
    mono = archive.read('fonts/DroidSansMono.ttf')
    target = CACHE / 'DroidSansMono.ttf'
    if not target.exists() or target.read_bytes() != mono:
        target.write_bytes(mono)
    manifest['mono'] = {'file': target.name, 'sha256': hashlib.sha256(mono).hexdigest()}
    collection = archive.read('fonts/NotoSansCJK-Regular.ttc')
    manifest['cjk'] = []
    for locale, index, name in [('zh-CN', 2, 'SC'), ('ja', 0, 'JP')]:
        data = standalone_face(collection, index)
        target = CACHE / ('NotoSansCJK-' + name + '.otf')
        if not target.exists() or target.read_bytes() != data:
            target.write_bytes(data)
        manifest['cjk'].append({'locale': locale, 'family': 'AndroidCjk' + name, 'index': index, 'file': target.name,
                                'sha256': hashlib.sha256(data).hexdigest(), 'sourceFile': 'NotoSansCJK-Regular.ttc',
                                'sourceSHA256': hashlib.sha256(collection).hexdigest()})
    manifest['fontConfigSHA256'] = hashlib.sha256(archive.read('fonts/fonts.xml')).hexdigest()
(CACHE / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
print('Prepared matching Robolectric/browser reference fonts (test assets only).')
