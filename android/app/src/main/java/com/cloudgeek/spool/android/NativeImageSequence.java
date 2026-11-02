package com.cloudgeek.spool.android;

import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.PorterDuff;
import android.graphics.Rect;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.util.ArrayList;
import java.util.List;

/** Bounded GIF/WebP container demux; pixel codecs remain Android's built-in, including on API 26. */
final class NativeImageSequence {
    static final int MAX_FRAMES = 10_000;
    static final class Frame {
        final int x, y, width, height, delay, disposal, offset, length, flags, transparency;
        final boolean blend;
        Frame(int x, int y, int width, int height, int delay, int disposal, boolean blend, int offset, int length, int flags, int transparency) {
            this.x = x; this.y = y; this.width = width; this.height = height;
            this.delay = delay <= 10 ? 100 : delay; this.disposal = disposal; this.blend = blend;
            this.offset = offset; this.length = length; this.flags = flags; this.transparency = transparency;
        }
    }
    final byte[] bytes;
    final List<Frame> frames = new ArrayList<>();
    final boolean gif;
    int width, height, loops = 1;
    private int paletteEnd;
    private int profileOffset, profileLength;

    private NativeImageSequence(byte[] bytes, boolean gif) { this.bytes = bytes; this.gif = gif; }

    static NativeImageSequence parse(byte[] bytes) throws IOException {
        NativeImageSequence result;
        if (ascii(bytes, 0, "GIF87a") || ascii(bytes, 0, "GIF89a")) { result = new NativeImageSequence(bytes, true); result.gif(); }
        else if (ascii(bytes, 0, "RIFF") && ascii(bytes, 8, "WEBP")) { result = new NativeImageSequence(bytes, false); result.webp(); }
        else return null;
        return result.frames.isEmpty() ? null : result;
    }
    private static boolean ascii(byte[] data, int at, String value) {
        if (at < 0 || at > data.length - value.length()) return false;
        for (int i = 0; i < value.length(); i++) if (data[at + i] != value.charAt(i)) return false;
        return true;
    }
    private void require(boolean condition) throws IOException { if (!condition) throw new IOException("Invalid or oversized animated image"); }
    private void available(int at, int length) throws IOException { require(at >= 0 && length >= 0 && at <= bytes.length - length); }
    private int u8(int at) throws IOException { available(at, 1); return bytes[at] & 255; }
    private int u16(int at) throws IOException { return u8(at) | u8(at + 1) << 8; }
    private int u24(int at) throws IOException { return u16(at) | u8(at + 2) << 16; }
    private long u32(int at) throws IOException { return u24(at) | (long) u8(at + 3) << 24; }
    private void dimensions() throws IOException { require(width > 0 && height > 0 && (long) width * height <= 25_000_000); }
    private void add(Frame frame) throws IOException {
        require(frames.size() < MAX_FRAMES && frame.width > 0 && frame.height > 0 && frame.x >= 0 && frame.y >= 0
                && (long) frame.x + frame.width <= width && (long) frame.y + frame.height <= height);
        frames.add(frame);
    }
    private int blocks(int at) throws IOException {
        for (;;) { int size = u8(at++); if (size == 0) return at; available(at, size); at += size; }
    }
    private void gif() throws IOException {
        available(0, 13); width = u16(6); height = u16(8); dimensions();
        paletteEnd = 13 + ((u8(10) & 128) == 0 ? 0 : 3 << ((u8(10) & 7) + 1)); available(0, paletteEnd);
        int at = paletteEnd, control = 0, delay = 100, transparent = 0;
        boolean trailer = false;
        while (at < bytes.length) {
            int type = u8(at++);
            if (type == 0x3b) { trailer = true; break; }
            if (type == 0x21) {
                int label = u8(at++);
                if (label == 0xf9) {
                    require(u8(at) == 4 && u8(at + 5) == 0); control = u8(at + 1); delay = u16(at + 2) * 10; transparent = u8(at + 4); at += 6;
                    require(((control >> 2) & 7) <= 3);
                } else {
                    int begin = at, end = blocks(at);
                    if (label == 0xff && u8(begin) == 11 && (ascii(bytes, begin + 1, "NETSCAPE2.0") || ascii(bytes, begin + 1, "ANIMEXTS1.0"))) {
                        require(end - begin >= 17 && u8(begin + 12) == 3 && u8(begin + 13) == 1);
                        int repeats = u16(begin + 14); loops = repeats == 0 ? 0 : repeats + 1;
                    }
                    at = end;
                }
            } else if (type == 0x2c) {
                available(at, 9); int descriptor = at, flags = u8(at + 8), start = at + 9;
                if ((flags & 128) != 0) start += 3 << ((flags & 7) + 1);
                available(at, start - at); require(u8(start) >= 2 && u8(start) <= 8); int end = blocks(start + 1);
                int disposal = (control >> 2) & 7;
                add(new Frame(u16(descriptor), u16(descriptor + 2), u16(descriptor + 4), u16(descriptor + 6), delay,
                        disposal == 2 ? 1 : disposal == 3 ? 2 : 0, true, descriptor, end - descriptor, control, transparent));
                at = end; control = 0; delay = 100; transparent = 0;
            } else require(false);
        }
        require(trailer && !frames.isEmpty());
    }
    private void webp() throws IOException {
        require(u32(4) + 8 == bytes.length); boolean animated = false, animationHeader = false;
        for (int at = 12; at < bytes.length;) {
            available(at, 8); long size = u32(at + 4); require(size <= bytes.length - at - 8); int end = at + 8 + (int) size, data = at + 8;
            if (ascii(bytes, at, "VP8X")) {
                require(at == 12 && size == 10); width = u24(data + 4) + 1; height = u24(data + 7) + 1; dimensions(); animated = (u8(data) & 2) != 0;
            } else if (ascii(bytes, at, "ICCP")) {
                require(profileLength == 0 && !animationHeader); profileOffset = at; profileLength = 8 + (int) size + ((int) size & 1);
            } else if (ascii(bytes, at, "ANIM")) {
                require(animated && !animationHeader && frames.isEmpty() && size == 6); loops = u16(data + 4); animationHeader = true;
            } else if (ascii(bytes, at, "ANMF")) {
                require(animated && animationHeader && size >= 24); int flags = u8(data + 15); require((flags & ~3) == 0);
                Frame frame = new Frame(u24(data) * 2, u24(data + 3) * 2, u24(data + 6) + 1, u24(data + 9) + 1,
                        u24(data + 12), flags & 1, (flags & 2) == 0, data + 16, (int) size - 16, 0, 0);
                validateWebpFrame(frame); add(frame);
            }
            at = end + ((int) size & 1); require(at <= bytes.length);
        }
        if (animated) require(animationHeader && !frames.isEmpty());
    }
    private void validateWebpFrame(Frame frame) throws IOException {
        int end = frame.offset + frame.length; boolean image = false, alpha = false;
        for (int at = frame.offset; at < end;) {
            require(at <= end - 8); long size = u32(at + 4); require(size <= end - at - 8);
            if (ascii(bytes, at, "ALPH")) { require(!alpha && !image); alpha = true; }
            else { require(!image && (ascii(bytes, at, "VP8 ") || ascii(bytes, at, "VP8L"))); image = true; }
            at += 8 + (int) size + ((int) size & 1); require(at <= end);
        }
        require(image);
    }
    private static void little(ByteArrayOutputStream out, int value, int count) { for (int i = 0; i < count; i++) out.write(value >>> (i * 8) & 255); }
    private byte[] encoded(Frame frame) throws IOException {
        ByteArrayOutputStream out = new ByteArrayOutputStream(frame.length + paletteEnd + profileLength + 40);
        if (gif) {
            out.write(new byte[] {'G','I','F','8','9','a'}); little(out, frame.width, 2); little(out, frame.height, 2);
            out.write(bytes, 10, paletteEnd - 10);
            out.write(new byte[] {0x21,(byte) 0xf9,4,(byte) (frame.flags & 1),0,0,(byte) frame.transparency,0,0x2c,0,0,0,0});
            out.write(bytes, frame.offset + 4, frame.length - 4); out.write(0x3b);
        } else {
            out.write(new byte[] {'R','I','F','F'}); little(out, frame.length + profileLength + 22, 4); out.write(new byte[] {'W','E','B','P','V','P','8','X',10,0,0,0,(byte) (profileLength == 0 ? 0x10 : 0x30),0,0,0});
            little(out, frame.width - 1, 3); little(out, frame.height - 1, 3); if (profileLength > 0) out.write(bytes, profileOffset, profileLength); out.write(bytes, frame.offset, frame.length);
        }
        return out.toByteArray();
    }

