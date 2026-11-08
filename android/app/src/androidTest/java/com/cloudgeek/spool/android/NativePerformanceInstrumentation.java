package com.cloudgeek.spool.android;

import android.app.Activity;
import android.app.Dialog;
import android.app.Instrumentation;
import android.content.Intent;
import android.os.Bundle;
import android.os.Looper;
import android.os.Handler;
import android.os.HandlerThread;
import android.os.SystemClock;
import android.view.FrameMetrics;
import android.view.View;
import android.view.ViewGroup;
import androidx.core.widget.NestedScrollView;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import org.json.JSONArray;
import org.json.JSONObject;

/** Device-only, read-only UI/long-chat diagnostics; never sends messages or writes preferences. */
public final class NativePerformanceInstrumentation extends Instrumentation {
    private Bundle arguments;
    private volatile Activity activity;
    private final Map<String, List<Double>> durations = new LinkedHashMap<>();
    private final Map<String, Integer> samples = new LinkedHashMap<>();

    @Override public void onCreate(Bundle arguments) {
        super.onCreate(arguments); this.arguments = arguments; start();
    }

    @Override public void callActivityOnCreate(Activity activity, Bundle state) {
        super.callActivityOnCreate(activity, state);
        if (activity instanceof MainActivity) this.activity = activity;
    }

    private Object field(String name) {
        try { Field field = MainActivity.class.getDeclaredField(name); field.setAccessible(true); return field.get(activity); }
        catch (ReflectiveOperationException error) { throw new AssertionError(error); }
    }

    private void call(String name) {
        try { Method method = MainActivity.class.getDeclaredMethod(name); method.setAccessible(true); method.invoke(activity); }
        catch (ReflectiveOperationException error) { throw new AssertionError(error); }
    }

