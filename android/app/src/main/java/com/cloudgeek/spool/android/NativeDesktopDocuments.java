package com.cloudgeek.spool.android;

import android.app.Activity;
import android.content.Intent;
import android.net.Uri;
import java.util.function.Consumer;

/** One explicit system document-picker round trip, owned by the current desktop view. */
final class NativeDesktopDocuments implements AutoCloseable {
    private static final java.util.concurrent.atomic.AtomicInteger nextRequest = new java.util.concurrent.atomic.AtomicInteger(12000);
    final int requestCode = nextRequest.getAndUpdate(value -> value == 31999 ? 12000 : value + 1);
    private final Activity activity;
    private final Consumer<Boolean> visibility;
    private volatile Consumer<Uri> completion;
    private boolean closed;

    NativeDesktopDocuments(Activity activity, Consumer<Boolean> visibility) {
        this.activity = activity; this.visibility = visibility;
    }
    boolean waiting() { return completion != null; }
    static boolean handles(int request) { return request >= 12000 && request < 32000; }
    void pick(Intent intent, Consumer<Uri> selected) {
        if (closed || waiting()) return;
        completion = selected; visibility.accept(true);
        try { activity.startActivityForResult(intent, requestCode); }
        catch (RuntimeException error) { completion = null; visibility.accept(false); throw error; }
    }
    void result(int request, int result, Intent data) {
        if (request != requestCode) return;
        Consumer<Uri> selected = completion; completion = null;
        if (closed || selected == null) return;
        visibility.accept(false);
        selected.accept(result == Activity.RESULT_OK && data != null ? data.getData() : null);
    }
    @Override public void close() { closed = true; completion = null; }
}
