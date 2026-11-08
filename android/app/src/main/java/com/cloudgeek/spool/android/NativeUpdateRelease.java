package com.cloudgeek.spool.android;

import java.io.IOException;
import java.math.BigInteger;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.json.JSONArray;
import org.json.JSONObject;

/** GitHub metadata is untrusted; only the exact APK and checksum assets are accepted. */
final class NativeUpdateRelease {
    static final String PACKAGE = "com.cloudgeek.spool.android";
    static final String REPOSITORY = "LarchLiu/spool";
    static final long MAX_APK_BYTES = 512L * 1024 * 1024;
    private static final Pattern VERSION = Pattern.compile("(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*))?");
    private static final Pattern SUM = Pattern.compile("([a-fA-F0-9]{64}) [ *](.+)");
    final String version, name, apkURL, sumsURL;
    final long size, versionCode;

    private NativeUpdateRelease(String version, long size) throws IOException {
        this.version = version; this.size = size;
        versionCode = versionCode(version);
        name = "spool-" + version + "-android-arm64.apk";
        String base = "https://github.com/" + REPOSITORY + "/releases/download/v" + version + "/";
        apkURL = base + name; sumsURL = base + "SHA256SUMS";
    }

    static NativeUpdateRelease parse(JSONObject json, String current) throws Exception {
        String tag = json.getString("tag_name");
        if (!tag.startsWith("v")) throw new IOException("Invalid release tag");
        String version = tag.substring(1);
        matcher(version);
        if (json.optBoolean("draft") || json.optBoolean("prerelease") || version.contains("-"))
            throw new IOException("Expected a published stable release");
        if (compare(version, current) <= 0) return null;
        JSONArray assets = json.getJSONArray("assets");
        NativeUpdateRelease release = null;
        int apks = 0, sums = 0;
        for (int i = 0; i < assets.length(); i++) {
            JSONObject asset = assets.getJSONObject(i);
            String name = asset.getString("name");
            if (name.equals("spool-" + version + "-android-arm64.apk")) {
                long size = asset.getLong("size");
                if (size <= 0 || size > MAX_APK_BYTES) throw new IOException("Invalid APK size");
                release = new NativeUpdateRelease(version, size); apks++;
                if (!release.apkURL.equals(asset.getString("browser_download_url"))) throw new IOException("Invalid APK URL");
            } else if (name.equals("SHA256SUMS")) {
                sums++;
                String expected = "https://github.com/" + REPOSITORY + "/releases/download/" + tag + "/SHA256SUMS";
                if (!expected.equals(asset.getString("browser_download_url")) || asset.getLong("size") <= 0 || asset.getLong("size") > 131072)
                    throw new IOException("Invalid checksum asset");
            }
        }
        if (apks != 1 || sums != 1) throw new IOException("Missing or duplicate Android release assets");
        return release;
    }

    String checksum(String sums) throws IOException {
        String checksum = null;
        for (String line : sums.split("\n")) {
            Matcher match = SUM.matcher(line.endsWith("\r") ? line.substring(0, line.length() - 1) : line);
            if (!match.matches() || !match.group(2).equals(name)) continue;
            if (checksum != null) throw new IOException("Duplicate APK checksum");
            checksum = match.group(1).toLowerCase(java.util.Locale.ROOT);
        }
        if (checksum == null) throw new IOException("Missing APK checksum");
        return checksum;
    }

    private static Matcher matcher(String value) throws IOException {
        Matcher result = VERSION.matcher(value == null ? "" : value);
        if (!result.matches()) throw new IOException("Invalid version");
        if (result.group(4) != null) for (String part : result.group(4).split("\\."))
            if (part.matches("0[0-9]+")) throw new IOException("Invalid prerelease version");
        return result;
    }

    static long versionCode(String version) throws IOException {
        Matcher match = matcher(version);
        BigInteger major = new BigInteger(match.group(1)), minor = new BigInteger(match.group(2)), patch = new BigInteger(match.group(3));
        BigInteger code = major.multiply(BigInteger.valueOf(1000000)).add(minor.multiply(BigInteger.valueOf(1000))).add(patch).add(BigInteger.ONE);
        if (minor.compareTo(BigInteger.valueOf(1000)) >= 0 || patch.compareTo(BigInteger.valueOf(1000)) >= 0 || code.compareTo(BigInteger.valueOf(2100000000)) > 0)
            throw new IOException("Version code out of range");
        return code.longValue();
    }

    static int compare(String first, String second) throws IOException {
        Matcher a = matcher(first), b = matcher(second);
        for (int i = 1; i <= 3; i++) {
            int order = new BigInteger(a.group(i)).compareTo(new BigInteger(b.group(i)));
            if (order != 0) return order;
        }
        String ap = a.group(4), bp = b.group(4);
        if (ap == null || bp == null) return ap == bp ? 0 : ap == null ? 1 : -1;
        String[] aa = ap.split("\\."), bb = bp.split("\\.");
        for (int i = 0; i < Math.min(aa.length, bb.length); i++) {
            boolean an = aa[i].matches("[0-9]+"), bn = bb[i].matches("[0-9]+");
            int order = an && bn ? new BigInteger(aa[i]).compareTo(new BigInteger(bb[i])) : an != bn ? (an ? -1 : 1) : aa[i].compareTo(bb[i]);
            if (order != 0) return order;
        }
        return Integer.compare(aa.length, bb.length);
    }
}