    private View described(View root, String label) {
        if (label.contentEquals(root.getContentDescription() == null ? "" : root.getContentDescription()) && root.isClickable()) return root;
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++) {
            View found = described(((ViewGroup) root).getChildAt(i), label); if (found != null) return found;
        }
        return null;
    }

    private void clickSettings() {
        NativeUi ui = (NativeUi) field("ui");
        View target = described((View) field("root"), ui.t("Appearance and language"));
        if (target == null) call("openSettings"); else target.performClick();
    }

    private void record(String name, double value) { durations.computeIfAbsent(name, key -> new ArrayList<>()).add(value); }

    private void action(String name, Runnable action) throws InterruptedException {
        CountDownLatch drawn = new CountDownLatch(1);
        long start = SystemClock.elapsedRealtimeNanos();
        runOnMainSync(() -> {
            long uiStart = SystemClock.elapsedRealtimeNanos(); action.run();
            record(name + ".uiMs", (SystemClock.elapsedRealtimeNanos() - uiStart) / 1e6);
            Dialog dialog = (Dialog) field("settingsDialog");
            View decor = dialog != null && dialog.isShowing() ? dialog.getWindow().getDecorView() : activity.getWindow().getDecorView();
            decor.getViewTreeObserver().registerFrameCommitCallback(drawn::countDown); decor.invalidate();
        });
        if (!drawn.await(5, TimeUnit.SECONDS)) throw new AssertionError("No committed frame: " + name);
        record(name + ".frameMs", (SystemClock.elapsedRealtimeNanos() - start) / 1e6);
        SystemClock.sleep(260);
    }

    private JSONObject summary() throws Exception {
        JSONObject result = new JSONObject().put("label", arguments.getString("label", "device"));
        JSONObject timings = new JSONObject();
        for (Map.Entry<String, List<Double>> entry : durations.entrySet()) {
            List<Double> sorted = new ArrayList<>(entry.getValue()); Collections.sort(sorted);
            timings.put(entry.getKey(), new JSONObject().put("count", sorted.size()).put("median", sorted.get(sorted.size() / 2))
                    .put("p95", sorted.get(Math.min(sorted.size() - 1, (int) Math.ceil(sorted.size() * .95) - 1))).put("max", sorted.get(sorted.size() - 1)));
        }
        JSONObject stacks = new JSONObject();
        samples.entrySet().stream().sorted((a, b) -> Integer.compare(b.getValue(), a.getValue())).limit(15)
                .forEach(entry -> { try { stacks.put(entry.getKey(), entry.getValue()); } catch (Exception error) { throw new AssertionError(error); } });
        Runtime runtime = Runtime.getRuntime();
        return result.put("timings", timings).put("mainThreadSamples", stacks).put("javaUsedBytes", runtime.totalMemory() - runtime.freeMemory())
                .put("javaMaxBytes", runtime.maxMemory());
    }

    @Override public void onStart() {
        Bundle output = new Bundle(); AtomicBoolean sampling = new AtomicBoolean(true);
        HandlerThread metrics = new HandlerThread("spool-frame-metrics"); metrics.start();
        List<Double> frames = Collections.synchronizedList(new ArrayList<>());
        Thread sampler = new Thread(() -> {
            while (sampling.get()) {
                StackTraceElement[] stack = Looper.getMainLooper().getThread().getStackTrace();
                for (StackTraceElement frame : stack) if (frame.getClassName().startsWith("com.cloudgeek.spool.android.")) {
                    samples.merge(frame.getClassName() + "." + frame.getMethodName(), 1, Integer::sum); break;
                }
                SystemClock.sleep(4);
            }
        }, "spool-ui-sampler");
        try {
            runOnMainSync(() -> getTargetContext().startActivity(new Intent(getTargetContext(), MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)));
            for (int i = 0; i < 120 && activity == null; i++) SystemClock.sleep(250);
            if (activity == null) throw new AssertionError("Activity launch not permitted");
            boolean[] ready = {false};
            for (int i = 0; i < 120 && !ready[0]; i++) {
                runOnMainSync(() -> ready[0] = field("user") != null && (boolean) field("workspaceReady")); SystemClock.sleep(250);
            }
            if (!ready[0]) throw new AssertionError("Workspace not ready; no session data changed");
            SystemClock.sleep(1500); sampler.start();
            runOnMainSync(() -> activity.getWindow().addOnFrameMetricsAvailableListener((window, frame, dropped) ->
                    frames.add(frame.getMetric(FrameMetrics.TOTAL_DURATION) / 1e6), new Handler(metrics.getLooper())));
            for (int round = 0; round < 12; round++) {
                final int velocity = round % 2 == 0 ? -7000 : 7000;
                action("history.fling", () -> {
                    NativeMarkwonHistory recycler = (NativeMarkwonHistory) field("markwonHistory");
                    if (recycler != null) recycler.fling(0, velocity);
                    else ((NestedScrollView) field("history")).fling(velocity);
                });
                SystemClock.sleep(500);
            }
            for (int round = 0; round < 8; round++) {
                action("sidebar.open", () -> call("openSidebar"));
                action("settings.open", this::clickSettings);
                action("settings.close", () -> ((NativeSettings) field("settingsDialog")).content.close.performClick());
                action("people.open", () -> call("openOnlinePeople"));
                action("people.close", () -> call("openOnlinePeople"));
            }
            sampling.set(false); sampler.join();
            synchronized (frames) { durations.put("render.frameMs", new ArrayList<>(frames)); }
            metrics.quitSafely(); metrics.join();
            output.putString("stream", "SPOOL_PERF " + summary().toString() + "\n");
            finish(Activity.RESULT_OK, output);
        } catch (Throwable error) {
            sampling.set(false);
            metrics.quitSafely();
            output.putString("stream", "SPOOL_PERF_ERROR " + error.getClass().getSimpleName() + ": " + error.getMessage() + "\n");
            finish(Activity.RESULT_CANCELED, output);
        }
    }
}
