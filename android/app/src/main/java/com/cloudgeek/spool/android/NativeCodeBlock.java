package com.cloudgeek.spool.android;

import android.content.ClipData;
import android.content.ClipboardManager;
import android.graphics.Typeface;
import android.text.TextUtils;
import android.view.View;
import android.widget.HorizontalScrollView;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

/** Selectable Markdown code with a fixed language/copy header outside the scroll area. */
final class NativeCodeBlock extends LinearLayout implements NativeMarkdownFlowBox {
    interface Copy { void write(String text); }
    final TextView language, code;
    final NativeUi.IconButton copy;
    final HorizontalScrollView scroll;
    private final NativeUi ui;
    private final LinearLayout header;
    private final int containerSize;
    private final Runnable reset;
    private String source = "";
    private String syntaxLanguage = "";
    private NativeSyntax.Binding syntaxBinding;
    private boolean showingFeedback;

    NativeCodeBlock(NativeUi ui, int color, int containerSize) {
        this(ui, color, containerSize, value -> {
            ClipboardManager clipboard = (ClipboardManager) ui.context.getSystemService(android.content.Context.CLIPBOARD_SERVICE);
            if (clipboard == null) throw new IllegalStateException("Clipboard unavailable");
            clipboard.setPrimaryClip(ClipData.newPlainText(ui.t("Copy code"), value));
        });
    }

    NativeCodeBlock(NativeUi ui, int color, int containerSize, Copy writer) {
        super(ui.context); this.ui = ui; this.containerSize = containerSize;
        setOrientation(VERTICAL);
        setBackground(ui.shape(ui.input, 10, ui.borderStrong));
        setPadding(ui.dp(1), ui.dp(1), ui.dp(1), ui.dp(1));
        header = ui.row();
        header.setPadding(ui.dp(12), ui.dp(7), ui.dp(12), 0);
        ImageView icon = new ImageView(ui.context);
        icon.setImageDrawable(NativeIcons.drawable(ui.context, "code-xml", ui.muted, ui.dp(14)));
        icon.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
        header.addView(icon, new LayoutParams(ui.dp(14), ui.dp(14)));
        language = ui.text("", 12, ui.muted);
        language.setSingleLine(); language.setEllipsize(TextUtils.TruncateAt.END);
        ui.preciseText(language); ui.fixedLineHeight(language, 1.5f);
        LayoutParams labelParams = new LayoutParams(0, -2, 1);
        labelParams.setMarginStart(ui.dp(8)); labelParams.setMarginEnd(ui.dp(8));
        header.addView(language, labelParams);
        copy = ui.icon("copy", 13, ui.t("Copy code"), () -> {});
        copy.setBackground(ui.states(0, 6, 0, ui.soft, 0));
        copyPadding();
        header.addView(copy, new LayoutParams(ui.dp(28), ui.dp(28)));
        addView(header, new LayoutParams(-1, -2));
        reset = () -> { if (showingFeedback) feedback("copy", "Copy code", false); };
        copy.setOnClickListener(view -> {
            try { writer.write(source); feedback("check", "Code copied", true); }
            catch (RuntimeException error) { feedback("circle-alert", "Could not copy code", true); }
            copy.removeCallbacks(reset); copy.postDelayed(reset, 2000);
        });
        code = ui.text("", 12, color);
        code.setTypeface(Typeface.MONOSPACE); ui.preciseText(code);
        code.setTextIsSelectable(true); code.setHorizontallyScrolling(true);
        // Match the CSS pre's tab grid and line strut around its 12px code text.
        ui.fixedLineHeight(code, containerSize * 1.65f / 12);
        code.setPadding(ui.dp(16), ui.dp(8), ui.dp(16), ui.dp(14));
        scroll = new HorizontalScrollView(ui.context);
        scroll.setHorizontalScrollBarEnabled(false);
        scroll.addView(code, new android.widget.FrameLayout.LayoutParams(-2, -2));
        scroll.setTag("markdown:pre");
        addView(scroll, new LayoutParams(-1, -2));
        copy.setEnabled(false);
    }

    void bind(String info, String literal) {
        String token = languageToken(info);
        String label = plainLanguage(token) ? ui.t("Plain text") : token;
        if (!language.getText().toString().equals(label)) language.setText(label);
        if (literal.endsWith("\n")) literal = literal.substring(0, literal.length() - 1);
        boolean changed = !source.equals(literal);
        if (changed) {
            source = literal;
            NativeCodeTabs.setText(code, source, code.getTextSize() * containerSize / 12);
            copy.removeCallbacks(reset); reset.run();
        }
        if (changed || !syntaxLanguage.equals(token)) {
            if (syntaxBinding != null) syntaxBinding.close();
            syntaxBinding = null; syntaxLanguage = token;
            NativeSyntax.clearSpans(code.getText());
            if (ui.syntax != null && !plainLanguage(token) && !source.isEmpty())
                syntaxBinding = ui.syntax.bindCode(code, token, source, ui.dark);
            code.invalidate();
        }
        copy.setEnabled(!source.isEmpty());
    }

    static String languageToken(String info) { return info.trim().split("\\s+", 2)[0]; }
    static boolean plainLanguage(String token) {
        return token.isEmpty() || token.equalsIgnoreCase("text") || token.equalsIgnoreCase("txt") || token.equalsIgnoreCase("plaintext");
    }

    private void feedback(String icon, String key, boolean announce) {
        showingFeedback = !icon.equals("copy");
        copy.setKind(icon); copyPadding(); copy.setContentDescription(ui.t(key));
        if (announce) copy.announceForAccessibility(ui.t(key));
    }

    private void copyPadding() {
        int gap = ui.dp(28) - ui.dp(13);
        copy.setPadding(gap / 2, gap / 2, gap - gap / 2, gap - gap / 2);
    }

    void resetFeedback() { copy.removeCallbacks(reset); reset.run(); }

    @Override public float cssHeight() {
        return getPaddingTop() + header.getMeasuredHeight() + NativeUi.cssTextHeight(code) + getPaddingBottom();
    }

    @Override protected void onDetachedFromWindow() {
        resetFeedback();
        super.onDetachedFromWindow();
    }
}
