package com.cloudgeek.spool.android;

import android.graphics.Typeface;
import android.text.SpannableString;
import android.text.Spanned;
import android.text.TextPaint;
import android.text.style.MetricAffectingSpan;
import android.text.style.StyleSpan;
import android.text.style.TypefaceSpan;
import android.util.Xml;
import java.io.File;
import java.io.FileInputStream;
import java.io.InputStream;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import org.xmlpull.v1.XmlPullParser;

/** Configured CJK faces for legacy metrics and CSS font-synthesis:none, without bundled fonts. */
final class NativeCjkFallback {
    private static final class SystemFonts {
        static final NativeCjkFallback INSTANCE = new NativeCjkFallback(new File("/system/etc/fonts.xml"),
                new File("/system/fonts"), new File("/product/fonts"), new File("/vendor/fonts"), new File("/system_ext/fonts"));
    }
    static NativeCjkFallback system() { return SystemFonts.INSTANCE; }

    private static final class Font {
        final String language, file, axes;
        final int index, weight, family;
        final boolean italic;
        Font(String language, String file, String axes, int index, int weight, int family, boolean italic) {
            this.language = language; this.file = file; this.axes = axes; this.index = index; this.weight = weight; this.family = family; this.italic = italic;
        }
    }
    private final File config;
    private final File[] directories;
    private List<Font> fonts;
    private final Map<String, Typeface> faces = new LinkedHashMap<String, Typeface>(16, .75f, true) {
        @Override protected boolean removeEldestEntry(Map.Entry<String, Typeface> item) { return size() > 12; }
    };

    NativeCjkFallback(File config, File... directories) { this.config = config; this.directories = directories.clone(); }

    synchronized Typeface face(String locale, int weight) {
        return face(locale, weight, false);
    }

    private synchronized Typeface face(String locale, int weight, boolean italic) {
        String language = Locale.forLanguageTag(locale).getLanguage();
        if (!language.equals("ja") && !language.equals("zh")) return null;
        String key = language + "/" + weight + "/" + italic;
        if (faces.containsKey(key)) return faces.get(key);
        if (fonts == null) fonts = read();
        int family = -1; boolean availableItalic = false, availableNormal = false;
        for (Font font : fonts) if (font.language.equals(language)) {
            if (family < 0) family = font.family;
            if (font.family != family) break;
            if (font.italic) availableItalic = true; else availableNormal = true;
        }
        boolean selectedItalic = italic ? availableItalic : !availableNormal && availableItalic;
        Font chosen = null;
        for (Font font : fonts) {
            if (!font.language.equals(language) || font.family != family || font.italic != selectedItalic) continue;
            if (chosen == null || weightPreference(weight, font.weight) < weightPreference(weight, chosen.weight)) chosen = font;
        }
        Typeface face = null;
        if (chosen != null) {
            File file = resolve(chosen.file);
            if (file != null) try {
                Typeface.Builder builder = new Typeface.Builder(file).setTtcIndex(chosen.index)
                        .setWeight(chosen.weight).setItalic(chosen.italic);
                if (!chosen.axes.isEmpty()) builder.setFontVariationSettings(chosen.axes);
                face = builder.build();
            } catch (RuntimeException ignored) { /* Unreadable OEM fonts retain platform fallback. */ }
        }
        faces.put(key, face);
        return face;
    }

    /** CSS Fonts searches up/down in ordered groups, not by nearest numeric weight. */
    static int weightPreference(int requested, int available) {
        if (requested < 400) return available <= requested ? requested - available : 2000 + available - requested;
        if (requested > 500) return available >= requested ? available - requested : 2000 + requested - available;
        if (available >= requested && available <= 500) return available - requested;
        return available < requested ? 2000 + requested - available : 4000 + available - 500;
    }

    static int bolderWeight(int weight) { return weight < 350 ? 400 : weight < 550 ? 700 : 900; }

