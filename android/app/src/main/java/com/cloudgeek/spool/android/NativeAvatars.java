package com.cloudgeek.spool.android;

import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Path;
import android.os.Handler;
import android.os.Looper;
import android.util.LruCache;
import android.view.View;
import android.widget.ImageView;
import com.caverock.androidsvg.SVG;
import org.json.JSONObject;
import java.lang.ref.WeakReference;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.Executor;

/** Local Go DiceBear generation and native SVG drawing; no image API or JS runtime. */
final class NativeAvatars {
    interface Source { String render(String style, String seed) throws Exception; }
    private final Executor worker;
    private final Source source;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final LruCache<String, Bitmap> cache = new LruCache<>(8 * 1024 * 1024) {
        @Override protected int sizeOf(String key, Bitmap value) { return value.getAllocationByteCount(); }
    };
    private final Map<String, List<WeakReference<ImageView>>> pending = new HashMap<>();
    private volatile int epoch;
    private static final java.util.regex.Pattern HREF = java.util.regex.Pattern.compile("(?:xlink:)?href\\s*=\\s*(['\"])(.*?)\\1");
    private static final java.util.regex.Pattern URL = java.util.regex.Pattern.compile("url\\(([^)]*)\\)");
    private static final java.util.regex.Pattern TRIM = java.util.regex.Pattern.compile("^[\\s\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]+|[\\s\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]+$");

    NativeAvatars(Executor worker) {
        this(worker, spoolnet.Spoolnet::renderAvatar);
    }

    NativeAvatars(Executor worker, Source source) {
        this.worker = worker;
        this.source = source;
    }

    static String name(JSONObject person) {
        String value = TRIM.matcher(person.optString("nickname")).replaceAll("");
        if (value.isEmpty()) value = TRIM.matcher(person.optString("username")).replaceAll("");
        return value.isEmpty() ? person.optString("id") : value;
    }

    static String seed(JSONObject person) {
        JSONObject config = config(person);
        String seed = config.optString("seed");
        return seed.isEmpty() ? name(person) : seed;
    }

    static JSONObject config(JSONObject person) {
        JSONObject config = person.optJSONObject("avatar");
        String style = config == null ? "clay" : config.optString("style", "clay");
        String seed = config == null ? "" : config.optString("seed");
        if (NativeAvatarCatalog.index(style) < 0) {
            if (!style.matches("bottts-neutral|shapes|initials")) seed = "";
            style = "clay";
        }
        return options(style, seed);
    }

    static JSONObject options(String style, String seed) {
        try { return new JSONObject().put("style", style).put("seed", seed); }
        catch (org.json.JSONException error) { throw new IllegalArgumentException(error); }
    }

    static JSONObject withAvatar(JSONObject person, String style, String seed) {
        try { return new JSONObject(person.toString()).put("avatar", options(style, seed)); }
        catch (org.json.JSONException error) { throw new IllegalArgumentException(error); }
    }

    static List<String> choices(String name, int page) {
        StringBuilder prefix = new StringBuilder();
        name.codePoints().filter(point -> Character.getType(point) != Character.CONTROL)
                .limit(24).forEach(prefix::appendCodePoint);
        if (prefix.length() == 0) prefix.append("spool");
        List<String> values = new ArrayList<>();
        for (int i = 0; i < 12; i++) values.add(prefix + ":" + ((long) page * 12 + i + 1));
        return values;
    }

    ImageView image(NativeUi ui, JSONObject person, int size) {
        ImageView image = placeholder(ui, size);
        bind(image, ui, person, size);
        return image;
    }

