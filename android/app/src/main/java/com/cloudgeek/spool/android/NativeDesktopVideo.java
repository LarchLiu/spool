package com.cloudgeek.spool.android;

import android.media.MediaCodec;
import android.media.MediaFormat;
import android.os.Handler;
import android.os.HandlerThread;
import android.os.SystemClock;
import android.view.Surface;
import java.nio.ByteBuffer;
import java.util.ArrayDeque;
import java.util.HashMap;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.function.Consumer;

/** Bounded H.264 Annex-B decoding. All codecs/surfaces are owned by one media thread. */
final class NativeDesktopVideo implements AutoCloseable {
    private static final int MAX_BYTES = 24 * 1024 * 1024;
    private final HandlerThread thread = new HandlerThread("spool-desktop-video");
    private final Handler handler;
    private final NativeDesktopProtocol.Layout layout;
    private final Consumer<String> send, error;
    private final Runnable firstFrame;
    private final AtomicInteger bytes = new AtomicInteger(), messages = new AtomicInteger();
    private final NativeDesktopProtocol.Frames frames;
    private final Stripe[] stripes;
    private volatile boolean closed;
    private boolean started, painted, ticking;
    private long keyframeTime, pendingSince;

    private static final class Stripe {
        Surface surface;
        MediaCodec codec;
        final ArrayDeque<NativeDesktopProtocol.Packet> input = new ArrayDeque<>();
        final Map<Long, NativeDesktopProtocol.Packet> decoding = new HashMap<>();
        long timestamp;
    }

    NativeDesktopVideo(NativeDesktopProtocol.Layout layout, Consumer<String> send, Consumer<String> error, Runnable firstFrame) {
        this.layout = layout; this.send = send; this.error = error; this.firstFrame = firstFrame;
        stripes = new Stripe[layout.y.length];
        for (int i = 0; i < stripes.length; i++) stripes[i] = new Stripe();
        frames = new NativeDesktopProtocol.Frames(frame -> {
            send.accept("a," + frame);
            pendingSince = SystemClock.elapsedRealtime();
        });
        thread.start(); handler = new Handler(thread.getLooper());
    }

    void surface(int stripe, Surface surface) {
        if (closed) { surface.release(); return; }
        handler.post(() -> {
            if (closed) { surface.release(); return; }
            if (stripes[stripe].surface != null) { surface.release(); return; }
            stripes[stripe].surface = surface;
            for (Stripe s : stripes) if (s.surface == null) return;
            started = true;
            send.accept("v,1"); send.accept("kf");
            wake();
        });
    }

    void offer(byte[] packet) {
        if (closed) return;
        int size = bytes.addAndGet(packet.length), count = messages.incrementAndGet();
        if (size > MAX_BYTES || count > 128) {
            bytes.addAndGet(-packet.length); messages.decrementAndGet();
            fail("Desktop video queue exceeded its limit"); return;
        }
        if (!handler.post(() -> {
            boolean retained = false;
            try {
                if (closed || !started) return;
                NativeDesktopProtocol.Packet p = new NativeDesktopProtocol.Packet(packet, stripes.length);
                frames.expect(p);
                Stripe s = stripes[p.stripe];
                if (s.codec == null && !p.key) {
                    frames.ready(p, render -> {});
                    long now = SystemClock.elapsedRealtime();
                    if (now - keyframeTime > 500) { keyframeTime = now; send.accept("kf"); }
                    return;
                }
                if (s.codec == null) createCodec(s, p);
                s.input.add(p);
                retained = true;
                if (pendingSince == 0) pendingSince = SystemClock.elapsedRealtime();
                wake();
            } catch (Exception e) { fail("Could not decode desktop video"); }
            finally { if (!retained) bytes.addAndGet(-packet.length); messages.decrementAndGet(); }
        })) { bytes.addAndGet(-packet.length); messages.decrementAndGet(); }
    }

    private void wake() {
        if (!ticking) { ticking = true; handler.post(tick); }
    }

