package com.cloudgeek.spool.android;

import android.app.Activity;
import android.content.ClipData;
import android.content.ClipDescription;
import android.content.Context;
import android.graphics.Canvas;
import android.graphics.ColorFilter;
import android.graphics.Paint;
import android.graphics.PixelFormat;
import android.graphics.RectF;
import android.graphics.drawable.Drawable;
import android.net.Uri;
import android.view.DragAndDropPermissions;
import android.view.DragEvent;
import android.widget.LinearLayout;
import java.util.ArrayList;
import java.util.List;
import java.util.function.BiConsumer;
import java.util.function.BooleanSupplier;

/** Intercepts document drops before EditText can insert their content URIs as text. */
final class NativeAttachmentDrop {
    static final class Page extends LinearLayout {
        private NativeAttachmentDrop fileDrop;

        Page(Context context) { super(context); setOrientation(VERTICAL); }

        @Override public boolean dispatchDragEvent(DragEvent event) {
            boolean documents = fileDrop != null && fileDrop.handles(event);
            boolean handled = super.dispatchDragEvent(event);
            if (!documents) return handled;
            // Observe the page boundary even when ViewGroup routes exit/end
            // directly to a child instead of invoking its own drag listener.
            switch (event.getAction()) {
                case DragEvent.ACTION_DRAG_ENTERED:
                case DragEvent.ACTION_DRAG_LOCATION:
                    fileDrop.setHovering(fileDrop.enabled.getAsBoolean());
                    break;
                default:
                    fileDrop.setHovering(false);
                    break;
            }
            // Only the composer's dispatch imports a completed drop.
            return handled || (event.getAction() != DragEvent.ACTION_DROP && fileDrop.enabled.getAsBoolean());
        }
    }

    private final Activity activity;
    private final NativeComposerBox box;
    private final BooleanSupplier enabled;
    private final BiConsumer<List<Uri>, Runnable> receive;
    private final Drawable hint;
    private boolean files;
    private boolean hovering;

    NativeAttachmentDrop(Activity activity, NativeUi ui, NativeComposerBox box,
            BooleanSupplier enabled, BiConsumer<List<Uri>, Runnable> receive) {
        this.activity = activity;
        this.box = box;
        this.enabled = enabled;
        this.receive = receive;
        hint = new Drawable() {
            private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
            private final Drawable plus = NativeIcons.drawable(activity, "plus", ui.primary, ui.dp(22));
            @Override public void draw(Canvas canvas) {
                RectF bounds = new RectF(getBounds());
                paint.setColor(ui.elevated);
                canvas.drawRoundRect(bounds, ui.dp(19), ui.dp(19), paint);
                paint.setColor(ui.primary);
                paint.setStyle(Paint.Style.STROKE);
                paint.setStrokeWidth(ui.dp(2));
                paint.setPathEffect(new android.graphics.DashPathEffect(new float[] {ui.dp(6), ui.dp(4)}, 0));
                bounds.inset(ui.dp(1), ui.dp(1));
                canvas.drawRoundRect(bounds, ui.dp(19), ui.dp(19), paint);
                paint.setStyle(Paint.Style.FILL);
                paint.setPathEffect(null);
                paint.setTextSize(ui.dp(14));
                String label = ui.t("Drop files or images here");
                float start = bounds.centerX() - (paint.measureText(label) + ui.dp(30)) / 2;
                plus.setBounds(Math.round(start), Math.round(bounds.centerY() - ui.dp(11)),
                        Math.round(start + ui.dp(22)), Math.round(bounds.centerY() + ui.dp(11)));
                plus.draw(canvas);
                paint.setTextAlign(Paint.Align.LEFT);
                canvas.drawText(label, start + ui.dp(30),
                        bounds.centerY() - (paint.ascent() + paint.descent()) / 2, paint);
            }
            @Override public void setAlpha(int alpha) {}
            @Override public void setColorFilter(ColorFilter filter) {}
            @Override public int getOpacity() { return PixelFormat.TRANSLUCENT; }
        };
        box.fileDrop = this;
    }

    void advertiseOn(Page page) { page.fileDrop = this; }

    private void setHovering(boolean active) {
        if (hovering == active) return;
        hovering = active;
        box.setForeground(active ? hint : null);
    }

    boolean handles(DragEvent event) {
        if (event.getAction() == DragEvent.ACTION_DRAG_STARTED) {
            ClipDescription description = event.getClipDescription();
            files = description != null && (description.hasMimeType("text/uri-list")
                    || description.hasMimeType("image/*") || description.hasMimeType("application/*")
                    || description.hasMimeType("audio/*") || description.hasMimeType("video/*"));
        }
        // MIME types alone cannot distinguish documents from text or links.
        // At drop, intercept only document URIs before EditText inserts them.
        if (event.getAction() == DragEvent.ACTION_DROP) {
            ClipData data = event.getClipData();
            if (data != null)
                for (int i = 0; i < data.getItemCount(); i++) {
                    Uri uri = data.getItemAt(i).getUri();
                    if (uri != null && "content".equals(uri.getScheme())) return true;
                }
            return false;
        }
        return files;
    }

    boolean dispatch(DragEvent event) {
        switch (event.getAction()) {
            case DragEvent.ACTION_DRAG_STARTED:
                setHovering(false);
                return enabled.getAsBoolean();
            case DragEvent.ACTION_DRAG_ENTERED:
            case DragEvent.ACTION_DRAG_LOCATION:
                setHovering(enabled.getAsBoolean());
                break;
            case DragEvent.ACTION_DRAG_EXITED:
                setHovering(false);
                break;
            case DragEvent.ACTION_DRAG_ENDED:
                setHovering(false);
                files = false;
                break;
            case DragEvent.ACTION_DROP:
                setHovering(false);
                if (!enabled.getAsBoolean()) return false;
                ClipData data = event.getClipData();
                if (data == null) return false;
                List<Uri> uris = new ArrayList<>();
                for (int i = 0; i < data.getItemCount(); i++) {
                    Uri uri = data.getItemAt(i).getUri();
                    if (uri != null && "content".equals(uri.getScheme())) uris.add(uri);
                }
                if (uris.isEmpty()) return false;
                DragAndDropPermissions permissions = activity.requestDragAndDropPermissions(event);
                receive.accept(uris, () -> { if (permissions != null) permissions.release(); });
                break;
            default:
                break;
        }
        return true;
    }
}
