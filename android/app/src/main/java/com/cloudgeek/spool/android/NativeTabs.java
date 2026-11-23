package com.cloudgeek.spool.android;

import android.animation.Animator;
import android.animation.AnimatorListenerAdapter;
import android.animation.ArgbEvaluator;
import android.animation.ValueAnimator;
import android.graphics.Canvas;
import android.graphics.RectF;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.StateListDrawable;
import android.text.TextUtils;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.animation.PathInterpolator;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.Map;

/** Reusable inset tabs on one rounded track. Callers own selection and content. */
class NativeTabs extends FrameLayout {
    enum Size { REGULAR, COMPACT }
    final LinearLayout tabs;
    final NativeSurface track;
    final HorizontalScrollView scroll;
    private final NativeUi ui;
    private final Size size;
    private final Map<String, Button> buttons = new LinkedHashMap<>();
    private final Drawable indicator;
    private final RectF indicatorBounds = new RectF(), indicatorTarget = new RectF();
    private ValueAnimator animation;
    private String selection;
    private boolean revealSelection = true, animateSelection;
    private int initialScroll = -1;

    void startAtScroll(int offset) { initialScroll = offset; }

    NativeTabs(NativeUi ui, String label) {
        this(ui, label, Size.REGULAR);
    }

    NativeTabs(NativeUi ui, String label, Size size) {
        super(ui.context); this.ui = ui; this.size = size;
        indicator = ui.shape(ui.dark ? ui.sidebar : ui.elevated, tabRadius(), ui.dark ? ui.borderStrong : 0);
        track = new NativeSurface(ui, ui.soft, ui.line, 8, 0, 0, 0);
        addView(track, new FrameLayout.LayoutParams(-1, -2));
        scroll = new HorizontalScrollView(ui.context);
        scroll.setHorizontalScrollBarEnabled(false); scroll.setFillViewport(true);
        scroll.setOverScrollMode(OVER_SCROLL_NEVER); scroll.setContentDescription(label);
        track.addView(scroll, new LinearLayout.LayoutParams(-1, -2));
        tabs = new LinearLayout(ui.context) {
            @Override protected void dispatchDraw(Canvas canvas) {
                if (!indicatorBounds.isEmpty()) {
                    indicator.setBounds(Math.round(indicatorBounds.left), Math.round(indicatorBounds.top),
                            Math.round(indicatorBounds.right), Math.round(indicatorBounds.bottom));
                    indicator.draw(canvas);
                }
                super.dispatchDraw(canvas);
            }
        };
        tabs.setOrientation(LinearLayout.HORIZONTAL); tabs.setGravity(Gravity.CENTER_VERTICAL);
        tabs.setPadding(ui.dp(4), ui.dp(4), ui.dp(4), ui.dp(4));
        scroll.addView(tabs, new HorizontalScrollView.LayoutParams(-2, -2));
    }

