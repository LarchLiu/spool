package com.cloudgeek.spool.android;

import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Matrix;
import androidx.exifinterface.media.ExifInterface;
import android.os.Handler;
import android.os.Looper;
import android.util.LruCache;
import android.widget.ImageView;
import java.io.IOException;
import java.lang.ref.WeakReference;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Collections;
import java.util.Set;
import java.util.WeakHashMap;
import java.util.concurrent.Executor;
import java.util.function.Consumer;
import java.util.function.Supplier;

/** Auth-scoped, coalesced native image loading. All remote reads are injected tsnet API calls. */
final class NativeImages {
    interface Source { NativeFileData load(String relativeAPIPath) throws Exception; }
    interface Load { NativeFileData load() throws Exception; }
    private static final class Target {
        final WeakReference<ImageView> image;
        final Consumer<Exception> completion;
        Target(ImageView image, Consumer<Exception> completion) { this.image = new WeakReference<>(image); this.completion = completion; }
    }
    private final Executor worker;
    private final Supplier<Source> source;
    private final Handler main = new Handler(Looper.getMainLooper());
    private static final class Asset {
        final Bitmap first;
        final NativeImageSequence sequence;
        Asset(Bitmap first, NativeImageSequence sequence) { this.first = first; this.sequence = sequence; }
        int size() { return first.getAllocationByteCount() + (sequence == null ? 0 : sequence.bytes.length + sequence.frames.size() * 64); }
    }
    private final LruCache<String, Asset> cache = new LruCache<>(12 * 1024 * 1024) {
        @Override protected int sizeOf(String key, Asset image) { return image.size(); }
    };
    private final Set<NativeAnimatedImage> animations = Collections.newSetFromMap(new WeakHashMap<>());
    private final Map<String, List<Target>> pending = new HashMap<>();
    private volatile int epoch;

    NativeImages(Executor worker, Source source) { this(worker, () -> source); }

    NativeImages(Executor worker, Supplier<Source> source) { this.worker = worker; this.source = source; }

    void remote(ImageView image, String path, int edge, Consumer<Exception> completion) {
        remote(image, path, "", edge, completion);
    }

    void remote(ImageView image, String path, String scope, int edge, Consumer<Exception> completion) {
        if (!NativeFileData.allowedPath(path)) throw new IllegalArgumentException("Only authorized workspace file APIs are supported");
        Source captured = source.get();
        request(image, "remote:" + path + (scope.isEmpty() ? "" : "\n" + scope), edge, () -> captured.load(path), completion);
    }

    void local(ImageView image, String id, NativeFileData data, int edge, Consumer<Exception> completion) {
        request(image, "local:" + id, edge, () -> data, completion);
    }

    private void request(ImageView image, String identity, int requestedEdge, Load load, Consumer<Exception> completion) {
        int edge = Math.min(2048, Math.max(1, requestedEdge));
        String key = identity + "\n" + edge;
        image.setTag(key);
        Asset cached = cache.get(key);
        if (cached != null) {
            bind(image, cached, edge);
            if (completion != null) completion.accept(null);
            return;
        }
        image.setImageDrawable(null);
        Target target = new Target(image, completion);
        List<Target> targets = pending.get(key);
        if (targets != null) { targets.add(target); return; }
        targets = new ArrayList<>();
        targets.add(target);
        pending.put(key, targets);
        int token = epoch;
        worker.execute(() -> {
            if (token != epoch) return;
            Asset asset = null;
            Exception error = null;
            try {
                NativeFileData file = load.load();
                if (!NativeFileData.image(file.mediaType)) throw new IOException("File is not an image");
                NativeImageSequence sequence = NativeImageSequence.parse(file.bytes);
                Bitmap first;
                if (sequence == null) first = decode(file, edge);
                else try (NativeImageSequence.Renderer renderer = sequence.renderer(edge)) { renderer.render(0); first = renderer.snapshot(); }
                asset = new Asset(first, sequence != null && sequence.frames.size() > 1 ? sequence : null);
            }
            catch (Exception failure) { error = failure; }
            Asset result = asset;
            Exception failure = error;
            main.post(() -> {
                if (token != epoch) { if (result != null) result.first.recycle(); return; }
                List<Target> listeners = pending.remove(key);
                if (listeners == null) return;
                if (result != null) cache.put(key, result);
                for (Target listener : listeners) {
                    ImageView current = listener.image.get();
                    if (current == null || !key.equals(current.getTag())) continue;
                    if (result != null) bind(current, result, edge);
                    if (listener.completion != null) listener.completion.accept(failure);
                }
            });
        });
    }

