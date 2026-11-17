package com.cloudgeek.spool.android;

import android.text.TextUtils;
import android.content.ClipData;
import android.view.DragEvent;
import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.function.Consumer;
import java.util.function.BiConsumer;

/**
 * Messages waiting for the running turn, attached to the top of the composer box. A long queue
 * scrolls within the height the Web composer gives it, so it never pushes the composer away.
 */
final class NativeQueuedMessages extends ScrollView {
    static final int MAX_HEIGHT_DP = 132, ROW_HEIGHT_DP = 34;
    private final NativeUi ui;
    private final LinearLayout list;
    private final Consumer<String> steer, delete, edit;
    private final BiConsumer<String, String> move;
    private boolean busy;
    private boolean dragging;
    private float dragY;
    private final Runnable dragScroll = this::scrollDrag;
    private NativePopover menu;
    private String shown = "";
    private static final class QueueDrag {
        final NativeQueuedMessages owner;
        final String id;
        QueueDrag(NativeQueuedMessages owner, String id) { this.owner = owner; this.id = id; }
    }

    NativeQueuedMessages(NativeUi ui, Consumer<String> steer, Consumer<String> delete,
                         Consumer<String> edit, BiConsumer<String, String> move) {
        super(ui.context);
        this.ui = ui; this.steer = steer; this.delete = delete; this.edit = edit; this.move = move;
        list = new LinearLayout(ui.context);
        list.setOrientation(LinearLayout.VERTICAL);
        addView(list, new LayoutParams(-1, -2));
        setBackground(ui.shape(ui.elevated, 12, ui.line));
        setClipToOutline(true);
        setOverScrollMode(OVER_SCROLL_IF_CONTENT_SCROLLS);
        setContentDescription(ui.t("Queued messages"));
        setVisibility(GONE);
        setOnDragListener((view, event) -> {
            if (!(event.getLocalState() instanceof QueueDrag)) return false;
            QueueDrag drag = (QueueDrag) event.getLocalState();
            if (drag.owner != this) return false;
            if (event.getAction() == DragEvent.ACTION_DRAG_ENDED || event.getAction() == DragEvent.ACTION_DRAG_EXITED) {
                dragging = false; removeCallbacks(dragScroll);
                markDrop(null); return true;
            }
            if (this.busy) return false;
            if (event.getAction() == DragEvent.ACTION_DRAG_LOCATION) {
                dragging = true; dragY = event.getY();
                removeCallbacks(dragScroll); scrollDrag();
                markDrop(before(event.getY()));
            } else if (event.getAction() == DragEvent.ACTION_DROP) {
                dragging = false; removeCallbacks(dragScroll);
                String before = before(event.getY());
                markDrop(null);
                if (!drag.id.equals(before)) move.accept(drag.id, before);
            }
            return true;
        });
    }

    private void scrollDrag() {
        if (!dragging || busy) return;
        int direction = dragY < ui.dp(24) ? -1 : dragY > getHeight() - ui.dp(24) ? 1 : 0;
        if (direction == 0 || !canScrollVertically(direction)) return;
        scrollBy(0, direction * ui.dp(8)); markDrop(before(dragY));
        postDelayed(dragScroll, 16);
    }

    @Override protected void onDetachedFromWindow() {
        dragging = false; removeCallbacks(dragScroll);
        if (menu != null) menu.dismiss();
        super.onDetachedFromWindow();
    }

    private String before(float y) {
        for (int i = 0; i < list.getChildCount(); i++) {
            View row = list.getChildAt(i);
            if (y + getScrollY() < row.getTop() + row.getHeight() / 2f) return (String) row.getTag();
        }
        return "";
    }

    private void markDrop(String id) {
        for (int i = 0; i < list.getChildCount(); i++) {
            View row = list.getChildAt(i);
            boolean selected = id != null && (id.equals(row.getTag()) || id.isEmpty() && i == list.getChildCount() - 1);
            row.setBackground(selected ? ui.shape(ui.controlHover, 4, ui.line) : null);
        }
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int limit = ui.dp(MAX_HEIGHT_DP);
        if (MeasureSpec.getMode(heightSpec) != MeasureSpec.UNSPECIFIED) limit = Math.min(limit, MeasureSpec.getSize(heightSpec));
        super.onMeasure(widthSpec, MeasureSpec.makeMeasureSpec(limit, MeasureSpec.AT_MOST));
    }