    Button addTab(String key, String label, Runnable choose) {
        Button tab = ui.button(label, false, choose);
        tab.setTag(key); tab.setContentDescription(label); tab.setMinimumWidth(ui.dp(size == Size.COMPACT ? 64 : 80));
        if (size == Size.COMPACT) tab.setPadding(ui.dp(8), ui.dp(6), ui.dp(8), ui.dp(6));
        tab.setMaxLines(1); tab.setHorizontallyScrolling(false);
        tab.setMaxWidth(ui.dp(200)); tab.setEllipsize(TextUtils.TruncateAt.END);
        style(tab, key.equals(selection));
        tab.setOnKeyListener((view, code, event) -> {
            int direction = code == KeyEvent.KEYCODE_DPAD_LEFT ? -1 : code == KeyEvent.KEYCODE_DPAD_RIGHT ? 1 : 0;
            if (direction == 0 || !event.hasNoModifiers()) return false;
            if (event.getAction() == KeyEvent.ACTION_DOWN && event.getRepeatCount() == 0) {
                ArrayList<Button> order = new ArrayList<>(buttons.values());
                int index = order.indexOf(tab), next = Math.floorMod(index + direction, order.size());
                Button target = order.get(next);
                target.requestFocus(); target.performClick();
            }
            return true;
        });
        buttons.put(key, tab);
        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-2, ui.dp(size == Size.COMPACT ? 32 : 40), 1);
        params.setMarginEnd(ui.dp(4));
        tabs.addView(tab, params);
        return tab;
    }

    NativeUi.IconButton addAction(String icon, String label, Runnable click) {
        NativeUi.IconButton button = ui.icon(icon, 18, label, click);
        button.setBackground(buttonBackground(false));
        int height = ui.dp(size == Size.COMPACT ? 32 : 40);
        tabs.addView(button, new LinearLayout.LayoutParams(height, height));
        return button;
    }

    Button button(String key) { return buttons.get(key); }

    void select(String key) {
        if (key.equals(selection)) return;
        stopAnimation();
        animateSelection = !indicatorBounds.isEmpty() && isAttachedToWindow() && isShown() && ValueAnimator.areAnimatorsEnabled();
        selection = key;
        for (Map.Entry<String, Button> entry : buttons.entrySet()) {
            Button tab = entry.getValue(); int color = tab.getCurrentTextColor();
            style(tab, key.equals(entry.getKey()));
            if (animateSelection) tab.setTextColor(color);
        }
        revealSelection = true; requestLayout();
    }

    private void style(Button tab, boolean selected) {
        tab.setSelected(selected); tab.setBackground(buttonBackground(selected));
        ui.typography(tab, size == Size.COMPACT ? 11 : 12, textColor(selected), selected ? 600 : 400);
    }

    private Drawable buttonBackground(boolean selected) {
        StateListDrawable background = new StateListDrawable();
        background.addState(new int[] {-android.R.attr.state_enabled}, ui.shape(ui.controlActive, tabRadius(), 0));
        background.addState(new int[] {android.R.attr.state_pressed}, ui.shape(selected ? 0 : ui.hover, tabRadius(), 0));
        background.addState(new int[] {android.R.attr.state_hovered}, ui.shape(selected ? 0 : ui.controlHover, tabRadius(), 0));
        background.addState(new int[0], ui.shape(0, tabRadius(), 0));
        return background;
    }

    private int textColor(boolean selected) { return selected && ui.dark ? ui.thumb : selected || !ui.dark ? ui.text : ui.muted; }

    private void updateIndicator() {
        Button selected = buttons.get(selection);
        if (selected == null || selected.getWidth() == 0) {
            stopAnimation(); animateSelection = false; indicatorBounds.setEmpty(); indicatorTarget.setEmpty();
            finishTextColors(); tabs.invalidate(); return;
        }
        RectF target = new RectF(selected.getLeft(), selected.getTop(), selected.getRight(), selected.getBottom());
        if (animateSelection) {
            animateSelection = false; animateIndicator(target);
        } else if (!indicatorTarget.equals(target) || animation == null) {
            stopAnimation(); indicatorBounds.set(target); indicatorTarget.set(target); finishTextColors(); tabs.invalidate();
        }
    }

    private void animateIndicator(RectF target) {
        stopAnimation();
        if (!ValueAnimator.areAnimatorsEnabled()) {
            indicatorBounds.set(target); indicatorTarget.set(target); finishTextColors(); tabs.invalidate(); return;
        }
        RectF from = new RectF(indicatorBounds); indicatorTarget.set(target);
        Map<Button, Integer> colors = new LinkedHashMap<>();
        for (Button tab : buttons.values()) colors.put(tab, tab.getCurrentTextColor());
        ArgbEvaluator evaluator = new ArgbEvaluator();
        ValueAnimator next = ValueAnimator.ofFloat(0, 1); animation = next;
        next.setDuration(220); next.setInterpolator(new PathInterpolator(.2f, .8f, .2f, 1));
        next.addUpdateListener(value -> {
            float progress = (float) value.getAnimatedValue();
            indicatorBounds.set(from.left + (target.left - from.left) * progress, from.top + (target.top - from.top) * progress,
                    from.right + (target.right - from.right) * progress, from.bottom + (target.bottom - from.bottom) * progress);
            for (Map.Entry<Button, Integer> entry : colors.entrySet())
                entry.getKey().setTextColor((int) evaluator.evaluate(progress, entry.getValue(), textColor(entry.getKey().isSelected())));
            tabs.invalidate();
        });
        next.addListener(new AnimatorListenerAdapter() {
            @Override public void onAnimationEnd(Animator animator) {
                if (animation != animator) return;
                animation = null; indicatorBounds.set(target); finishTextColors(); tabs.invalidate();
            }
        });
        next.start();
    }

    private void finishTextColors() { for (Button tab : buttons.values()) tab.setTextColor(textColor(tab.isSelected())); }

    private void stopAnimation() {
        if (animation == null) return;
        ValueAnimator previous = animation; animation = null;
        previous.removeAllUpdateListeners(); previous.removeAllListeners(); previous.cancel();
    }

    int tabRadius() { return 4; }

    @Override protected void onSizeChanged(int width, int height, int previousWidth, int previousHeight) {
        super.onSizeChanged(width, height, previousWidth, previousHeight);
        if (width != previousWidth) revealSelection = true;
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        super.onLayout(changed, left, top, right, bottom);
        updateIndicator();
        if (!revealSelection) return;
        Button selected = buttons.get(selection);
        int viewport = scroll.getWidth();
        if (selected == null || viewport <= 0) return;
        int target = initialScroll >= 0 ? initialScroll : scroll.getScrollX();
        initialScroll = -1;
        if (selected.getLeft() < target) target = selected.getLeft();
        else if (selected.getRight() > target + viewport) target = selected.getRight() - viewport;
        scroll.scrollTo(Math.max(0, Math.min(target, tabs.getWidth() - viewport)), 0);
        revealSelection = false;
    }

    @Override protected void onDetachedFromWindow() {
        stopAnimation(); animateSelection = false; indicatorBounds.set(indicatorTarget); finishTextColors();
        super.onDetachedFromWindow();
    }
}
