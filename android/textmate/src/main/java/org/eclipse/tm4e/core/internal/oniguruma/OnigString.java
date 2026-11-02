/*
 * Android replacement for Eclipse TM4E's OnigString, preserving its binary API.
 * Copyright (c) 2015-2017 Angelo ZERR and TM4E contributors; EPL-2.0.
 * Original adaptation from atom/node-oniguruma, Copyright (c) 2013 GitHub Inc., MIT.
 * Modified for Spool: index UTF-8 captures in UTF-16 units, including surrogate pairs.
 * See android/licenses/TM4E.txt and android/textmate/README.md.
 */
package org.eclipse.tm4e.core.internal.oniguruma;

import java.nio.charset.StandardCharsets;

/** The grammar and regex engines exchange Java/JS string offsets, not Unicode code-point counts. */
public abstract class OnigString {
    public final String content;
    public final int bytesCount;
    final byte[] bytesUTF8;
    private OnigString(String content, byte[] bytes) { this.content = content; bytesUTF8 = bytes; bytesCount = bytes.length; }

    public static OnigString of(String content) {
        byte[] bytes = content.getBytes(StandardCharsets.UTF_8);
        if (bytes.length == content.length()) return new OnigString(content, bytes) {
            int getByteIndexOfChar(int index) { return checked(index, content.length()); }
            int getCharIndexOfByte(int index) { return checked(index, bytes.length); }
        };
        return new Indexed(content, bytes);
    }
    private static final class Indexed extends OnigString {
        private final int[] charToByte, byteToChar;
        Indexed(String content, byte[] bytes) {
            super(content, bytes);
            charToByte = new int[content.length() + 1]; byteToChar = new int[bytes.length + 1];
            for (int character = 0, offset = 0; character < content.length();) {
                int point = content.codePointAt(character), units = Character.charCount(point);
                int size = point < 0x80 || point >= 0xd800 && point <= 0xdfff ? 1 : point < 0x800 ? 2 : point < 0x10000 ? 3 : 4;
                for (int i = 0; i < units; i++) charToByte[character + i] = offset;
                for (int i = 0; i < size; i++) byteToChar[offset + i] = character;
                character += units; offset += size;
                charToByte[character] = offset; byteToChar[offset] = character;
            }
        }
        int getByteIndexOfChar(int index) { return charToByte[checked(index, content.length())]; }
        int getCharIndexOfByte(int index) { return byteToChar[checked(index, bytesCount)]; }
    }
    static int checked(int index, int length) {
        if (index < 0 || index > length) throw new IndexOutOfBoundsException(index + " outside 0.." + length);
        return index;
    }
    protected final void throwOutOfBoundsException(String name, int index, int min, int max) {
        throw new ArrayIndexOutOfBoundsException(name + " index " + index + " outside " + min + ".." + max);
    }
    abstract int getByteIndexOfChar(int index);
    abstract int getCharIndexOfByte(int index);
    @Override public String toString() { return "OnigString[" + content + "]"; }
}
