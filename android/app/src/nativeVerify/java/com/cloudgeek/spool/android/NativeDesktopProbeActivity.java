package com.cloudgeek.spool.android;

import android.app.Activity;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.os.SystemClock;
import android.view.MotionEvent;
import android.view.inputmethod.EditorInfo;
import java.io.DataInputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Executors;
import org.json.JSONArray;
import org.json.JSONObject;

/** Synthetic, network-free probe of the production dialog and real device H.264 decoders. */
public final class NativeDesktopProbeActivity extends Activity {
    private final Handler main = new Handler(Looper.getMainLooper());
    private final java.util.concurrent.ExecutorService io = Executors.newSingleThreadExecutor();
    private final List<String> commands = java.util.Collections.synchronizedList(new ArrayList<>());
    private final byte[][][] packets = new byte[8][2][];
    private NativeDesktopConnection.Listener listener;
    private NativeDesktopDialog dialog;
    private volatile int sent, acknowledged;
    private volatile boolean wireClosed, disconnected;
    private boolean exercised;
    private int windowedHeight, fullscreenHeight;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        NativeUi ui = new NativeUi(this, false, "zh-CN");
        setContentView(ui.text("Synthetic offline remote desktop verification", 14, ui.text));
        try {
            for (int stripe = 0; stripe < 2; stripe++) try (DataInputStream input = new DataInputStream(getAssets().open("desktop/stripe-" + stripe + ".bin"))) {
                for (int frame = 0; frame < 8; frame++) { int count = input.readInt(); packets[frame][stripe] = new byte[count]; input.readFully(packets[frame][stripe]); }
            }
            dialog = new NativeDesktopDialog(ui, new NativeDesktopConnection.Transport() {
                public JSONObject request(String path, boolean post) throws Exception {
                    if (path.equals("/desktop/connect")) return new JSONObject().put("token", "x".repeat(43));
                    if (path.startsWith("/desktop/disconnect")) disconnected = true;
                    return new JSONObject().put("available", !getIntent().getBooleanExtra("revoke", false) || acknowledged < 8);
                }
                public NativeDesktopConnection.Wire open(String token, NativeDesktopConnection.Listener callback) throws Exception {
                    listener = callback; listener.opened();
                    listener.message(("{\"t\":\"hello\",\"w\":640,\"h\":360,\"stripes\":[[0,180],[180,180]],\"readOnly\":"
                            + getIntent().getBooleanExtra("readOnly", false) + "}").getBytes(StandardCharsets.UTF_8), false);
                    return new NativeDesktopConnection.Wire() {
                        public void send(String command) {
                            commands.add(command);
                            if (command.equals("v,1")) io.execute(() -> sendFrame(0));
                            if (command.startsWith("a,")) {
                                acknowledged = Integer.parseInt(command.substring(2));
                                if (acknowledged < 8) io.execute(() -> sendFrame(acknowledged));
                            }
                        }
                        public void close() { wireClosed = true; }
                    };
                }
            }, io, () -> {});
            dialog.show(); main.postDelayed(this::report, 100);
        } catch (Exception error) { save(new JSONObject(), error.toString()); }
    }

    private void sendFrame(int frame) {
        if (wireClosed || frame < sent) return;
        sent = frame + 1;
        // Reverse stripe arrival order to exercise frame grouping.
        listener.message(packets[frame][1], true); listener.message(packets[frame][0], true);
    }
    private void touch(int action, float x, float y) {
        long now = SystemClock.uptimeMillis();
        MotionEvent event = MotionEvent.obtain(now, now, action, x, y, 0);
        dialog.desktop.dispatchTouchEvent(event); event.recycle();
    }
    private void report() {
        try {
            if (acknowledged == 8 && !exercised && dialog.desktop.ready()) {
                exercised = true;
                windowedHeight = dialog.desktop.getHeight();
                float x = dialog.desktop.getWidth() / 2f, y = dialog.desktop.getHeight() / 2f;
                touch(MotionEvent.ACTION_DOWN, x, y); touch(MotionEvent.ACTION_UP, x, y);
                dialog.desktop.onCreateInputConnection(new EditorInfo()).commitText("中文", 1);
                dialog.desktop.panMode(true); dialog.desktop.zoom(2);
                main.postDelayed(() -> {
                    dialog.desktop.fit(); dialog.desktop.panMode(false);
                    if ("control".equals(getIntent().getStringExtra("case")))
                        labelled(dialog.getWindow().getDecorView(), dialog.label(R.string.desktop_fullscreen)).performClick();
                }, 250);
            }
            android.view.View decor = dialog.getWindow().getDecorView();
            if (dialog.fullscreen()) fullscreenHeight = Math.max(fullscreenHeight, dialog.desktop.getHeight());
            int[] buttons = buttons(decor);
            android.view.View fullscreenButton = labelled(decor, dialog.label(dialog.fullscreen() ? R.string.desktop_fullscreen_exit : R.string.desktop_fullscreen));
            int[] buttonPosition = new int[2]; fullscreenButton.getLocationOnScreen(buttonPosition);
            android.view.View toolbarToggle = labelled(decor, dialog.label(dialog.toolbarCollapsed() ? R.string.desktop_toolbar_expand : R.string.desktop_toolbar_collapse));
            int[] togglePosition = new int[2]; toolbarToggle.getLocationOnScreen(togglePosition);
            JSONObject report = new JSONObject().put("sent", sent).put("acknowledged", acknowledged)
                    .put("case", getIntent().getStringExtra("case"))
                    .put("controllable", dialog.desktop.controllable()).put("exercised", exercised)
                    .put("wireClosed", wireClosed).put("disconnected", disconnected).put("showing", dialog.isShowing())
                    .put("zoom", dialog.desktop.viewport.zoom).put("fullscreen", dialog.fullscreen())
                    .put("windowedHeight", windowedHeight).put("fullscreenHeight", fullscreenHeight).put("height", dialog.desktop.getHeight())
                    .put("iconButtons", buttons[0]).put("textButtons", buttons[1])
                    .put("fullscreenButtonX", buttonPosition[0] + fullscreenButton.getWidth() / 2)
                    .put("fullscreenButtonY", buttonPosition[1] + fullscreenButton.getHeight() / 2)
                    .put("toolbarCollapsed", dialog.toolbarCollapsed()).put("toolbarWidth", ((android.view.View) toolbarToggle.getParent()).getWidth())
                    .put("width", dialog.desktop.getWidth())
                    .put("toolbarToggleX", togglePosition[0] + toolbarToggle.getWidth() / 2)
                    .put("toolbarToggleY", togglePosition[1] + toolbarToggle.getHeight() / 2);
            android.view.WindowInsets insets = decor.getRootWindowInsets();
            if (android.os.Build.VERSION.SDK_INT >= 30 && insets != null)
                report.put("systemBarsHidden", !insets.isVisible(android.view.WindowInsets.Type.statusBars())
                        && !insets.isVisible(android.view.WindowInsets.Type.navigationBars()));
            if (dialog.desktop.getChildCount() > 0) {
                android.view.ViewGroup surfaces = (android.view.ViewGroup)dialog.desktop.getChildAt(0);
                JSONArray pixels = new JSONArray();
                boolean match = true;
                for (int i = 0; i < surfaces.getChildCount(); i++) {
                    android.graphics.Bitmap image = ((android.view.TextureView)surfaces.getChildAt(i)).getBitmap(32, 9);
                    int color = image == null ? 0 : image.getPixel(i == 0 ? 7 : 20, 4);
                    pixels.put(color);
                    match &= i == 0 ? android.graphics.Color.red(color) > 180 && android.graphics.Color.blue(color) < 60
                            : android.graphics.Color.blue(color) > 180 && android.graphics.Color.red(color) < 60;
                    if (image != null) image.recycle();
                }
                report.put("pixels", pixels).put("pixelsMatch", match);
            }
            synchronized (commands) { report.put("commands", new JSONArray(commands)); }
            save(report, null);
            main.postDelayed(this::report, 100);
        } catch (Exception error) { save(new JSONObject(), error.toString()); }
    }
    private static android.view.View labelled(android.view.View view, String label) {
        if (label.equals(view.getContentDescription())) return view;
        if (view instanceof android.view.ViewGroup group) for (int i = 0; i < group.getChildCount(); i++) {
            android.view.View result = labelled(group.getChildAt(i), label);
            if (result != null) return result;
        }
        return null;
    }
    private static int[] buttons(android.view.View view) {
        int[] result = {view instanceof NativeUi.IconButton && view.getContentDescription() != null && view.getTooltipText() != null ? 1 : 0,
                view instanceof android.widget.Button ? 1 : 0};
        if (view instanceof android.view.ViewGroup group) for (int i = 0; i < group.getChildCount(); i++) {
            int[] children = buttons(group.getChildAt(i)); result[0] += children[0]; result[1] += children[1];
        }
        return result;
    }
    private void save(JSONObject report, String error) {
        try {
            if (error != null) report.put("error", error);
            File target = new File(getFilesDir(), "desktop-probe.json");
            try (FileOutputStream out = new FileOutputStream(target)) { out.write(report.toString().getBytes(StandardCharsets.UTF_8)); }
        } catch (Exception ignored) {}
    }
    @Override protected void onStop() { if (dialog != null) dialog.dismiss(); super.onStop(); }
    @Override protected void onDestroy() {
        if (dialog != null) dialog.dismiss();
        main.removeCallbacksAndMessages(null); io.shutdown(); super.onDestroy();
    }
}
