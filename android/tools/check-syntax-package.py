#!/usr/bin/env python3
"""Check the shipped APK, not just host Java tests, for syntax assets/ABIs/API-26 backports."""
import argparse
import hashlib
import json
import pathlib
import struct
import zipfile

ROOT = pathlib.Path(__file__).resolve().parents[2]
FORBIDDEN_METHODS = {
    ('Ljava/lang/CharSequence;', 'isEmpty'),
    ('Ljava/lang/StringBuilder;', 'isEmpty'),
    ('Ljava/lang/String;', 'isBlank'),
    ('Ljava/lang/String;', 'repeat'),
    ('Ljava/lang/System;', 'getLogger'),
    ('Ljava/util/stream/Stream;', 'toList'),
}


def dex_methods(data):
    if not data.startswith(b'dex\n'):
        raise ValueError('Not a DEX file')
    strings_count, strings_offset = struct.unpack_from('<II', data, 0x38)
    strings = []
    for i in range(strings_count):
        at = struct.unpack_from('<I', data, strings_offset + i * 4)[0]
        while data[at] & 128:
            at += 1
        at += 1
        # Names checked here are ASCII; arbitrary DEX MUTF-8 literal strings are not interpreted.
        strings.append(data[at:data.index(b'\0', at)].decode('utf8', errors='replace'))
    types_count, types_offset = struct.unpack_from('<II', data, 0x40)
    types = [strings[struct.unpack_from('<I', data, types_offset + i * 4)[0]] for i in range(types_count)]
    methods_count, methods_offset = struct.unpack_from('<II', data, 0x58)
    methods = set()
    for i in range(methods_count):
        owner, _, name = struct.unpack_from('<HHI', data, methods_offset + i * 8)
        methods.add((types[owner], strings[name]))
    if any(name.startswith(('Lorg/joni/', 'Lorg/jcodings/')) for name in types):
        raise ValueError('Unused Joni/Jcodings backend was packaged')
    return methods


def check_elf(data, machine):
    if data[:6] != b'\x7fELF\x02\x01' or struct.unpack_from('<H', data, 18)[0] != machine:
        raise ValueError('Unexpected native ELF format/ABI')
    offset = struct.unpack_from('<Q', data, 32)[0]
    size, count = struct.unpack_from('<HH', data, 54)
    loads = [struct.unpack_from('<Q', data, offset + i * size + 48)[0] for i in range(count)
             if struct.unpack_from('<I', data, offset + i * size)[0] == 1]
    if not loads or min(loads) < 16384:
        raise ValueError('Syntax native library lacks 16 KiB PT_LOAD alignment')
    for method in ('nCompile', 'nSearch', 'nClose'):
        if ('Java_com_cloudgeek_spool_textmate_NativeOniguruma_' + method).encode() not in data:
            raise ValueError('Missing syntax JNI export: ' + method)
    return loads


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--apk', type=pathlib.Path, default=ROOT / 'build/android/gradle/app/outputs/apk/debug/app-debug.apk')
    parser.add_argument('--abis', nargs='+', choices=('arm64-v8a', 'x86_64'), default=['arm64-v8a'])
    parser.add_argument('--report', type=pathlib.Path)
    args = parser.parse_args()
    assets = ROOT / 'android/app/src/main/assets/syntax'
    with zipfile.ZipFile(args.apk) as apk:
        names = set(apk.namelist())
        expected = {f'assets/syntax/{path.relative_to(assets)}': path for path in assets.rglob('*') if path.is_file()}
        if {name for name in names if name.startswith('assets/syntax/')} != set(expected):
            raise ValueError('Missing or stale packaged syntax assets')
        for name, source in expected.items():
            if apk.read(name) != source.read_bytes():
                raise ValueError('Packaged asset drift: ' + name)
        abis = {}
        packaged_abis = {name.split('/')[1] for name in names if name.startswith('lib/') and name.endswith('.so')}
        if packaged_abis != set(args.abis):
            raise ValueError('Unexpected packaged ABIs: ' + repr(sorted(packaged_abis)))
        machines = {'arm64-v8a': 183, 'x86_64': 62}
        for abi in args.abis:
            machine = machines[abi]
            abis[abi] = check_elf(apk.read(f'lib/{abi}/libspool-textmate.so'), machine)
        methods = set()
        dexes = [name for name in names if name.endswith('.dex')]
        for name in dexes:
            methods.update(dex_methods(apk.read(name)))
        syntax_jni = {('Lcom/cloudgeek/spool/textmate/NativeOniguruma;', name)
                      for name in ('nCompile', 'nSearch', 'nClose')}
        if not syntax_jni <= methods:
            raise ValueError('Syntax JNI names were removed or renamed: ' + repr(sorted(syntax_jni - methods)))
        unresolved = sorted(methods & FORBIDDEN_METHODS)
        if unresolved:
            raise ValueError('Methods unavailable on API 26 remain in DEX: ' + repr(unresolved))
        notices = apk.read('assets/third-party-notices.txt')
        if notices != (ROOT / 'android/THIRD_PARTY_NOTICES').read_bytes():
            raise ValueError('Packaged third-party notice drift')
        report = {
            'apkSHA256': hashlib.sha256(args.apk.read_bytes()).hexdigest(),
            'syntaxAssets': len(expected),
            'syntaxBytes': sum(apk.getinfo(name).file_size for name in expected),
            'syntaxCompressedBytes': sum(apk.getinfo(name).compress_size for name in expected),
            'nativeLoadAlignments': abis,
            'syntaxJniMethodsPreserved': True,
            'dexFiles': len(dexes),
            'unresolvedCheckedAPI26Methods': unresolved,
            'joniOrJcodingsRuntime': False,
            'thirdPartyNoticesMatch': True,
        }
    contents = json.dumps(report, indent=2) + '\n'
    if args.report:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(contents)
    print(contents, end='')


if __name__ == '__main__':
    main()