    Renderer renderer(int edge) { return new Renderer(edge); }
    final class Renderer implements AutoCloseable {
        final Bitmap surface;
        private final Canvas canvas;
        private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG | Paint.FILTER_BITMAP_FLAG);
        private Bitmap restore;
        private int previous = -1;
        Renderer(int edge) {
            float scale = Math.min(1, (float) Math.min(2048, Math.max(1, edge)) / Math.max(width, height));
            surface = Bitmap.createBitmap(Math.max(1, Math.round(width * scale)), Math.max(1, Math.round(height * scale)), Bitmap.Config.ARGB_8888);
            surface.setDensity(android.util.DisplayMetrics.DENSITY_DEFAULT); canvas = new Canvas(surface);
        }
        private Rect area(Frame frame) {
            return new Rect(Math.round((float) frame.x * surface.getWidth() / width), Math.round((float) frame.y * surface.getHeight() / height),
                    Math.round((float) (frame.x + frame.width) * surface.getWidth() / width), Math.round((float) (frame.y + frame.height) * surface.getHeight() / height));
        }
        private void clear(Rect area) { int save = canvas.save(); canvas.clipRect(area); canvas.drawColor(0, PorterDuff.Mode.CLEAR); canvas.restoreToCount(save); }
        void render(int index) throws IOException {
            require(index >= 0 && index < frames.size() && (index == 0 || index == previous + 1));
            if (index == 0) canvas.drawColor(0, PorterDuff.Mode.CLEAR);
            else if (previous >= 0) {
                Frame last = frames.get(previous);
                if (last.disposal == 1) clear(area(last));
                else if (last.disposal == 2 && restore != null) { canvas.drawColor(0, PorterDuff.Mode.CLEAR); canvas.drawBitmap(restore, 0, 0, paint); }
            }
            Frame frame = frames.get(index);
            if (restore != null) { restore.recycle(); restore = null; }
            if (frame.disposal == 2) restore = surface.copy(Bitmap.Config.ARGB_8888, false);
            byte[] encoded = encoded(frame); BitmapFactory.Options options = new BitmapFactory.Options(); options.inScaled = false; options.inJustDecodeBounds = true;
            BitmapFactory.decodeByteArray(encoded, 0, encoded.length, options);
            require(options.outWidth == frame.width && options.outHeight == frame.height);
            Rect area = area(frame); options.inSampleSize = 1;
            while (Math.max(frame.width, frame.height) / options.inSampleSize > Math.max(1, Math.max(area.width(), area.height())) * 2
                    || (long) (frame.width / options.inSampleSize) * (frame.height / options.inSampleSize) > 8_000_000) options.inSampleSize *= 2;
            options.inJustDecodeBounds = false; Bitmap bitmap = BitmapFactory.decodeByteArray(encoded, 0, encoded.length, options); require(bitmap != null);
            if (!frame.blend) clear(area);
            canvas.drawBitmap(bitmap, null, area, paint); bitmap.recycle(); previous = index;
        }
        Bitmap snapshot() {
            // Chrome's animated WebP img uses canvas orientation even when EXIF requests rotation.
            Bitmap result = surface.copy(Bitmap.Config.ARGB_8888, false);
            result.setDensity(android.util.DisplayMetrics.DENSITY_DEFAULT); return result;
        }
        @Override public void close() { surface.recycle(); if (restore != null) { restore.recycle(); restore = null; } }
    }
}
