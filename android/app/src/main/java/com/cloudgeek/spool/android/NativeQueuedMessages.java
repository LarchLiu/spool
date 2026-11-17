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
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

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
    private String draggedID, pendingID, pendingBefore;
    private float dragY;
    private Map<String, Float> positions;
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
        android.graphics.drawable.GradientDrawable surface = ui.shape(ui.elevated, 12, ui.borderStrong);
        surface.setCornerRadii(new float[] {ui.dp(12), ui.dp(12), ui.dp(12), ui.dp(12), 0, 0, 0, 0});
        setBackground(surface);
        setClipToOutline(true);
        setOverScrollMode(OVER_SCROLL_IF_CONTENT_SCROLLS);
        setContentDescription(ui.t("Queued messages"));
        setVisibility(GONE);
        setOnDragListener((view, event) -> {
            if (!(event.getLocalState() instanceof QueueDrag)) return false;
            QueueDrag drag = (QueueDrag) event.getLocalState();
            if (drag.owner != this) return false;
            if (event.getAction() == DragEvent.ACTION_DRAG_ENDED) {
                dragging = false; removeCallbacks(dragScroll);
                markDrop(null); draggedID = null; return true;
            }
            if (event.getAction() == DragEvent.ACTION_DRAG_EXITED) {
                dragging = false; removeCallbacks(dragScroll);
                markDrop(null); return true;
            }
            if (this.busy) return false;
            if (event.getAction() == DragEvent.ACTION_DRAG_LOCATION) {
                draggedID = drag.id; dragging = true; dragY = event.getY();
                removeCallbacks(dragScroll); scrollDrag();
                markDrop(before(event.getY()));
            } else if (event.getAction() == DragEvent.ACTION_DROP) {
                dragging = false; removeCallbacks(dragScroll);
                String before = before(event.getY()), originalBefore = "";
                for (int i = 0; i < list.getChildCount() - 1; i++)
                    if (drag.id.equals(list.getChildAt(i).getTag())) originalBefore = (String) list.getChildAt(i + 1).getTag();
                if (!before.equals(originalBefore)) moveMessage(drag.id, before);
                else markDrop(null);
                draggedID = null;
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
        draggedID = null; markDrop(null);
        for (int i = 0; i < list.getChildCount(); i++) list.getChildAt(i).animate().cancel();
        if (menu != null) menu.dismiss();
        super.onDetachedFromWindow();
    }

    private String before(float y) {
        for (int i = 0; i < list.getChildCount(); i++) {
            View row = list.getChildAt(i);
            if (row.getTag().equals(draggedID)) continue;
            if (y + getScrollY() < row.getTop() + row.getHeight() / 2f) return (String) row.getTag();
        }
        return "";
    }

    private void markDrop(String id) {
        int from = -1, to = list.getChildCount();
        for (int i = 0; i < list.getChildCount(); i++) {
            if (list.getChildAt(i).getTag().equals(draggedID)) from = i;
            if (list.getChildAt(i).getTag().equals(id)) to = i;
        }
        if (to > from) to--;
        for (int i = 0; i < list.getChildCount(); i++) {
            View row = list.getChildAt(i);
            row.setAlpha(id != null && i == from ? .2f : 1);
            float offset = id == null || from < 0 ? 0 : i > from && i <= to ? -row.getHeight()
                    : i < from && i >= to ? row.getHeight() : 0;
            if (android.animation.ValueAnimator.areAnimatorsEnabled())
                row.animate().translationY(offset).setDuration(180).setInterpolator(new android.view.animation.DecelerateInterpolator()).start();
            else { row.animate().cancel(); row.setTranslationY(offset); }
        }
    }

    private void rememberPositions() {
        positions = new HashMap<>();
        for (int i = 0; i < list.getChildCount(); i++) {
            View row = list.getChildAt(i);
            positions.put((String) row.getTag(), row.getY());
        }
    }

    private void moveMessage(String id, String before) {
        rememberPositions();
        pendingID = id; pendingBefore = before;
        View moving = null;
        for (int i = 0; i < list.getChildCount(); i++)
            if (id.equals(list.getChildAt(i).getTag())) moving = list.getChildAt(i);
        if (moving != null) {
            list.removeView(moving);
            int at = list.getChildCount();
            for (int i = 0; i < list.getChildCount(); i++)
                if (before.equals(list.getChildAt(i).getTag())) { at = i; break; }
            list.addView(moving, at);
        }
        move.accept(id, before);
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        super.onLayout(changed, left, top, right, bottom);
        if (positions == null) return;
        for (int i = 0; i < list.getChildCount(); i++) {
            View row = list.getChildAt(i);
            row.animate().cancel(); row.setAlpha(1);
            Float previous = positions.get(row.getTag());
            row.setTranslationY(previous == null ? 0 : previous - row.getTop());
            if (android.animation.ValueAnimator.areAnimatorsEnabled())
                row.animate().translationY(0).setDuration(180).setInterpolator(new android.view.animation.DecelerateInterpolator()).start();
            else row.setTranslationY(0);
        }
        positions = null;
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
        if (positions == null) rememberPositions();
        if (!busy) { pendingID = null; pendingBefore = null; }
        if (menu != null) { menu.dismiss(); menu = null; }
        List<JSONObject> ordered = new ArrayList<>();
        JSONObject moving = null;
        for (int i = 0; i < count; i++) {
            JSONObject message = messages.optJSONObject(i);
            if (message == null) continue;
            if (message.optString("id").equals(pendingID)) moving = message;
            else ordered.add(message);
        }
        if (moving != null) {
            int at = ordered.size();
            for (int i = 0; i < ordered.size(); i++)
                if (ordered.get(i).optString("id").equals(pendingBefore)) { at = i; break; }
            ordered.add(at, moving);
        }
        for (int i = 0; i < list.getChildCount(); i++) list.getChildAt(i).animate().cancel();
        list.removeAllViews();
        setVisibility(count == 0 ? GONE : VISIBLE);
        for (int i = 0; i < ordered.size(); i++) {
            JSONObject message = ordered.get(i);
            final String id = message.optString("id");
            String deliveryStatus = message.optString("delivery_status");
            String body = message.optString("message").trim();
            JSONArray files = message.optJSONArray("attachments");
            if (body.isEmpty() && files != null) body = files.optString(0);
            LinearLayout row = ui.row();
            row.setTag(id);
            row.setPadding(ui.dp(12), ui.dp(2), ui.dp(8), ui.dp(2));
            if (i > 0) {
                int color = ui.dark ? androidx.core.graphics.ColorUtils.blendARGB(ui.line, ui.borderStrong, .65f) : ui.line;
                android.graphics.drawable.LayerDrawable divider = new android.graphics.drawable.LayerDrawable(
                        new android.graphics.drawable.Drawable[] {ui.shape(color, 0, 0)});
                divider.setLayerHeight(0, ui.dp(1)); divider.setLayerGravity(0, Gravity.TOP);
                divider.setLayerInset(0, ui.dp(1), 0, ui.dp(1), 0);
                row.setBackground(divider);
            }
            if (count > 1) {
                NativeUi.IconButton grip = ui.icon("grip_vertical", 15, ui.t("Drag to reorder"), () -> {});
                grip.setPadding(ui.dp(5.5f), ui.dp(5.5f), ui.dp(5.5f), ui.dp(5.5f));
                boolean provisional = false;
                for (JSONObject entry : ordered) provisional |= !entry.optString("delivery_status").isEmpty();
                grip.setEnabled(!busy && !provisional);
                grip.setOnLongClickListener(view -> {
                    if (!view.isEnabled()) return false;
                    draggedID = id;
                    boolean started = view.startDragAndDrop(ClipData.newPlainText("", ""), new View.DragShadowBuilder(row) {
                        @Override public void onProvideShadowMetrics(android.graphics.Point size, android.graphics.Point touch) {
                            super.onProvideShadowMetrics(size, touch);
                            touch.set(Math.round(view.getX() + view.getWidth() / 2f), row.getHeight() / 2);
                        }
                        @Override public void onDrawShadow(android.graphics.Canvas canvas) {
                            android.graphics.Paint paint = new android.graphics.Paint(android.graphics.Paint.ANTI_ALIAS_FLAG);
                            android.graphics.RectF bounds = new android.graphics.RectF(.5f, .5f, row.getWidth() - .5f, row.getHeight() - .5f);
                            paint.setColor(ui.elevated); canvas.drawRoundRect(bounds, ui.dp(9), ui.dp(9), paint);
                            paint.setStyle(android.graphics.Paint.Style.STROKE); paint.setStrokeWidth(ui.dp(1));
                            paint.setColor(ui.borderStrong); canvas.drawRoundRect(bounds, ui.dp(9), ui.dp(9), paint);
                            super.onDrawShadow(canvas);
                        }
                    }, new QueueDrag(this, id), 0);
                    if (!started) draggedID = null;
                    return started;
                });
                final int index = i;
                grip.setOnKeyListener((view, code, event) -> {
                    if (event.getAction() != android.view.KeyEvent.ACTION_DOWN || !view.isEnabled()) return false;
                    if (code == android.view.KeyEvent.KEYCODE_DPAD_UP && index > 0) {
                        moveMessage(id, ordered.get(index - 1).optString("id")); return true;
                    }
                    if (code == android.view.KeyEvent.KEYCODE_DPAD_DOWN && index < count - 1) {
                        moveMessage(id, index + 2 < count ? ordered.get(index + 2).optString("id") : ""); return true;
                    }
                    return false;
                });
                LinearLayout.LayoutParams gripParams = new LinearLayout.LayoutParams(ui.dp(26), ui.dp(26));
                gripParams.rightMargin = ui.dp(8);
                row.addView(grip, gripParams);
            }
            TextView text = ui.text(body, 13, ui.text);
            text.setSingleLine(true); text.setEllipsize(TextUtils.TruncateAt.END);
            row.addView(text, new LinearLayout.LayoutParams(0, -2, 1));
            if (!deliveryStatus.isEmpty()) {
                LinearLayout.LayoutParams statusParams = new LinearLayout.LayoutParams(-2, -2);
                statusParams.leftMargin = ui.dp(8);
                row.addView(new NativeDeliveryStatus(ui, deliveryStatus, null), statusParams);
            }
            if (deliveryStatus.equals("sending")) {
                list.addView(row, new LinearLayout.LayoutParams(-1, ui.dp(ROW_HEIGHT_DP)));
                continue;
            }
            if (deliveryStatus.isEmpty()) {
                TextView action = ui.text("↳ " + ui.t(running ? "Steer" : "Send now"), 12, ui.muted);
                action.setGravity(Gravity.CENTER); action.setPadding(ui.dp(8), 0, ui.dp(8), 0);
                action.setFocusable(true); action.setEnabled(!busy); action.setAlpha(busy ? .45f : 1);
                action.setOnClickListener(view -> steer.accept(id));
                LinearLayout.LayoutParams actionParams = new LinearLayout.LayoutParams(-2, ui.dp(26));
                actionParams.leftMargin = ui.dp(8); actionParams.rightMargin = ui.dp(8);
                row.addView(action, actionParams);
            }
            NativeUi.IconButton remove = ui.icon("trash", 15, ui.t("Delete queued message"), () -> delete.accept(id));
            remove.setPadding(ui.dp(5.5f), ui.dp(5.5f), ui.dp(5.5f), ui.dp(5.5f));
            remove.setEnabled(!busy);
            row.addView(remove, new LinearLayout.LayoutParams(ui.dp(26), ui.dp(26)));
            NativeUi.IconButton more = ui.icon("more", 16, ui.t("Queued message actions"), () -> {});
            more.setPadding(ui.dp(5), ui.dp(5), ui.dp(5), ui.dp(5));
            more.setEnabled(!busy);
            more.setOnClickListener(view -> {
                menu = new NativePopover(ui, 180, 9, 4);
                menu.selectOption(ui.t("Edit message"), false, () -> { menu.dismiss(); edit.accept(id); });
                menu.show(more, true);
            });
            LinearLayout.LayoutParams moreParams = new LinearLayout.LayoutParams(ui.dp(26), ui.dp(26));
            moreParams.leftMargin = ui.dp(8);
            row.addView(more, moreParams);
            list.addView(row, new LinearLayout.LayoutParams(-1, ui.dp(ROW_HEIGHT_DP)));
        }
    }

    int rows() { return getVisibility() == View.VISIBLE ? list.getChildCount() : 0; }
}
