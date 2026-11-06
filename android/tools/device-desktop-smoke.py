#!/usr/bin/env python3
"""Verify native desktop decoding/inputs in an owned network-free Android package."""
import argparse
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = 'com.cloudgeek.spool.android.nativeverify'
OUTPUT = ROOT / 'build/artifacts/android-desktop'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--serial', required=True)
    args = parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9_.:-]+', args.serial):
        raise SystemExit('Invalid device serial')
    if not os.environ.get('TMPDIR', '').startswith(str(ROOT / 'build/tmp/')):
        raise SystemExit('Run through sh android/tools/run.sh')
    sdk = Path(os.environ.get('ANDROID_HOME') or os.environ.get('ANDROID_SDK_ROOT') or Path.home() / 'Library/Android/sdk')
    adb = sdk / 'platform-tools/adb'
    server = log = None
    installed = False
    reports = {}

    def run(*arguments, check=True):
        result = subprocess.run([str(adb), '-s', args.serial, *map(str, arguments)], capture_output=True, timeout=60)
        if check and result.returncode:
            raise RuntimeError(result.stdout.decode(errors='replace') + result.stderr.decode(errors='replace'))
        return result

    def shell(*arguments):
        return run('shell', *arguments).stdout.decode().strip()

    def read(case, predicate):
        deadline = time.monotonic() + 25
        latest = None
        while time.monotonic() < deadline:
            result = run('exec-out', 'run-as', PACKAGE, 'cat', 'files/desktop-probe.json', check=False)
            try:
                latest = json.loads(result.stdout)
                if latest.get('case') == case:
                    if 'error' in latest:
                        raise RuntimeError(latest['error'])
                    if predicate(latest):
                        return latest
            except (ValueError, KeyError):
                pass
            time.sleep(.15)
        raise RuntimeError(f'Desktop probe timed out ({case}): {latest}')

    try:
        try:
            with socket.create_connection(('127.0.0.1', 5037), timeout=2):
                pass
        except OSError:
            log = open(Path(os.environ['TMPDIR']) / 'adb-server.log', 'w+')
            server = subprocess.Popen([str(adb), '-L', 'tcp:localhost:5037', 'server', 'nodaemon'], stdout=log, stderr=subprocess.STDOUT)
            for _ in range(50):
                if server.poll() is not None:
                    raise RuntimeError('Could not start owned ADB server')
                try:
                    with socket.create_connection(('127.0.0.1', 5037), timeout=.2):
                        break
                except OSError:
                    time.sleep(.1)
            else:
                raise RuntimeError('ADB server did not become ready')
        if shell('pm', 'list', 'packages', PACKAGE):
            raise RuntimeError('nativeverify is already installed; refusing to overwrite an unowned package')
        apk = ROOT / 'build/android/gradle/app/outputs/apk/nativeVerify/app-nativeVerify.apk'
        badging = subprocess.run([str(sdk / 'build-tools/36.1.0/aapt2'), 'dump', 'badging', str(apk)], check=True, capture_output=True).stdout.decode()
        permissions = re.findall(r"^uses-permission: name='([^']+)'", badging, re.MULTILINE)
        if f"name='{PACKAGE}'" not in badging or set(permissions) - {PACKAGE + '.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION'}:
            raise RuntimeError('Test package identity or network isolation failed')
        OUTPUT.mkdir(parents=True, exist_ok=True)
        install = run('install', '--no-incremental', apk)
        if b'Success' not in install.stdout:
            raise RuntimeError('Test package installation not confirmed')
        installed = True
        for case in ('control', 'readonly', 'revoke'):
            run('shell', 'am', 'start', '-S', '-n', PACKAGE + '/com.cloudgeek.spool.android.NativeDesktopProbeActivity',
                '--es', 'case', case, '--ez', 'readOnly', str(case == 'readonly').lower(), '--ez', 'revoke', str(case == 'revoke').lower())
            report = read(case, lambda value: value['acknowledged'] == 8 and value['exercised'] and value.get('pixelsMatch') and value['zoom'] == 1
                          and (case != 'control' or value['fullscreen'] and value['fullscreenHeight'] > value['windowedHeight'] and value.get('systemBarsHidden')))
            if report['iconButtons'] != 17 or report['textButtons'] != 0:
                raise RuntimeError('Desktop controls must use labelled icon buttons')
            commands = report['commands']
            if [value for value in commands if value.startswith('a,')] != [f'a,{i}' for i in range(1, 9)]:
                raise RuntimeError('Frame acknowledgements were incomplete or out of order')
            inputs = [value for value in commands if value.startswith(('m,', 'b,', 'k,'))]
            if case == 'readonly':
                if inputs or report['controllable']:
                    raise RuntimeError('Read-only desktop sent remote input')
            elif not all(command in commands for command in ('m,320,180', 'b,1,1', 'b,1,0', 'k,16797229,1', 'k,16797229,0')):
                raise RuntimeError('Touch/Chinese IME commands did not match voxl')
            if any(command.startswith('sz,') for command in commands):
                raise RuntimeError('Local zoom resized the host desktop')
            if case == 'control':
                # Recent Android versions put display focus outside the `windows` section.
                focus = re.search(r'mCurrentFocus=([^\r\n]*)', shell('dumpsys', 'window'))
                if focus is None or PACKAGE not in focus.group(1):
                    raise RuntimeError('Synthetic viewer is not foreground; refusing screenshot')
                (OUTPUT / 'device-fullscreen.png').write_bytes(run('exec-out', 'screencap', '-p').stdout)
                run('shell', 'input', 'tap', report['toolbarToggleX'], report['toolbarToggleY'])
                report['toolbarCollapsedState'] = read(case, lambda value: value['toolbarCollapsed'] and value['toolbarWidth'] < value['width'] / 4
                                                      and value['fullscreen'] and value['showing'] and not value['wireClosed'])
                (OUTPUT / 'device-toolbar-collapsed.png').write_bytes(run('exec-out', 'screencap', '-p').stdout)
                collapsed = report['toolbarCollapsedState']
                run('shell', 'input', 'tap', collapsed['toolbarToggleX'], collapsed['toolbarToggleY'])
                report['toolbarExpandedState'] = read(case, lambda value: not value['toolbarCollapsed'] and value['toolbarWidth'] > value['width'] / 2
                                                     and value['fullscreen'] and value['showing'] and not value['wireClosed'])
                run('shell', 'input', 'tap', report['fullscreenButtonX'], report['fullscreenButtonY'])
                report['fullscreenExit'] = read(case, lambda value: not value['fullscreen'] and value['showing'] and not value['wireClosed']
                                                and value['height'] < value['fullscreenHeight'] and not value.get('systemBarsHidden'))
                (OUTPUT / 'device.png').write_bytes(run('exec-out', 'screencap', '-p').stdout)
                run('shell', 'am', 'start', '-n', PACKAGE + '/com.cloudgeek.spool.android.NativeVisualProbeActivity')
                report['backgroundCleanup'] = read(case, lambda value: value['wireClosed'] and value['disconnected'])
            if case == 'revoke':
                report['revocation'] = read(case, lambda value: value['wireClosed'] and value['disconnected'] and not value['controllable'])
            reports[case] = report
            (OUTPUT / 'device.json').write_text(json.dumps(reports, ensure_ascii=False, indent=2) + '\n')
            print(f'PASS: {case}', flush=True)
    finally:
        if installed:
            result = run('uninstall', PACKAGE, check=False)
            if result.returncode or b'Success' not in result.stdout:
                print('Owned nativeverify package cleanup failed', flush=True)
        if server is not None:
            server.terminate()
            try:
                server.wait(timeout=10)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait()
        if log is not None:
            log.close()


if __name__ == '__main__':
    main()
