package com.cloudgeek.spool.android;

import java.util.LinkedHashMap;
import java.util.HashMap;
import java.util.Map;
import java.util.function.LongConsumer;
import org.json.JSONArray;
import org.json.JSONObject;

/** voxl 0.3.3 wire format; see its web/src/protocol.ts. No browser runtime. */
final class NativeDesktopProtocol {
    static final int HEADER = 16, MAX_STRIPES = 16;
    static final class Layout {
        final int width, height;
        final int[] y, heights;
        final boolean readOnly, mac;
        Layout(JSONObject hello) throws Exception {
            width = hello.getInt("w"); height = hello.getInt("h");
            if (width < 1 || height < 1 || width > 16384 || height > 16384 || (long) width * height > 32L * 1024 * 1024)
                throw new IllegalArgumentException("Unsupported desktop dimensions");
            readOnly = hello.optBoolean("readOnly"); mac = hello.optString("os").equals("macos");
            JSONArray stripes = hello.optJSONArray("stripes");
            int count = stripes == null || stripes.length() == 0 ? 1 : stripes.length();
            if (count > MAX_STRIPES) throw new IllegalArgumentException("Too many desktop video streams");
            y = new int[count]; heights = new int[count];
            int next = 0;
            for (int i = 0; i < count; i++) {
                JSONArray stripe = stripes == null || stripes.length() == 0 ? null : stripes.getJSONArray(i);
                y[i] = stripe == null ? 0 : stripe.getInt(0);
                heights[i] = stripe == null ? height : stripe.getInt(1);
                if (y[i] != next || heights[i] <= 0 || heights[i] > height - next)
                    throw new IllegalArgumentException("Invalid desktop video layout");
                next += heights[i];
            }
            if (next != height) throw new IllegalArgumentException("Incomplete desktop video layout");
        }
    }

    static final class Packet {
        byte[] bytes;
        final long frame;
        final int stripe, parts;
        final boolean key;
        Packet(byte[] bytes, int stripes) {
            if (bytes.length <= HEADER || bytes[0] != 1) throw new IllegalArgumentException("Invalid desktop video packet");
            this.bytes = bytes;
            key = (bytes[1] & 1) != 0;
            frame = ((long)(bytes[2] & 255) << 24) | ((long)(bytes[3] & 255) << 16) | ((bytes[4] & 255) << 8) | (bytes[5] & 255);
            stripe = bytes[10] & 255; parts = bytes[12] & 255;
            if (stripe >= stripes || parts < 1 || parts > stripes) throw new IllegalArgumentException("Invalid desktop frame layout");
        }
    }

    /** Hold codec outputs until every stripe of the oldest frame is decoded. */
    static final class Frames {
        private static final class Frame {
            final int count;
            final Map<Integer, java.util.function.Consumer<Boolean>> outputs = new HashMap<>();
            final java.util.Set<Integer> received = new java.util.HashSet<>();
            Frame(int count) { this.count = count; }
        }
        final LinkedHashMap<Long, Frame> pending = new LinkedHashMap<>();
        final LongConsumer acknowledge;
        Frames(LongConsumer acknowledge) { this.acknowledge = acknowledge; }
        void expect(Packet packet) {
            Frame frame = pending.computeIfAbsent(packet.frame, key -> new Frame(packet.parts));
            if (pending.size() > 16 || frame.count != packet.parts || !frame.received.add(packet.stripe))
                throw new IllegalArgumentException("Invalid or stalled desktop frame sequence");
        }
        void ready(Packet packet, java.util.function.Consumer<Boolean> output) {
            Frame frame = pending.get(packet.frame);
            if (frame == null || frame.outputs.putIfAbsent(packet.stripe, output) != null) {
                output.accept(false);
                throw new IllegalArgumentException("Unexpected desktop decoder output");
            }
            while (!pending.isEmpty()) {
                Map.Entry<Long, Frame> first = pending.entrySet().iterator().next();
                if (first.getValue().outputs.size() != first.getValue().count) break;
                pending.remove(first.getKey());
                for (java.util.function.Consumer<Boolean> release : first.getValue().outputs.values()) release.accept(true);
                acknowledge.accept(first.getKey());
            }
        }
        void clear() {
            for (Frame frame : pending.values()) for (java.util.function.Consumer<Boolean> release : frame.outputs.values()) release.accept(false);
            pending.clear();
        }
    }

    static int keysym(int point) {
        return point >= 0x20 && point <= 0x7e || point >= 0xa0 && point <= 0xff ? point : 0x01000000 + point;
    }
}
