#!/usr/bin/env python3
"""Exercise an owned, network-free nativeVerify package, never the user's .dev app."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = 'com.cloudgeek.spool.android.nativeverify'
OUTPUT = ROOT / 'build/artifacts/android-native/device-ui'
APK = ROOT / 'build/android/gradle/app/outputs/apk/nativeVerify/app-nativeVerify.apk'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--serial', required=True)
    args = parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9_.:-]+', args.serial):
        raise SystemExit('Invalid adb serial')
    if not os.environ.get('TMPDIR', '').startswith(str(ROOT / 'build/tmp/')):
        raise SystemExit('Run through sh android/tools/run.sh')
    sdk = Path(os.environ.get('ANDROID_HOME', os.environ.get('ANDROID_SDK_ROOT', str(Path.home() / 'Library/Android/sdk'))))
    adb = sdk / 'platform-tools/adb'
    OUTPUT.mkdir(parents=True, exist_ok=True)

    def command(executable, *arguments, check=True):
        result = subprocess.run([str(executable), *map(str, arguments)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
        if check and result.returncode:
            raise RuntimeError(result.stderr.decode(errors='replace') or result.stdout.decode(errors='replace'))
        return result

    def device(*arguments, check=True):
        return command(adb, '-s', args.serial, *arguments, check=check)

    def shell(*arguments, check=True):
        return device('shell', *arguments, check=check).stdout.decode(errors='replace').strip()

    def focus():
        current = shell('dumpsys window | grep mCurrentFocus')
        if PACKAGE not in current:
            raise RuntimeError('Verification app is not foreground; no screenshot or input was performed')

    def report(predicate=lambda report: True):
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            result = device('exec-out', 'run-as', PACKAGE, 'cat', 'files/probe.json', check=False)
            try:
                data = json.loads(result.stdout)
                if predicate(data):
                    return data
            except (ValueError, KeyError):
                pass
            time.sleep(.15)
        raise RuntimeError('Synthetic layout report did not reach the expected state')

    port = os.environ.get('ANDROID_ADB_SERVER_PORT', '5037')
    if command('lsof', '-a', '-c', 'adb', '-iTCP:' + port, '-sTCP:LISTEN', '-t', check=False).returncode:
        raise SystemExit('Run probe-devices.sh first; this helper only reuses an existing adb server')
    if device('get-state').stdout.strip() != b'device':
        raise SystemExit('Selected device is not ready')
    if shell('pm', 'list', 'packages', PACKAGE):
        raise SystemExit('nativeverify is already installed; refusing to overwrite or clean an unowned installation')
    if not APK.is_file():
        raise SystemExit('Build :app:assembleNativeVerify first')
    badging = command(sdk / 'build-tools/36.1.0/aapt2', 'dump', 'badging', APK).stdout.decode()
    if "name='" + PACKAGE + "'" not in badging or re.search(r'^uses-permission', badging, re.MULTILINE):
        raise SystemExit('Verification APK identity/network-permission isolation failed')
    summary = {'package': PACKAGE, 'api': shell('getprop', 'ro.build.version.sdk'),
               'model': shell('getprop', 'ro.product.model'), 'networkPermissions': [], 'scenes': [], 'removed': False}
    installed = False
    try:
        result = device('install', '--no-incremental', APK)
        if b'Success' not in result.stdout:
            raise RuntimeError('Verification APK installation was not confirmed')
        installed = True
        scenes = [('login', theme, locale, None) for theme in (False, True) for locale in ('zh-CN', 'en', 'ja')]
        scenes += [('workspace', theme, 'en', None) for theme in (False, True)]
        scenes += [('markdown', theme, locale, case) for theme in (False, True) for locale, case in
                   [('en', 'tight-list'), ('en', 'table-wrapped'), ('zh-CN', 'chinese-blocks'), ('ja', 'japanese-blocks')]]
        scenes += [('messages', theme, 'en', None) for theme in (False, True)]
        scenes += [('settings', theme, 'zh-CN', None) for theme in (False, True)]
        scenes += [('language', theme, locale, None) for theme in (False, True) for locale in ('zh-CN', 'en', 'ja')]
        scenes += [('source', theme, 'en', None) for theme in (False, True)]

        def start(screen, dark, locale, case=None, ime=False):
            shell('am', 'force-stop', PACKAGE)
            shell('run-as', PACKAGE, 'rm', '-f', 'files/probe.json')
            arguments = ['am', 'start', '-W', '-n', PACKAGE + '/com.cloudgeek.spool.android.NativeVisualProbeActivity',
                         '--es', 'screen', screen, '--es', 'locale', locale, '--ez', 'dark', str(dark).lower()]
            if case:
                arguments += ['--es', 'case', case]
            if ime:
                arguments += ['--ez', 'imeProbe', 'true']
            shell(*arguments)
            data = report(lambda value: value['screen'] == screen and value['dark'] == dark and value['locale'] == locale
                          and (not case or case in value['boxes'])
                          and (screen not in ('settings', 'language') or value.get('settingsShowing'))
                          and (screen != 'language' or value.get('languageMenuShowing'))
                          and (screen != 'source' or value.get('syntaxColorSpans', 0) > 0 or value.get('syntaxError')))
            if data.get('syntaxError'):
                raise RuntimeError(data['syntaxError'])
            if screen == 'language' and (len(data.get('languageOptions', [])) != 4 or data['languageOptions'][1:] != ['简体中文', 'English', '日本語']):
                raise RuntimeError('Physical-device language menu differs from the three-locale requirement')
            focus()
            return data

        for screen, dark, locale, case in scenes:
            data = start(screen, dark, locale, case)
            name = '-'.join(filter(None, [screen, case, 'dark' if dark else 'light', locale]))
            image = device('exec-out', 'screencap', '-p').stdout
            if not image.startswith(b'\x89PNG\r\n\x1a\n'):
                raise RuntimeError('Device screenshot was not a PNG')
            (OUTPUT / (name + '.png')).write_bytes(image)
            (OUTPUT / (name + '.json')).write_text(json.dumps(data, indent=2) + '\n')
            summary['scenes'].append(name)
            print('Captured isolated native scene:', name, flush=True)

        start('workspace', False, 'en', ime=True)
        deadline = time.monotonic() + 25
        while True:
            result = device('exec-out', 'run-as', PACKAGE, 'cat', 'files/ime.json', check=False)
            try:
                ime = json.loads(result.stdout)
                break
            except ValueError:
                if time.monotonic() >= deadline:
                    raise RuntimeError('App-owned IME probe did not finish')
                time.sleep(.15)
        if ime.get('error'):
            raise RuntimeError(ime['error'])
        data, visible, grown, hidden = (ime[key] for key in ('before', 'visible', 'grown', 'hidden'))
        if not visible.get('imeVisible') or visible['paddingBottom'] <= data['paddingBottom']:
            raise RuntimeError('IME did not resize the shared window shell')
        composer = visible['boxes']['composer']
        if composer['y'] + composer['height'] > visible['root']['y'] + visible['root']['height'] - visible['paddingBottom'] + 1:
            raise RuntimeError('Composer overlaps the physical-device IME')
        if not grown.get('imeVisible') or grown['boxes']['input']['height'] <= data['boxes']['input']['height'] or hidden.get('imeVisible'):
            raise RuntimeError('Native multiline growth or IME hide did not match the expected state')
        # Only bounds are retained; never retain the user's keyboard UI or suggestions.
        (OUTPUT / 'ime-layout.json').write_text(json.dumps({'before': data, 'visible': visible, 'grown': grown, 'hidden': hidden}, indent=2) + '\n')
        summary['ime'] = 'app-owned focus/show, synthetic multiline growth, hide; composer stays above keyboard; no injected events or real gesture/composition claim'
    except Exception as error:
        summary['error'] = str(error)
        raise
    finally:
        if installed:
            shell('am', 'force-stop', PACKAGE, check=False)
            result = device('uninstall', PACKAGE, check=False)
            summary['removed'] = result.returncode == 0 and b'Success' in result.stdout
        (OUTPUT / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
    if not summary['removed']:
        raise RuntimeError('Owned verification package cleanup requires attention')
    print('Verified', len(summary['scenes']), 'isolated physical-device scenes and IME layout; owned package removed.')


if __name__ == '__main__':
    main()