    private void bind(ImageView image, Asset asset, int edge) {
        if (asset.sequence == null) image.setImageBitmap(asset.first);
        else {
            NativeAnimatedImage animation = new NativeAnimatedImage(asset.sequence, asset.first, edge, image.getResources().getDisplayMetrics().density, worker);
            animations.add(animation); image.setImageDrawable(animation);
        }
    }

    static Bitmap decode(NativeFileData file, int edge) throws IOException {
        if (!NativeFileData.image(file.mediaType)) throw new IOException("File is not an image");
        BitmapFactory.Options options = new BitmapFactory.Options();
        options.inJustDecodeBounds = true;
        options.inScaled = false;
        BitmapFactory.decodeByteArray(file.bytes, 0, file.bytes.length, options);
        if (options.outWidth <= 0 || options.outHeight <= 0 || (long) options.outWidth * options.outHeight > 25_000_000)
            throw new IOException("Invalid or oversized image");
        edge = Math.min(2048, Math.max(1, edge));
        options.inSampleSize = 1;
        while (Math.max(options.outWidth, options.outHeight) / options.inSampleSize > edge * 2
                || (long) (options.outWidth / options.inSampleSize) * (options.outHeight / options.inSampleSize) > 8_000_000)
            options.inSampleSize *= 2;
        options.inJustDecodeBounds = false;
        Bitmap bitmap = BitmapFactory.decodeByteArray(file.bytes, 0, file.bytes.length, options);
        if (bitmap == null) throw new IOException("Invalid image");
        Matrix orientation = new Matrix();
        try {
            int value = new ExifInterface(new java.io.ByteArrayInputStream(file.bytes)).getAttributeInt(ExifInterface.TAG_ORIENTATION, ExifInterface.ORIENTATION_NORMAL);
            switch (value) {
                case ExifInterface.ORIENTATION_FLIP_HORIZONTAL: orientation.setScale(-1, 1); break;
                case ExifInterface.ORIENTATION_ROTATE_180: orientation.setRotate(180); break;
                case ExifInterface.ORIENTATION_FLIP_VERTICAL: orientation.setScale(1, -1); break;
                case ExifInterface.ORIENTATION_TRANSPOSE: orientation.setRotate(90); orientation.postScale(-1, 1); break;
                case ExifInterface.ORIENTATION_ROTATE_90: orientation.setRotate(90); break;
                case ExifInterface.ORIENTATION_TRANSVERSE: orientation.setRotate(-90); orientation.postScale(-1, 1); break;
                case ExifInterface.ORIENTATION_ROTATE_270: orientation.setRotate(-90); break;
                default: break;
            }
        } catch (IOException ignored) { /* A valid raster image need not contain EXIF. */ }
        if (!orientation.isIdentity()) {
            Bitmap oriented = Bitmap.createBitmap(bitmap, 0, 0, bitmap.getWidth(), bitmap.getHeight(), orientation, true);
            if (oriented != bitmap) bitmap.recycle();
            bitmap = oriented;
        }
        int longest = Math.max(bitmap.getWidth(), bitmap.getHeight());
        if (longest > edge) {
            Bitmap scaled = Bitmap.createScaledBitmap(bitmap, Math.max(1, Math.round((float) bitmap.getWidth() * edge / longest)),
                    Math.max(1, Math.round((float) bitmap.getHeight() * edge / longest)), true);
            if (scaled != bitmap) bitmap.recycle();
            bitmap = scaled;
        }
        // A source image pixel is one CSS pixel for intrinsic (non-upscaled) previews.
        bitmap.setDensity(android.util.DisplayMetrics.DENSITY_DEFAULT);
        return bitmap;
    }

    void clear() { epoch++; pending.clear(); cache.evictAll(); for (NativeAnimatedImage animation : animations) animation.release(true); animations.clear(); }
}
