package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.ColorFilter;
import android.graphics.PixelFormat;
import android.graphics.drawable.Drawable;
import android.text.Editable;
import android.text.InputType;
import android.text.TextWatcher;
import android.view.View;
import android.widget.EditText;
import android.widget.FrameLayout;

/** Native search input with a stable border and reserved clear slot. */
final class NativeSearchField extends NativeSurface {
    final EditText input;
    final View clear;

    NativeSearchField(NativeUi ui, String hint, String clearLabel) {
        super(ui, ui.elevated, ui.borderStrong, 8, 0, 0, 0);
        float density = getResources().getDisplayMetrics().density;
        int border = Math.round(density), borders = Math.round(2 * density);
        setPadding(border, border, borders - border, borders - border);
        setClipChildren(false); setClipToPadding(false);
        FrameLayout body = new FrameLayout(ui.context); addView(body, new LayoutParams(-1, -1));
        input = ui.field(hint, false); ui.typography(input, 12, ui.text, 400);
        input.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS);
        input.setImportantForAutofill(IMPORTANT_FOR_AUTOFILL_NO);
        input.setContentDescription(hint); input.setBackground(null); input.setPadding(ui.dp(8), 0, ui.dp(8), 0);
        Drawable slot = new Drawable() {
            @Override public void draw(Canvas canvas) {}
            @Override public void setAlpha(int alpha) {}
            @Override public void setColorFilter(ColorFilter filter) {}
            @Override public int getOpacity() { return PixelFormat.TRANSPARENT; }
        };
        // Chromium reserves the 8px cancel box plus 1px/3px margins even while it is transparent.
        slot.setBounds(0, 0, ui.dp(12), ui.dp(8)); input.setCompoundDrawables(null, null, slot, null); input.setCompoundDrawablePadding(0);
        body.addView(input, new FrameLayout.LayoutParams(-1, -1));
        clear = new NativeSearchCancel(ui);
        clear.setTag("search-clear"); clear.setContentDescription(clearLabel); clear.setClickable(true); clear.setFocusable(false);
        clear.setOnClickListener(view -> { input.setText(""); input.requestFocus(); });
        FrameLayout.LayoutParams at = new FrameLayout.LayoutParams(ui.dp(8), ui.dp(8), android.view.Gravity.RIGHT | android.view.Gravity.CENTER_VERTICAL);
        at.rightMargin = ui.dp(11); body.addView(clear, at);
        input.setOnFocusChangeListener((view, focused) -> { updateClear(); invalidate(); });
        input.addTextChangedListener(new TextWatcher() {
            public void beforeTextChanged(CharSequence value, int start, int count, int after) {}
            public void onTextChanged(CharSequence value, int start, int before, int count) { updateClear(); }
            public void afterTextChanged(Editable value) {}
        });
        updateClear();
    }

    private void updateClear() { clear.setVisibility(input.hasFocus() && input.length() > 0 && input.isEnabled() ? VISIBLE : INVISIBLE); }

    @Override public void setEnabled(boolean enabled) {
        super.setEnabled(enabled);
        if (input == null || clear == null) return;
        input.setEnabled(enabled); clear.setEnabled(enabled);
        if (!enabled) input.clearFocus();
        updateClear(); invalidate();
    }

}