    private synchronized boolean variableWeightFamily(String locale) {
        String language = Locale.forLanguageTag(locale).getLanguage();
        if (!language.equals("ja") && !language.equals("zh")) return false;
        if (fonts == null) fonts = read();
        int family = -1;
        for (Font font : fonts) if (font.language.equals(language)) {
            if (family < 0) family = font.family;
            if (family != font.family) break;
            if (font.axes.contains("'wght' ")) return true;
        }
        return false;
    }

    private File resolve(String name) {
        if (name.isEmpty() || name.indexOf('\0') >= 0) return null;
        for (File directory : directories) try {
            File candidate = (new File(name).isAbsolute() ? new File(name) : new File(directory, name)).getCanonicalFile();
            String prefix = directory.getCanonicalPath() + File.separator;
            if (candidate.getPath().startsWith(prefix) && candidate.isFile() && candidate.length() <= 64L * 1024 * 1024) return candidate;
        } catch (java.io.IOException | SecurityException ignored) { }
        return null;
    }

    private List<Font> read() {
        List<Font> result = new ArrayList<>();
        if (!config.isFile() || config.length() > 2 * 1024 * 1024) return result;
        try (InputStream input = new FileInputStream(config)) {
            XmlPullParser parser = Xml.newPullParser(); parser.setInput(input, "UTF-8");
            String languages = "", file = "", axes = "";
            boolean family = false, font = false, italic = false;
            int index = 0, weight = 400, count = 0, familyIndex = 0;
            for (int event = parser.getEventType(); event != XmlPullParser.END_DOCUMENT; event = parser.nextToken()) {
                if (event == XmlPullParser.DOCDECL || parser.getDepth() > 8 || ++count > 20000) return new ArrayList<>();
                if (event == XmlPullParser.START_TAG) {
                    String tag = parser.getName();
                    if (tag.equals("family")) {
                        familyIndex++;
                        String name = parser.getAttributeValue(null, "name"), variant = parser.getAttributeValue(null, "variant");
                        family = name == null && !"elegant".equals(variant);
                        languages = value(parser, "lang", "");
                    } else if (tag.equals("font") && family) {
                        font = "sans-serif".equals(value(parser, "fallbackFor", "sans-serif"));
                        italic = "italic".equals(value(parser, "style", "normal"));
                        file = axes = ""; index = Integer.parseInt(value(parser, "index", "0"));
                        weight = Integer.parseInt(value(parser, "weight", "400"));
                    } else if (tag.equals("axis") && font) {
                        String axis = value(parser, "tag", ""), style = value(parser, "stylevalue", "");
                        if (!axis.matches("[A-Za-z0-9]{4}") || !Float.isFinite(Float.parseFloat(style))) return new ArrayList<>();
                        axes += (axes.isEmpty() ? "" : ",") + "'" + axis + "' " + style;
                    }
                } else if (event == XmlPullParser.TEXT && font && parser.getDepth() == 3) file += parser.getText().trim();
                else if (event == XmlPullParser.END_TAG) {
                    if (parser.getName().equals("font")) {
                        if (font && index >= 0 && index < 256 && weight >= 1 && weight <= 1000) {
                            for (String language : languages.split("[, ]+")) {
                                if (language.equals("ja") || language.equals("zh-Hans") || language.equals("zh"))
                                    result.add(new Font(language.equals("ja") ? "ja" : "zh", file, axes, index, weight, familyIndex, italic));
                            }
                        }
                        font = false;
                    } else if (parser.getName().equals("family")) family = false;
                }
            }
        } catch (Exception ignored) { return new ArrayList<>(); }
        return result;
    }

    private static String value(XmlPullParser parser, String name, String fallback) {
        String value = parser.getAttributeValue(null, name); return value == null ? fallback : value;
    }

    CharSequence apply(CharSequence value, String locale, int weight) {
        return apply(value, locale, weight, false, true);
    }

    CharSequence applyCss(CharSequence value, String locale, int weight) {
        return applyCss(value, locale, weight, true);
    }

    CharSequence applyCss(CharSequence value, String locale, int weight, boolean preservePlainText) {
        return apply(value, locale, weight, true, preservePlainText);
    }