    private void createCodec(Stripe stripe, NativeDesktopProtocol.Packet packet) throws Exception {
        MediaFormat format = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, layout.width, layout.heights[packet.stripe]);
        format.setInteger(MediaFormat.KEY_MAX_INPUT_SIZE, Math.min(MAX_BYTES, Math.max(1024 * 1024, layout.width * layout.heights[packet.stripe] * 2)));
        format.setInteger(MediaFormat.KEY_PRIORITY, 0);
        if (android.os.Build.VERSION.SDK_INT >= 30) format.setInteger(MediaFormat.KEY_LOW_LATENCY, 1);
        // SPS/PPS travel in the first Annex-B keyframe, as in voxl's WebCodecs client.
        MediaCodec codec = MediaCodec.createDecoderByType(MediaFormat.MIMETYPE_VIDEO_AVC);
        stripe.codec = codec;
        codec.configure(format, stripe.surface, null, 0);
        codec.start();
    }

    private final Runnable tick = new Runnable() {
        @Override public void run() {
            ticking = false;
            if (closed) return;
            try {
                boolean pending = false;
                MediaCodec.BufferInfo info = new MediaCodec.BufferInfo();
                for (Stripe stripe : stripes) {
                    MediaCodec codec = stripe.codec;
                    if (codec == null) continue;
                    int limit = 16;
                    while (!stripe.input.isEmpty() && limit-- > 0) {
                        int index = codec.dequeueInputBuffer(0);
                        if (index < 0) break;
                        NativeDesktopProtocol.Packet packet = stripe.input.remove();
                        ByteBuffer buffer = codec.getInputBuffer(index);
                        if (buffer == null || buffer.capacity() < packet.bytes.length - NativeDesktopProtocol.HEADER)
                            throw new IllegalArgumentException("Desktop keyframe exceeds decoder capacity");
                        buffer.clear(); buffer.put(packet.bytes, NativeDesktopProtocol.HEADER, packet.bytes.length - NativeDesktopProtocol.HEADER);
                        long timestamp = ++stripe.timestamp;
                        stripe.decoding.put(timestamp, packet);
                        codec.queueInputBuffer(index, 0, buffer.position(), timestamp, 0);
                        bytes.addAndGet(-packet.bytes.length);
                        packet.bytes = null;
                    }
                    limit = 16;
                    while (limit-- > 0) {
                        int index = codec.dequeueOutputBuffer(info, 0);
                        if (index == MediaCodec.INFO_TRY_AGAIN_LATER) break;
                        if (index < 0) continue;
                        NativeDesktopProtocol.Packet packet = stripe.decoding.remove(info.presentationTimeUs);
                        if (packet == null) { codec.releaseOutputBuffer(index, false); continue; }
                        final int output = index;
                        frames.ready(packet, render -> {
                            codec.releaseOutputBuffer(output, render);
                            if (render && !painted) { painted = true; firstFrame.run(); }
                        });
                    }
                    pending |= !stripe.input.isEmpty() || !stripe.decoding.isEmpty();
                    if (stripe.input.size() + stripe.decoding.size() > 32) throw new IllegalStateException("Desktop decoder stalled");
                }
                pending |= !frames.pending.isEmpty();
                long now = SystemClock.elapsedRealtime();
                if (!pending) pendingSince = 0;
                else if (pendingSince != 0 && now - pendingSince > 8000) throw new IllegalStateException("Desktop decoder timed out");
                if (pending) { ticking = true; handler.postDelayed(this, 4); }
            } catch (Exception e) { fail("This device could not decode the desktop video streams"); }
        }
    };

    private void fail(String reason) {
        if (closed) return;
        close(); error.accept(reason);
    }

    @Override public synchronized void close() {
        if (closed) return;
        closed = true;
        handler.post(() -> {
            try { frames.clear(); } catch (Exception ignored) {}
            for (Stripe s : stripes) {
                if (s.codec != null) {
                    try { s.codec.stop(); } catch (Exception ignored) {}
                    try { s.codec.release(); } catch (Exception ignored) {}
                }
                if (s.surface != null) s.surface.release();
                s.input.clear(); s.decoding.clear();
            }
            thread.quitSafely();
        });
    }
}
