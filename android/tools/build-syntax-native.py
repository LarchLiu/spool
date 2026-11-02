#!/usr/bin/env python3
"""Build the same pinned Oniguruma/JNI sources for host tests and the app's two Android ABIs."""
import argparse
import fcntl
import hashlib
import os
import pathlib
import platform
import subprocess
import tarfile
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[2]
CACHE = ROOT / 'build/cache/android-syntax'
OUTPUT = ROOT / 'build/android/syntax'
HASH = 'ad92309d0d13eebc27f6592e875f3efbfa3dda2bf6da5952e00f0a2120c921a8'
NDK_VERSION = '29.0.14206865'


def build(name, options):
    directory = OUTPUT / (name + '-build')
    subprocess.run(['cmake', '-S', str(ROOT / 'android/textmate/src/main/cpp'), '-B', str(directory),
                    '-DCMAKE_BUILD_TYPE=Release', '-DONIG_SOURCE=' + str(CACHE / 'oniguruma-6.9.10'),
                    '-DCMAKE_LIBRARY_OUTPUT_DIRECTORY=' + str(OUTPUT / name), *options], check=True)
    subprocess.run(['cmake', '--build', str(directory), '--target', 'spool-textmate', '--parallel', '4'], check=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--host', action='store_true')
    parser.add_argument('--android', action='store_true')
    args = parser.parse_args()
    if not args.host and not args.android:
        parser.error('Specify --host and/or --android')
    CACHE.mkdir(parents=True, exist_ok=True)
    archive = CACHE / 'oniguruma-6.9.10.tar.gz'
    if not archive.exists():
        temporary = pathlib.Path(os.environ['TMPDIR']) / archive.name
        urllib.request.urlretrieve('https://codeload.github.com/kkos/oniguruma/tar.gz/refs/tags/v6.9.10', temporary)
        if hashlib.sha256(temporary.read_bytes()).hexdigest() != HASH:
            raise ValueError('Unexpected Oniguruma source archive')
        temporary.rename(archive)
    if hashlib.sha256(archive.read_bytes()).hexdigest() != HASH:
        raise ValueError('Unexpected Oniguruma source archive')
    source = CACHE / 'oniguruma-6.9.10'
    marker = source / '.spool-source-sha256'
    if marker.is_file() and marker.read_text() != HASH:
        raise ValueError('Unexpected extracted Oniguruma source marker')
    if not marker.is_file():
        with tarfile.open(archive) as tar:
            tar.extractall(CACHE, filter='data')
        marker.write_text(HASH)
    sdk = pathlib.Path(os.environ.get('ANDROID_HOME', os.environ.get('ANDROID_SDK_ROOT', str(pathlib.Path.home() / 'Library/Android/sdk'))))
    ndk = pathlib.Path(os.environ.get('ANDROID_NDK_HOME', sdk / 'ndk' / NDK_VERSION))
    java = pathlib.Path(os.environ.get('JAVA_HOME', '/Applications/Android Studio.app/Contents/jbr/Contents/Home'))
    needs_headers = args.host and not (java / 'include/jni.h').is_file()
    if (args.android or needs_headers) and not (ndk / 'toolchains/llvm/prebuilt').is_dir():
        raise ValueError(f'Missing NDK {ndk}; install {NDK_VERSION} or set ANDROID_NDK_HOME. Host checks can instead use a JDK with JNI headers.')
    if args.host:
        options = ['-DJAVA_HOME=' + str(java), '-DJNI_PLATFORM=' + ('darwin' if platform.system() == 'Darwin' else 'linux')]
        if needs_headers:
            prebuilt = next((ndk / 'toolchains/llvm/prebuilt').iterdir())
            options.append('-DJNI_NDK_INCLUDE=' + str(prebuilt / 'sysroot/usr/include'))
        build('host', options)
    if args.android:
        for abi in ['arm64-v8a', 'x86_64']:
            build('jni/' + abi, ['-DCMAKE_TOOLCHAIN_FILE=' + str(ndk / 'build/cmake/android.toolchain.cmake'), '-DANDROID_ABI=' + abi, '-DANDROID_PLATFORM=android-26'])


if __name__ == '__main__':
    OUTPUT.mkdir(parents=True, exist_ok=True)
    with (OUTPUT / '.build.lock').open('w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        main()
