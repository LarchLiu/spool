package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import com.cloudgeek.spool.textmate.NativeOniguruma;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NativeSyntaxClassLoaderTest {
    @Test public void apiSandboxesAndPlainJUnitShareTheRealJniBridge() throws Exception {
        ClassLoader host = RobolectricTestRunner.class.getClassLoader();
        assertSame(Class.forName(NativeOniguruma.class.getName(), false, host), NativeOniguruma.class);
        assertSame(Class.forName(NativeOniguruma.Pool.class.getName(), false, host), NativeOniguruma.Pool.class);
        try (NativeOniguruma.Pool pool = new NativeOniguruma.Pool(); NativeOniguruma.Scope ignored = pool.activate()) {
            NativeOniguruma.Regex regex = NativeOniguruma.compile("(a+)", false);
            assertArrayEquals(new int[] {0, 2, 0, 2}, regex.search(new byte[] {'a', 'a'}, 0));
            assertEquals(1, pool.size());
        }
    }
}
