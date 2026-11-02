#!/usr/bin/env python3
"""Reproducible Android platform adaptation of the pinned, otherwise unmodified TM4E core.

The replacement OnigString source lives in android/textmate. No tokenizer rules,
theme matching, grammars or regex patterns are rewritten here.
"""
import hashlib
import pathlib
import struct
import sys
import zipfile

EXPECTED = 'd78182d6362657a745236d1a085ac2ee11a958f9c3354d8faa87e36563e16174'
BRIDGE = b'com/cloudgeek/spool/textmate/TextMatePlatform'


def instruction_length(code, offset):
    opcode = code[offset]
    if opcode in (0xaa, 0xab):
        aligned = (offset + 4) & ~3
        if opcode == 0xaa:
            low, high = struct.unpack_from('>ii', code, aligned + 4)
            return aligned - offset + 12 + (high - low + 1) * 4
        pairs = struct.unpack_from('>i', code, aligned + 4)[0]
        return aligned - offset + 8 + pairs * 8
    if opcode == 0xc4:
        return 6 if code[offset + 1] == 0x84 else 4
    if opcode in (0x10, 0x12, 0xa9, 0xbc) or 0x15 <= opcode <= 0x19 or 0x36 <= opcode <= 0x3a:
        return 2
    if opcode in (0x11, 0x13, 0x14, 0x84, 0xbb, 0xbd, 0xc0, 0xc1, 0xc6, 0xc7) or 0x99 <= opcode <= 0xa8 or 0xb2 <= opcode <= 0xb8:
        return 3
    if opcode == 0xc5:
        return 4
    if opcode in (0xb9, 0xba, 0xc8, 0xc9):
        return 5
    return 1


def adapt_empty_calls(tail, pool, targets, bridge_method):
    """Keep bytecode offsets/stack maps unchanged, replacing only unsupported isEmpty calls."""
    def attributes(offset, count, methods):
        for _ in range(count):
            name, length = struct.unpack_from('>HI', tail, offset)
            start = offset + 6
            if methods and pool[name] == b'Code':
                size = struct.unpack_from('>I', tail, start + 4)[0]
                code = memoryview(tail)[start + 8:start + 8 + size]
                at = 0
                while at < size:
                    opcode = code[at]
                    step = instruction_length(code, at)
                    if opcode in (0xb6, 0xb9) and struct.unpack_from('>H', code, at + 1)[0] in targets:
                        code[at] = 0xb8  # invokestatic consumes the same receiver and returns the same boolean.
                        struct.pack_into('>H', code, at + 1, bridge_method)
                        if opcode == 0xb9:
                            code[at + 3:at + 5] = b'\0\0'  # NOP padding retains all branch/exception offsets.
                    at += step
                if at != size:
                    raise ValueError('Invalid bytecode boundary')
            offset = start + length
        return offset

    offset = 8 + struct.unpack_from('>H', tail, 6)[0] * 2
    for methods in (False, True):
        count = struct.unpack_from('>H', tail, offset)[0]
        offset += 2
        for _ in range(count):
            attributes_count = struct.unpack_from('>H', tail, offset + 6)[0]
            offset = attributes(offset + 8, attributes_count, methods)


def adapt(data):
    if data[:4] != b'\xca\xfe\xba\xbe':
        raise ValueError('Invalid Java class')
    count = struct.unpack_from('>H', data, 8)[0]
    result = bytearray(data[:10])
    pool = [None] * count
    offset, index = 10, 1
    sizes = {3: 4, 4: 4, 5: 8, 6: 8, 7: 2, 8: 2, 9: 4, 10: 4,
             11: 4, 12: 4, 15: 3, 16: 2, 17: 4, 18: 4, 19: 2, 20: 2}
    while index < count:
        tag = data[offset]
        offset += 1
        result.append(tag)
        if tag == 1:
            length = struct.unpack_from('>H', data, offset)[0]
            offset += 2
            value = data[offset:offset + length]
            pool[index] = value
            offset += length
            value = value.replace(b'java/lang/System$Logger$Level', BRIDGE + b'$Level')
            value = value.replace(b'java/lang/System$Logger', BRIDGE + b'$Logger')
            value = value.replace(b'java/lang/System', BRIDGE)
            result.extend(struct.pack('>H', len(value)))
            result.extend(value)
        else:
            length = sizes[tag]
            if tag in (7, 8, 16, 19, 20):
                pool[index] = (tag, struct.unpack_from('>H', data, offset)[0])
            elif tag in (9, 10, 11, 12, 17, 18):
                pool[index] = (tag, *struct.unpack_from('>HH', data, offset))
            result.extend(data[offset:offset + length])
            offset += length
            if tag in (5, 6):
                index += 1
        index += 1
    targets = set()
    for index, value in enumerate(pool):
        if not isinstance(value, tuple) or value[0] not in (10, 11):
            continue
        owner = pool[pool[value[1]][1]]
        _, name, descriptor = pool[value[2]]
        if owner in (b'java/lang/CharSequence', b'java/lang/StringBuilder') and pool[name] == b'isEmpty' and pool[descriptor] == b'()Z':
            targets.add(index)
    tail = bytearray(data[offset:])
    if targets:
        # Android 26 lacks CharSequence/StringBuilder.isEmpty; core desugaring does not backport them.
        for value in (BRIDGE, b'isEmpty', b'(Ljava/lang/CharSequence;)Z'):
            result.extend(b'\x01' + struct.pack('>H', len(value)) + value)
        result.extend(b'\x07' + struct.pack('>H', count))
        result.extend(b'\x0c' + struct.pack('>HH', count + 1, count + 2))
        result.extend(b'\x0a' + struct.pack('>HH', count + 3, count + 4))
        struct.pack_into('>H', result, 8, count + 6)
        adapt_empty_calls(tail, pool, targets, count + 5)
    result.extend(tail)
    return bytes(result)


def main():
    source, target = map(pathlib.Path, sys.argv[1:])
    if hashlib.sha256(source.read_bytes()).hexdigest() != EXPECTED:
        raise ValueError('Unexpected TM4E binary; review the adaptation before upgrading')
    target.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(source) as original, zipfile.ZipFile(target, 'w', zipfile.ZIP_DEFLATED) as output:
        for name in sorted(original.namelist()):
            if name.endswith('.class') and any(name.startswith('org/eclipse/tm4e/core/internal/oniguruma/' + replacement) for replacement in ('OnigString', 'OnigRegExp', 'OnigResult')):
                continue
            if name.startswith('META-INF/') and name.endswith(('.SF', '.RSA', '.DSA')):
                continue
            data = original.read(name)
            if name.endswith('.class'):
                data = adapt(data)
            entry = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
            entry.compress_type = zipfile.ZIP_DEFLATED
            output.writestr(entry, data)


if __name__ == '__main__':
    main()