    static boolean containsCjk(CharSequence value) {
        if (value == null) return false;
        for (int i = 0; i < value.length();) {
            int code = Character.codePointAt(value, i); if (cjk(code)) return true;
            i += Character.charCount(code);
        }
        return false;
    }

    private CharSequence apply(CharSequence value, String locale, int weight, boolean css, boolean preservePlainText) {
        if (value == null || value.length() == 0) return value;
        boolean containsCjk = containsCjk(value);
        boolean previousFaces = value instanceof Spanned && ((Spanned) value).getSpans(0, value.length(), FaceSpan.class).length > 0;
        if (!containsCjk && !previousFaces) return value;
        String language = Locale.forLanguageTag(locale).getLanguage();
        // face() only supplies Japanese/Chinese locale faces. Wrapping an unchanged
        // English-locale label in an empty Spannable disables BoringLayout's direct
        // String drawing path and allocates span structures for every recycled row.
        // Legacy TextView renders mixed-direction String/Spanned text differently.
        // Preserve that platform's existing shaping path.
        if (android.os.Build.VERSION.SDK_INT >= 28 && preservePlainText && !previousFaces
                && !language.equals("ja") && !language.equals("zh")) return value;
        // Do not freeze a real variable family to one fontconfig named instance.
        if (css && containsCjk && variableWeightFamily(locale)) return value;
        // Builder excludes NoCopySpan (TextView watchers/selection), unlike SpannableString's copy constructor.
        SpannableString text = new SpannableString(new android.text.SpannableStringBuilder(value));
        for (FaceSpan old : text.getSpans(0, text.length(), FaceSpan.class)) text.removeSpan(old);
        for (int i = 0; i < text.length();) {
            int code = Character.codePointAt(text, i), start = i; i += Character.charCount(code);
            if (!cjk(code)) continue;
            int end = css ? text.nextSpanTransition(start, text.length(), StyleSpan.class) : text.length();
            if (css) end = Math.min(end, text.nextSpanTransition(start, text.length(), TypefaceSpan.class));
            while (i < end) {
                int next = Character.codePointAt(text, i), type = Character.getType(next);
                if (!cjk(next) && type != Character.NON_SPACING_MARK && type != Character.COMBINING_SPACING_MARK) break;
                i += Character.charCount(next);
            }
            // Explicit code/font-family spans retain their own native fallback chain.
            if (css && text.getSpans(start, i, TypefaceSpan.class).length > 0) continue;
            int requested = weight; boolean italic = false;
            if (css) for (StyleSpan style : text.getSpans(start, i, StyleSpan.class)) {
                if ((style.getStyle() & Typeface.BOLD) != 0) requested = bolderWeight(requested);
                italic |= (style.getStyle() & Typeface.ITALIC) != 0;
            }
            Typeface face = face(locale, requested, italic);
            if (face != null) text.setSpan(new FaceSpan(face, css), start, i, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        }
        return text;
    }

    private static boolean cjk(int code) {
        if (code < 0x2e80) return false;
        Character.UnicodeScript script = Character.UnicodeScript.of(code);
        return script == Character.UnicodeScript.HAN || script == Character.UnicodeScript.HIRAGANA
                || script == Character.UnicodeScript.KATAKANA || script == Character.UnicodeScript.BOPOMOFO
                || code >= 0x3000 && code <= 0x30ff
                || code >= 0xff01 && code <= 0xff60;
    }

    static final class FaceSpan extends MetricAffectingSpan {
        final Typeface face;
        private final boolean noSynthesis;
        FaceSpan(Typeface face) { this(face, false); }
        FaceSpan(Typeface face, boolean noSynthesis) { this.face = face; this.noSynthesis = noSynthesis; }
        @Override public void updateMeasureState(TextPaint paint) {
            paint.setTypeface(face);
            if (noSynthesis) { paint.setFakeBoldText(false); paint.setTextSkewX(0); }
        }
        @Override public void updateDrawState(TextPaint paint) { updateMeasureState(paint); }
    }
}
