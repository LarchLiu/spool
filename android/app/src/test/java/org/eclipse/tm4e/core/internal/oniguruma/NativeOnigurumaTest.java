package org.eclipse.tm4e.core.internal.oniguruma;

import static org.junit.Assert.*;
import com.cloudgeek.spool.textmate.NativeOniguruma;
import com.cloudgeek.spool.textmate.TextMatePlatform;
import java.nio.charset.StandardCharsets;
import org.junit.Test;

public class NativeOnigurumaTest {
    @Test public void android26PlatformEmptyChecksPreserveCharSequenceAndBuilderSemantics() {
        assertTrue(TextMatePlatform.isEmpty("")); assertFalse(TextMatePlatform.isEmpty("value"));
        assertTrue(TextMatePlatform.isEmpty(new StringBuilder())); assertFalse(TextMatePlatform.isEmpty(new StringBuilder("value")));
        assertThrows(NullPointerException.class, () -> TextMatePlatform.isEmpty(null));
    }
    @Test public void nestedOwnersDisposeEveryHandleAndRestoreThePreviousScope() {
        assertThrows(IllegalStateException.class, () -> NativeOniguruma.compile("a", false));
        NativeOniguruma.Pool outer = new NativeOniguruma.Pool(), inner = new NativeOniguruma.Pool();
        NativeOniguruma.Regex expression;
        try (NativeOniguruma.Scope ignored = outer.activate()) {
            expression = NativeOniguruma.compile("(a)", false);
            assertArrayEquals(new int[] {0, 1, 0, 1}, expression.search(new byte[] {'a'}, 0));
            try (NativeOniguruma.Scope nested = inner.activate()) { NativeOniguruma.compile("b", false); }
            NativeOniguruma.compile("c", false);
            assertEquals(2, outer.size()); assertEquals(1, inner.size());
            inner.close(); assertEquals(0, inner.size());
            outer.close(); assertEquals(0, outer.size());
            assertThrows(IllegalStateException.class, () -> NativeOniguruma.compile("a", false));
        } finally { outer.close(); inner.close(); }
        assertThrows(IllegalStateException.class, () -> expression.search(new byte[] {'a'}, 0));
        assertThrows(IllegalStateException.class, outer::activate);
        assertThrows(IllegalStateException.class, () -> NativeOniguruma.compile("a", false));
    }

    @Test public void utf8CapturesMapToExactUtf16OffsetsIncludingSurrogatePairsAndMalformedInput() {
        String value = "a\u4e2d\ud83d\ude80\u00e9z";
        OnigString string = OnigString.of(value);
        int[] bytes = {0, 1, 4, 4, 8, 10, 11};
        for (int i = 0; i < bytes.length; i++) assertEquals("UTF16 " + i, bytes[i], string.getByteIndexOfChar(i));
        int[] characters = {0, 1, 1, 1, 2, 2, 2, 2, 4, 4, 5, 6};
        for (int i = 0; i < characters.length; i++) assertEquals("UTF8 " + i, characters[i], string.getCharIndexOfByte(i));
        try (NativeOniguruma.Pool pool = new NativeOniguruma.Pool(); NativeOniguruma.Scope ignored = pool.activate()) {
            OnigResult capture = new OnigRegExp("(\ud83d\ude80)(\u00e9)").search(string, 0);
            assertNotNull(capture); assertEquals(4, capture.locationAt(0)); assertEquals(6, capture.lengthAt(0));
            assertEquals(2, string.getCharIndexOfByte(capture.locationAt(1)));
            assertEquals(4, string.getCharIndexOfByte(capture.locationAt(1) + capture.lengthAt(1)));
        }
        OnigString isolated = OnigString.of("x\ud800\u4e2d");
        assertEquals(2, isolated.getByteIndexOfChar(2)); assertEquals(5, isolated.bytesCount);
        assertThrows(IndexOutOfBoundsException.class, () -> string.getByteIndexOfChar(7));
        assertThrows(IndexOutOfBoundsException.class, () -> string.getCharIndexOfByte(12));
    }

    @Test(timeout = 3000) public void invalidAndPathologicalPatternsFailWithoutLeakingOwnedHandles() {
        try (NativeOniguruma.Pool pool = new NativeOniguruma.Pool(); NativeOniguruma.Scope ignored = pool.activate()) {
            assertThrows(IllegalArgumentException.class, () -> NativeOniguruma.compile("[", false));
            assertEquals(0, pool.size());
            NativeOniguruma.Regex expression = NativeOniguruma.compile("(a+)+$", false);
            byte[] input = ("a".repeat(200) + "!").getBytes(StandardCharsets.UTF_8);
            assertThrows(IllegalStateException.class, () -> expression.search(input, 0));
            assertThrows(IndexOutOfBoundsException.class, () -> expression.search(input, -1));
            assertEquals(1, pool.size());
        }
    }
}
