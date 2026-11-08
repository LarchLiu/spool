package com.cloudgeek.spool.android;

import android.app.Activity;
import android.app.Application;
import android.content.pm.ApplicationInfo;
import android.os.Bundle;
import android.os.Handler;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import org.json.JSONArray;
import org.json.JSONObject;

/** Synthetic startup smoke for the non-debuggable, R8/resource-shrunk verification APK. */
@android.annotation.TargetApi(29)
public final class ReleaseRendererProbe extends Application implements Application.ActivityLifecycleCallbacks {
    private boolean started, reported;
    private NativeFilePreview sourcePreview;
    private final Handler main = new Handler(android.os.Looper.getMainLooper());

    @Override public void onCreate() {
        super.onCreate();
        // Prove a release startup ignores an old debug preference, even when false.
        getSharedPreferences("rendering-trial", MODE_PRIVATE).edit().putBoolean("markwon", false).commit();
        registerActivityLifecycleCallbacks(this);
    }
    private static Object field(Activity activity, String name) throws Exception {
        Field field = MainActivity.class.getDeclaredField(name); field.setAccessible(true); return field.get(activity);
    }
    private static void field(Activity activity, String name, Object value) throws Exception {
        Field field = MainActivity.class.getDeclaredField(name); field.setAccessible(true); field.set(activity, value);
    }
    private static void call(Activity activity, String name) throws Exception {
        Method method = MainActivity.class.getDeclaredMethod(name); method.setAccessible(true); method.invoke(activity);
    }
    private static boolean text(View view, String expected) {
        if (view instanceof TextView && ((TextView) view).getText().toString().contains(expected)) return true;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++)
            if (text(((ViewGroup) view).getChildAt(i), expected)) return true;
        return false;
    }
    private static <T> T child(View view, Class<T> type) {
        if (type.isInstance(view)) return type.cast(view);
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            T found = child(((ViewGroup) view).getChildAt(i), type); if (found != null) return found;
        }
        return null;
    }
    private static int colors(TextView view) {
        return view != null && view.getText() instanceof android.text.Spanned
                ? ((android.text.Spanned) view.getText()).getSpans(0, view.length(), android.text.style.ForegroundColorSpan.class).length : 0;
    }
    private void report(Activity activity, JSONObject value) {
        if (reported) return; reported = true;
        try { value.put("token", activity.getIntent().getStringExtra("release_probe_token")); }
        catch (Exception impossible) { throw new IllegalStateException(impossible); }
        android.util.Log.i("SpoolReleaseVerify", value.toString());
    }
    private void failure(Activity activity, Exception error) {
        try { report(activity, new JSONObject().put("error", error.getClass().getSimpleName() + ": " + error.getMessage())); }
        catch (Exception impossible) { throw new IllegalStateException(impossible); }
    }
    @Override public void onActivityResumed(Activity activity) {
        if (!(activity instanceof MainActivity) || started) return; started = true;
        try {
            if ((getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0) throw new IllegalStateException("Verification APK is debuggable");
            if (!(boolean) field(activity, "useMarkwonHistory")) throw new IllegalStateException("Release selected the legacy renderer");
            call(activity, "closeDrawer");
            field(activity, "user", new JSONObject().put("id", "release-fixture"));
            field(activity, "chatID", "release-fixture");
            JSONArray parts = new JSONArray().put(new JSONObject().put("role", "user").put("kind", "text").put("text", "Synthetic startup check"))
                    .put(new JSONObject().put("role", "assistant").put("kind", "text").put("phase", "final_answer")
                            .put("text", "Release **Markdown 中文**\n\n| Name | Value |\n| --- | --- |\n| fixture | ready |\n\n- [x] Task ready\n\n```java\nint releaseValue = 42;\n```"));
            JSONObject turn = new JSONObject().put("id", "turn-fixture").put("status", "completed").put("backend", "codex")
                    .put("part_start", 0).put("input_end", 1).put("part_end", 2);
            field(activity, "chat", new JSONObject().put("id", "release-fixture").put("title", "Release fixture")
                    .put("backend", "codex").put("status", "completed").put("turns", new JSONArray().put(turn))
                    .put("transcript", new JSONObject().put("parts", parts)));
            call(activity, "render");
            awaitRender(activity, 0);
        } catch (Exception error) { failure(activity, error); }
    }
    private void awaitRender(Activity activity, int attempts) {
        main.postDelayed(() -> {
            if (reported) return;
            try {
                NativeMarkwonHistory history = (NativeMarkwonHistory) field(activity, "markwonHistory");
                NativeCodeBlock code = history == null ? null : child(history, NativeCodeBlock.class);
                if (history != null && history.pending() == 0 && history.getChildCount() > 0
                        && text(history, "Markdown 中文") && text(history, "releaseValue = 42") && code != null && colors(code.code) > 0) {
                    if (field(activity, "history") != null) throw new IllegalStateException("Legacy history also mounted");
                    if (sourcePreview == null) {
                        NativeUi ui = (NativeUi) field(activity, "ui");
                        sourcePreview = new NativeFilePreview(ui, "fixture", null, target -> {}, path -> {}, file -> {}, () -> {});
                        sourcePreview.bind(new JSONObject().put("kind", "text").put("path", "fixture.ts").put("name", "fixture.ts")
                                        .put("content", "export const ready = true;\n\tconst label = 'Native source';\n"),
                                new NativeFileTarget("fixture.ts", 0, 0), "");
                        ((ViewGroup) activity.findViewById(android.R.id.content)).addView(sourcePreview, new ViewGroup.LayoutParams(-1, -1));
                    }
                    NativeFileSource source = child(sourcePreview, NativeFileSource.class);
                    if (colors(source) == 0) {
                        if (attempts >= 150) throw new IllegalStateException("Release source syntax did not render");
                        awaitRender(activity, attempts + 1); return;
                    }
                    history.getViewTreeObserver().registerFrameCommitCallback(() -> {
                        try { report(activity, new JSONObject().put("debuggable", false).put("defaultMarkwon", true)
                                .put("legacyPreferenceIgnored", true).put("legacyIntentIgnored", !activity.getIntent().getBooleanExtra("spool_markwon", true))
                                .put("markdownRendered", true).put("codeBlockRendered", true).put("codeSyntaxHighlighted", true)
                                .put("fileSourceSyntaxHighlighted", true).put("codeColorSpans", colors(code.code)).put("sourceColorSpans", colors(source))
                                .put("frameCommitted", true).put("blocks", history.blocks())); }
                        catch (Exception error) { failure(activity, error); }
                    });
                    history.invalidate();
                } else if (attempts >= 150) throw new IllegalStateException("Release Markdown/code syntax did not render");
                if (!reported) awaitRender(activity, attempts + 1);
            } catch (Exception error) { failure(activity, error); }
        }, 100);
    }
    @Override public void onActivityCreated(Activity activity, Bundle state) {}
    @Override public void onActivityStarted(Activity activity) {}
    @Override public void onActivityPaused(Activity activity) {}
    @Override public void onActivityStopped(Activity activity) {}
    @Override public void onActivitySaveInstanceState(Activity activity, Bundle state) {}
    @Override public void onActivityDestroyed(Activity activity) {}
}
