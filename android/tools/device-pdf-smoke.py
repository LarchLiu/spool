#!/usr/bin/env python3
"""Test the actual PDF renderer in an owned, network-free nativeVerify installation."""
import argparse
import json
import os
import re
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = 'com.cloudgeek.spool.android.nativeverify'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--serial', required=True)
    args = parser.parse_args()
    if not os.environ.get('TMPDIR', '').startswith(str(ROOT / 'build/tmp/')):
        raise SystemExit('Run through sh android/tools/run.sh')
    sdk = Path(os.environ.get('ANDROID_HOME', str(Path.home() / 'Library/Android/sdk')))
    adb = sdk / 'platform-tools/adb'
    output = ROOT / 'build/artifacts/android-pdf'
    output.mkdir(parents=True, exist_ok=True)

    def device(*arguments, check=True):
        result = subprocess.run([str(adb), '-s', args.serial, *map(str, arguments)], capture_output=True, timeout=60)
        if check and result.returncode:
            raise RuntimeError(result.stderr.decode(errors='replace') or result.stdout.decode(errors='replace'))
        return result.stdout

    def shell(*arguments):
        return device('shell', *arguments).decode(errors='replace').strip()

    def report(predicate):
        deadline = time.monotonic() + 12
        last = None
        while time.monotonic() < deadline:
            raw = device('exec-out', 'run-as', PACKAGE, 'cat', 'files/probe.json', check=False)
            try:
                last = json.loads(raw)
                if predicate(last):
                    return last
            except (ValueError, KeyError):
                pass
            time.sleep(.15)
        raise AssertionError(f'PDF state did not settle: {last}')

    def tap(snapshot, name):
        if PACKAGE not in shell('dumpsys window | grep mCurrentFocus'):
            raise AssertionError('Verification app is not foreground; refusing to tap another app')
        box = snapshot['boxes'][name]
        shell('input', 'tap', int(box['x'] + box['width'] / 2), int(box['y'] + box['height'] / 2))

    def swipe(snapshot, forward=True):
        if PACKAGE not in shell('dumpsys window | grep mCurrentFocus'):
            raise AssertionError('Verification app is not foreground; refusing to swipe another app')
        box = snapshot['boxes']['pdfViewport']
        x = int(box['x'] + box['width'] / 2)
        top, bottom = int(box['y'] + box['height'] * .2), int(box['y'] + box['height'] * .85)
        shell('input', 'swipe', x, bottom if forward else top, x, top if forward else bottom, 700)

    if shell('pm', 'list', 'packages', PACKAGE):
        raise SystemExit('nativeverify is already installed; refusing to overwrite an unowned installation')
    apk = ROOT / 'build/android/gradle/app/outputs/apk/nativeVerify/app-nativeVerify.apk'
    badging = subprocess.run([str(sdk / 'build-tools/36.1.0/aapt2'), 'dump', 'badging', str(apk)], capture_output=True, check=True).stdout.decode()
    permissions = set(re.findall(r"^uses-permission: name='([^']+)'", badging, re.MULTILINE))
    # AndroidX contributes a package-local signature permission, without network access.
    if f"name='{PACKAGE}'" not in badging or permissions - {PACKAGE + '.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION'}:
        raise AssertionError('Verification APK identity or network isolation failed')
    installed = False
    summary = {}
    try:
        if b'Success' not in device('install', '--no-incremental', apk):
            raise AssertionError('Verification APK installation failed')
        installed = True
        component = PACKAGE + '/com.cloudgeek.spool.android.NativeVisualProbeActivity'
        shell('am', 'start', '-W', '-n', component, '--es', 'screen', 'pdf', '--es', 'locale', 'zh-CN')
        first = report(lambda data: data.get('pdfReady') and data.get('pdfPosition') == '1 / 2')
        assert first['pdfPageWidth'] == 320 and first['pdfPageHeight'] == 480
        (output / 'page-1.png').write_bytes(device('exec-out', 'screencap', '-p'))
        current = first
        for _ in range(3):
            swipe(current)
            current = report(lambda data: data.get('pdfScroll', 0) > 0)
            if current['pdfPosition'] == '2 / 2':
                break
        second = report(lambda data: data.get('pdfReady') and data.get('pdfPosition') == '2 / 2')
        assert second['pdfPageWidth'] == 480 and second['pdfPageHeight'] == 320
        assert 0 < second['pdfCachedPages'] <= 3
        (output / 'page-2.png').write_bytes(device('exec-out', 'screencap', '-p'))
        tap(second, 'pdfPrevious')
        first = report(lambda data: data.get('pdfReady') and data.get('pdfPosition') == '1 / 2')
        tap(first, 'pdfNext')
        second = report(lambda data: data.get('pdfReady') and data.get('pdfPosition') == '2 / 2')
        tap(second, 'pdfZoomIn')
        zoomed = report(lambda data: data.get('pdfReady') and data.get('pdfZoom', 1) > 1)
        for _ in range(6):
            current = zoomed['pdfZoom']
            tap(zoomed, 'pdfZoomIn')
            zoomed = report(lambda data: data.get('pdfReady') and data.get('pdfZoom', 1) > current)
            if zoomed['pdfZoom'] == 4:
                break
        assert zoomed['pdfZoom'] == 4 and 0 < zoomed['pdfBitmapPixels'] <= 4_000_000
        tap(zoomed, 'pdfFit')
        fitted = report(lambda data: data.get('pdfReady') and data.get('pdfZoom') == 1)
        tap(fitted, 'pdfPrevious')
        back = report(lambda data: data.get('pdfReady') and data.get('pdfPosition') == '1 / 2')
        tap(back, 'pdfClose')
        report(lambda data: not data.get('pdfOpen', True))
        assert 'spool-pdf-' not in shell('run-as', PACKAGE, 'ls', 'cache')
        shell('am', 'force-stop', PACKAGE)
        shell('run-as', PACKAGE, 'rm', '-f', 'files/probe.json')
        shell('am', 'start', '-W', '-n', component, '--es', 'screen', 'pdf', '--es', 'locale', 'zh-CN', '--ez', 'invalidPdf', 'true')
        damaged = report(lambda data: '无法预览' in data.get('pdfStatus', ''))
        summary = {'nativePdfRendering': True, 'pages': 2, 'portraitAndLandscape': True, 'navigation': True, 'continuousScrolling': True,
                   'zoomAndFit': True, 'boundedPageBitmap': True, 'closeReleasesDocument': True, 'invalidPdfFallback': True,
                   'api': first['api'], 'model': shell('getprop', 'ro.product.model'), 'removed': False}
        print(json.dumps(summary, ensure_ascii=False))
    finally:
        if installed:
            shell('am', 'force-stop', PACKAGE)
            if b'Success' not in device('uninstall', PACKAGE):
                raise AssertionError('Owned verification app could not be removed')
            summary['removed'] = True
        if summary:
            (output / 'device-report.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2) + '\n')


if __name__ == '__main__':
    main()