    void bind(JSONArray messages, boolean running, boolean busy) {
        int count = messages == null ? 0 : messages.length();
        String state = count == 0 ? "" : messages + "|" + running + "|" + busy;
        if (state.equals(shown)) return;
        shown = state;
        this.busy = busy;
        dragging = false; removeCallbacks(dragScroll);
        if (menu != null) { menu.dismiss(); menu = null; }
        list.removeAllViews();
        setVisibility(count == 0 ? GONE : VISIBLE);
        for (int i = 0; i < count; i++) {
            JSONObject message = messages.optJSONObject(i);
            if (message == null) continue;
            final String id = message.optString("id");
            String body = message.optString("message").trim();
            JSONArray files = message.optJSONArray("attachments");
            if (body.isEmpty() && files != null) body = files.optString(0);
            LinearLayout row = ui.row();
            row.setTag(id);
            row.setPadding(ui.dp(count > 1 ? 4 : 12), ui.dp(1), ui.dp(4), ui.dp(1));
            if (count > 1) {
                NativeUi.IconButton grip = ui.icon("grip_vertical", 15, ui.t("Drag to reorder"), () -> {});
                grip.setEnabled(!busy);
                grip.setOnLongClickListener(view -> view.startDragAndDrop(ClipData.newPlainText("", ""),
                        new View.DragShadowBuilder(row), new QueueDrag(this, id), 0));
                final int index = i;
                grip.setOnKeyListener((view, code, event) -> {
                    if (event.getAction() != android.view.KeyEvent.ACTION_DOWN || this.busy) return false;
                    if (code == android.view.KeyEvent.KEYCODE_DPAD_UP && index > 0) {
                        move.accept(id, messages.optJSONObject(index - 1).optString("id")); return true;
                    }
                    if (code == android.view.KeyEvent.KEYCODE_DPAD_DOWN && index < count - 1) {
                        move.accept(id, index + 2 < count ? messages.optJSONObject(index + 2).optString("id") : ""); return true;
                    }
                    return false;
                });
                row.addView(grip, new LinearLayout.LayoutParams(ui.dp(24), ui.dp(32)));
            }
            TextView text = ui.text(body, 13, ui.text);
            text.setSingleLine(true); text.setEllipsize(TextUtils.TruncateAt.END);
            row.addView(text, new LinearLayout.LayoutParams(0, -2, 1));
            TextView action = ui.text("↳ " + ui.t(running ? "Steer" : "Send now"), 12, ui.muted);
            action.setGravity(Gravity.CENTER); action.setPadding(ui.dp(8), 0, ui.dp(8), 0);
            action.setFocusable(true); action.setEnabled(!busy); action.setAlpha(busy ? .45f : 1);
            action.setOnClickListener(view -> steer.accept(id));
            row.addView(action, new LinearLayout.LayoutParams(-2, ui.dp(32)));
            NativeUi.IconButton remove = ui.icon("close", 15, ui.t("Delete queued message"), () -> delete.accept(id));
            remove.setEnabled(!busy);
            row.addView(remove, new LinearLayout.LayoutParams(ui.dp(32), ui.dp(32)));
            NativeUi.IconButton more = ui.icon("more", 16, ui.t("Queued message actions"), () -> {});
            more.setEnabled(!busy);
            more.setOnClickListener(view -> {
                menu = new NativePopover(ui, 180, 9, 4);
                menu.selectOption(ui.t("Edit message"), false, () -> { menu.dismiss(); edit.accept(id); });
                menu.show(more, true);
            });
            row.addView(more, new LinearLayout.LayoutParams(ui.dp(32), ui.dp(32)));
            list.addView(row, new LinearLayout.LayoutParams(-1, ui.dp(ROW_HEIGHT_DP)));
        }
    }

    int rows() { return getVisibility() == View.VISIBLE ? list.getChildCount() : 0; }
}
