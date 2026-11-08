package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageInfo;
import android.content.pm.Signature;
import android.content.pm.SigningInfo;
import android.os.Build;
import java.io.ByteArrayInputStream;
import java.io.File;
import java.io.IOException;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Rule;
import org.junit.Test;
import org.junit.rules.TemporaryFolder;
import org.junit.runner.RunWith;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NativeUpdateTest {
    @Rule public TemporaryFolder files = new TemporaryFolder();
    static JSONObject metadata(String version, byte[] data) throws Exception {
        String name = "spool-" + version + "-android-arm64.apk";
        String base = "https://github.com/" + NativeUpdateRelease.REPOSITORY + "/releases/download/v" + version + "/";
        JSONArray assets = new JSONArray();
        for (String asset : new String[]{name, "SHA256SUMS"}) assets.put(new JSONObject().put("name", asset)
                .put("browser_download_url", base + asset).put("size", asset.equals(name) ? data.length : 120));
        return new JSONObject().put("tag_name", "v" + version).put("draft", false).put("prerelease", false).put("assets", assets);
    }
    static NativeUpdateRelease release(byte[] data) throws Exception { return NativeUpdateRelease.parse(metadata("0.1.18", data), "0.1.17"); }
    static byte[] bytes(String text) { return text.getBytes(StandardCharsets.UTF_8); }
    static String hash(byte[] data) throws Exception {
        StringBuilder hex = new StringBuilder(); for (byte b : MessageDigest.getInstance("SHA-256").digest(data)) hex.append(String.format("%02x", b & 255)); return hex.toString();
    }
    static final class Connection extends HttpURLConnection {
        byte[] data; int status; String location; boolean disconnected;
        Runnable reading = () -> {};
        Connection(URL url, byte[] data, int status, String location) { super(url); this.data = data; this.status = status; this.location = location; }
        @Override public int getResponseCode() { return status; }
        @Override public String getHeaderField(String name) { return name.equals("Location") ? location : null; }
        @Override public long getContentLengthLong() { return data.length; }
        @Override public java.io.InputStream getInputStream() { reading.run(); return new ByteArrayInputStream(data); }
        @Override public void disconnect() { disconnected = true; }
        @Override public boolean usingProxy() { return false; }
        @Override public void connect() {}
    }

    @Test public void releaseMetadataRequiresTheExactStableApkChecksumAndTrustedUrls() throws Exception {
        byte[] data = bytes("apk");
        NativeUpdateRelease release = release(data);
        assertEquals("0.1.18", release.version); assertEquals(1019, release.versionCode);
        assertNull(NativeUpdateRelease.parse(metadata("0.1.17", data), "0.1.17"));
        assertNull(NativeUpdateRelease.parse(metadata("0.1.16", data), "0.1.17"));
        assertNotNull(NativeUpdateRelease.parse(metadata("0.1.18", data), "0.1.18-beta.2"));
        for (String change : new String[]{"draft", "prerelease", "no-apk", "no-sums", "duplicate", "url", "large", "invalid-tag"}) {
            JSONObject json = metadata("0.1.18", data); JSONArray assets = json.getJSONArray("assets");
            switch (change) {
                case "draft": case "prerelease": json.put(change, true); break;
                case "no-apk": assets.remove(0); break;
                case "no-sums": assets.remove(1); break;
                case "duplicate": assets.put(assets.get(0)); break;
                case "url": assets.getJSONObject(0).put("browser_download_url", "https://evil.example/apk"); break;
                case "large": assets.getJSONObject(0).put("size", NativeUpdateRelease.MAX_APK_BYTES + 1); break;
                case "invalid-tag": json.put("tag_name", "v0.1.018"); break;
            }
            assertThrows(change, Exception.class, () -> NativeUpdateRelease.parse(json, "0.1.17"));
        }
        assertEquals(1018, NativeUpdateRelease.versionCode("0.1.17"));
        assertEquals(0, NativeUpdateRelease.compare("0.1.18", "0.1.18"));
        assertTrue(NativeUpdateRelease.compare("0.1.18-beta.10", "0.1.18-beta.2") > 0);
        assertTrue(NativeUpdateRelease.compare("1.0.0", "0.100.999") > 0);
        for (String bad : new String[]{"0.1.1000", "0.1000.1", "999999999.0.0", "0.1.18-beta.01"})
            assertThrows(Exception.class, () -> NativeUpdateRelease.versionCode(bad));
    }

    @Test public void downloadRejectsTamperingTruncationOversizedResponsesAndDuplicateChecksums() throws Exception {
        byte[] apk = bytes("a fake APK payload"); NativeUpdateRelease release = release(apk);
        String sums = hash(apk) + "  " + release.name + "\n";
        assertEquals(hash(apk), release.checksum(sums));
        assertThrows(IOException.class, () -> release.checksum(sums + sums));
        assertThrows(IOException.class, () -> release.checksum(hash(apk) + "  wrong.apk\n"));
        for (String mode : new String[]{"valid", "tampered", "truncated", "oversized"}) {
            byte[] payload = apk.clone();
            if (mode.equals("tampered")) payload[0] ^= 1;
            if (mode.equals("truncated")) payload = Arrays.copyOf(apk, apk.length - 1);
            if (mode.equals("oversized")) payload = Arrays.copyOf(apk, apk.length + 1);
            byte[] content = payload;
            NativeUpdateNetwork network = new NativeUpdateNetwork(url -> new Connection(url, url.toString().endsWith("SHA256SUMS") ? bytes(sums) : content, 200, null));
            File file = new File(files.getRoot(), mode + ".apk"); List<Long> progress = new ArrayList<>();
            if (mode.equals("valid")) {
                assertEquals(hash(apk), network.download(release, file, progress::add));
                assertArrayEquals(apk, Files.readAllBytes(file.toPath()));
                assertEquals(Long.valueOf(apk.length), progress.get(progress.size() - 1));
            } else { assertThrows(Exception.class, () -> network.download(release, file, progress::add)); assertFalse(file.exists()); }
        }
    }

    @Test public void networkBoundsRedirectsAndResponsesAndNeverUsesPlainHttpOrOtherHosts() throws Exception {
        for (String bad : new String[]{"http://github.com/a", "https://github.com.evil.test/a", "https://user:pass@github.com/a", "https://github.com:444/a", "file:///etc/hosts", "https://github.com/a#fragment"})
            assertFalse(bad, NativeUpdateNetwork.allowed(new URL(bad)));
        List<Connection> opened = new ArrayList<>();
        NativeUpdateNetwork network = new NativeUpdateNetwork(url -> {
            Connection connection = new Connection(url, bytes("ok"), opened.isEmpty() ? 302 : 200, "https://release-assets.githubusercontent.com/asset?token=example");
            opened.add(connection); return connection;
        });
        assertArrayEquals(bytes("ok"), network.read("https://github.com/owner/repo/releases/download/v1/apk", 10));
        assertEquals(2, opened.size());
        for (Connection connection : opened) {
            assertTrue(connection.disconnected); assertFalse(connection.getInstanceFollowRedirects());
            assertEquals("", connection.getRequestProperty("Cookie")); assertNull(connection.getRequestProperty("Authorization"));
        }
        NativeUpdateNetwork evil = new NativeUpdateNetwork(url -> new Connection(url, bytes(""), 302, "https://evil.example/update"));
        assertThrows(IOException.class, () -> evil.read("https://github.com/a", 10));
        NativeUpdateNetwork loop = new NativeUpdateNetwork(url -> new Connection(url, bytes(""), 302, "https://github.com/loop"));
        assertThrows(IOException.class, () -> loop.read("https://github.com/a", 10));
        NativeUpdateNetwork big = new NativeUpdateNetwork(url -> new Connection(url, new byte[11], 200, null));
        assertThrows(IOException.class, () -> big.read("https://github.com/a", 10));
    }

    @Test public void cancellationDisconnectsTheRequestAndRemovesThePartialApk() throws Exception {
        byte[] apk = bytes("payload"); NativeUpdateRelease release = release(apk);
        byte[] sums = bytes(hash(apk) + "  " + release.name);
        NativeUpdateNetwork[] ref = new NativeUpdateNetwork[1]; Connection[] transfer = new Connection[1];
        ref[0] = new NativeUpdateNetwork(url -> {
            if (url.toString().endsWith("SHA256SUMS")) return new Connection(url, sums, 200, null);
            transfer[0] = new Connection(url, apk, 200, null); transfer[0].reading = () -> ref[0].cancel(); return transfer[0];
        });
        File file = new File(files.getRoot(), "partial.apk");
        assertThrows(Exception.class, () -> ref[0].download(release, file, received -> {}));
        assertTrue(transfer[0].disconnected); assertFalse(file.exists());
    }

    @SuppressWarnings("deprecation")
    static PackageInfo identity(String version, String signer) throws Exception {
        PackageInfo info = new PackageInfo(); info.packageName = NativeUpdateRelease.PACKAGE;
        info.versionName = version; info.versionCode = (int) NativeUpdateRelease.versionCode(version);
        Signature[] signatures = {new Signature(signer.getBytes(StandardCharsets.UTF_8))};
        info.signatures = signatures;
        if (Build.VERSION.SDK_INT >= 28) {
            info.signingInfo = new SigningInfo(); Shadows.shadowOf(info.signingInfo).setSignatures(signatures);
        }
        return info;
    }
    @Test public void packageVerificationRejectsOtherAppsVersionsDowngradesAndDifferentSigners() throws Exception {
        NativeUpdateRelease release = release(bytes("apk"));
        PackageInfo installed = identity("0.1.17", "key-one");
        NativeAppUpdate.verifyIdentity(installed, identity("0.1.18", "key-one"), release);
        for (String change : new String[]{"package", "version", "code", "signer", "unsigned"}) {
            PackageInfo candidate = identity("0.1.18", "key-one");
            if (change.equals("package")) candidate.packageName += ".dev";
            if (change.equals("version")) candidate.versionName = "0.1.19";
            if (change.equals("code")) candidate.versionCode--;
            if (change.equals("signer")) candidate = identity("0.1.18", "key-two");
            if (change.equals("unsigned")) { candidate.signatures = null; if (Build.VERSION.SDK_INT >= 28) candidate.signingInfo = null; }
            PackageInfo value = candidate;
            assertThrows(change, Exception.class, () -> NativeAppUpdate.verifyIdentity(installed, value, release));
        }
        assertThrows(Exception.class, () -> NativeAppUpdate.verifyIdentity(identity("0.1.19", "key-one"), identity("0.1.18", "key-one"), release));
    }

    @Test public void installerUsesOnlyAReadGrantedContentUriForThePrivateApkCache() throws Exception {
        Context context = RuntimeEnvironment.getApplication();
        File directory = new File(context.getCacheDir(), "app-updates"); assertTrue(directory.isDirectory() || directory.mkdirs());
        File apk = new File(directory, "update.apk"); Files.write(apk.toPath(), bytes("APK"));
        try {
            Intent intent = NativeAppUpdate.installIntent(context, apk);
            assertEquals(Intent.ACTION_VIEW, intent.getAction()); assertEquals("content", intent.getData().getScheme());
            assertEquals(context.getPackageName() + ".updates", intent.getData().getAuthority());
            assertEquals("application/vnd.android.package-archive", intent.getType());
            assertEquals(Intent.FLAG_GRANT_READ_URI_PERMISSION, intent.getFlags());
            assertEquals(intent.getData(), intent.getClipData().getItemAt(0).getUri());
            assertThrows(IllegalArgumentException.class, () -> NativeAppUpdate.installIntent(context, new File(context.getFilesDir(), "session")));
        } finally { apk.delete(); }
    }
}
