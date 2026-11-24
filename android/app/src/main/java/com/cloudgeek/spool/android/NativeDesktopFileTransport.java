package com.cloudgeek.spool.android;

import java.io.InputStream;
import java.io.OutputStream;
import org.json.JSONObject;
import spoolnet.Client;
import spoolnet.DesktopFiles;

/** Streaming adapter: document-provider chunks use the authenticated Go HTTP transport. */
final class NativeDesktopFileTransport implements NativeDesktopFiles.Transport {
    private final DesktopFiles files;
    private final String csrf, language;
    private volatile boolean closed;
    NativeDesktopFileTransport(Client client, String token, String csrf, String language) throws Exception {
        files = client.openDesktopFiles(token); this.csrf = csrf; this.language = language;
    }
    public JSONObject list(String path) throws Exception { return new JSONObject(files.list(path, language)); }
    public void upload(String path, String name, InputStream input) throws Exception {
        files.upload(path, name, csrf, language, max -> {
            byte[] data = new byte[(int) Math.min(max, 256 * 1024)];
            int count = input.read(data);
            return count < 0 ? null : count == data.length ? data : java.util.Arrays.copyOf(data, count);
        });
    }
    public void download(String path, OutputStream output) throws Exception { files.download(path, language, output::write); }
    public void cancel() { close(); }
    public void close() { closed = true; files.close(); }
    public boolean closed() { return closed; }
}
