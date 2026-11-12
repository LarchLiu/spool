package com.cloudgeek.spool.android;

import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.net.Uri;
import android.os.Handler;
import android.os.Looper;
import android.view.inputmethod.InputContentInfo;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.Executor;
import java.util.function.BiConsumer;
import java.util.function.BooleanSupplier;

/** Session-owned clipboard bridge. Local reads happen only on explicit paste. */
final class NativeDesktopClipboard implements AutoCloseable {
    // Android 8 parcels text as UTF-16. Leave room in Binder's shared 1 MiB
    // transaction buffer for the clip metadata and other in-flight requests.
    static final int MAX_TEXT = 256 << 10, MAX_IMAGE = 15 << 20;
    private final Context context;
    private final ClipboardManager clipboard;
    private final Executor io;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final BooleanSupplier controllable;
    private final BiConsumer<String, Integer> text;
    private final BiConsumer<byte[], Integer> image;
    private final Runnable failed;
    private final String label;
    private boolean closed, focused, writing;
    private int remoteVersion, pasteVersion;
    private String pendingText, lastText;
    private byte[] pendingImage, lastImage;

    NativeDesktopClipboard(Context context, Executor io, BooleanSupplier controllable,
            BiConsumer<String, Integer> text, BiConsumer<byte[], Integer> image, Runnable failed, String label) {
        this.context = context;
        clipboard = (ClipboardManager) context.getSystemService(Context.CLIPBOARD_SERVICE);
        this.io = io; this.controllable = controllable; this.text = text; this.image = image;
        this.failed = failed; this.label = label;
    }
    void focus(boolean value) { focused = value; if (value) flush(); else pasteVersion++; }
    void remoteText(String value) {
        if (closed) return;
        remoteVersion++; pendingImage = lastImage = null;
        if (!validText(value)) { pendingText = lastText = null; failed.run(); return; }
        pendingText = lastText = value; flush();
    }
    void remoteImage(byte[] packet) {
        if (closed || packet.length <= 1 || packet.length > MAX_IMAGE + 1 || packet[0] != 4) return;
        remoteVersion++; pendingText = lastText = null;
        pendingImage = lastImage = java.util.Arrays.copyOfRange(packet, 1, packet.length); flush();
    }
    void copy() {
        if (closed || !focused || !controllable.getAsBoolean()) return;
        // voxl suppresses unchanged clipboard contents, including a repeated
        // host copy after another Android app has replaced the local clipboard.
        pasteVersion++;
        pendingText = lastText; pendingImage = lastImage; flush();
    }
    void flush() {
        if (closed || !focused || clipboard == null) return;
        if (pendingText != null) {
            String value = pendingText; pendingText = null;
            try { clipboard.setPrimaryClip(ClipData.newPlainText(label, value)); }
            catch (RuntimeException error) { failed.run(); }
        } else if (pendingImage != null && !writing) {
            writing = true;
            final int version = remoteVersion;
            final byte[] bytes = pendingImage;
            io.execute(() -> {
                File staged = null;
                try {
                    byte[] png = png(bytes);
                    File directory = new File(context.getCacheDir(), "desktop-clipboard");
                    if (!directory.isDirectory() && !directory.mkdirs()) throw new IllegalStateException("Clipboard cache unavailable");
                    staged = File.createTempFile("pending-", ".png", directory);
                    try (FileOutputStream out = new FileOutputStream(staged)) { out.write(png); }
                } catch (Exception error) {
                    if (staged != null) staged.delete();
                    main.post(() -> {
                        writing = false;
                        if (!closed && version == remoteVersion) { pendingImage = lastImage = null; failed.run(); }
                        else flush();
                    });
                    return;
                }
                final File ready = staged;
                main.post(() -> {
                    writing = false;
                    try {
                        if (closed || version != remoteVersion || !focused) return;
                        File latest = new File(ready.getParentFile(), "clipboard.png");
                        Uri uri = androidx.core.content.FileProvider.getUriForFile(context, context.getPackageName() + ".desktop-clipboard", latest);
                        context.revokeUriPermission(uri, android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION);
                        java.nio.file.Files.move(ready.toPath(), latest.toPath(), java.nio.file.StandardCopyOption.REPLACE_EXISTING);
                        clipboard.setPrimaryClip(ClipData.newUri(context.getContentResolver(), label, uri));
                        pendingImage = null;
                    } catch (Exception ignored) { /* Retain pending image for the next focused interaction. */ }
                    finally { ready.delete(); if (!closed && version != remoteVersion) flush(); }
                });
            });
        }
    }
    boolean paste() { return paste(0); }
    boolean paste(int modifiers) {
        if (closed || !focused || !controllable.getAsBoolean() || clipboard == null) return false;
        try {
            ClipData clip = clipboard.getPrimaryClip();
            if (clip == null || clip.getItemCount() == 0) return false;
            ClipData.Item item = clip.getItemAt(0);
            if (item.getText() != null) {
                String value = item.getText().toString();
                if (!validText(value)) { failed.run(); return true; }
                pasteVersion++; remoteVersion++; pendingText = null; pendingImage = lastImage = null;
                lastText = value; text.accept(value, modifiers); return true;
            }
            return pasteUri(item.getUri(), null, modifiers);
        } catch (RuntimeException error) { failed.run(); return true; }
    }
    boolean content(InputContentInfo info, int flags) {
        if (info == null || !info.getDescription().hasMimeType("image/*")) return false;
        return pasteUri(info.getContentUri(), (flags & 1) != 0 ? info : null, 0);
    }
    private boolean pasteUri(Uri uri, InputContentInfo permission, int modifiers) {
        if (closed || !focused || !controllable.getAsBoolean() || uri == null || !"content".equals(uri.getScheme())) return false;
        final int version = ++pasteVersion;
        remoteVersion++; pendingText = null; pendingImage = null;
        try { if (permission != null) permission.requestPermission(); }
        catch (RuntimeException error) { failed.run(); return true; }
        io.execute(() -> {
            try (InputStream input = context.getContentResolver().openInputStream(uri)) {
                if (input == null) throw new IllegalArgumentException("Clipboard image unavailable");
                ByteArrayOutputStream bytes = new LimitedBytes();
                byte[] buffer = new byte[8192];
                for (int n; (n = input.read(buffer)) != -1;) bytes.write(buffer, 0, n);
                byte[] png = png(bytes.toByteArray());
                main.post(() -> {
                    if (closed || !focused || version != pasteVersion || !controllable.getAsBoolean()) return;
                    remoteVersion++; pendingText = lastText = null; pendingImage = null;
                    lastImage = png; image.accept(png, modifiers);
                });
            } catch (Exception error) {
                main.post(() -> { if (!closed && version == pasteVersion) failed.run(); });
            } finally { if (permission != null) permission.releasePermission(); }
        });
        return true;
    }
    static boolean validText(String text) {
        return text.length() <= MAX_TEXT && text.indexOf('\0') < 0 && text.getBytes(StandardCharsets.UTF_8).length <= MAX_TEXT;
    }
    private static final class LimitedBytes extends ByteArrayOutputStream {
        @Override public synchronized void write(byte[] bytes, int offset, int length) {
            if (length > MAX_IMAGE - size()) throw new IllegalArgumentException("Clipboard image too large");
            super.write(bytes, offset, length);
        }
        @Override public synchronized void write(int value) {
            if (size() == MAX_IMAGE) throw new IllegalArgumentException("Clipboard image too large");
            super.write(value);
        }
    }
    static byte[] png(byte[] bytes) {
        if (bytes.length > MAX_IMAGE) throw new IllegalArgumentException("Clipboard image too large");
        BitmapFactory.Options size = new BitmapFactory.Options(); size.inJustDecodeBounds = true;
        BitmapFactory.decodeByteArray(bytes, 0, bytes.length, size);
        if (size.outWidth < 1 || size.outHeight < 1 || (long) size.outWidth * size.outHeight > 16L * 1024 * 1024)
            throw new IllegalArgumentException("Unsupported clipboard image");
        Bitmap bitmap = BitmapFactory.decodeByteArray(bytes, 0, bytes.length);
        if (bitmap == null) throw new IllegalArgumentException("Invalid clipboard image");
        try {
            LimitedBytes out = new LimitedBytes();
            if (!bitmap.compress(Bitmap.CompressFormat.PNG, 100, out)) throw new IllegalArgumentException("Invalid clipboard image");
            return out.toByteArray();
        } finally { bitmap.recycle(); }
    }
    @Override public void close() {
        closed = true; remoteVersion++; pasteVersion++; pendingText = lastText = null; pendingImage = lastImage = null;
        // The latest published image remains readable from the system clipboard.
        // It occupies one cache file, replaced by the next published image.
    }
}
