package com.cloudgeek.spool.android;

import android.graphics.Picture;
import android.graphics.RectF;
import android.util.LruCache;
import android.util.Xml;
import com.caverock.androidsvg.SVG;
import java.io.StringReader;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.Locale;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.xmlpull.v1.XmlPullParser;

/** Workspace-owned vector cache. No browser, Activity, source text or disk files are retained. */
final class NativeMermaidCache {
    static final int MAX_SVG = 1024 * 1024;
    static final class Image {
        final Picture picture;
        final float width, height;
        final int cost;
        final String accessibilityLabel;
        Image(Picture picture, float width, float height, int cost, String accessibilityLabel) {
            this.picture = picture; this.width = width; this.height = height; this.cost = cost; this.accessibilityLabel = accessibilityLabel;
        }
    }
    // Minimum weight also caps the cache at 32 diagrams, including syntax errors.
    static final Image ERROR = new Image(null, 0, 0, 128 * 1024, "");
    private final LruCache<String, Image> images = new LruCache<>(4 * 1024 * 1024) {
        @Override protected int sizeOf(String key, Image image) { return image.cost; }
    };
    private static final Pattern REFERENCE = Pattern.compile("url\\(\\s*[\"']?([^)'\"]*)[\"']?\\s*\\)", Pattern.CASE_INSENSITIVE);

    String key(String source, boolean dark) {
        try {
            byte[] digest = MessageDigest.getInstance("SHA-256").digest(source.getBytes(StandardCharsets.UTF_8));
            return (dark ? "dark:" : "light:") + android.util.Base64.encodeToString(digest, android.util.Base64.NO_WRAP);
        } catch (java.security.NoSuchAlgorithmException impossible) { throw new IllegalStateException(impossible); }
    }

    Image get(String key) { return images.get(key); }
    void put(String key, Image image) { images.put(key, image); }

    static Image decode(String document) throws Exception {
        if (document == null || document.length() > MAX_SVG) throw new IllegalArgumentException("Diagram is too large");
        String lower = document.toLowerCase(Locale.ROOT);
        if (lower.contains("<!doctype") || lower.contains("<!entity"))
            throw new IllegalArgumentException("Diagram is not a self-contained vector");
        XmlPullParser parser = Xml.newPullParser(); parser.setInput(new StringReader(document));
        for (int event = parser.getEventType(); event != XmlPullParser.END_DOCUMENT; event = parser.nextToken()) {
            if (event == XmlPullParser.DOCDECL) throw new IllegalArgumentException("Diagram contains a document declaration");
            if (event != XmlPullParser.START_TAG) continue;
            String tag = parser.getName().toLowerCase(Locale.ROOT);
            if (tag.equals("image") || tag.equals("script") || tag.equals("foreignobject") || tag.equals("style"))
                throw new IllegalArgumentException("Diagram is not a self-contained vector");
            // Inspect resource attributes, not labels which may describe URLs or CSS.
            for (int i = 0; i < parser.getAttributeCount(); i++) {
                String value = parser.getAttributeValue(i).trim();
                if (parser.getAttributeName(i).equalsIgnoreCase("href") && !value.startsWith("#"))
                    throw new IllegalArgumentException("Diagram contains an external reference");
                Matcher references = REFERENCE.matcher(value);
                while (references.find()) if (!references.group(1).trim().startsWith("#"))
                    throw new IllegalArgumentException("Diagram contains an external reference");
            }
        }
        SVG svg = SVG.getFromString(document);
        RectF bounds = svg.getDocumentViewBox();
        if (bounds == null || !Float.isFinite(bounds.width()) || !Float.isFinite(bounds.height())
                || bounds.width() <= 0 || bounds.height() <= 0 || bounds.width() > 20000 || bounds.height() > 20000)
            throw new IllegalArgumentException("Invalid diagram bounds");
        svg.setDocumentWidth(bounds.width()); svg.setDocumentHeight(bounds.height());
        Picture picture = svg.renderToPicture((int) Math.ceil(bounds.width()), (int) Math.ceil(bounds.height()));
        String title = svg.getDocumentTitle(), description = svg.getDocumentDescription();
        title = title == null ? "" : title.trim(); description = description == null ? "" : description.trim();
        String label = title.isEmpty() ? "Mermaid" : title;
        if (!description.isEmpty()) label += "\n" + description;
        return new Image(picture, bounds.width(), bounds.height(), Math.max(128 * 1024, document.length() * 4), label);
    }
}
