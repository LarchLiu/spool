package com.cloudgeek.spool.android;

import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.RectF;
import android.graphics.SurfaceTexture;
import android.os.Handler;
import android.os.Looper;
import android.text.InputType;
import android.view.InputDevice;
import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.Surface;
import android.view.TextureView;
import android.view.View;
import android.view.ViewConfiguration;
import android.view.ViewGroup;
import android.view.inputmethod.BaseInputConnection;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputConnection;
import android.view.inputmethod.InputMethodManager;
import android.widget.FrameLayout;
import java.util.HashMap;
import java.util.Locale;
import java.util.Map;
import java.util.function.Consumer;

/** Native video surfaces, fit-relative viewport, pointer gestures and Android IME. */
final class NativeDesktopView extends FrameLayout implements AutoCloseable {
    final NativeDesktopViewport viewport = new NativeDesktopViewport();
    private final NativeUi ui;
    private final Consumer<String> send, failed;
    private final Runnable changed, firstFrame;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG | Paint.FILTER_BITMAP_FLAG);
    private final Map<Integer, Integer> keys = new HashMap<>();
    private final java.util.Set<Integer> sticky = new java.util.HashSet<>();
    private NativeDesktopProtocol.Layout layout;
    private volatile NativeDesktopVideo video;
    private ViewGroup surface;
    private Bitmap cursor;
    private int hotX, hotY, pointerX, pointerY;
    private boolean pointerVisible, active, panMode;
    private volatile boolean closed;
    private NativeDesktopClipboard clipboard;
    private int gesture;
    private float startX, startY, lastX, lastY, midX, midY, distance;

    NativeDesktopView(NativeUi ui, Consumer<String> send, Consumer<String> failed, Runnable changed, Runnable firstFrame) {
        super(ui.context);
        this.ui = ui; this.send = send; this.failed = failed; this.changed = changed; this.firstFrame = firstFrame;
        setBackgroundColor(0xff101010); setClipChildren(true); setFocusableInTouchMode(true);
        setContentDescription(ui.label("Remote desktop", "远程桌面"));
    }

    void configure(NativeDesktopProtocol.Layout value) {
        if (closed) return;
        stopVideo();
        layout = value; active = false; pointerVisible = false;
        viewport.reset(); keys.clear(); sticky.clear(); changed.run();
        surface = new ViewGroup(getContext()) {
            @Override protected void onMeasure(int ws, int hs) {
                int w = MeasureSpec.getSize(ws), h = MeasureSpec.getSize(hs);
                setMeasuredDimension(w, h);
                for (int i = 0; i < getChildCount(); i++) {
                    int top = Math.round((float) value.y[i] * h / value.height);
                    int bottom = Math.round((float) (value.y[i] + value.heights[i]) * h / value.height);
                    getChildAt(i).measure(MeasureSpec.makeMeasureSpec(w, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(bottom - top, MeasureSpec.EXACTLY));
                }
            }
            @Override protected void onLayout(boolean changed, int l, int t, int r, int b) {
                for (int i = 0; i < getChildCount(); i++) {
                    int top = Math.round((float) value.y[i] * getHeight() / value.height);
                    View child = getChildAt(i);
                    child.layout(0, top, getWidth(), top + child.getMeasuredHeight());
                }
            }
        };
        NativeDesktopVideo decoder = new NativeDesktopVideo(value, send, failed, () -> main.post(() -> {
            if (closed || video == null || layout != value) return;
            active = true; firstFrame.run(); changed.run();
        }));
        video = decoder;
        for (int i = 0; i < value.y.length; i++) {
            final int stripe = i;
            TextureView texture = new TextureView(getContext());
            texture.setSurfaceTextureListener(new TextureView.SurfaceTextureListener() {
                public void onSurfaceTextureAvailable(SurfaceTexture texture, int w, int h) {
                    texture.setDefaultBufferSize(value.width, value.heights[stripe]);
                    decoder.surface(stripe, new Surface(texture));
                }
                public void onSurfaceTextureSizeChanged(SurfaceTexture texture, int w, int h) {}
                public void onSurfaceTextureUpdated(SurfaceTexture texture) {}
                public boolean onSurfaceTextureDestroyed(SurfaceTexture texture) {
                    decoder.close();
                    if (!closed && video == decoder) failed.accept("Desktop video surface was lost");
                    return true;
                }
            });
            surface.addView(texture);
        }
        addView(surface);
        requestLayout();
    }

    void binary(byte[] bytes) {
        if (closed || bytes.length == 0) return;
        NativeDesktopVideo decoder = video;
        if (bytes[0] == 1 && decoder != null) decoder.offer(bytes);
        else if (bytes[0] == 3 && bytes.length >= 14 && bytes.length <= 14 + 256 * 256 * 4) {
            // Cursor messages are small; video never crosses the main-thread queue.
            main.post(() -> cursor(bytes));
        }
    }

    private void cursor(byte[] data) {
        if (closed) return;
        int w = u16(data, 2), h = u16(data, 4);
        if (w == 0 || h == 0) { cursor = null; invalidate(); return; }
        if (w > 256 || h > 256 || data.length != 14 + w * h * 4) return;
        int[] pixels = new int[w * h];
        for (int i = 0; i < pixels.length; i++) {
            int p = 14 + i * 4;
            pixels[i] = (data[p + 3] & 255) << 24 | (data[p] & 255) << 16 | (data[p + 1] & 255) << 8 | data[p + 2] & 255;
        }
        cursor = Bitmap.createBitmap(pixels, w, h, Bitmap.Config.ARGB_8888);
        hotX = Math.min(w - 1, u16(data, 6)); hotY = Math.min(h - 1, u16(data, 8)); invalidate();
    }
    private static int u16(byte[] data, int at) { return (data[at] & 255) << 8 | data[at + 1] & 255; }

    @Override protected void onMeasure(int ws, int hs) {
        int w = MeasureSpec.getSize(ws), h = MeasureSpec.getSize(hs);
        setMeasuredDimension(w, h);
        if (layout != null && surface != null) {
            viewport.resize(w, h, layout.width, layout.height);
            surface.measure(MeasureSpec.makeMeasureSpec(Math.max(1, (int)Math.round(layout.width * viewport.fit())), MeasureSpec.EXACTLY),
                    MeasureSpec.makeMeasureSpec(Math.max(1, (int)Math.round(layout.height * viewport.fit())), MeasureSpec.EXACTLY));
        }
    }
    @Override protected void onLayout(boolean changed, int l, int t, int r, int b) {
        if (surface == null) return;
        int x = (getWidth() - surface.getMeasuredWidth()) / 2, y = (getHeight() - surface.getMeasuredHeight()) / 2;
        surface.layout(x, y, x + surface.getMeasuredWidth(), y + surface.getMeasuredHeight()); transform();
    }
    private void transform() {
        if (surface != null) {
            surface.setPivotX(surface.getWidth() / 2f); surface.setPivotY(surface.getHeight() / 2f);
            surface.setScaleX((float) viewport.zoom); surface.setScaleY((float) viewport.zoom);
            surface.setTranslationX((float) viewport.x); surface.setTranslationY((float) viewport.y);
        }
        invalidate(); changed.run();
    }
    @Override protected void dispatchDraw(Canvas canvas) {
        super.dispatchDraw(canvas);
        if (!active || !pointerVisible || cursor == null || layout == null) return;
        double scale = viewport.scale();
        float size = Math.max(ui.dp(18), (float) (Math.max(cursor.getWidth(), cursor.getHeight()) * scale));
        float cursorScale = size / Math.max(cursor.getWidth(), cursor.getHeight());
        float x = (float)(viewport.width / 2 + viewport.x + (pointerX - layout.width / 2.0) * scale) - hotX * cursorScale;
        float y = (float)(viewport.height / 2 + viewport.y + (pointerY - layout.height / 2.0) * scale) - hotY * cursorScale;
        canvas.drawBitmap(cursor, null, new RectF(x, y, x + cursor.getWidth() * cursorScale, y + cursor.getHeight() * cursorScale), paint);
    }
    boolean controllable() { return active && !closed && layout != null && !layout.readOnly; }
    boolean ready() { return active && !closed; }
    boolean panMode() { return panMode; }
    void panMode(boolean enabled) { releaseAll(); panMode = enabled; gesture = 0; transform(); }
    void zoom(double factor) { viewport.zoomAt(viewport.zoom * factor, getWidth()/2.0, getHeight()/2.0, getWidth()/2.0, getHeight()/2.0); transform(); }
    void fit() { viewport.reset(); transform(); }
    private void move(float x, float y) {
        pointerX = (int)Math.round(viewport.remoteX(x)); pointerY = (int)Math.round(viewport.remoteY(y)); pointerVisible = true;
        send.accept("m," + pointerX + "," + pointerY); invalidate();
    }
    void tap(int keysym) { if (controllable()) { send.accept("k," + keysym + ",1"); send.accept("k," + keysym + ",0"); } }
    void modifier(int keysym) {
        if (!controllable()) return;
        boolean down = sticky.add(keysym);
        if (!down) sticky.remove(keysym);
        send.accept("k," + keysym + "," + (down ? 1 : 0)); changed.run();
    }
    boolean held(int keysym) { return sticky.contains(keysym); }
    int shortcut() { return layout != null && layout.mac ? 0xffeb : 0xffe3; }
    void clipboard(NativeDesktopClipboard value) { clipboard = value; }
    void copy() {
        if (!controllable()) return;
        if (clipboard != null) clipboard.copy();
        shortcut('c');
    }
    void pasteText(String text, int modifiers) {
        if (!controllable()) return;
        send.accept("c," + text); pasteShortcut(modifiers);
    }
    void pasteShortcut(int modifiers) {
        if (!controllable()) return;
        java.util.Set<Integer> held = heldModifiers(), wanted = new java.util.LinkedHashSet<>();
        addModifier(wanted, held, shortcut());
        if ((modifiers & KeyEvent.META_SHIFT_MASK) != 0) addModifier(wanted, held, 0xffe1);
        if ((modifiers & KeyEvent.META_ALT_MASK) != 0) addModifier(wanted, held, 0xffe9);
        if ((modifiers & KeyEvent.META_META_MASK) != 0) addModifier(wanted, held, layout.mac ? 0xffe3 : 0xffeb);
        // Image decoding may finish after physical modifiers have been released.
        // Synthesize the original shortcut, then restore the currently held keys.
        for (int key : held) if (!wanted.contains(key)) send.accept("k," + key + ",0");
        for (int key : wanted) if (!held.contains(key)) send.accept("k," + key + ",1");
        tap('v');
        for (int key : wanted) if (!held.contains(key)) send.accept("k," + key + ",0");
        for (int key : held) if (!wanted.contains(key)) send.accept("k," + key + ",1");
    }
    private java.util.Set<Integer> heldModifiers() {
        java.util.Set<Integer> result = new java.util.TreeSet<>(sticky);
        result.addAll(keys.values());
        result.removeIf(key -> key != 0xffe1 && key != 0xffe2 && key != 0xffe3 && key != 0xffe4
                && key != 0xffe9 && key != 0xffea && key != 0xffeb && key != 0xffec);
        return result;
    }
    private static void addModifier(java.util.Set<Integer> wanted, java.util.Set<Integer> held, int left) {
        if (held.contains(left)) wanted.add(left);
        if (held.contains(left + 1)) wanted.add(left + 1);
        if (!held.contains(left) && !held.contains(left + 1)) wanted.add(left);
    }
    private int pasteModifiers(KeyEvent event) {
        int modifiers = event.getMetaState();
        java.util.Set<Integer> held = heldModifiers();
        if (held.contains(0xffe1) || held.contains(0xffe2)) modifiers |= KeyEvent.META_SHIFT_ON;
        if (held.contains(0xffe9) || held.contains(0xffea)) modifiers |= KeyEvent.META_ALT_ON;
        int meta = layout.mac ? 0xffe3 : 0xffeb;
        if (held.contains(meta) || held.contains(meta + 1)) modifiers |= KeyEvent.META_META_ON;
        return modifiers;
    }
    private void shortcut(int key) {
        if (!controllable()) return;
        releaseAll();
        int modifier = shortcut();
        send.accept("k," + modifier + ",1"); tap(key); send.accept("k," + modifier + ",0");
    }
    void releaseAll() {
        if (main == null) return;
        main.removeCallbacks(longPress);
        if (controllable()) send.accept("r");
        keys.clear(); sticky.clear(); gesture = 0; changed.run();
    }
    private final Runnable longPress = () -> {
        if (gesture == 1 && controllable() && !panMode) {
            move(startX, startY); click(3); gesture = 4;
            performHapticFeedback(android.view.HapticFeedbackConstants.LONG_PRESS);
        }
    };
    private void click(int button) { send.accept("b," + button + ",1"); send.accept("b," + button + ",0"); }
    @Override public boolean onInterceptTouchEvent(MotionEvent event) { return true; }
    @Override public boolean onTouchEvent(MotionEvent e) {
        if (closed || layout == null || !active) return true;
        if (e.isFromSource(InputDevice.SOURCE_MOUSE)) {
            if (!controllable() || panMode) return true;
            move(e.getX(), e.getY());
            if (e.getActionMasked() == MotionEvent.ACTION_DOWN) send.accept("b,1,1");
            if (e.getActionMasked() == MotionEvent.ACTION_UP || e.getActionMasked() == MotionEvent.ACTION_CANCEL) send.accept("b,1,0");
            return true;
        }
        int action = e.getActionMasked();
        if (action == MotionEvent.ACTION_DOWN) {
            requestFocus(); startX = lastX = e.getX(); startY = lastY = e.getY();
            gesture = viewport.contains(startX, startY) ? 1 : 4;
            if (!panMode && controllable() && gesture == 1) main.postDelayed(longPress, 550);
        } else if (action == MotionEvent.ACTION_POINTER_DOWN) {
            main.removeCallbacks(longPress);
            if (gesture == 2 && controllable()) send.accept("b,1,0");
            gesture = 3; midpoint(e);
        } else if (action == MotionEvent.ACTION_MOVE) {
            if (e.getPointerCount() >= 2 && gesture == 3) {
                float oldX = midX, oldY = midY, oldDistance = distance;
                midpoint(e);
                if (panMode) {
                    viewport.zoomAt(viewport.zoom * (oldDistance > 0 ? distance / oldDistance : 1), oldX, oldY, midX, midY); transform();
                } else if (controllable()) {
                    scroll((oldX - midX) / viewport.scale(), (oldY - midY) / viewport.scale());
                }
            } else if (panMode && (gesture == 1 || gesture == 2)) {
                viewport.pan(e.getX() - lastX, e.getY() - lastY); transform(); gesture = 2;
            } else if (controllable() && (gesture == 1 || gesture == 2)) {
                if (gesture == 1 && Math.hypot(e.getX() - startX, e.getY() - startY) > ViewConfiguration.get(getContext()).getScaledTouchSlop()) {
                    main.removeCallbacks(longPress); move(startX, startY); send.accept("b,1,1"); gesture = 2;
                }
                if (gesture == 2) move(e.getX(), e.getY());
            }
            lastX = e.getX(); lastY = e.getY();
        } else if (action == MotionEvent.ACTION_POINTER_UP || action == MotionEvent.ACTION_UP || action == MotionEvent.ACTION_CANCEL) {
            main.removeCallbacks(longPress);
            if (!panMode && controllable()) {
                if (action == MotionEvent.ACTION_UP && gesture == 1) { move(startX, startY); click(1); performClick(); }
                else if (gesture == 2) send.accept("b,1,0");
            }
            gesture = action == MotionEvent.ACTION_POINTER_UP ? 4 : 0;
        }
        return true;
    }
    private void midpoint(MotionEvent e) {
        midX = (e.getX(0) + e.getX(1)) / 2; midY = (e.getY(0) + e.getY(1)) / 2;
        distance = (float)Math.hypot(e.getX(0) - e.getX(1), e.getY(0) - e.getY(1));
    }
    @Override public boolean performClick() { super.performClick(); return true; }
    @Override public boolean onGenericMotionEvent(MotionEvent e) {
        if (!controllable() || panMode) return super.onGenericMotionEvent(e);
        if (e.getActionMasked() == MotionEvent.ACTION_HOVER_MOVE) { move(e.getX(), e.getY()); return true; }
        if (e.getActionMasked() == MotionEvent.ACTION_SCROLL) {
            scroll(-e.getAxisValue(MotionEvent.AXIS_HSCROLL) * 48, -e.getAxisValue(MotionEvent.AXIS_VSCROLL) * 48); return true;
        }
        if (e.getActionMasked() == MotionEvent.ACTION_BUTTON_PRESS || e.getActionMasked() == MotionEvent.ACTION_BUTTON_RELEASE) {
            int button = e.getActionButton() == MotionEvent.BUTTON_SECONDARY ? 3 : e.getActionButton() == MotionEvent.BUTTON_TERTIARY ? 2 : 0;
            if (button != 0) { move(e.getX(), e.getY()); send.accept("b," + button + "," + (e.getActionMasked() == MotionEvent.ACTION_BUTTON_PRESS ? 1 : 0)); return true; }
        }
        return super.onGenericMotionEvent(e);
    }
    private void scroll(double dx, double dy) {
        send.accept(String.format(Locale.ROOT, "sp,%.1f,%.1f", Math.max(-65536, Math.min(65536, dx)), Math.max(-65536, Math.min(65536, dy))));
    }
    void keyboard() {
        if (!controllable()) return;
        requestFocus();
        InputMethodManager manager = (InputMethodManager)getContext().getSystemService(android.content.Context.INPUT_METHOD_SERVICE);
        manager.showSoftInput(this, InputMethodManager.SHOW_IMPLICIT);
    }
    @Override public boolean onCheckIsTextEditor() { return true; }
    @Override public InputConnection onCreateInputConnection(EditorInfo out) {
        out.inputType = InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_MULTI_LINE | InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS;
        out.imeOptions = EditorInfo.IME_FLAG_NO_EXTRACT_UI | EditorInfo.IME_FLAG_NO_PERSONALIZED_LEARNING;
        out.initialSelStart = out.initialSelEnd = 0;
        out.contentMimeTypes = new String[]{"image/*"};
        return new BaseInputConnection(this, false) {
            private String composing = "";
            @Override public boolean setComposingText(CharSequence text, int cursor) { composing = text.toString(); return true; }
            @Override public boolean commitText(CharSequence text, int cursor) { composing = ""; type(text.toString()); return true; }
            @Override public boolean finishComposingText() { String text = composing; composing = ""; type(text); return true; }
            @Override public boolean deleteSurroundingText(int before, int after) {
                if (!composing.isEmpty()) { composing = ""; return true; }
                for (int i = 0; i < Math.min(64, before); i++) tap(0xff08);
                for (int i = 0; i < Math.min(64, after); i++) tap(0xffff);
                return true;
            }
            @Override public boolean deleteSurroundingTextInCodePoints(int before, int after) { return deleteSurroundingText(before, after); }
            @Override public boolean sendKeyEvent(KeyEvent event) { return event.getAction() == KeyEvent.ACTION_DOWN ? onKeyDown(event.getKeyCode(), event) : onKeyUp(event.getKeyCode(), event); }
            @Override public CharSequence getTextBeforeCursor(int count, int flags) { return ""; }
            @Override public CharSequence getTextAfterCursor(int count, int flags) { return ""; }
            @Override public CharSequence getSelectedText(int flags) { return ""; }
            @Override public boolean performEditorAction(int action) { tap(0xff0d); return true; }
            @Override public boolean performContextMenuAction(int action) {
                if (action == android.R.id.paste || action == android.R.id.pasteAsPlainText) return clipboard != null && clipboard.paste();
                if (action == android.R.id.copy) { copy(); return true; }
                if (action == android.R.id.cut) { shortcut('x'); return true; }
                return false;
            }
            @Override public boolean commitContent(android.view.inputmethod.InputContentInfo info, int flags, android.os.Bundle opts) {
                return clipboard != null && clipboard.content(info, flags);
            }
        };
    }
    private void type(String text) {
        if (!controllable() || text.isEmpty()) return;
        final int[] points = text.codePoints().limit(4096).toArray();
        main.post(new Runnable() {
            int at;
            public void run() {
                if (!controllable()) return;
                int end = Math.min(points.length, at + 24);
                while (at < end) { int point = points[at++]; tap(point == '\n' ? 0xff0d : point == '\t' ? 0xff09 : NativeDesktopProtocol.keysym(point)); }
                if (at < points.length) main.postDelayed(this, 16);
            }
        });
    }
    @Override public boolean onKeyDown(int code, KeyEvent event) {
        if (!controllable() || code == KeyEvent.KEYCODE_BACK || code == KeyEvent.KEYCODE_VOLUME_UP || code == KeyEvent.KEYCODE_VOLUME_DOWN) return super.onKeyDown(code, event);
        if (clipboard != null && (code == KeyEvent.KEYCODE_PASTE || code == KeyEvent.KEYCODE_V
                && (event.isCtrlPressed() || held(shortcut())) && !event.isAltPressed())) {
            if (event.getRepeatCount() == 0) clipboard.paste(pasteModifiers(event));
            keys.put(code, 0); return true;
        }
        int symbol = key(code, event);
        if (symbol == 0) return super.onKeyDown(code, event);
        keys.put(code, symbol); send.accept("k," + symbol + ",1"); return true;
    }
    @Override public boolean onKeyUp(int code, KeyEvent event) {
        Integer symbol = keys.remove(code);
        if (symbol == null || !controllable()) return super.onKeyUp(code, event);
        if (symbol == 0) return true;
        send.accept("k," + symbol + ",0"); return true;
    }
    private int key(int code, KeyEvent event) {
        switch (code) {
            case KeyEvent.KEYCODE_DEL: return 0xff08;
            case KeyEvent.KEYCODE_FORWARD_DEL: return 0xffff;
            case KeyEvent.KEYCODE_ENTER: case KeyEvent.KEYCODE_NUMPAD_ENTER: return 0xff0d;
            case KeyEvent.KEYCODE_TAB: return 0xff09;
            case KeyEvent.KEYCODE_ESCAPE: return 0xff1b;
            case KeyEvent.KEYCODE_DPAD_LEFT: return 0xff51;
            case KeyEvent.KEYCODE_DPAD_UP: return 0xff52;
            case KeyEvent.KEYCODE_DPAD_RIGHT: return 0xff53;
            case KeyEvent.KEYCODE_DPAD_DOWN: return 0xff54;
            case KeyEvent.KEYCODE_MOVE_HOME: return 0xff50;
            case KeyEvent.KEYCODE_MOVE_END: return 0xff57;
            case KeyEvent.KEYCODE_PAGE_UP: return 0xff55;
            case KeyEvent.KEYCODE_PAGE_DOWN: return 0xff56;
            case KeyEvent.KEYCODE_INSERT: return 0xff63;
            case KeyEvent.KEYCODE_SHIFT_LEFT: return 0xffe1;
            case KeyEvent.KEYCODE_SHIFT_RIGHT: return 0xffe2;
            case KeyEvent.KEYCODE_ALT_LEFT: return 0xffe9;
            case KeyEvent.KEYCODE_ALT_RIGHT: return 0xffea;
            case KeyEvent.KEYCODE_CTRL_LEFT: return shortcut();
            case KeyEvent.KEYCODE_CTRL_RIGHT: return shortcut() + 1;
            case KeyEvent.KEYCODE_META_LEFT: return layout.mac ? 0xffe3 : 0xffeb;
            case KeyEvent.KEYCODE_META_RIGHT: return layout.mac ? 0xffe4 : 0xffec;
            default:
                if (code >= KeyEvent.KEYCODE_F1 && code <= KeyEvent.KEYCODE_F12) return 0xffbe + code - KeyEvent.KEYCODE_F1;
                int point = event.getUnicodeChar(event.getMetaState() & ~(KeyEvent.META_CTRL_MASK | KeyEvent.META_META_MASK));
                return point == 0 || (point & android.view.KeyCharacterMap.COMBINING_ACCENT) != 0 ? 0 : NativeDesktopProtocol.keysym(point);
        }
    }
    @Override public void onWindowFocusChanged(boolean focus) { super.onWindowFocusChanged(focus); if (!focus) releaseAll(); }
    private void stopVideo() {
        releaseAll(); active = false;
        NativeDesktopVideo old = video; video = null;
        if (old != null) old.close();
        removeAllViews(); surface = null; cursor = null;
    }
    @Override public void close() {
        if (closed) return;
        stopVideo(); closed = true;
        main.removeCallbacksAndMessages(null);
        InputMethodManager manager = (InputMethodManager)getContext().getSystemService(android.content.Context.INPUT_METHOD_SERVICE);
        manager.hideSoftInputFromWindow(getWindowToken(), 0);
    }
}