    void bind(ImageView image, NativeUi ui, JSONObject person, int size) {
        if (person.optBoolean("bot") || person.optString("id").equals("spool-bot")) {
            if ("spool-bot".equals(image.getTag()) && image.getDrawable() != null) return;
            image.setTag("spool-bot");
            image.setImageResource(R.drawable.spool_logo);
            return;
        }
        String style = config(person).optString("style"), seed = seed(person);
        int edge = Math.min(512, Math.max(96, ui.dp(size)));
        String key = new org.json.JSONArray().put(style).put(seed).put(edge).toString();
        if (key.equals(image.getTag()) && (image.getDrawable() != null || pending.containsKey(key))) return;
        image.setTag(key);
        image.setImageDrawable(null);
        Bitmap cached = cache.get(key);
        if (cached != null) image.setImageBitmap(cached);
        else {
            List<WeakReference<ImageView>> listeners = pending.get(key);
            if (listeners != null) listeners.add(new WeakReference<>(image));
            else {
                listeners = new ArrayList<>();
                listeners.add(new WeakReference<>(image));
                pending.put(key, listeners);
                int token = epoch;
                worker.execute(() -> {
                    if (epoch != token) return;
                    Bitmap bitmap = null;
                    try { bitmap = decode(source.render(style, seed), edge); } catch (Exception ignored) {}
                    Bitmap result = bitmap;
                    main.post(() -> {
                        if (epoch != token) {
                            if (result != null) result.recycle();
                            return;
                        }
                        List<WeakReference<ImageView>> targets = pending.remove(key);
                        if (result == null || targets == null) return;
                        cache.put(key, result);
                        for (WeakReference<ImageView> target : targets) {
                            ImageView current = target.get();
                            if (current != null && key.equals(current.getTag())) current.setImageBitmap(result);
                        }
                    });
                });
            }
        }
    }

    static ImageView placeholder(NativeUi ui, int size) {
        ImageView image = new ImageView(ui.context) {
            private final Path circle = new Path();
            @Override protected void onSizeChanged(int width, int height, int oldWidth, int oldHeight) {
                super.onSizeChanged(width, height, oldWidth, oldHeight);
                circle.reset();
                circle.addOval(0, 0, width, height, Path.Direction.CW);
            }
            @Override protected void onDraw(Canvas canvas) {
                int checkpoint = canvas.save();
                canvas.clipPath(circle);
                super.onDraw(canvas);
                canvas.restoreToCount(checkpoint);
            }
        };
        image.setLayoutParams(new android.widget.LinearLayout.LayoutParams(ui.dp(size), ui.dp(size)));
        image.setBackground(ui.shape(ui.soft, 999, 0));
        image.setClipToOutline(true);
        image.setScaleType(ImageView.ScaleType.CENTER_CROP);
        image.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
        return image;
    }

    static Bitmap decode(String source) throws Exception {
        return decode(source, 96);
    }

    private static Bitmap decode(String source, int edge) throws Exception {
        if (source == null || source.length() > 256 * 1024) throw new IllegalArgumentException("Invalid avatar size");
        String lower = source.toLowerCase(Locale.ROOT);
        // Even local SVGs must never resolve another resource.
        if (lower.contains("<!doctype") || lower.contains("<!entity") || lower.contains("<image")
                || lower.contains("<script") || lower.contains("<foreignobject") || lower.contains("@import"))
            throw new IllegalArgumentException("Avatar contains an external or active resource");
        java.util.regex.Matcher hrefs = HREF.matcher(lower);
        while (hrefs.find()) if (!hrefs.group(2).startsWith("#"))
            throw new IllegalArgumentException("Avatar contains an external reference");
        java.util.regex.Matcher urls = URL.matcher(lower);
        while (urls.find()) {
            String target = urls.group(1).trim().replaceAll("^['\"]|['\"]$", "");
            if (!target.startsWith("#")) throw new IllegalArgumentException("Avatar contains an external paint resource");
        }
        SVG svg = SVG.getFromString(source);
        svg.setDocumentWidth(edge);
        svg.setDocumentHeight(edge);
        Bitmap bitmap = Bitmap.createBitmap(edge, edge, Bitmap.Config.ARGB_8888);
        svg.renderToCanvas(new Canvas(bitmap));
        return bitmap;
    }

    void clear() {
        epoch++;
        pending.clear();
        cache.evictAll();
    }
}
