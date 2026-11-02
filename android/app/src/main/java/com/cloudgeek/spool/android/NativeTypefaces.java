package com.cloudgeek.spool.android;

import android.graphics.Paint;
import android.graphics.Typeface;
import android.os.Build;
import java.util.LinkedHashMap;
import java.util.Map;

/** Share immutable system-font variants instead of rebuilding their native fallback chains per label. */
final class NativeTypefaces {
    private static final Map<Integer, Typeface> FACES = new LinkedHashMap<Integer, Typeface>(16, .75f, true) {
        @Override protected boolean removeEldestEntry(Map.Entry<Integer, Typeface> entry) { return size() > 32; }
    };

    static synchronized Typeface weight(int weight) {
        Typeface cached = FACES.get(weight);
        if (cached != null) return cached;
        int face = NativeUi.staticFontWeight(weight);
        Typeface base = Build.VERSION.SDK_INT >= 28
                ? Typeface.create(Typeface.create("sans-serif", Typeface.NORMAL), face, false)
                : Typeface.create(face == 100 ? "sans-serif-thin" : face == 300 ? "sans-serif-light" : face == 500 ? "sans-serif-medium" : face == 900 ? "sans-serif-black" : "sans-serif",
                        face == 700 ? Typeface.BOLD : Typeface.NORMAL);
        Paint font = new Paint();
        font.setTypeface(base);
        // Bake the exact variable axis into the shared face; static fonts keep CSS's fallback weight.
        font.setFontVariationSettings("'wght' " + weight);
        Typeface result = font.getTypeface();
        FACES.put(weight, result);
        return result;
    }

    private NativeTypefaces() {}
}
