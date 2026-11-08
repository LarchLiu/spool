package com.cloudgeek.spool.android;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.concurrent.atomic.AtomicBoolean;
import org.json.JSONObject;

/** Public GitHub traffic uses system HTTPS, independently of every server's Go session. */
final class NativeUpdateNetwork {
    interface Connections { HttpURLConnection open(URL url) throws IOException; }
    interface Progress { void received(long bytes); }
    private final Connections connections;
    private final AtomicBoolean cancelled = new AtomicBoolean();
    private volatile HttpURLConnection active;

    NativeUpdateNetwork() { this(url -> (HttpURLConnection) url.openConnection()); }
    NativeUpdateNetwork(Connections connections) { this.connections = connections; }

    void cancel() { cancelled.set(true); HttpURLConnection connection = active; if (connection != null) connection.disconnect(); }
    void checkCancelled() throws IOException { if (cancelled.get() || Thread.currentThread().isInterrupted()) throw new IOException("Cancelled"); }

    static boolean allowed(URL url) {
        if (!url.getProtocol().equals("https") || url.getUserInfo() != null || url.getRef() != null || url.getPort() != -1 && url.getPort() != 443) return false;
        String host = url.getHost();
        return host.equals("api.github.com") || host.equals("github.com") || host.equals("release-assets.githubusercontent.com") || host.equals("objects.githubusercontent.com");
    }

    private HttpURLConnection open(String address) throws IOException {
        URL url = new URL(address);
        for (int redirects = 0; redirects <= 5; redirects++) {
            checkCancelled();
            if (!allowed(url)) throw new IOException("Untrusted update URL");
            HttpURLConnection connection = connections.open(url); active = connection;
            connection.setConnectTimeout(15000); connection.setReadTimeout(30000);
            connection.setInstanceFollowRedirects(false); connection.setUseCaches(false);
            connection.setRequestProperty("User-Agent", "Spool-Android");
            connection.setRequestProperty("Accept", url.getHost().equals("api.github.com") ? "application/vnd.github+json" : "application/octet-stream");
            connection.setRequestProperty("Accept-Encoding", "identity");
            connection.setRequestProperty("Cookie", "");
            int status;
            try { checkCancelled(); status = connection.getResponseCode(); }
            catch (IOException error) { connection.disconnect(); active = null; throw error; }
            if (status == 200) return connection;
            String location = connection.getHeaderField("Location");
            connection.disconnect(); active = null;
            if ((status == 301 || status == 302 || status == 303 || status == 307 || status == 308) && location != null) url = new URL(url, location);
            else throw new IOException("Update server returned HTTP " + status);
        }
        throw new IOException("Too many update redirects");
    }

    byte[] read(String url, int limit) throws IOException {
        HttpURLConnection connection = open(url);
        try (InputStream input = connection.getInputStream(); ByteArrayOutputStream output = new ByteArrayOutputStream()) {
            byte[] buffer = new byte[8192]; int count;
            while ((count = input.read(buffer)) != -1) {
                checkCancelled();
                if (output.size() + count > limit) throw new IOException("Update response exceeds size limit");
                output.write(buffer, 0, count);
            }
            return output.toByteArray();
        } finally { connection.disconnect(); active = null; }
    }

    NativeUpdateRelease latest(String current) throws Exception {
        byte[] data = read("https://api.github.com/repos/" + NativeUpdateRelease.REPOSITORY + "/releases/latest", 2 * 1024 * 1024);
        return NativeUpdateRelease.parse(new JSONObject(new String(data, StandardCharsets.UTF_8)), current);
    }

    String download(NativeUpdateRelease release, File destination, Progress progress) throws Exception {
        String expected = release.checksum(new String(read(release.sumsURL, 131072), StandardCharsets.UTF_8));
        HttpURLConnection connection = open(release.apkURL);
        boolean complete = false;
        try (InputStream input = connection.getInputStream(); FileOutputStream output = new FileOutputStream(destination)) {
            if (connection.getContentLengthLong() > 0 && connection.getContentLengthLong() != release.size) throw new IOException("APK length does not match release");
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            byte[] buffer = new byte[65536]; int count; long bytes = 0, last = 0;
            while ((count = input.read(buffer)) != -1) {
                checkCancelled(); bytes += count;
                if (bytes > release.size) throw new IOException("APK exceeds release size");
                output.write(buffer, 0, count); digest.update(buffer, 0, count);
                if (System.nanoTime() - last >= 250000000 || bytes == release.size) { last = System.nanoTime(); progress.received(bytes); }
            }
            if (bytes != release.size || !hex(digest.digest()).equals(expected)) throw new IOException("APK checksum or size mismatch");
            checkCancelled(); complete = true; return expected;
        } finally {
            connection.disconnect(); active = null;
            if (!complete) destination.delete();
        }
    }

    static String digest(File file) throws Exception {
        MessageDigest digest = MessageDigest.getInstance("SHA-256");
        try (InputStream input = new java.io.FileInputStream(file)) {
            byte[] buffer = new byte[65536]; int count;
            while ((count = input.read(buffer)) != -1) digest.update(buffer, 0, count);
        }
        return hex(digest.digest());
    }

    private static String hex(byte[] bytes) {
        StringBuilder value = new StringBuilder();
        for (byte item : bytes) value.append(String.format(java.util.Locale.ROOT, "%02x", item & 255));
        return value.toString();
    }
}
